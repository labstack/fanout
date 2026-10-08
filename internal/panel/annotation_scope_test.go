package panel

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

func TestAnnotationScopeUsesCheckedAST(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO version_rollup VALUES ('shop','checkout','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS),('shop','payment','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, at, at, at, at)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	sig, _ := lookupSignal("spans")
	vars := map[string]Variable{"services": {Name: "services", Kind: "custom", Options: []string{"checkout", "payment"}, Multi: true, IncludeAll: true}}
	scope := Scope{Start: at, End: at.Add(time.Hour), Vars: map[string]Value{"services": {Values: []string{"checkout"}}}}
	p := &Panel{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans"}}
	for _, expr := range []string{"service IN $services", "service LIKE '%out'", "namespace = 'shop' AND service = 'checkout' AND http_route = '/cart'"} {
		f, err := checkFilter(t.Context(), engine, sig, vars, expr)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got.Services) != 1 || got.Services[0].Service != "checkout" {
			t.Fatalf("%s: %+v", expr, got)
		}
	}
	f, err := checkFilter(t.Context(), engine, sig, vars, "service = 'checkout' OR http_route = '/cart'")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
	if err != nil || got != nil {
		t.Fatalf("mixed OR must remain unscoped for annotation service matching: %+v %v", got, err)
	}
	f, err = checkFilter(t.Context(), engine, sig, vars, "service IN $services")
	if err != nil {
		t.Fatal(err)
	}
	scope.Vars["services"] = Value{All: true}
	got, err = e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
	if err != nil || got != nil {
		t.Fatalf("All: %+v %v", got, err)
	}
	scope.Vars["services"] = Value{Values: []string{}}
	got, err = e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
	if err != nil || got == nil || len(got.Services) != 0 {
		t.Fatalf("empty list: %+v %v", got, err)
	}
}
func projectedFilters(t *testing.T, parser Parser, sig *signal, vars map[string]Variable, f Filter) []AnnotationFilter {
	t.Helper()
	projected, err := projectAnnotationFilter(t.Context(), parser, sig, vars, f)
	if err != nil {
		t.Fatal(err)
	}
	if projected == nil {
		return nil
	}
	return []AnnotationFilter{*projected}
}
func TestAnnotationScopeRunPanelAndAllSources(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	if _, err := engine.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'checkout',1,1,1,2,0,0,0)`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DB.Exec(`INSERT INTO anomaly_log VALUES ('shop','anomalyonly','latency',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS,'Slow','warn')`, at, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := Dashboard{Name: "Scope", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "5m", Where: []string{"namespace = 'shop'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	scope := got[0].AnnotationScope
	if scope == nil || !scope.NamespaceScoped || len(scope.Services) != 2 || scope.Services[0].Service != "anomalyonly" || scope.Services[1].Service != "checkout" {
		t.Fatalf("runPanel not wired or keys incomplete: %+v", got)
	}
}

type failExtraParse struct {
	Parser
	parses  int
	failure error
}

func (p *failExtraParse) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
	p.parses++
	if p.parses == 3 {
		return nil, p.failure
	}
	return p.Parser.ParseSQL(ctx, text)
}
func TestCheckOperationalFailures(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, p := range []Panel{
			{ID: "p", Title: "P", Viz: "health", Query: &Query{From: "spans", Where: []string{"service = 'checkout'"}}},
			{ID: "p", Title: "P", Viz: "bar", Options: &Options{Split: "deploy"}, Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"operation"}, Where: []string{"service = 'checkout'"}}},
			{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto", Where: []string{"service = 'checkout'"}}},
		} {
			d := Dashboard{Name: "Operational", Panels: []Panel{p}}
			Normalize(&d)
			parser := &failExtraParse{Parser: engine, failure: failure}
			_, problems, err := Check(t.Context(), parser, &d)
			if !errors.Is(err, failure) || len(problems) != 0 || parser.parses != 3 {
				t.Fatalf("%s converted operational error to Problems: %+v %v calls=%d", p.Viz, problems, err, parser.parses)
			}
		}
	}
}

type annotationParseCounter struct {
	Engine
	parses int
}

func (p *annotationParseCounter) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
	p.parses++
	return p.Engine.ParseSQL(ctx, text)
}
func TestAnnotationProjectionMemoized(t *testing.T) {
	e := newFixtureExecutor(t)
	counter := &annotationParseCounter{Engine: e.engine}
	e.engine = counter
	d := Dashboard{Name: "Memoized", Panels: []Panel{{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto", Where: []string{"service = 'checkout'"}}}}}
	Normalize(&d)
	first, err := e.check(t.Context(), &d)
	if err != nil {
		t.Fatal(err)
	}
	count := counter.parses
	second, err := e.check(t.Context(), &d)
	if err != nil || first != second || count == 0 || counter.parses != count {
		t.Fatalf("memoization: %v count=%d now=%d", err, count, counter.parses)
	}
	scope := Scope{Start: e.now().Add(-time.Hour), End: e.now(), Vars: map[string]Value{}}
	for i := 0; i < 2; i++ {
		if _, err := e.annotationScope(t.Context(), &d.Panels[0], first.AnnotationFilters["p"], scope); err != nil {
			t.Fatal(err)
		}
	}
	if counter.parses != count {
		t.Fatalf("runtime reparsed annotation predicate: %d -> %d", count, counter.parses)
	}
}

func TestAnnotationProjectionOmitsOnlySignalRestrictions(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	if _, err := engine.DB.Exec(`INSERT INTO version_rollup VALUES ('shop','checkout','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS),('shop','payment','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, at, at, at, at); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	sig, _ := lookupSignal("spans")
	vars := map[string]Variable{
		"service": {Name: "service", Kind: "custom", Options: []string{"checkout", "payment"}},
		"route":   {Name: "route", Kind: "custom", Options: []string{"/cart"}, IncludeAll: true},
	}
	scope := Scope{Vars: map[string]Value{"service": {Values: []string{"checkout"}}, "route": {All: true}}}
	p := &Panel{ID: "p", Viz: "timeseries", Query: &Query{From: "spans"}}
	for _, expr := range []string{
		"service = $service AND http_route = $route",
		"namespace = 'shop' AND CAST(service AS VARCHAR) = $service AND http_route = $route",
		"service = $service AND NOT (http_route = $route)",
		"service = $service AND (namespace = 'shop' OR http_route = $route)",
	} {
		f, err := checkFilter(t.Context(), engine, sig, vars, expr)
		if err != nil {
			t.Fatal(err)
		}
		filters := projectedFilters(t, engine, sig, vars, f)
		if len(filters) != 1 || !slices.Equal(filters[0].Filter.Params, []string{"service"}) {
			t.Fatalf("%s retained omitted route parameter: %+v", expr, filters)
		}
		got, err := e.annotationScope(t.Context(), p, filters, scope)
		if err != nil || got == nil || !slices.Equal(got.Services, []AnnotationService{{Namespace: "shop", Service: "checkout"}}) {
			t.Fatalf("%s: %+v %v", expr, got, err)
		}
	}
	for _, expr := range []string{"service = $service OR http_route = $route", "NOT (service = $service AND http_route = $route)"} {
		f, err := checkFilter(t.Context(), engine, sig, vars, expr)
		if err != nil {
			t.Fatal(err)
		}
		if filters := projectedFilters(t, engine, sig, vars, f); filters != nil {
			t.Fatalf("mixed OR/NOT must remain unscoped: %s %+v", expr, filters)
		}
	}
}

