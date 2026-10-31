package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type SchemaRequest struct {
	Window    string
	Namespace string
}

type Schema struct {
	SQLRelations map[string]SQLRelationSchema `json:"sql_relations"`
	Window       string                       `json:"window"`
	Signals      map[string]SignalSchema      `json:"signals"`
	Services     []Option                     `json:"services"`
	Measures     []string                     `json:"measures"`
	Buckets      []string                     `json:"buckets"`
	Units        []string                     `json:"units"`
}

type SignalSchema struct {
	TimeColumn string            `json:"time_column"`
	Columns    []ColumnSchema    `json:"columns"`
	Attributes []AttributeSchema `json:"attributes"`
	Metrics    []MetricSchema    `json:"metrics,omitempty"`
}

type ColumnSchema struct {
	Field
	Values []Option `json:"values,omitempty"`
}

type AttributeSchema struct {
	Key      string   `json:"key"`
	Scope    string   `json:"scope"`
	Type     string   `json:"type"`
	Count    int64    `json:"count"`
	Services string   `json:"services,omitempty"`
	Values   []Option `json:"values,omitempty"`
}

type MetricSchema struct {
	Name        string `json:"name"`
	Unit        string `json:"unit,omitempty"`
	Type        string `json:"type"`
	Temporality string `json:"temporality,omitempty"`
	Count       int64  `json:"count"`
}

// A schemaEntry holds a discovered schema, fresh until expires and served
// stale until stale while one refresh runs, and the discovery in flight for
// its key, which concurrent callers of a new key join.
type schemaEntry struct {
	schema         *Schema
	expires, stale time.Time
	flight         *schemaFlight
	namespaces     []string
}

type schemaFlight struct {
	done    chan struct{}
	schema  *Schema
	err     error
	waiters int
	refresh bool
	cancel  context.CancelFunc
}

const (
	schemaTTL        = time.Minute
	schemaStale      = 10 * time.Minute
	maxSchemaEntries = 64
	schemaDeadline   = 20 * time.Second
	namespacesKey    = "\x00namespaces"
)

// Detached discoveries run at most two at a time process-wide, like the
// engine's read pool; the rest wait for a slot until their callers leave.
var schemaSlots = make(chan struct{}, 2)

// namespaceMu serializes the namespace listing so a burst runs it once.
var namespaceMu sync.Mutex

// ErrSchemaBusy answers a new window and namespace when the cache holds only
// fresh and in-flight entries. Cached keys are always served.
var ErrSchemaBusy = errors.New("schema discovery is busy; try again shortly")

