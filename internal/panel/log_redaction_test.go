package panel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

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
		"SELECT body FROM memory.main.logs WHERE $__window(time)",
		"SELECT body FROM read_parquet('/private/telemetry/logs.parquet') WHERE $__window(time)",
		"SELECT body, getvariable('secret') FROM logs WHERE $__window(time)",
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

func TestQualifiedLogColumnsFailClosed(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{Namespace: "shop", Body: "token=private failed", BodyTemplate: "token=private failed", EventUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Qualified logs", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Qualified", Viz: "table", SQL: "SELECT main.logs.body FROM main.logs WHERE $__window(time)"}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err == nil {
		t.Fatalf("qualified column unexpectedly accepted: %+v", got)
	}
	var problems Problems
	if !errors.As(err, &problems) || !strings.Contains(err.Error(), "Referenced table") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe qualified failure: %v", err)
	}
	t.Logf("deferred M4 redaction-safe failure: %s", err)
}
