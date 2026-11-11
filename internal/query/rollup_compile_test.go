package query

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/engine"
	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
)

func TestRollupPlan(t *testing.T) {
	duck, _ := newTestEngine(t)
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Interval: time.Minute}
	for _, tc := range []struct {
		name, from, viz, measure, by, filter string
		want                                 bool
	}{
		{"count", "spans", "stat", "count()", "", "", true},
		{"duration", "spans", "timeseries", "p95(duration_ms)", "service", "kind='SPAN_KIND_SERVER' AND status!='STATUS_CODE_ERROR'", true},
		{"quantile", "spans", "table", "quantile(duration_ms,0.999)", "namespace", "lower(service)='checkout'", true},
		{"status", "spans", "table", "share()", "status", "status IS NULL", true},
		{"constant", "spans", "stat", "count()", "", "1=1", true},
		{"attribute", "spans", "stat", "count()", "", "attributes['http.route']='/cart'", false},
		{"operation filter", "spans", "stat", "count()", "", "operation='GET'", false},
		{"operation group", "spans", "table", "count()", "operation", "", false},
		{"distinct", "spans", "stat", "count_distinct(service)", "", "", false},
		{"expression", "spans", "stat", "count()/2 as half", "", "", false},
		{"logs", "logs", "stat", "count()", "", "", true},
		{"log severity", "logs", "stat", "count()", "", "upper(severity) IN ('ERROR','FATAL')", true},
		{"log body", "logs", "stat", "count()", "", "body='failure'", false},
		{"log distinct", "logs", "stat", "count_distinct(service)", "", "", false},
		{"endpoint", "spans", "table", "p95(duration_ms)", "attributes['http.route']", "attributes['http.route'] <> ''", true},
		{"endpoint missing population", "spans", "table", "count()", "attributes['http.route']", "", false},
		{"endpoint lookup", "spans", "table", "count()", "attributes['http.route']", "attributes['http.route']='/cart'", false},
		{"traces", "spans", "traces", "count()", "", "", false},
		{"heatmap", "spans", "heatmap", "count()", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Panel{Viz: tc.viz, Query: &Spec{From: tc.from}}
			if tc.by != "" {
				p.Query.By = []string{tc.by}
			}
			var filters []Filter
			if tc.filter != "" {
				sig, _ := lookupSignal(tc.from)
				f, err := checkFilter(t.Context(), duck, sig, nil, tc.filter)
				if err != nil {
					t.Fatal(err)
				}
				filters = []Filter{f}
			}
			measures := measuresOf(t, tc.from, tc.measure)
			if got := rollupPlan(p, measures, filters, scope); got != tc.want {
				t.Fatalf("routed=%v want=%v columns=%+v", got, tc.want, filters)
			}
			if tc.viz != "traces" && tc.viz != "heatmap" {
				c, err := compileQuery(p, measures, filters, scope)
				if err != nil {
					t.Fatal(err)
				}
				if (strings.Contains(c.SQL, "FROM span_rollup") || strings.Contains(c.SQL, "FROM log_rollup") || strings.Contains(c.SQL, "FROM endpoint_rollup")) != tc.want {
					t.Fatal(c.SQL)
				}
			}
		})
	}
}

func readCompiledFrame(t *testing.T, d *engine.Duck, c Compiled, scope Scope) *Frame {
	t.Helper()
	rows, err := d.QueryContext(queryrows.WithWindow(t.Context(), queryrows.Window{Start: scope.Start, End: scope.End}), c.SQL, c.Args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, c.SQL)
	}
	f, err := scanFrame(rows, c.Columns, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.HasTotals {
		extractGroupingTotals(f)
	}
	return f
}