// Schema describes what telemetry exists: columns with their common values,
// attribute keys and values sampled from recent rows, metric names and
// services. It is the agent's map before it drafts panels. An entry is fresh
// for a minute and then served stale while one background refresh runs.
// Concurrent callers of a new key share one discovery, cancelled when its last
// caller leaves. Keys are bounded: windows are the fixed set and a namespace
// must have data in the last 24h.
func (e *Executor) Schema(ctx context.Context, req SchemaRequest) (*Schema, error) {
	if req.Window == "" {
		req.Window = "1h"
	}
	span, ok := ranges[req.Window]
	if !ok || span > 24*time.Hour {
		return nil, Problems{{Path: "window", Message: "window must be one of 5m, 15m, 1h, 3h, 6h, 12h or 24h"}}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := req.Window + "|" + req.Namespace
	e.mu.Lock()
	entry, ok := e.schemas[key]
	now := e.now()
	if ok && entry.schema != nil && now.Before(entry.stale) {
		if !now.Before(entry.expires) && entry.flight == nil {
			work, cancel := context.WithCancel(context.WithoutCancel(ctx))
			entry.flight = &schemaFlight{done: make(chan struct{}), refresh: true, cancel: cancel}
			e.schemas[key] = entry
			go e.discoverFlight(work, key, req, span, entry.flight)
		}
		e.mu.Unlock()
		return entry.schema, nil
	}
	e.mu.Unlock()
	if req.Namespace != "" {
		if err := e.checkNamespace(ctx, req.Namespace); err != nil {
			return nil, err
		}
	}
	e.mu.Lock()
	entry = e.schemas[key]
	flight := entry.flight
	if flight != nil && flight.waiters == 0 && !flight.refresh {
		// Every caller of this flight left and cancelled it; start afresh.
		flight = nil
	}
	if flight == nil {
		if !e.makeSchemaRoom() {
			e.mu.Unlock()
			return nil, ErrSchemaBusy
		}
		work, cancel := context.WithCancel(context.WithoutCancel(ctx))
		flight = &schemaFlight{done: make(chan struct{}), cancel: cancel}
		e.schemas[key] = schemaEntry{flight: flight}
		go e.discoverFlight(work, key, req, span, flight)
	}
	flight.waiters++
	e.mu.Unlock()
	select {
	case <-flight.done:
		return flight.schema, flight.err
	case <-ctx.Done():
		e.mu.Lock()
		if flight.waiters--; flight.waiters == 0 && !flight.refresh {
			flight.cancel()
		}
		e.mu.Unlock()
		return nil, ctx.Err()
	}
}

// checkNamespace rejects a namespace without data in the last 24h, so junk
// namespaces never start a discovery or take a cache entry.
func (e *Executor) checkNamespace(ctx context.Context, namespace string) error {
	namespaceMu.Lock()
	defer namespaceMu.Unlock()
	e.mu.Lock()
	entry, ok := e.schemas[namespacesKey]
	e.mu.Unlock()
	if !ok || !e.now().Before(entry.expires) {
		end := e.now().UTC()
		var parts []string
		var args []any
		for _, name := range SignalNames() {
			where, a := windowWhere(signals[name], end.Add(-24*time.Hour), end, "")
			parts = append(parts, "SELECT DISTINCT namespace FROM "+name+" WHERE "+where)
			args = append(args, a...)
		}
		rows, err := e.engine.QueryContext(ctx, "SELECT DISTINCT namespace FROM ("+strings.Join(parts, " UNION ALL ")+") WHERE namespace <> '' ORDER BY 1 LIMIT 1000", args...)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			names = append(names, n)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		entry = schemaEntry{namespaces: names, expires: e.now().Add(schemaTTL)}
		e.mu.Lock()
		e.schemas[namespacesKey] = entry
		e.mu.Unlock()
	}
	if slices.Contains(entry.namespaces, namespace) {
		return nil
	}
	return Problems{{Path: "namespace", Message: fmt.Sprintf("namespace %q has no data in the last 24h", namespace),
		Hint: "use one of " + strings.Join(entry.namespaces[:min(len(entry.namespaces), 20)], ", ")}}
}

// makeSchemaRoom removes entries past their stale horizon. In-flight and
// cached entries are never evicted for a new key; it reports whether one fits.
func (e *Executor) makeSchemaRoom() bool {
	now := e.now()
	for k, v := range e.schemas {
		if v.flight == nil && k != namespacesKey && !now.Before(v.stale) {
			delete(e.schemas, k)
		}
	}
	return len(e.schemas) < maxSchemaEntries
}

func (e *Executor) discoverFlight(ctx context.Context, key string, req SchemaRequest, span time.Duration, flight *schemaFlight) {
	defer flight.cancel()
	select {
	case schemaSlots <- struct{}{}:
		ctx, cancel := context.WithTimeout(ctx, schemaDeadline)
		flight.schema, flight.err = e.discover(ctx, req, span)
		cancel()
		<-schemaSlots
	case <-ctx.Done():
		flight.err = ctx.Err()
	}
	e.mu.Lock()
	if entry := e.schemas[key]; entry.flight == flight {
		switch {
		case flight.err == nil:
			now := e.now()
			e.schemas[key] = schemaEntry{schema: flight.schema, expires: now.Add(schemaTTL), stale: now.Add(schemaTTL + schemaStale)}
		case entry.schema != nil:
			// A failed refresh keeps serving the stale schema until its
			// horizon and retries ten seconds later.
			entry.flight = nil
			entry.expires = e.now().Add(10 * time.Second)
			e.schemas[key] = entry
		default:
			delete(e.schemas, key)
		}
	}
	e.mu.Unlock()
	close(flight.done)
}

func (e *Executor) discover(ctx context.Context, req SchemaRequest, span time.Duration) (*Schema, error) {
	end := e.now().UTC()
	start := end.Add(-span)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end, Namespace: req.Namespace})
	out := &Schema{Window: req.Window, Signals: map[string]SignalSchema{}, SQLRelations: SQLRelations(), Measures: measureNames(), Buckets: BucketNames(), Units: unitNames()}
	for _, name := range SignalNames() {
		sig := signals[name]
		values, err := e.lowCardinality(ctx, sig, start, end, req.Namespace)
		if err != nil {
			return nil, err
		}
		schema := SignalSchema{TimeColumn: sig.time}
		if schema.Attributes, err = e.attributes(ctx, sig, start, end, req.Namespace); err != nil {
			return nil, err
		}
		for _, a := range schema.Attributes {
			for column, key := range derivedColumns {
				if a.Scope == "attributes" && a.Key == key && slices.Contains(sig.lowCard, column) {
					values[column] = a.Values
				}
			}
		}
		for _, f := range sig.fields {
			schema.Columns = append(schema.Columns, ColumnSchema{Field: f, Values: values[f.Name]})
		}
		if name == "metrics" {
			if schema.Metrics, err = e.metricNames(ctx, start, end, req.Namespace); err != nil {
				return nil, err
			}
		}
		if name == "spans" {
			out.Services = values["service"]
		}
		out.Signals[name] = schema
	}
	return out, nil
}

