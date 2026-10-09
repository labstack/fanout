package panel

import (
	"errors"
	"github.com/labstack/fanout/internal/telemetry"
	"math"
	"slices"
	"testing"
	"time"
)

func TestExemplarsCheckedScope(t *testing.T) {
	e := newFixtureExecutor(t)
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", From: fixtureStart.Add(30 * time.Minute), To: fixtureStart.Add(31 * time.Minute), Dimensions: map[string]string{"http_route": "/cart"}}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Traces) != 1 || got.Traces[0].Service != "checkout" || got.Traces[0].DurationMS != 900 || got.Traces[0].Status != "STATUS_CODE_ERROR" {
		t.Fatalf("traces: %+v", got)
	}
	req.Vars = map[string]Value{"service": {Values: []string{"frontend"}}}
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("filter escaped: %+v %v", got, err)
	}
	req.From = fixtureStart.Add(-time.Hour)
	req.To = fixtureStart.Add(2 * time.Hour)
	req.Dimensions = nil
	req.Vars = nil
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 20 || !got.Truncated {
		t.Fatalf("cap: %+v %v", got, err)
	}
}
func TestExemplarsRejectForgedDimensions(t *testing.T) {
	e := newFixtureExecutor(t)
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", From: fixtureStart, To: fixtureStart.Add(time.Hour), Dimensions: map[string]string{"service": "frontend"}}
	_, err := e.Exemplars(t.Context(), req)
	var problems Problems
	want := Problem{Path: "dimensions", Message: "dimensions must name query.by fields and values at most 500 characters", Hint: "select one of the panel grouping fields"}
	if !errors.As(err, &problems) || !slices.Contains(problems, want) {
		t.Fatalf("got %v want %+v", err, want)
	}
	req.Dimensions = map[string]string{"http_route": "x'); DROP TABLE spans; --"}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("hostile value: %+v %v", got, err)
	}
	req.Dashboard.Panels[2].Query.Where = []string{"service IN (SELECT service FROM spans)"}
	if _, err := e.Exemplars(t.Context(), req); err == nil {
		t.Fatal("guard bypass")
	}
}
func TestExemplarBucketValidation(t *testing.T) {
	p := &Panel{Query: &Query{From: "spans", Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}
	bad := math.NaN()
	upper := 1.0
	cases := []struct {
		bucket *SelectionBucket
		want   Problem
	}{
		{&SelectionBucket{Lower: bad}, Problem{Path: "bucket", Message: "bucket needs a spans duration_ms histogram and a finite nonnegative lower bound", Hint: "select a finite duration bucket"}},
		{&SelectionBucket{Lower: 2, Upper: &upper}, Problem{Path: "bucket.upper", Message: "upper must be finite and greater than lower", Hint: "select a larger upper bound or omit it for overflow"}},
	}
	for _, tc := range cases {
		_, _, err := selectionWhere(p, nil, scope, nil, tc.bucket)
		var got Problems
		if !errors.As(err, &got) || !slices.Contains(got, tc.want) {
			t.Fatalf("got %v want %+v", err, tc.want)
		}
	}
}
func TestExemplarsLogsCandidateUsesRedactedBody(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	spans := []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", TraceID: "log-trace", SpanID: "root", Name: "root", DurationMS: 1, StartUnixNanos: n, EndUnixNanos: n + 1000000, IngestedAt: n}}
	logs := []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", TraceID: "log-trace", Body: "token=secret failed", BodyTemplate: "token=secret failed", EventUnixNanos: n, IngestedAt: n}}
	commit(t, repo, spans, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Log lineage", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "bar", Query: &Query{From: "logs", Where: []string{"body LIKE '%failed%'"}, Measures: []string{"count()"}, By: []string{"severity"}}}}}
	req := ExemplarRequest{Dashboard: d, PanelID: "p", From: fixtureStart, To: fixtureStart.Add(time.Minute)}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 1 || got.Traces[0].TraceID != "log-trace" {
		t.Fatalf("logs candidate: %+v %v", got, err)
	}
	req.Dashboard.Panels[0].Query.Where = []string{"body LIKE '%secret%'"}
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("searched unredacted source: %+v %v", got, err)
	}
}