func TestRollupFramesAndQuantiles(t *testing.T) {
	d, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Interval: 5 * time.Minute}
	for _, viz := range []string{"stat", "timeseries", "bar", "table"} {
		for _, measure := range []string{"count()", "rate()", "error_rate()", "share()", "avg(duration_ms)", "sum(duration_ms)", "min(duration_ms)", "max(duration_ms)", "p95(duration_ms)"} {
			t.Run(viz+"/"+measure, func(t *testing.T) {
				p := &Panel{Viz: viz, Query: &Spec{From: "spans"}}
				sc := scope
				if viz == "table" || viz == "bar" {
					sc.Interval = 0
					p.Query.By = []string{"service"}
				}
				if viz == "timeseries" {
					p.Query.By = []string{"service"}
				}
				ms := measuresOf(t, "spans", measure)
				f, err := checkFilter(t.Context(), d, signals["spans"], nil, "kind='SPAN_KIND_SERVER' AND status IN ('STATUS_CODE_ERROR','STATUS_CODE_OK')")
				if err != nil {
					t.Fatal(err)
				}
				rolled, err := compileQuery(p, ms, []Filter{f}, sc)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := compileAggregateQuery(p, ms, []Filter{f}, sc, false)
				if err != nil {
					t.Fatal(err)
				}
				rf, exact := readCompiledFrame(t, d, rolled, sc), readCompiledFrame(t, d, raw, sc)
				if !reflect.DeepEqual(rf.Columns, exact.Columns) || rf.Rows != exact.Rows {
					t.Fatalf("shapes differ: %+v %+v", rf, exact)
				}
				compare := func(a, b any) {
					t.Helper()
					x, ok := a.(float64)
					y, ok2 := b.(float64)
					if !ok || !ok2 {
						if !reflect.DeepEqual(a, b) {
							t.Fatalf("%v != %v", a, b)
						}
						return
					}
					tolerance := 1e-9
					if ms[0].Func == "p95" {
						tolerance = math.Abs(y) * 0.09051
					}
					if math.Abs(x-y) > tolerance {
						t.Fatalf("rollup=%g exact=%g tolerance=%g", x, y, tolerance)
					}
				}
				for col := range rf.Columns {
					for row := range rf.Rows {
						compare(rf.Values[col][row], exact.Values[col][row])
					}
				}
				if len(rf.Totals) != len(exact.Totals) {
					t.Fatalf("totals differ")
				}
				for i := range rf.Totals {
					compare(rf.Totals[i], exact.Totals[i])
				}
			})
		}
	}
}

func TestRollupCoveredSecondsAndInterval(t *testing.T) {
	d, repo := newTestEngine(t)
	spans := shopSpans()[:3]
	for i := range spans {
		spans[i].StartUnixNanos = fixtureStart.Add(35 * time.Second).UnixNano()
	}
	commit(t, repo, spans, nil)
	p := &Panel{Viz: "timeseries", Query: &Spec{From: "spans", Bucket: "auto"}}
	scope := Scope{Start: fixtureStart.Add(30 * time.Second), End: fixtureStart.Add(2 * time.Minute), FreshTo: fixtureStart.Add(45 * time.Second), Interval: time.Second}
	c, err := compileQuery(p, measuresOf(t, "spans", "rate()"), nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	f := readCompiledFrame(t, d, c, scope)
	if f.Rows != 1 || math.Abs(f.Values[1][0].(float64)-0.2) > 1e-9 {
		t.Fatalf("frame=%+v", f)
	}
	if !strings.Contains(c.SQL, "INTERVAL '60 seconds'") {
		t.Fatal(c.SQL)
	}
}

type freshRollupEngine struct {
	*engine.Duck
	at      time.Time
	queries []string
}

func (d *freshRollupEngine) RollupWatermark(context.Context) (time.Time, error) { return d.at, nil }
func (d *freshRollupEngine) QueryContext(ctx context.Context, q string, args ...any) (queryrows.Rows, error) {
	d.queries = append(d.queries, q)
	return d.Duck.QueryContext(ctx, q, args...)
}

func TestRollupResultFreshnessAndSingleStatementTotals(t *testing.T) {
	d, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	wrapped := &freshRollupEngine{Duck: d, at: fixtureStart.Add(time.Hour)}
	e := NewExecutor(wrapped, 30)
	defer e.Close()
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	dashboard := Dashboard{Name: "Totals", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "n", Title: "N", Viz: "stat", Query: &Spec{From: "spans", Measures: []string{"count()"}}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: dashboard})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != StatusOK || results[0].FreshToMS != wrapped.at.UnixMilli() || results[0].Frame.Totals[1] != float64(180) {
		t.Fatalf("%+v", results)
	}
	n := 0
	for _, q := range wrapped.queries {
		if strings.Contains(q, "FROM span_rollup") {
			n++
			if !strings.Contains(q, "GROUPING SETS") {
				t.Fatal(q)
			}
		}
	}
	if n != 1 {
		t.Fatalf("rollup statements=%d", n)
	}
}

