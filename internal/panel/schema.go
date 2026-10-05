package panel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type SchemaRequest struct {
	Window    string
	Namespace string
}

type Schema struct {
	Window   string                  `json:"window"`
	Signals  map[string]SignalSchema `json:"signals"`
	Services []Option                `json:"services"`
	Measures []string                `json:"measures"`
	Units    []string                `json:"units"`
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
	Key      string `json:"key"`
	Scope    string `json:"scope"`
	Type     string `json:"type"`
	Count    int64  `json:"count"`
	Services string `json:"services,omitempty"`
}

type MetricSchema struct {
	Name  string `json:"name"`
	Unit  string `json:"unit,omitempty"`
	Type  string `json:"type"`
	Count int64  `json:"count"`
}

type schemaEntry struct {
	schema  *Schema
	expires time.Time
}

const (
	schemaTTL        = time.Minute
	maxSchemaEntries = 64
)

// Schema describes what telemetry exists: columns with their common values,
// attribute keys sampled from recent rows, metric names and services. It is
// the agent's map before it drafts panels, and is cached for a minute.
func (e *Executor) Schema(ctx context.Context, req SchemaRequest) (*Schema, error) {
	if req.Window == "" {
		req.Window = "1h"
	}
	span, ok := ranges[req.Window]
	if !ok || span > 24*time.Hour {
		return nil, Problems{{Path: "window", Message: "window must be one of 5m, 15m, 1h, 3h, 6h, 12h or 24h"}}
	}
	key := req.Window + "|" + req.Namespace
	e.mu.Lock()
	entry, ok := e.schemas[key]
	e.mu.Unlock()
	if ok && e.now().Before(entry.expires) {
		return entry.schema, nil
	}
	end := e.now().UTC()
	start := end.Add(-span)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end, Namespace: req.Namespace})
	out := &Schema{Window: req.Window, Signals: map[string]SignalSchema{}, Measures: measureNames(), Units: unitNames()}
	for _, name := range SignalNames() {
		sig := signals[name]
		values, err := e.lowCardinality(ctx, sig, start, end, req.Namespace)
		if err != nil {
			return nil, err
		}
		schema := SignalSchema{TimeColumn: sig.time}
		for _, f := range sig.fields {
			schema.Columns = append(schema.Columns, ColumnSchema{Field: f, Values: values[f.Name]})
		}
		if schema.Attributes, err = e.attributes(ctx, sig, start, end, req.Namespace); err != nil {
			return nil, err
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
	e.mu.Lock()
	now := e.now()
	for k, v := range e.schemas {
		if !now.Before(v.expires) {
			delete(e.schemas, k)
		}
	}
	if len(e.schemas) >= maxSchemaEntries {
		clear(e.schemas)
	}
	e.schemas[key] = schemaEntry{schema: out, expires: now.Add(schemaTTL)}
	e.mu.Unlock()
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

func (e *Executor) lowCardinality(ctx context.Context, sig *signal, start, end time.Time, namespace string) (map[string][]Option, error) {
	where, args := windowWhere(sig, start, end, namespace)
	parts := make([]string, 0, len(sig.lowCard))
	for _, column := range sig.lowCard {
		parts = append(parts, fmt.Sprintf("SELECT %s AS c, CAST(%s AS VARCHAR) AS v, count(*) AS n FROM base WHERE %s IS NOT NULL AND CAST(%s AS VARCHAR) <> '' GROUP BY 2",
			sqlString(column), quoteIdent(column), quoteIdent(column), quoteIdent(column)))
	}
	query := "WITH base AS (SELECT * FROM " + sig.name + " WHERE " + where + ") " + strings.Join(parts, " UNION ALL ")
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
	query := `WITH sample AS (
  SELECT service, to_json(attributes) AS a, to_json(resource) AS r FROM ` + sig.name + ` WHERE ` + where + ` LIMIT 5000
), keyed AS (
  SELECT service, 'attributes' AS scope, unnest(json_keys(a)) AS k, a AS j FROM sample
  UNION ALL
  SELECT service, 'resource' AS scope, unnest(json_keys(r)) AS k, r AS j FROM sample
)
SELECT scope, k, any_value(json_type(j, '$."' || replace(k, '"', '\"') || '"')) AS t, count(*) AS n,
       array_to_string(list(DISTINCT service ORDER BY service)[1:3], ', ') AS services
FROM keyed GROUP BY scope, k ORDER BY n DESC, k LIMIT 200`
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
		var jsonType *string
		if err := rows.Scan(&a.Scope, &a.Key, &jsonType, &a.Count, &a.Services); err != nil {
			return nil, err
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
	rows, err := e.engine.QueryContext(ctx, "SELECT name, coalesce(any_value(unit), ''), coalesce(any_value(type), ''), count(*) FROM metrics WHERE "+where+" GROUP BY 1 ORDER BY 4 DESC, 1 LIMIT 300", args...)
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
		if err := rows.Scan(&m.Name, &m.Unit, &m.Type, &m.Count); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
