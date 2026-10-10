package panel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

func TestSQLPanelExecutionMatchesDescribedStructBinding(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{Body: "token=private failed", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	text := "SELECT telemetry.logs.body AS b FROM (SELECT {'body': 42} AS logs) AS telemetry, logs WHERE $__window(logs.time)"
	expanded, err := expandMacros(text, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalSQL(t.Context(), engine, expanded)
	if err != nil {
		t.Fatal(err)
	}
	query, describe, args, err := bindParams(canonical, func(string) ([]any, bool, error) { return []any{fixtureStart}, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	var name, logicalType string
	var rest [4]any
	if err := engine.DB.QueryRowContext(t.Context(), "DESCRIBE "+describe).Scan(&name, &logicalType, &rest[0], &rest[1], &rest[2], &rest[3]); err != nil {
		t.Fatal(err)
	}
	if logicalType != "INTEGER" {
		t.Fatalf("described type = %s", logicalType)
	}
	prepared, _, err := engine.PrepareTelemetrySQL(t.Context(), query, describe, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Execute with the production snapshot path and an actual nonempty window.
	ctx := queryrows.WithWindow(context.Background(), queryrows.Window{Start: fixtureStart, End: fixtureStart.Add(time.Hour)})
	args[1] = fixtureStart.Add(time.Hour)
	rows, err := engine.QueryContext(ctx, prepared, args...)
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing row")
	}
	err = rows.Scan(&got)
	rows.Close()
	if err != nil || got != int32(42) {
		t.Fatalf("described INTEGER 42, executed %T %v: %v", got, got, err)
	}
	d := Dashboard{Name: "Struct binding", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "SQL", Viz: "table", SQL: text}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(results) != 1 || results[0].Status != StatusOK || results[0].Frame.Columns[0].Type != "number" || results[0].Frame.Values[0][0] != float64(42) {
		t.Fatalf("panel execution: %+v %v", results, err)
	}
}

func TestSQLPanelKeepsQualifiedNamesThatACTEShadows(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{Body: "token=private failed", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	// DuckDB binds main.trace_candidates.x to the struct alias, never to the CTE.
	text := "WITH trace_candidates AS (SELECT 'cte' AS x) SELECT main.trace_candidates.x AS b FROM (SELECT {'x': 42} AS trace_candidates) AS main, trace_candidates, logs WHERE $__window(logs.time)"
	d := Dashboard{Name: "CTE shadow", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "SQL", Viz: "table", SQL: text}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(results) != 1 || results[0].Status != StatusOK || results[0].Frame.Values[0][0] != float64(42) {
		t.Fatalf("panel execution: %+v %v", results, err)
	}
}

func TestSQLPanelPreservesLiteralPrecisionAndSampling(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{Body: "token=private failed", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	// Project exact literals as strings because panel numeric display values are float64.
	projection := "SELECT 9007199254740993::VARCHAR AS integer, epoch_ns(make_timestamp_ns(1791370800000000001))::VARCHAR AS nanos, body "
	for _, test := range []struct{ name, query string }{
		{"literals", "FROM logs WHERE $__window(time)"},
		{"query_sample", "FROM logs WHERE $__window(time) USING SAMPLE 100 PERCENT (bernoulli)"},
		{"table_sample", "FROM (SELECT * FROM logs TABLESAMPLE 100 PERCENT (bernoulli)) l WHERE $__window(l.time)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			text := projection + test.query
			d := Dashboard{Name: "Exact literals", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "SQL", Viz: "table", SQL: text}}}
			results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || len(results) != 1 || results[0].Status != StatusOK {
				t.Fatalf("execution: %+v %v", results, err)
			}
			f := results[0].Frame
			if f.Rows != 1 || f.Values[0][0] != "9007199254740993" || f.Values[1][0] != "1791370800000000001" || f.Values[2][0] != "token=[REDACTED] failed" {
				t.Fatalf("precision or redaction changed: %+v", f)
			}
		})
	}
}

func TestLogReadRedaction(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, []telemetry.Span{{Namespace: "n", TraceID: "t", SpanID: "r", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n}}, []telemetry.Log{{Namespace: "n", TraceID: "t", Body: "token=bodysecret failed", BodyTemplate: "token=templatesecret failed", EventUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, field := range []string{"body", "body_template"} {
		t.Run(field, func(t *testing.T) {
			d := exemplarDashboard("logs")
			d.Panels[0].Query.By = []string{field}
			results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || len(results) != 1 || results[0].Status != "ok" {
				t.Fatalf("grouping: %+v %v", results, err)
			}
			data, _ := json.Marshal(results)
			if strings.Contains(string(data), "bodysecret") || strings.Contains(string(data), "templatesecret") || !strings.Contains(string(data), "[REDACTED]") {
				t.Fatalf("grouping leaked: %s", data)
			}
			d.Panels[0].Query.Where = []string{field + " LIKE '%secret%'"}
			results, err = e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || results[0].Status != "empty" {
				t.Fatalf("raw search matched: %+v %v", results, err)
			}
			d.Panels[0].Query.Where = []string{field + " = 'missing'"}
			results, err = e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || strings.Contains(results[0].Diagnosis, "secret") || !strings.Contains(results[0].Diagnosis, "[REDACTED]") {
				t.Fatalf("diagnosis leaked: %+v %v", results, err)
			}
			d.Panels[0].Query.Where = []string{field + " LIKE '%secret%'", "service = 'missing'"}
			results, err = e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || !strings.Contains(results[0].Diagnosis, "filters together match none") {
				t.Fatalf("diagnosis counted a raw secret: %+v %v", results, err)
			}
			d.Panels[0].Query.Where = nil
			d.Variables = []Variable{{Name: "text", Kind: "query", From: "logs", Field: field}}
			options, err := e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: d})
			data, _ = json.Marshal(options)
			if err != nil || strings.Contains(string(data), "bodysecret") || strings.Contains(string(data), "templatesecret") || !strings.Contains(string(data), "[REDACTED]") {
				t.Fatalf("options leaked: %s %v", data, err)
			}
			d.Variables[0].Where = []string{field + " LIKE '%secret%'"}
			options, err = e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: d})
			if err != nil || len(options["text"]) != 0 {
				t.Fatalf("variable raw search: %+v %v", options, err)
			}
			req := exemplarRequest(d)
			req.Dashboard.Variables = nil
			req.Dashboard.Panels[0].Query.Where = []string{field + " LIKE '%secret%'"}
			got, err := e.Exemplars(t.Context(), req)
			if err != nil || len(got.Traces) != 0 {
				t.Fatalf("exemplar raw search: %+v %v", got, err)
			}
		})
	}
	for _, sql := range []string{
		"SELECT body, body_template FROM logs WHERE $__window(time)",
		"SELECT body FROM logs WHERE $__window(time) AND body LIKE '%secret%'",
		"SELECT l.body FROM main.logs l WHERE $__window(l.time)",
		"WITH x AS (SELECT * FROM logs WHERE $__window(time)) SELECT body FROM x",
		"WITH history AS (SELECT body, time FROM main.logs) SELECT body FROM history WHERE $__window(time)",
		"WITH history AS (SELECT service, time FROM main.logs) SELECT service FROM history WHERE $__window(time)",
		"SELECT logs.body FROM logs WHERE $__window(logs.time)",
		"WITH history AS (SELECT service FROM spans) SELECT l.body FROM main.logs l WHERE $__window(l.time)",
		"SELECT body FROM telemetry.logs WHERE $__window(ingested_at)",
		"WITH x AS (SELECT 1 AS one) SELECT body FROM logs, x WHERE $__window(time)",
	} {
		t.Run(sql, func(t *testing.T) {
			d := exemplarDashboard("logs")
			d.Panels[0] = Panel{ID: "p", Title: "SQL", Viz: "table", SQL: sql}
			results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || len(results) != 1 || results[0].Status == "error" {
				t.Fatalf("SQL redaction: %+v %v", results, err)
			}
			data, _ := json.Marshal(results)
			if strings.Contains(string(data), "bodysecret") || strings.Contains(string(data), "templatesecret") {
				t.Fatalf("SQL leaked: %s", data)
			}
			if strings.Contains(sql, "LIKE") && results[0].Status != "empty" {
				t.Fatalf("SQL raw filter matched: %s", data)
			}
		})
	}
}

func TestSQLLogRedactionKeepsBoundary(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, sql := range []string{
		"SELECT body FROM private.logs WHERE $__window(time)",
		"SELECT private.logs.body FROM private.logs WHERE $__window(time)",
		"SELECT body FROM memory.main.logs WHERE $__window(time)",
		"SELECT memory.main.logs.body FROM memory.main.logs WHERE $__window(time)",
		"SELECT body FROM read_parquet('/private/telemetry/logs.parquet') WHERE $__window(time)",
		"SELECT body, getvariable('secret') FROM logs WHERE $__window(time)",
		"SELECT current_query() FROM logs WHERE $__window(time)",
		"SELECT current_query_id() FROM logs WHERE $__window(time)",
		"SELECT pg_get_viewdef(22636) FROM logs WHERE $__window(time)",
		"SELECT pg_get_constraintdef(1) FROM logs WHERE $__window(time)",
		"SELECT format_type(1, NULL) FROM logs WHERE $__window(time)",
		"SELECT get_block_size(current_database()) FROM logs WHERE $__window(time)",
		"SELECT write_log('boundary probe') FROM logs WHERE $__window(time)",
		"SELECT t.i, pg_get_viewdef(t.i) FROM range(22636,22673) AS t(i), (SELECT time FROM logs LIMIT 1) l WHERE $__window(l.time) AND pg_get_viewdef(t.i) IS NOT NULL",
	} {
		d := exemplarDashboard("logs")
		d.Panels[0] = Panel{ID: "p", Title: "SQL", Viz: "table", SQL: sql}
		_, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		var problems Problems
		if !errors.As(err, &problems) {
			t.Fatalf("unsafe SQL accepted: %s %v", sql, err)
		}
	}
}

func TestQualifiedLogColumnsReturnOnlyRedactedValues(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", TraceID: "t", SpanID: "s", StartUnixNanos: n, EndUnixNanos: n + 1000000, IngestedAt: n}}, []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", Body: "token=private failed", BodyTemplate: "token=private failed", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, text := range []string{
		"SELECT main.logs.body,main.logs.body_template FROM main.logs WHERE $__window(time)",
		"SELECT telemetry.logs.body FROM telemetry.logs WHERE $__window(time)",
		"SELECT main.logs.body FROM logs WHERE $__window(time)",
		"SELECT logs.body FROM main.logs WHERE $__window(logs.time)",
		"SELECT l.body FROM main.logs AS l WHERE $__window(l.time)",
		"WITH history AS (SELECT main.logs.body,time FROM main.logs) SELECT body FROM history WHERE $__window(time)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND main.logs.body LIKE '%private%'",
		"SELECT main.logs.body AS message FROM main.logs WHERE $__window(time) ORDER BY main.logs.body_template",
		"SELECT main.logs.body FROM main.logs JOIN telemetry.logs AS other ON main.logs.body = other.body WHERE $__window(logs.time)",
		"SELECT history.body FROM (SELECT telemetry.logs.body,time FROM telemetry.logs) AS history WHERE $__window(history.time)",
		`SELECT "main"."logs"."body" AS "message" FROM "main"."logs" WHERE $__window(time)`,
		"SELECT l.body AS left_body,r.body_template AS right_body FROM main.logs AS l JOIN telemetry.logs AS r ON l.body = r.body WHERE $__window(l.time)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND EXISTS (SELECT 1 FROM spans AS s WHERE s.service = main.logs.service)",
		"SELECT l.body FROM main.logs AS l WHERE $__window(l.time) AND EXISTS (SELECT 1 FROM spans AS s WHERE s.service = l.service)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND EXISTS (SELECT 1 FROM main.logs AS inner_logs WHERE inner_logs.body = main.logs.body)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND EXISTS (SELECT 1 FROM main.logs WHERE main.logs.body LIKE '%REDACTED%')",
		"SELECT main.logs.body, 'main.logs.body' AS literal FROM main.logs WHERE $__window(time)",
		"SELECT main.logs.body AS message FROM main.logs WHERE $__window(time) AND main.logs.body = $message",
		"SELECT l.body AS message FROM main.logs AS l(tenant) WHERE $__window(l.time) AND l.tenant = 'shop'",
		`SELECT "main.logs"."body" FROM main.logs AS "main.logs" WHERE $__window(time)`,
		"WITH history AS (WITH recent AS (SELECT telemetry.logs.body,time FROM telemetry.logs) SELECT * FROM recent) SELECT body FROM history WHERE $__window(time)",
		"SELECT logs.* FROM main.logs WHERE $__window(time)",
	} {
		t.Run(text, func(t *testing.T) {
			d := Dashboard{Name: "Qualified logs", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Qualified", Viz: "table", SQL: text}}}
			if strings.Contains(text, "$message") {
				d.Variables = []Variable{{Name: "message", Kind: "constant", Value: "token=[REDACTED] failed"}}
			}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || len(got) != 1 || got[0].Status == StatusError {
				t.Fatalf("qualified execution: %+v %v", got, err)
			}
			wire, _ := json.Marshal(got[0].Frame)
			if strings.Contains(string(wire), "private") {
				t.Fatalf("body leaked: %s", wire)
			}
			if strings.Contains(text, "'%private%'") {
				if got[0].Status != StatusEmpty {
					t.Fatalf("raw body matched: %+v", got[0])
				}
			} else if got[0].Frame.Rows != 1 || !strings.Contains(string(wire), "[REDACTED]") {
				t.Fatalf("missing redacted row: %s", wire)
			}
			if strings.Contains(text, "AS literal") && got[0].Frame.Values[1][0] != "main.logs.body" {
				t.Fatalf("SQL literal changed: %s", wire)
			}
			if (strings.Contains(text, "AS message") || strings.Contains(text, `AS "message"`)) && got[0].Frame.Columns[0].Name != "message" {
				t.Fatalf("column alias changed: %s", wire)
			}
		})
	}

	for _, text := range []string{
		// DuckDB's native parser rejects multiple qualifiers before a STAR.
		"SELECT main.logs.* FROM main.logs WHERE $__window(time)",
		"SELECT telemetry.logs.* FROM telemetry.logs WHERE $__window(time)",
		"SELECT main.logs.body FROM main.logs AS l WHERE $__window(l.time)",
		"SELECT telemetry.logs.body FROM telemetry.logs AS l WHERE $__window(l.time)",
		"SELECT main.logs.body FROM main.logs, main.logs WHERE $__window(logs.time)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND EXISTS (SELECT 1 FROM spans AS logs WHERE main.logs.body IS NOT NULL)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND EXISTS (SELECT 1 FROM (SELECT service FROM spans) AS logs WHERE main.logs.body IS NOT NULL)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND EXISTS (SELECT 1 FROM main.logs AS logs WHERE main.logs.body IS NOT NULL)",
		"SELECT main.logs.body FROM main.logs WHERE $__window(time) AND EXISTS (WITH history AS (SELECT main.logs.body) SELECT * FROM history)",
		"WITH logs AS (SELECT 1 AS body) SELECT body FROM logs WHERE $__window(time)",
		"SELECT main.logs.body FROM telemetry.logs WHERE $__window(time)",
		"SELECT telemetry.logs.body FROM main.logs WHERE $__window(time)",
	} {
		t.Run(text, func(t *testing.T) {
			d := Dashboard{Name: "Qualified logs", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Qualified", Viz: "table", SQL: text}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			var problems Problems
			if !errors.As(err, &problems) || strings.Contains(err.Error(), "token=private") {
				t.Fatalf("invalid qualified reference accepted or secret leaked: %+v %v", got, err)
			}
		})
	}
}