func TestSpanRollupSQLPanel(t *testing.T) {
	d, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	e := NewExecutor(d, 30)
	defer e.Close()
	start, end := fixtureStart, fixtureStart.Add(time.Hour)
	dashboard := Dashboard{Name: "SQL", Time: Time{From: &start, To: &end}, Panels: []Panel{{ID: "sql", Title: "Span rollup", Viz: "table", SQL: "SELECT service,sum(spans) AS n FROM span_rollup WHERE $__window(bucket) GROUP BY service ORDER BY service"}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: dashboard})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != StatusOK || results[0].Frame.Rows != 2 {
		t.Fatalf("%+v", results)
	}
}

func TestLogRollupFramesMatchRaw(t *testing.T) {
	d, repo := newTestEngine(t)
	logs := []telemetry.Log{}
	for i := 0; i < 100; i++ {
		severity := "INFO"
		if i%3 == 0 {
			severity = "ERROR"
		}
		logs = append(logs, telemetry.Log{Namespace: "shop", ServiceName: "checkout", Severity: severity, TimeUnixNanos: fixtureStart.Add(time.Duration(i) * time.Second).UnixNano()})
	}
	commit(t, repo, nil, logs)
	for _, viz := range []string{"stat", "timeseries", "bar"} {
		for _, measure := range []string{"count()", "rate()", "share()"} {
			p := &Panel{Viz: viz, Query: &Spec{From: "logs", By: []string{"severity"}}}
			scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Interval: time.Minute}
			ms := measuresOf(t, "logs", measure)
			filter, err := checkFilter(t.Context(), d, signals["logs"], nil, "upper(severity) IN ('ERROR','INFO')")
			if err != nil {
				t.Fatal(err)
			}
			rolled, err := compileQuery(p, ms, []Filter{filter}, scope)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := compileAggregateQuery(p, ms, []Filter{filter}, scope, false)
			if err != nil {
				t.Fatal(err)
			}
			rf, exact := readCompiledFrame(t, d, rolled, scope), readCompiledFrame(t, d, raw, scope)
			if !reflect.DeepEqual(rf, exact) {
				t.Fatalf("%s %s: rolled=%+v exact=%+v", viz, measure, rf, exact)
			}
		}
	}
}

