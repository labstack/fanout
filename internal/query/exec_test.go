package query

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/engine"
)

func TestRunResolvesOneInstantForAllPanelWindows(t *testing.T) {
	e := newFixtureExecutor(t)
	var calls atomic.Int64
	e.now = func() time.Time {
		return fixtureStart.Add(time.Hour).Add(time.Duration(calls.Add(1)-1) * 27 * time.Millisecond)
	}
	d := Dashboard{Name: "Shared window", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "current", Title: "Current", Viz: "timeseries", Query: &Spec{From: "spans", Measures: []string{"count()"}, Bucket: "5m"}},
		{ID: "other", Title: "Other", Viz: "timeseries", Query: &Spec{From: "spans", Measures: []string{"count()"}, Bucket: "5m"}},
		{ID: "prior", Title: "Prior", Viz: "timeseries", Time: &TimeOverride{Shift: "1d"}, Query: &Spec{From: "spans", Measures: []string{"count()"}, Bucket: "5m"}},
	}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(results)
	for _, r := range results {
		if r.Status == StatusError {
			t.Fatalf("panel failed: %+v", r)
		}
	}
	current, other, prior := got["current"], got["other"], got["prior"]
	if current.FromMS != other.FromMS || current.ToMS != other.ToMS {
		t.Errorf("unshifted windows differ: current=%+v other=%+v", current, other)
	}
	if current.FromMS-prior.FromMS != 86400000 || current.ToMS-prior.ToMS != 86400000 {
		t.Errorf("1d shift differs from 86400000ms: current=%+v prior=%+v", current, prior)
	}
	if current.FromMS != fixtureStart.UnixMilli() || current.ToMS != fixtureStart.Add(time.Hour).UnixMilli() {
		t.Errorf("batch instant drifted: %+v", current)
	}
	if calls.Load() != 1 {
		t.Errorf("clock called %d times, want one batch instant", calls.Load())
	}
}

func newFixtureExecutor(t *testing.T) *Executor {
	t.Helper()
	d, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	e := NewExecutor(d, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	return e
}

func shopDashboard() Dashboard {
	return Dashboard{
		Name: "Shop",
		Time: Time{Range: "1h"},
		Variables: []Variable{
			{Name: "service", Kind: "query", From: "spans", Field: "service", Default: "checkout"},
			{Name: "route", Kind: "query", From: "spans", Field: "attributes['http.route']", Where: []string{"service = $service"}, IncludeAll: true, Default: AllValue},
		},
		Panels: []Panel{
			{ID: "latency", Title: "Latency", Viz: "timeseries", Query: &Spec{From: "spans", Where: []string{"service = $service", "attributes['http.route'] = $route"}, Measures: []string{"p95(duration_ms)"}, Bucket: "5m"}},
			{ID: "requests", Title: "Requests", Viz: "stat", Query: &Spec{From: "spans", Where: []string{"service = $service"}, Measures: []string{"count()"}}},
			{ID: "by_route", Title: "By route", Viz: "bar", Query: &Spec{From: "spans", Where: []string{"service = $service"}, Measures: []string{"count()"}, By: []string{"attributes['http.route']"}}},
			{ID: "services", Title: "Services", Viz: "table", SQL: "SELECT service, count(*) AS n FROM spans WHERE $__window(start_time) GROUP BY 1 ORDER BY 2 DESC"},
			{ID: "notes", Title: "Notes", Viz: "text", Content: "Checkout is the money path."},
		},
	}
}

func byID(results []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range results {
		out[r.ID] = r
	}
	return out
}

func TestRunProducesFrames(t *testing.T) {
	e := newFixtureExecutor(t)
	results, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Widths: map[string]int{"latency": 800}})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(results)
	latency := got["latency"]
	if latency.Status != "ok" || latency.Interval != "5m" || latency.Frame.Rows != 12 {
		t.Fatalf("latency = %+v", latency)
	}
	p95 := latency.Frame.Values[1]
	if p95[0].(float64) >= p95[len(p95)-1].(float64) {
		t.Fatalf("checkout should slow down after 12:30: %v", p95)
	}
	requests := got["requests"]
	if requests.Frame.Totals[1].(float64) != 120 {
		t.Fatalf("requests totals = %v", requests.Frame.Totals)
	}
	if got["by_route"].Frame.Rows != 2 || got["by_route"].Frame.Values[0][0] == "" {
		t.Fatalf("by_route = %+v", got["by_route"].Frame)
	}
	services := got["services"].Frame
	if services.Rows != 2 || services.Columns[0].Role != "dimension" || services.Columns[1].Role != "measure" || services.Values[0][0] != "checkout" {
		t.Fatalf("services = %+v", services)
	}
	if got["notes"].Status != "ok" || got["notes"].Frame != nil {
		t.Fatalf("notes = %+v", got["notes"])
	}
}