func windowWhere(sig *signal, start, end time.Time, namespace string) (string, []any) {
	column := quoteIdent(sig.time)
	where := fmt.Sprintf("%s >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND %s < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS", column, column)
	args := []any{start, end}
	if namespace != "" {
		where += " AND namespace = ?"
		args = append(args, namespace)
	}
	return where, args
}

// derivedColumns are read from the attributes map by the spans view, so a
// full scan extracts every row's VARIANT, which dominated a 24h discovery.
// Their values come from the attribute sample instead (see Schema).
var derivedColumns = map[string]string{"messaging_system": "messaging.system"}

func (e *Executor) lowCardinality(ctx context.Context, sig *signal, start, end time.Time, namespace string) (map[string][]Option, error) {
	where, args := windowWhere(sig, start, end, namespace)
	// UNPIVOT counts every listed column in one scan and drops NULLs.
	var projection []string
	for _, column := range sig.lowCard {
		if derivedColumns[column] == "" {
			projection = append(projection, fmt.Sprintf("CAST(%s AS VARCHAR) AS %s", quoteIdent(column), quoteIdent(column)))
		}
	}
	query := "SELECT c, v, count(*) AS n FROM (UNPIVOT (SELECT " + strings.Join(projection, ", ") + " FROM " + sig.name + " WHERE " + where +
		") ON COLUMNS(*) INTO NAME c VALUE v) WHERE v <> '' GROUP BY c, v"
	rows, err := e.engine.QueryContext(ctx, query, args...)
	if err != nil {
		if isOperational(err) {
			return nil, err
		}
		return nil, fmt.Errorf("discover %s values: %w", sig.name, err)
	}
	defer rows.Close()
	out := map[string][]Option{}
	for rows.Next() {
		var column string
		var o Option
		if err := rows.Scan(&column, &o.Value, &o.Count); err != nil {
			return nil, err
		}
		out[column] = append(out[column], o)
	}
	for column, opts := range out {
		sort.Slice(opts, func(i, j int) bool {
			return opts[i].Count > opts[j].Count || opts[i].Count == opts[j].Count && opts[i].Value < opts[j].Value
		})
		limit := 12
		if column == "service" {
			limit = 200
		}
		out[column] = opts[:min(len(opts), limit)]
	}
	return out, rows.Err()
}

