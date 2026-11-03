package query

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/queryrows"
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
		{"logs", "logs", "stat", "count()", "", "", false},
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
				if strings.Contains(c.SQL, "FROM span_rollup") != tc.want {
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