func TestRunComparesWithThePreviousPeriod(t *testing.T) {
	e := newFixtureExecutor(t)
	compare := true
	results, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}, Compare: &compare})
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Previous == nil || r.Previous.Totals[1].(float64) != 0 {
		t.Fatalf("previous = %+v", r.Previous)
	}
	if r.ShiftMS != time.Hour.Milliseconds() {
		t.Fatalf("shift_ms = %d, want the window length", r.ShiftMS)
	}
	off := false
	results, err = e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}, Compare: &off})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Previous != nil || results[0].ShiftMS != 0 {
		t.Fatalf("uncompared run = %+v", results[0])
	}
}

func TestRunExplainsEmptyPanels(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = []Panel{{ID: "nope", Title: "Nope", Viz: "bar", Query: &Spec{From: "spans", Where: []string{"service = $service", "attributes['http.route'] = '/nope'"}, Measures: []string{"count()"}, By: []string{"attributes['http.route']"}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Status != "empty" || !strings.Contains(r.Diagnosis, "/nope") || !strings.Contains(r.Diagnosis, "/cart") {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunIsolatesPanelErrors(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = append(d.Panels, Panel{ID: "broken", Title: "Broken", Viz: "stat", Query: &Spec{From: "spans", Where: []string{"service::INTEGER > 1"}, Measures: []string{"count()"}}})
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(results)
	if got["broken"].Status != "error" || got["broken"].Error == "" {
		t.Fatalf("broken = %+v", got["broken"])
	}
	if got["latency"].Status != "ok" {
		t.Fatalf("latency failed alongside: %+v", got["latency"])
	}
}

func TestRunBindsHostileValuesAsData(t *testing.T) {
	e := newFixtureExecutor(t)
	results, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}, Vars: map[string]Value{"service": {Values: []string{"x'); DROP TABLE spans; --"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusOK || results[0].Frame.Totals[1].(float64) != 0 {
		t.Fatalf("hostile value = %+v", results[0])
	}
	again, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}})
	if err != nil || again[0].Frame.Totals[1].(float64) != 120 {
		t.Fatalf("spans damaged: %+v %v", again, err)
	}
}

func TestRunIsolatesInvalidPanels(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Stale", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "stale", Title: "Stale", Viz: "bar", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"http_route"}, Where: []string{"http_route = '/cart'"}}},
		{ID: "valid", Title: "Valid", Viz: "stat", Query: &Spec{From: "spans", Measures: []string{"count()"}}},
	}}
	for _, stream := range []bool{false, true} {
		var results []Result
		var err error
		if stream {
			err = e.Stream(t.Context(), RunRequest{Dashboard: d}, func(r Result) { results = append(results, r) })
		} else {
			results, err = e.Run(t.Context(), RunRequest{Dashboard: d})
		}
		if err != nil {
			t.Fatal(err)
		}
		got := byID(results)
		if len(results) != 2 || got["stale"].Status != "invalid" || !strings.Contains(got["stale"].Error, "http_route") || got["valid"].Status != StatusOK {
			t.Fatalf("results = %+v", results)
		}
		if !stream && results[0].ID != "stale" {
			t.Fatalf("order = %+v", results)
		}
	}

	// Selection retains request order, and invalid panels never enter the cache.
	selected, err := e.Run(t.Context(), RunRequest{Dashboard: d, Panels: []string{"valid", "stale"}})
	if err != nil || len(selected) != 2 || selected[0].ID != "valid" || selected[1].ID != "stale" || selected[1].Cached || len(selected[1].Problems) == 0 {
		t.Fatalf("selection = %+v, %v", selected, err)
	}
	reduced := d
	reduced.Panels = d.Panels[1:]
	results, err := e.Run(t.Context(), RunRequest{Dashboard: reduced})
	if err != nil || results[0].Cached {
		t.Fatalf("reduced spec shared cache: %+v, %v", results, err)
	}
	allInvalid := d
	allInvalid.Panels = d.Panels[:1]
	results, err = e.Run(t.Context(), RunRequest{Dashboard: allInvalid})
	if err != nil || len(results) != 1 || results[0].Status != StatusInvalid {
		t.Fatalf("all invalid = %+v, %v", results, err)
	}
	// Rechecking after a structural error must retain original panel paths.
	mixed := d
	mixed.Panels = append(slices.Clone(d.Panels), Panel{ID: "filter", Title: "Filter", Viz: "stat", Query: &Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"http_route = '/cart'"}}})
	results, err = e.Run(t.Context(), RunRequest{Dashboard: mixed})
	if err != nil || len(results) != 3 || results[2].Status != StatusInvalid || len(results[2].Problems) == 0 || results[2].Problems[0].Path != "panels[2].query.where[0]" {
		t.Fatalf("remapped results = %+v, %v", results, err)
	}
	// A structural panel fault must not hide database-backed variable faults.
	allInvalid.Panels = []Panel{{ID: "broken", Title: "Broken", Viz: "unknown"}}
	allInvalid.Variables = []Variable{{Name: "route", Kind: "query", From: "spans", Field: "service", Where: []string{"http_route = '/cart'"}}}
	if _, err := e.Run(t.Context(), RunRequest{Dashboard: allInvalid}); err == nil {
		t.Fatal("variable filter accepted")
	}
	var problems Problems
	if err := e.Validate(t.Context(), &d); !errors.As(err, &problems) {
		t.Fatalf("save validation = %v", err)
	}
	d.Variables = []Variable{{Name: "route", Kind: "query", From: "spans", Field: "http_route"}}
	emitted := false
	err = e.Stream(t.Context(), RunRequest{Dashboard: d}, func(Result) { emitted = true })
	if !errors.As(err, &problems) || emitted {
		t.Fatalf("global failure = %v, emitted = %v", err, emitted)
	}
}

