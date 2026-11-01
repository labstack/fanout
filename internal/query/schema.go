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
	// Cached answers only from the cache, never waiting for a discovery.
	Cached bool
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
	// Types lists every scalar type seen when rows disagree, e.g. a status
	// code stored as a number by one service and as text by another.
	Types []string `json:"types,omitempty"`
	// Numbers lists the sampled values some rows store as numbers, with
	// those rows' counts, when rows disagree on the type.
	Numbers []Option `json:"numbers,omitempty"`
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
	used           time.Time
	flight         *schemaFlight
	namespaces     []string
}

type schemaFlight struct {
	done   chan struct{}
	schema *Schema
	err    error
}

const (
	schemaTTL        = time.Minute
	schemaStale      = 10 * time.Minute
	maxSchemaEntries = 64
	namespacesKey    = "\x00namespaces"
)

// namespaceMu serializes the namespace listing so a burst runs it once.
var namespaceMu sync.Mutex

// ErrSchemaBusy answers a new window and namespace when the cache holds only
// in-flight entries, and a caller whose deadline passes while its discovery
// is still running. Cached keys are always served.
var ErrSchemaBusy = errors.New("schema discovery is busy; try again shortly")

// ErrSchemaNotCached answers a Cached request for a key with no schema yet.
var ErrSchemaNotCached = errors.New("schema is not cached")

// Schema describes what telemetry exists: columns with their common values,
// attribute keys and values sampled from recent rows, metric names and
// services. It is the agent's map before it drafts panels. An entry is fresh
// for a minute and then served stale while one background refresh runs.
// Concurrent callers of a new key share one discovery, which runs on after
// they leave, until the executor closes, so the next request finds it
// cached. Discoveries are bounded: two run at a time, each waits at most
// discoverWait for a slot and runs at most discoverRun, windows are the
// fixed set and a namespace must have data in the last 24h. A Cached request
// is answered at once.
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
		entry.used = now
		e.schemas[key] = entry
		if !now.Before(entry.expires) && entry.flight == nil {
			entry.flight = &schemaFlight{done: make(chan struct{})}
			e.schemas[key] = entry
			e.startDiscovery(key, req, span, entry.flight)
		}
		e.mu.Unlock()
		return entry.schema, nil
	}
	e.mu.Unlock()
	if req.Cached {
		return nil, ErrSchemaNotCached
	}
	if req.Namespace != "" {
		if err := e.checkNamespace(ctx, req.Namespace); err != nil {
			return nil, err
		}
	}
	e.mu.Lock()
	entry = e.schemas[key]
	flight := entry.flight
	if flight == nil {
		if !e.makeSchemaRoom() {
			e.mu.Unlock()
			return nil, ErrSchemaBusy
		}
		flight = &schemaFlight{done: make(chan struct{})}
		e.schemas[key] = schemaEntry{flight: flight}
		e.startDiscovery(key, req, span, flight)
	}
	e.mu.Unlock()
	select {
	case <-flight.done:
		return flight.schema, flight.err
	case <-ctx.Done():
		// The discovery runs on; a caller out of time is told to come back.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrSchemaBusy
		}
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

// makeSchemaRoom removes entries past their stale horizon, then, while the
// table is full, the least recently used finished entry. In-flight entries
// are never evicted, so a new key is refused only when every slot is one.
func (e *Executor) makeSchemaRoom() bool {
	now := e.now()
	for k, v := range e.schemas {
		if v.flight == nil && k != namespacesKey && !now.Before(v.stale) {
			delete(e.schemas, k)
		}
	}
	for len(e.schemas) >= maxSchemaEntries {
		oldest := ""
		for k, v := range e.schemas {
			if v.flight == nil && k != namespacesKey && (oldest == "" || v.used.Before(e.schemas[oldest].used)) {
				oldest = k
			}
		}
		if oldest == "" {
			return false
		}
		delete(e.schemas, oldest)
	}
	return true
}

// startDiscovery runs flight detached from its callers, under the
// executor's lifetime; once Close has run, it fails at once. The caller
// holds e.mu, as Close does to cancel.
func (e *Executor) startDiscovery(key string, req SchemaRequest, span time.Duration, flight *schemaFlight) {
	if err := e.life.Err(); err != nil {
		flight.err = err
		go e.settle(key, flight)
		return
	}
	e.running.Add(1)
	go func() {
		defer e.running.Done()
		e.discoverFlight(req, span, flight)
		e.settle(key, flight)
	}()
}

func (e *Executor) discoverFlight(req SchemaRequest, span time.Duration, flight *schemaFlight) {
	wait := time.NewTimer(e.discoverWait)
	select {
	case e.discoveries <- struct{}{}:
		wait.Stop()
		ctx, cancel := context.WithTimeout(e.life, e.discoverRun)
		flight.schema, flight.err = e.discover(ctx, req, span)
		cancel()
		<-e.discoveries
	case <-wait.C:
		flight.err = ErrSchemaBusy
	case <-e.life.Done():
		wait.Stop()
		flight.err = e.life.Err()
	}
}

