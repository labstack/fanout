package panel

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
			p := Panel{ID: "logs", Title: "Logs", Viz: viz, Options: &Options{Highlight: "error"}, Query: &Query{From: "logs", Where: []string{"service = 'missing'"}}}
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
			if r := got[0]; r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "match service = 'missing'") || strings.Contains(r.Diagnosis, "No logs contain") {
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
	d.Panels = []Panel{{ID: "empty", Title: "Empty", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{
		"service = $service", "kind = 'SPAN_KIND_SERVER'", "http_method = 'GET'", "duration_ms > 0", "status <> 'unknown'", "namespace = 'shop'", "http_route <> ''",
	}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Status != "empty" || !strings.Contains(r.Diagnosis, "match http_route <> ''. Without that filter, 120 do.") {
		t.Fatalf("result = %+v", r)
	}
	if strings.Contains(r.Diagnosis, "match service = $service") {
		t.Fatalf("wrong filter blamed: %s", r.Diagnosis)
	}
}

func TestDiagnosisChoosesMostSelectiveQualifyingFilter(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	// Removing service recovers 60 frontend rows; removing route recovers
	// 120 checkout rows, so route is the more selective conflicting filter.
	d.Panels = []Panel{{ID: "empty", Title: "Empty", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{"service = $service", "http_route = '/api/cart'"}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Status != "empty" || !strings.Contains(r.Diagnosis, "match http_route = '/api/cart'. Without that filter, 120 do.") {
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
	p := &Panel{ID: "empty", Title: "Empty", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}, Where: where}}
	d := shopDashboard()
	d.Panels = []Panel{*p}
	Normalize(&d)
	checked, problems, err := Check(t.Context(), engine, &d)
	if err != nil || len(problems) > 0 {
		t.Fatalf("check: %v %v", err, problems)
	}
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Vars: map[string]Value{"service": {Values: []string{"checkout"}}}}
	_ = e.diagnose(t.Context(), &d.Panels[0], checked.Filters["empty"], scope)
	// One total count, at most maxDiagnosedFilters reruns, and one value lookup.
	if counting.queries > 2+maxDiagnosedFilters {
		t.Fatalf("diagnosis ran %d queries, want at most %d", counting.queries, 2+maxDiagnosedFilters)
	}
}