func TestRunRejectsInvalidSpecs(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels[0].Query.Measures = []string{"p95(duraton_ms)"}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || results[0].Status != StatusInvalid || results[0].Problems[0].Path != "panels[0].query.measures[0]" {
		t.Fatalf("results = %+v, err = %v", results, err)
	}
	if _, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"missing"}}); err == nil {
		t.Fatal("unknown panel id accepted")
	}
}

func TestResolveVariablesFollowsDependencies(t *testing.T) {
	e := newFixtureExecutor(t)
	options, err := e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: shopDashboard()})
	if err != nil {
		t.Fatal(err)
	}
	if len(options["service"]) != 2 || options["service"][0].Value != "checkout" || options["service"][0].Count != 120 {
		t.Fatalf("service options = %+v", options["service"])
	}
	routes := []string{}
	for _, o := range options["route"] {
		routes = append(routes, o.Value)
	}
	if strings.Join(routes, ",") != "/cart,/quote" && strings.Join(routes, ",") != "/quote,/cart" {
		t.Fatalf("route options for checkout = %v", routes)
	}
	options, err = e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: shopDashboard(), Vars: map[string]Value{"service": {Values: []string{"frontend"}}}})
	if err != nil || len(options["route"]) != 1 || options["route"][0].Value != "/api/cart" {
		t.Fatalf("route options for frontend = %+v %v", options["route"], err)
	}
}

func TestRunWithCancelledContextIsNotProblems(t *testing.T) {
	e := newFixtureExecutor(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := e.Run(ctx, RunRequest{Dashboard: shopDashboard()})
	var problems Problems
	if err == nil || errors.As(err, &problems) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunReportsSQLTruncation(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = []Panel{{ID: "big", Title: "Big", Viz: "table", SQL: "SELECT s1.span_id FROM spans s1, spans s2 WHERE $__window(s1.start_time)"}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if f := results[0].Frame; f == nil || f.Rows != 1000 || !f.Truncated {
		t.Fatalf("result = %+v", results[0])
	}
}

func TestRunResolvesUnsupportedAllToFirstOption(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Variables[0].Default = ""
	vars := map[string]Value{"service": {All: true}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d, Panels: []string{"requests"}, Vars: vars})
	if err != nil || results[0].Frame.Totals[1].(float64) != 120 {
		t.Fatalf("results = %+v %v", results, err)
	}
	options, err := e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: d, Vars: vars})
	if err != nil || options["service"][0].Value != "checkout" || options["service"][0].Count != 120 {
		t.Fatalf("options = %+v %v", options, err)
	}
}

func TestRunWarmCacheCancelledContextIsError(t *testing.T) {
	e := newFixtureExecutor(t)
	if _, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := e.Run(ctx, RunRequest{Dashboard: shopDashboard()})
	var problems Problems
	if err == nil || errors.As(err, &problems) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyVariableOptionsWire(t *testing.T) {
	engine, _ := newTestEngine(t)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := shopDashboard()
	options, err := e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"route":[],"service":[]}` {
		t.Fatalf("empty wire options: %s", raw)
	}
}

// A variable used as a pattern in panel SQL or a filter is bounded when bound:
// its length times the text's is a statement's cost per row.
func TestRunBoundsVariablePatterns(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Variables = append(d.Variables, Variable{Name: "q", Kind: "text"})
	d.Panels = []Panel{
		{ID: "sql", Title: "SQL", Viz: "table", SQL: "SELECT count(*) AS n FROM spans WHERE $__window(start_time) AND regexp_matches(operation, $q)"},
		{ID: "filter", Title: "Filter", Viz: "stat", Query: &Spec{From: "spans", Where: []string{"operation ILIKE $q"}, Measures: []string{"count()"}}},
	}
	long := map[string]Value{"q": {Values: []string{strings.Repeat("a", engine.MaxPatternBytes+1)}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d, Vars: long})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !strings.Contains(r.Error, "$q is a pattern of at most 256 bytes") {
			t.Errorf("%s: %+v", r.ID, r)
		}
	}
	results, err = e.Run(t.Context(), RunRequest{Dashboard: d, Vars: map[string]Value{"q": {Values: []string{"GET.*"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("%s: %s", r.ID, r.Error)
		}
	}
}