func (e *Executor) attributes(ctx context.Context, sig *signal, start, end time.Time, namespace string) ([]AttributeSchema, error) {
	where, args := windowWhere(sig, start, end, namespace)
	// Values come from the same sample: the most common 12 scalar values of
	// each key, as text, for completion.
	query := `WITH sample AS (
  SELECT service, to_json(attributes) AS a, to_json(resource) AS r FROM ` + sig.name + ` WHERE ` + where + ` LIMIT 5000
), keyed AS (
  SELECT service, 'attributes' AS scope, unnest(json_keys(a)) AS k, a AS j FROM sample
  UNION ALL
  SELECT service, 'resource' AS scope, unnest(json_keys(r)) AS k, r AS j FROM sample
), typed AS (
  SELECT service, scope, k, json_type(j, '$."' || replace(k, '"', '\"') || '"') AS t, json_extract_string(j, '$."' || replace(k, '"', '\"') || '"') AS v FROM keyed
), keys AS (
  SELECT scope, k, any_value(t) AS t, count(*) AS n,
         coalesce(array_to_string(list(DISTINCT service ORDER BY service) FILTER (WHERE service <> '')[1:3], ', '), '') AS services
  FROM typed GROUP BY scope, k ORDER BY n DESC, k LIMIT 200
), counted AS (
  SELECT scope, k, v, count(*) AS c FROM typed
  WHERE v IS NOT NULL AND v <> '' AND length(v) <= 100 AND t NOT IN ('OBJECT', 'ARRAY') GROUP BY scope, k, v
), top AS (
  SELECT scope, k, CAST(to_json(list({'value': v, 'count': c} ORDER BY c DESC, v)[1:12]) AS VARCHAR) AS vals FROM counted GROUP BY scope, k
)
SELECT keys.scope, keys.k, keys.t, keys.n, keys.services, top.vals
FROM keys LEFT JOIN top USING (scope, k) ORDER BY keys.n DESC, keys.k`
	rows, err := e.engine.QueryContext(ctx, query, args...)
	if err != nil {
		if isOperational(err) {
			return nil, err
		}
		return nil, fmt.Errorf("discover %s attributes: %w", sig.name, err)
	}
	defer rows.Close()
	var out []AttributeSchema
	for rows.Next() {
		var a AttributeSchema
		var jsonType, values *string
		if err := rows.Scan(&a.Scope, &a.Key, &jsonType, &a.Count, &a.Services, &values); err != nil {
			return nil, err
		}
		if values != nil {
			if err := json.Unmarshal([]byte(*values), &a.Values); err != nil {
				return nil, fmt.Errorf("discover %s attribute values: %w", sig.name, err)
			}
		}
		a.Type = "string"
		if jsonType != nil {
			switch *jsonType {
			case "BIGINT", "UBIGINT", "DOUBLE":
				a.Type = "number"
			case "BOOLEAN":
				a.Type = "boolean"
			case "OBJECT", "ARRAY":
				a.Type = "object"
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (e *Executor) metricNames(ctx context.Context, start, end time.Time, namespace string) ([]MetricSchema, error) {
	where, args := windowWhere(signals["metrics"], start, end, namespace)
	rows, err := e.engine.QueryContext(ctx, "SELECT name,coalesce(unit,''),type,coalesce(temporality,''),count(*) FROM metrics WHERE "+where+" GROUP BY name,unit,type,temporality ORDER BY 5 DESC,1,3,4 LIMIT 300", args...)
	if err != nil {
		if isOperational(err) {
			return nil, err
		}
		return nil, fmt.Errorf("discover metric names: %w", err)
	}
	defer rows.Close()
	var out []MetricSchema
	for rows.Next() {
		var m MetricSchema
		if err := rows.Scan(&m.Name, &m.Unit, &m.Type, &m.Temporality, &m.Count); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
