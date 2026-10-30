package query

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

func TestDiagnosisHighlightOnlyWhenActiveFiltersMatch(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{ServiceName: "checkout", Namespace: "shop", Body: "request accepted", BodyTemplate: "request accepted", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, viz := range []string{"logs", "log_patterns"} {
		t.Run(viz, func(t *testing.T) {
			p := Panel{ID: "logs", Title: "Logs", Viz: viz, Options: &Options{Highlight: "error"}, Query: &Spec{From: "logs", Where: []string{"service = 'missing'"}}}
			if viz == "log_patterns" {
				p.Query.Measures = []string{"count()"}
				p.Query.By = []string{"body_template"}
				p.Query.Bucket = "auto"
			}
			d := Dashboard{Name: "Diagnosis", Time: Time{Range: "1h"}, Panels: []Panel{p}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil {
				t.Fatal(err)
			}
			if r := got[0]; r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "for service missing") || strings.Contains(r.Diagnosis, "No logs contain") {
				t.Errorf("filter-caused empty result blames highlight: %+v", r)
			}
			d.Panels[0].Query.Where = []string{"service = 'checkout'"}
			got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil {
				t.Fatal(err)
			}
			if r := got[0]; r.Status != StatusEmpty || r.Diagnosis != `No logs contain "error" in this range` {
				t.Errorf("highlight-caused emptiness not diagnosed: %+v", r)
			}
		})
	}
}

func TestDiagnosisNamesOnlyEmptyingRouteFilter(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := shopSpans()
	for i := range spans {
		spans[i].HTTPRoute = ""
		spans[i].Attributes["http.route"] = ""
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := shopDashboard()
	// The causal filter is deliberately beyond the old six-filter cutoff.
	d.Panels = []Panel{{ID: "empty", Title: "Empty", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{
		"service = $service", "kind = 'SPAN_KIND_SERVER'", "http_method = 'GET'", "duration_ms > 0", "status <> 'unknown'", "namespace = 'shop'", "http_route <> ''",
	}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Status != "empty" || !strings.Contains(r.Diagnosis, "Without HTTP route filter, 120 match the remaining filters.") {
		t.Fatalf("result = %+v", r)
	}
	if strings.Contains(r.Diagnosis, "Without service = $service") {
		t.Fatalf("wrong filter blamed: %s", r.Diagnosis)
	}
}

func TestDiagnosisReportsCombinationWhenEachFilterMatchesAlone(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, []telemetry.Span{
		{ServiceName: "checkout", Kind: "SPAN_KIND_SERVER", Name: "PlaceOrder", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n},
		{ServiceName: "fraud-detection", Kind: "SPAN_KIND_CONSUMER", Name: "orders process", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n},
	}, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	service, kind := "service = 'checkout'", "kind = 'SPAN_KIND_CONSUMER'"
	d := Dashboard{Name: "Diagnosis", Time: Time{Range: "1h"}}
	for i, where := range [][]string{{service}, {kind}, {service, kind}} {
		d.Panels = append(d.Panels, Panel{ID: fmt.Sprintf("p%d", i), Title: "Spans", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: where}})
	}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results[:2] {
		if r.Status != StatusOK || r.Frame == nil || r.Frame.Rows != 1 {
			t.Fatalf("filter alone should match: %+v", r)
		}
	}
	// Removing either filter recovers a row. Author order breaks the tie,
	// but checkout is present, so the diagnosis must name the combination.
	want := "No spans match this combination of filters in this time range. Without service checkout, 1 match the remaining filters. Values seen: fraud-detection (1)."
	if r := results[2]; r.Status != StatusEmpty || r.Diagnosis != want {
		t.Fatalf("result = %+v, want diagnosis %q", r, want)
	}
}

func TestDiagnosisChoosesMostSelectiveQualifyingFilter(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	// Removing service recovers 60 frontend rows; removing route recovers
	// 120 checkout rows, so route is the more selective conflicting filter.
	d.Panels = []Panel{{ID: "empty", Title: "Empty", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{"service = $service", "http_route = '/api/cart'"}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	want := "No spans match this combination of filters in this time range. Without HTTP route /api/cart, 120 match the remaining filters. Values seen: /cart (60), /quote (60)."
	// Each filter alone matches spans: checkout has 120 and /api/cart has
	// 60. Only their combination is empty; neither filter is absent globally.
	if r := results[0]; r.Status != StatusEmpty || r.Diagnosis != want {
		t.Fatalf("result = %+v", r)
	}
}

func TestDiagnosisSkipsFailingFilterRerun(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, []telemetry.Span{
		{ServiceName: "checkout", HTTPStatusCode: "200", HTTPRoute: "/ok", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n},
		{ServiceName: "frontend", HTTPStatusCode: "", HTTPRoute: "/ok", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n},
	}, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Diagnosis", Time: Time{Range: "1h"}, Panels: []Panel{{
		ID: "empty", Title: "Empty", Viz: "table", Query: &Spec{
			From: "spans", Measures: []string{"count()"}, By: []string{"service"},
			Where: []string{"service = 'checkout'", "CAST(http_status_code AS BIGINT) > 100", "http_route = '/missing'"},
		},
	}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "Without HTTP route /missing, 1 match the remaining filters.") {
		t.Fatalf("result = %+v", r)
	}
}

// countingEngine counts the statements a diagnosis issues.
type countingEngine struct {
	Engine
	queries int
}

func (c *countingEngine) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	c.queries++
	return c.Engine.QueryContext(ctx, query, args...)
}

func TestDiagnosisBoundsReruns(t *testing.T) {
	engine, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	counting := &countingEngine{Engine: engine}
	e := NewExecutor(counting, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	where := make([]string, 0, 16)
	for i := range 16 {
		where = append(where, fmt.Sprintf("duration_ms > %d", 1000000+i))
	}
	p := &Panel{ID: "empty", Title: "Empty", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, Where: where}}
	d := shopDashboard()
	d.Panels = []Panel{*p}
	Normalize(&d)
	checked, problems, err := Check(t.Context(), engine, &d)
	if err != nil || len(problems) > 0 {
		t.Fatalf("check: %v %v", err, problems)
	}
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Vars: map[string]Value{"service": {Values: []string{"checkout"}}}}
	_ = e.diagnose(t.Context(), &d.Panels[0], checked.Filters["empty"], scope)
	// One scan counts the window and the first maxDiagnosedFilters reruns
	// together; a value lookup for the blamed equality is the only other read.
	if counting.queries > 2 {
		t.Fatalf("diagnosis ran %d queries, want at most 2", counting.queries)
	}
}