type annotationQueryFailure struct {
	Engine
}

func (e *annotationQueryFailure) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	if strings.Contains(text, "WITH keys AS") {
		return nil, context.DeadlineExceeded
	}
	return e.Engine.QueryContext(ctx, text, args...)
}

func TestAnnotationScopeFailurePreservesFrame(t *testing.T) {
	e := newFixtureExecutor(t)
	e.engine = &annotationQueryFailure{Engine: e.engine}
	d := shopDashboard()
	d.Panels = d.Panels[:1]
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(got) != 1 || got[0].Status != StatusOK || got[0].Frame == nil || got[0].Frame.Rows != 12 || got[0].AnnotationScope != nil || got[0].AnnotationError != "Annotation scope is unavailable." {
		t.Fatalf("annotation failure discarded frame: %+v %v", got, err)
	}
}

func TestAnnotationScopeLimit(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	if _, err := engine.DB.Exec(`INSERT INTO version_rollup SELECT 'shop', 'service' || lpad(i::VARCHAR, 4, '0'), 'v1', ?::TIMESTAMP_NS::TIMESTAMPTZ_NS, ?::TIMESTAMP_NS::TIMESTAMPTZ_NS FROM range(1001) t(i)`, at, at); err != nil {
		t.Fatal(err)
	}
	sig, _ := lookupSignal("spans")
	f, err := checkFilter(t.Context(), engine, sig, nil, "namespace = 'shop'")
	if err != nil {
		t.Fatal(err)
	}
	p := &Panel{ID: "p", Viz: "timeseries", Query: &Query{From: "spans"}}
	got, err := NewExecutor(engine, 30).annotationScope(t.Context(), p, projectedFilters(t, engine, sig, nil, f), Scope{})
	if err != nil || got == nil || !got.Limited || !got.NamespaceScoped || len(got.Services) != 1000 || got.Services[999].Service != "service0999" {
		t.Fatalf("bounded scope: %+v %v", got, err)
	}
}