// settle caches flight's schema, or drops or keeps the stale entry on
// failure, and releases its waiters.
func (e *Executor) settle(key string, flight *schemaFlight) {
	e.mu.Lock()
	if entry := e.schemas[key]; entry.flight == flight {
		switch {
		case flight.err == nil:
			now := e.now()
			e.schemas[key] = schemaEntry{schema: flight.schema, expires: now.Add(schemaTTL), stale: now.Add(schemaTTL + schemaStale), used: now}
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
		for column, keys := range derivedColumns {
			if _, ok := sig.index[column]; ok {
				values[column] = derivedValues(schema.Attributes, keys)
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

// derivedColumns hold an attribute's value: ingest copies it into a span
// column, or the spans view reads it from the attributes map, where a full
// scan extracts every row's VARIANT. Their values come from the attribute
// sample instead, so completion offers them like the attribute's.
var derivedColumns = map[string][]string{
	"messaging_system": {"messaging.system"},
	"rpc_service":      {"rpc.service"},
}

// valueColumns are scanned for their common values without being low
// cardinality, so completion can offer them.
var valueColumns = map[string][]string{"spans": {"operation"}}

// derivedValues merges the sampled values of keys, most common first.
func derivedValues(attributes []AttributeSchema, keys []string) []Option {
	counts := map[string]int64{}
	for _, a := range attributes {
		if a.Scope == "attributes" && slices.Contains(keys, a.Key) {
			for _, v := range a.Values {
				counts[v.Value] += v.Count
			}
		}
	}
	var out []Option
	for v, n := range counts {
		out = append(out, Option{Value: v, Count: n})
	}
	sortOptions(out)
	return out[:min(len(out), 12)]
}

func sortOptions(opts []Option) {
	sort.Slice(opts, func(i, j int) bool {
		return opts[i].Count > opts[j].Count || opts[i].Count == opts[j].Count && opts[i].Value < opts[j].Value
	})
}

func (e *Executor) lowCardinality(ctx context.Context, sig *signal, start, end time.Time, namespace string) (map[string][]Option, error) {
	where, args := windowWhere(sig, start, end, namespace)
	// UNPIVOT counts every listed column in one scan and drops NULLs.
	var projection []string
	for _, column := range append(slices.Clone(sig.lowCard), valueColumns[sig.name]...) {
		if derivedColumns[column] == nil {
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
		sortOptions(opts)
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
	// Decoding a row's VARIANT maps dominates discovery, so it reads a fixed
	// sample of rows, once: inlined, each reference reruns the scan. Spans
	// sort by trace, so the sample spans services. json_each parses each map
	// once rather than once per key. Values come from the same sample: the
	// most common 12 scalar values of each key, as text, for completion.
	query := `WITH sample AS MATERIALIZED (
  SELECT service, to_json(attributes) AS a, to_json(resource) AS r FROM ` + sig.name + ` WHERE ` + where + ` LIMIT 1000
), typed AS MATERIALIZED (
  SELECT service, 'attributes' AS scope, e.key AS k, e.type AS t, e.value ->> '$' AS v FROM sample, json_each(a) AS e
  UNION ALL
  SELECT service, 'resource' AS scope, e.key AS k, e.type AS t, e.value ->> '$' AS v FROM sample, json_each(r) AS e
), keys AS (
  SELECT scope, k, any_value(t) AS t, count(*) AS n,
         coalesce(array_to_string(list(DISTINCT service ORDER BY service) FILTER (WHERE service <> '')[1:3], ', '), '') AS services,
         array_to_string(list(DISTINCT t ORDER BY t) FILTER (WHERE t NOT IN ('OBJECT', 'ARRAY', 'NULL')), ',') AS ts
  FROM typed GROUP BY scope, k ORDER BY n DESC, k LIMIT 200
), counted AS (
  SELECT scope, k, v, count(*) AS c, count(*) FILTER (WHERE t IN ('BIGINT', 'UBIGINT', 'DOUBLE')) AS cn FROM typed
  WHERE v IS NOT NULL AND v <> '' AND length(v) <= 100 AND t NOT IN ('OBJECT', 'ARRAY') GROUP BY scope, k, v
), top AS (
  SELECT scope, k, CAST(to_json(list({'value': v, 'count': c, 'numbers': cn} ORDER BY c DESC, v)[1:12]) AS VARCHAR) AS vals FROM counted GROUP BY scope, k
)
SELECT keys.scope, keys.k, keys.t, keys.n, keys.services, keys.ts, top.vals
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
		var jsonType, types, values *string
		if err := rows.Scan(&a.Scope, &a.Key, &jsonType, &a.Count, &a.Services, &types, &values); err != nil {
			return nil, err
		}
		var sampled []struct {
			Value   string
			Count   int64
			Numbers int64
		}
		if values != nil {
			if err := json.Unmarshal([]byte(*values), &sampled); err != nil {
				return nil, fmt.Errorf("discover %s attribute values: %w", sig.name, err)
			}
		}
		for _, v := range sampled {
			a.Values = append(a.Values, Option{Value: v.Value, Count: v.Count})
		}
		if jsonType != nil {
			a.Type = attributeType(*jsonType)
		} else {
			a.Type = "string"
		}
		if types != nil {
			for t := range strings.SplitSeq(*types, ",") {
				if t := attributeType(t); !slices.Contains(a.Types, t) {
					a.Types = append(a.Types, t)
				}
			}
			if len(a.Types) < 2 {
				a.Types = nil
			}
			for _, v := range sampled {
				if a.Types != nil && v.Numbers > 0 {
					a.Numbers = append(a.Numbers, Option{Value: v.Value, Count: v.Numbers})
				}
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// attributeType names a JSON type as the schema reports it.
func attributeType(jsonType string) string {
	switch jsonType {
	case "BIGINT", "UBIGINT", "DOUBLE":
		return "number"
	case "BOOLEAN":
		return "boolean"
	case "OBJECT", "ARRAY":
		return "object"
	}
	return "string"
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