func TestEndpointRollupFramesMatchRaw(t *testing.T) {
	d, repo := newTestEngine(t)
	spans := shopSpans()
	for i := range spans {
		spans[i].Attributes = map[string]any{"http.route": "/cart"}
	}
	commit(t, repo, spans, nil)
	p := &Panel{Viz: "table", Query: &Spec{From: "spans", By: []string{"service", "attributes['http.route']"}, Sort: "p95", Limit: 15}}
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}
	filter, err := checkFilter(t.Context(), d, signals["spans"], nil, "attributes['http.route'] <> ''")
	if err != nil {
		t.Fatal(err)
	}
	ms := measuresOf(t, "spans", "p95(duration_ms)", "error_rate()", "rate()")
	rolled, err := compileQuery(p, ms, []Filter{filter}, scope)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := compileAggregateQuery(p, ms, []Filter{filter}, scope, false)
	if err != nil {
		t.Fatal(err)
	}
	rf, exact := readCompiledFrame(t, d, rolled, scope), readCompiledFrame(t, d, raw, scope)
	if rf.Rows != exact.Rows || !reflect.DeepEqual(rf.Columns, exact.Columns) {
		t.Fatalf("shape: %+v %+v", rf, exact)
	}
	for c := range rf.Columns {
		for row := range rf.Rows {
			a, b := rf.Values[c][row], exact.Values[c][row]
			if x, ok := a.(float64); ok {
				y := b.(float64)
				tolerance := 1e-9
				if rf.Columns[c].Name == "p95" {
					tolerance = math.Abs(y) * 0.09051
				}
				if math.Abs(x-y) > tolerance {
					t.Fatalf("%s: %g != %g", rf.Columns[c].Name, x, y)
				}
			} else if !reflect.DeepEqual(a, b) {
				t.Fatalf("%v != %v", a, b)
			}
		}
	}
}

func TestHistogramPlanPreservesMultipleQuantilesTotalsAndFoldedGroups(t *testing.T) {
	d, repo := newTestEngine(t)
	var spans []telemetry.Span
	for i := range 8 {
		for _, s := range shopSpans() {
			s.ServiceName = string(rune('a' + i))
			s.DurationMS += float64(i * 1000)
			spans = append(spans, s)
		}
	}
	commit(t, repo, spans, nil)
	for _, viz := range []string{"stat", "timeseries"} {
		p := &Panel{Viz: viz, Query: &Spec{From: "spans"}}
		if viz == "timeseries" {
			p.Query.By = []string{"service"}
		}
		scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Interval: 5 * time.Minute}
		ms := measuresOf(t, "spans", "p95(duration_ms)", "p50(duration_ms)", "count()", "avg(duration_ms)")
		rolled, err := compileQuery(p, ms, nil, scope)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rolled.SQL, "list_transform(list(hist)") {
			t.Fatal("correlated map-list plan returned")
		}
		raw, err := compileAggregateQuery(p, ms, nil, scope, false)
		if err != nil {
			t.Fatal(err)
		}
		// A histogram estimates a weighted rank. With gaps between samples,
		// quantile_cont interpolates a different value; compare its median to
		// the exact discrete rank when checking the one-bucket error bound.
		raw.SQL = strings.ReplaceAll(raw.SQL, `quantile_cont("duration_ms", 0.5)`, `quantile_disc("duration_ms", 0.5)`)

		got, want := readCompiledFrame(t, d, rolled, scope), readCompiledFrame(t, d, raw, scope)
		if got.Rows != want.Rows || !reflect.DeepEqual(got.Columns, want.Columns) {
			t.Fatalf("%s shapes differ: %+v %+v", viz, got, want)
		}
		compare := func(c int, a, b any) {
			t.Helper()
			x, ok := a.(float64)
			if !ok {
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("%s %s: %v != %v", viz, got.Columns[c].Name, a, b)
				}
				return
			}
			y := b.(float64)
			tolerance := 1e-9
			if got.Columns[c].Name == "p95" || got.Columns[c].Name == "p50" {
				tolerance = math.Abs(y) * .09051
			}
			if math.Abs(x-y) > tolerance {
				t.Fatalf("%s %s: %g != %g", viz, got.Columns[c].Name, x, y)
			}
		}
		for c := range got.Columns {
			for row := range got.Rows {
				compare(c, got.Values[c][row], want.Values[c][row])
			}
		}
		if len(got.Totals) != len(want.Totals) {
			t.Fatal("totals differ")
		}
		for c := range got.Totals {
			compare(c, got.Totals[c], want.Totals[c])
		}
	}
}
