package panel

import (
	"fmt"
	"github.com/labstack/fanout/internal/telemetry"
	"strings"
	"testing"
	"time"
)

func TestLogPanelsPreserveExactWindowRedactionLimitsAndLateBatches(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart
	makeLog := func(ns, service, body string, n int64) telemetry.Log {
		return telemetry.Log{Namespace: ns, ServiceName: service, Body: body, Severity: "ERROR", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: at.UnixNano()}
	}
	start := at.Add(time.Second)
	end := start.Add(time.Minute)
	commit(t, repo, nil, []telemetry.Log{makeLog("shop", "checkout", "start password=secret", start.UnixNano()), makeLog("shop", "checkout", "before", start.UnixNano()-1), makeLog("shop", "checkout", "end", end.UnixNano()), makeLog("other", "checkout", "wrong namespace", start.UnixNano()), makeLog("shop", "payment", "wrong service", start.UnixNano())})
	for i := range 4 {
		commit(t, repo, nil, []telemetry.Log{makeLog("shop", "checkout", fmt.Sprintf("late-%d password=secret", i), start.UnixNano()+int64(i+1))})
	}
	executor := NewExecutor(engine, 30)
	spec := Dashboard{Name: "Logs", Time: Time{From: &start, To: &end}, Panels: []Panel{{ID: "p", Title: "Logs", Viz: "logs", Query: &Query{From: "logs", Where: []string{"namespace = 'shop'", "service = 'checkout'"}, Limit: 3}}}}
	for _, warm := range []bool{false, true} {
		if warm {
			if _, err := engine.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		out, err := executor.Run(t.Context(), RunRequest{Dashboard: spec})
		if err != nil {
			t.Fatal(err)
		}
		f := out[0].Frame
		if out[0].Status != "ok" || f == nil || f.Rows != 3 || !f.Truncated {
			t.Fatalf("bounded rows: %+v", out)
		}
		var bodies []string
		for i, c := range f.Columns {
			if c.Name == "body" {
				for _, v := range f.Values[i] {
					bodies = append(bodies, fmt.Sprint(v))
				}
			}
		}
		if len(bodies) != 3 || !strings.Contains(bodies[0], "late-3") || !strings.Contains(bodies[2], "late-1") {
			t.Fatalf("newest late rows: %v", bodies)
		}
		for _, body := range bodies {
			if strings.Contains(body, "secret") || !strings.Contains(body, "[REDACTED]") {
				t.Fatalf("redaction: %q", body)
			}
		}
	}
	spec.Panels[0].Query.Limit = 100
	out, err := executor.Run(t.Context(), RunRequest{Dashboard: spec})
	if err != nil || out[0].Frame.Rows != 5 {
		t.Fatalf("exact window: %+v %v", out, err)
	}
	spec.Panels[0].Query.Where = append(spec.Panels[0].Query.Where, "body LIKE '%secret%'")
	out, err = executor.Run(t.Context(), RunRequest{Dashboard: spec})
	if err != nil || out[0].Frame.Rows != 0 {
		t.Fatalf("redacted search: %+v %v", out, err)
	}
}
func TestEndpointPanelUsesExactNanosecondBoundaries(t *testing.T) {
	engine, repo := newTestEngine(t)
	start := fixtureStart.Add(time.Second)
	end := start.Add(time.Minute)
	spans := []telemetry.Span{}
	for i, n := range []int64{start.UnixNano() - 1, start.UnixNano(), end.UnixNano() - 1, end.UnixNano()} {
		spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: "checkout", TraceID: fmt.Sprint(i), SpanID: fmt.Sprint(i), HTTPMethod: "GET", HTTPRoute: "/cart", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: fixtureStart.UnixNano(), DurationMS: float64(i + 1)})
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	spec := Dashboard{Name: "Endpoints", Time: Time{From: &start, To: &end}, Panels: []Panel{{ID: "p", Title: "Calls", Viz: "table", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'", "service = 'checkout'"}, Measures: []string{"count()", "p95(duration_ms)"}, By: []string{"http_route"}}}}}
	for _, warm := range []bool{false, true} {
		if warm {
			if _, err := engine.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		out, err := e.Run(t.Context(), RunRequest{Dashboard: spec})
		if err != nil || out[0].Status != "ok" || out[0].Frame.Rows != 1 {
			t.Fatalf("endpoints: %+v %v", out, err)
		}
		found := false
		for i, c := range out[0].Frame.Columns {
			if c.Role == "measure" && out[0].Frame.Values[i][0] == float64(2) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected two exact-window calls: %+v", out[0].Frame)
		}
	}
}
