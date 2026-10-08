package panel

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/observability"
)

func TestRollupPanelsReuseServices(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO service_rollup(namespace,bucket,service,spans,served_spans,p50_ms,p95_ms,error_rate,log_count,metric_count) VALUES ('shop',?,'checkout',10,10,50,100,.1,2,3),('shop',?,'payment',20,20,20,40,0,1,1)`, at, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO edge_rollup VALUES ('shop',?,'checkout','payment',10,20,.1,'call')`, at)
	if err != nil {
		t.Fatal(err)
	}
	reader := observability.New(engine, engine, 30)
	e := NewExecutor(engine, 30)
	e.SetRollupReader(reader)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Overview", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "health", Title: "Health", Viz: "health", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'"}}},
		{ID: "map", Title: "Map", Viz: "service_map", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'"}}},
	}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := reader.Overview(t.Context(), observability.Scope{Namespace: "shop", Start: at, End: at.Add(time.Hour)}, 400)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != "ok" || got[0].Frame.Rows != len(overview.Data.Services) || got[0].Frame.Values[3][0].(float64) != 10 {
		t.Fatalf("overview frame: %+v", got[0])
	}
	if got[1].Status != "ok" || got[1].Frame.Rows != 3 || got[1].Frame.Values[7][2].(float64) != 10 {
		t.Fatalf("edges: %+v", got[1])
	}
	if got[1].Frame.Columns[9].Name != "p95_ms" || got[1].Frame.Values[9][0] != float64(100) || got[1].Frame.Values[6][0] != nil || got[1].Frame.Values[9][2] != nil || got[1].Frame.Values[6][2] != float64(20) {
		t.Fatalf("mixed node/edge latency semantics: %+v", got[1].Frame)
	}
	d.Panels[0].Query.Where = []string{"namespace = 'missing'"}
	d.Panels = d.Panels[:1]
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "empty" {
		t.Fatalf("empty health: %+v %v", got, err)
	}
}

func TestRollupPanelsAggregateContract(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'checkout',10,10,50,100,.1,2,3),('shop',?,'checkout',30,30,70,200,0,4,5),('shop',?,'payment',20,20,20,40,0,1,1)`, at, at.Add(10*time.Minute), at)
	if err != nil {
		t.Fatal(err)
	}
	reader := observability.New(engine, engine, 30)
	e := NewExecutor(engine, 30)
	e.SetRollupReader(reader)
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := Dashboard{Name: "Health", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "h", Title: "H", Viz: "health", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'", "'checkout' = service"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	scope := observability.Scope{Namespace: "shop", Service: "checkout", Start: at, End: at.Add(time.Hour)}
	o, err := reader.Overview(t.Context(), scope, 400)
	if err != nil {
		t.Fatal(err)
	}
	p, err := reader.HealthErrorTrend(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	want := &HealthFrame{Health: string(o.Data.Health), Counts: o.Data.Counts, TotalSpans: o.Data.TotalSpans, ErrorRate: o.Data.ErrorRate * 100, ServiceCount: o.Data.ServiceCount, ErrorTrend: p}
	if got[0].Frame == nil || !reflect.DeepEqual(got[0].Frame.Health, want) {
		t.Fatalf("health aggregate got %+v want %+v", got, want)
	}
	if got[0].Frame.Rows != 1 || got[0].Frame.Values[5][0] != float64(200) {
		t.Fatalf("service p95: %+v", got[0])
	}
}

func TestRollupPanelsRejectCastSemantics(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, where := range []string{"CAST(service AS BOOLEAN) = CAST('true' AS BOOLEAN)", "service = CAST('checkout' AS BOOLEAN)"} {
		d := Dashboard{Name: "Cast", Panels: []Panel{{ID: "h", Title: "H", Viz: "health", Query: &Query{From: "spans", Where: []string{where}}}}}
		if err := e.Validate(t.Context(), &d); err == nil {
			t.Fatalf("silently discarded cast in %s", where)
		}
	}
}

func TestRollupPanelsRejectNormalizedEquality(t *testing.T) {
	e := newFixtureExecutor(t)
	scope := Scope{Vars: map[string]Value{"s": {Values: []string{" checkout "}}}}
	for _, where := range []string{"namespace = ' shop '", "service = ' checkout '", "service = $s"} {
		if _, _, err := rollupFilterValue(t.Context(), e.engine, Filter{Source: where}, scope); err == nil {
			t.Fatalf("scope normalization would change equality: %s", where)
		}
	}
}

func TestRollupPanelsScopesAndSelection(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, viz := range []string{"health", "service_map"} {
		p := Panel{ID: "p", Title: "P", Viz: viz, Query: &Query{From: "spans"}}
		scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Vars: map[string]Value{"s": {Values: []string{"checkout"}}}}
		field, value, err := rollupFilterValue(t.Context(), e.engine, Filter{Source: "$s = service"}, scope)
		if err != nil || field != "service" || value != "checkout" {
			t.Fatalf("variable: %s %s %v", field, value, err)
		}
		for _, v := range []Value{{All: true}, {Values: []string{"checkout", "payment"}}, {Values: []string{""}}} {
			scope.Vars["s"] = v
			if _, _, err := rollupFilterValue(t.Context(), e.engine, Filter{Source: "service = $s"}, scope); err == nil {
				t.Fatalf("accepted %+v", v)
			}
		}
		where, args, err := selectionWhere(&p, nil, scope, map[string]string{"service": "checkout"}, nil)
		if err != nil || !strings.Contains(where, "service") || args[len(args)-1] != "checkout" {
			t.Fatalf("selection %s %+v %v", where, args, err)
		}
		if _, _, err := selectionWhere(&p, nil, scope, map[string]string{"operation": "pay"}, nil); err == nil {
			t.Fatal("accepted non-service selection")
		}
		frame, _, err := e.runRollupPanel(t.Context(), &p, nil, scope)
		if err == nil || frame != nil {
			t.Fatalf("missing reader: %+v %v", frame, err)
		}
	}
}

func TestRollupPanelsValidateScopeOnly(t *testing.T) {
	for _, viz := range []string{"health", "service_map"} {
		for _, q := range []*Query{{From: "logs"}, {From: "spans", Measures: []string{"count()"}}, {From: "spans", By: []string{"service"}}, {From: "spans", Bucket: "auto"}, {From: "spans", Sort: "spans"}} {
			d := Dashboard{Name: "Scope", Panels: []Panel{{ID: "p", Title: "P", Viz: viz, Query: q}}}
			Normalize(&d)
			if len(Validate(&d)) == 0 {
				t.Fatalf("accepted %s %+v", viz, q)
			}
		}
	}
}
func TestRollupPanelsApplyServiceBeforeLimit(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO service_rollup SELECT 'shop',?,'noisy-'||i,100,100,3000,4000,.5,0,0 FROM generate_series(1,500) AS t(i)`, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'quiet',1,1,1,2,0,0,0)`, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO edge_rollup SELECT 'shop',?,'noisy-'||i,'sink',100,100,.5,'call' FROM generate_series(1,500) AS t(i)`, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO edge_rollup VALUES ('shop',?,'quiet','sink',1,2,0,'call')`, at)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.SetRollupReader(observability.New(engine, engine, 30))
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := Dashboard{Name: "Quiet", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "h", Title: "H", Viz: "health", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'", "service = 'quiet'"}}}, {ID: "m", Title: "M", Viz: "service_map", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'", "service = 'quiet'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Frame.Rows != 1 || got[0].Frame.Values[0][0] != "quiet" || got[1].Frame.Rows != 2 {
		t.Fatalf("post-limit scope loss: %+v", got)
	}
	for i := range d.Panels {
		d.Panels[i].Query.Where = []string{"namespace = 'shop'"}
		d.Panels[i].Query.Limit = 1000
	}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame.Rows != 400 || got[1].Frame.Rows != 800 || !got[0].Frame.Truncated || !got[1].Frame.Truncated {
		t.Fatalf("400 per-read cap: %+v %v", got, err)
	}
}
func TestRollupPanelsRejectUnrepresentableFilters(t *testing.T) {
	e := newFixtureExecutor(t)
	cases := []struct{ where, message string }{
		{"http_route = '/cart'", "rollup panels support equality filters on namespace or service"},
		{"service <> 'checkout'", "rollup panels support equality filters on namespace or service"},
		{"service = operation", "rollup equality needs a literal or variable"},
	}
	for _, tc := range cases {
		d := Dashboard{Name: "Map", Panels: []Panel{{ID: "map", Title: "Map", Viz: "service_map", Query: &Query{From: "spans", Where: []string{tc.where}}}}}
		err := e.Validate(t.Context(), &d)
		var got Problems
		want := Problem{Path: "panels[0].query.where[0]", Message: tc.message, Hint: "use namespace or service equality with a literal or single-value variable"}
		if !errors.As(err, &got) || !slices.Contains(got, want) {
			t.Fatalf("%s: got %v want %+v", tc.where, err, want)
		}
	}
}
