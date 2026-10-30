package query

import (
	"context"
	"github.com/labstack/fanout/internal/queryrows"
	"strings"
	"testing"
	"time"
)

func TestCheckCountIfFilters(t *testing.T) {
	duck, _ := newTestEngine(t)
	for _, tc := range []struct{ text, want string }{
		{"count_if(service = $service) as e", "variables are not allowed inside count_if"},
		{"count_if(regexp_replace(service, 'a', 'b') = 'x') as e", "not allowed"},
		{"count_if(" + strings.Repeat(" ", 480) + "status = 'STATUS_CODE_ERROR') as e", "filters are 1 to 500 characters"},
	} {
		d := exprDashboard(tc.text)
		_, problems, err := Check(t.Context(), duck, &d)
		if err != nil || len(problems) != 1 || problems[0].Path != "panels[0].query.measures[0]" || !strings.Contains(problems[0].Message, tc.want) {
			t.Errorf("%s: %+v %v", tc.text, problems, err)
		}
	}
	d := exprDashboard("count_if(status = 'STATUS_CODE_ERROR') as e")
	checked, problems, err := Check(t.Context(), duck, &d)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%+v %v", problems, err)
	}
	leaf := checked.Measures["e"][0].Expr.(MeasureCountIf)
	filter, err := checkFilter(t.Context(), duck, signals["spans"], nil, "status = 'STATUS_CODE_ERROR'")
	if err != nil || leaf.Expr != filter.Expr || leaf.Expr == "" {
		t.Fatalf("%+v %+v %v", leaf, filter, err)
	}
}
func TestCompileExpressionSQL(t *testing.T) {
	duck, _ := newTestEngine(t)
	text := "count_if(TRY_CAST(http_status_code AS INTEGER) >= 500) / count() * 100 as pct_5xx"
	d := exprDashboard(text)
	d.Panels[0].Query.By = []string{"http_route"}
	d.Panels[0].Query.Where = []string{"kind = 'SPAN_KIND_SERVER'"}
	checked, problems, err := Check(t.Context(), duck, &d)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%+v %v", problems, err)
	}
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}
	compiled, err := compileQuery(&d.Panels[0], checked.Measures["e"], checked.Filters["e"], scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"count(*) FILTER (WHERE (", "TRY_CAST(http_status_code AS INTEGER)", " / nullif(count(*)::DOUBLE, 0) * 100 AS \"pct_5xx\""} {
		if !strings.Contains(compiled.SQL, part) {
			t.Fatalf("missing %s: %s", part, compiled.SQL)
		}
	}
	d = exprDashboard("(count_if(duration_ms <= 500) + count_if(duration_ms > 500 AND duration_ms <= 2000) / 2) / count() as apdex")
	checked, problems, err = Check(t.Context(), duck, &d)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%+v %v", problems, err)
	}
	compiled, err = compileQuery(&d.Panels[0], checked.Measures["e"], nil, scope)
	if err != nil || strings.Count(compiled.SQL, "FILTER (WHERE") != 2 || !strings.Contains(compiled.SQL, " / nullif(2, 0)") || !strings.Contains(compiled.SQL, " / nullif(count(*)::DOUBLE, 0)") {
		t.Fatalf("%s %v", compiled.SQL, err)
	}
	d = exprDashboard("count_if(status = 'STATUS_CODE_ERROR') as a", "p95(duration_ms)")
	d.Panels[0].Viz = "scatter"
	d.Panels[0].Query.By = []string{"service"}
	checked, problems, err = Check(t.Context(), duck, &d)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%+v %v", problems, err)
	}
	compiled, err = compileItems(&d.Panels[0], checked.Measures["e"], nil, scope)
	if err != nil || !strings.Contains(compiled.SQL, "FILTER (WHERE") {
		t.Fatalf("%s %v", compiled.SQL, err)
	}
}

type expressionSQLCapture struct {
	Engine
	queries []string
}

func (e *expressionSQLCapture) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	e.queries = append(e.queries, text)
	return e.Engine.QueryContext(ctx, text, args...)
}
func TestCompileExpressionTrendsAndTotals(t *testing.T) {
	duck, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	capture := &expressionSQLCapture{Engine: duck}
	e := NewExecutor(capture, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, tc := range []struct{ measure, alias, fragment string }{{"count_if(status = 'STATUS_CODE_ERROR') / count() as e", "e", "FILTER (WHERE"}, {"share()", "share", "_trend_totals AS"}} {
		capture.queries = nil
		d := exprDashboard(tc.measure)
		p := &d.Panels[0]
		p.Query.By = []string{"service"}
		p.Query.Limit = 1
		p.Options = &Options{Columns: []ColumnFormat{{Field: tc.alias, Format: "sparkline"}}}
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || len(got) != 1 || got[0].Status != StatusOK || got[0].Frame.Trends == nil {
			t.Fatalf("%+v %v", got, err)
		}
		found := false
		for _, sql := range capture.queries {
			if strings.Contains(sql, "ORDER BY _t DESC") {
				found = true
				if !strings.Contains(sql, tc.fragment) {
					t.Fatalf("trend: %s", sql)
				}
				if tc.alias == "share" && (!strings.Contains(sql, "100.0*count(*)/nullif(max(_trend_total),0)") || !strings.Contains(sql, "FROM _trend_scope JOIN _trend_totals")) {
					t.Fatalf("share scope changed: %s", sql)
				}
			}
		}
		if !found {
			t.Fatal("no trend query")
		}
	}
	capture.queries = nil
	d := exprDashboard("count_if(status = 'STATUS_CODE_ERROR') as e")
	d.Panels[0].Viz = "stat"
	d.Panels[0].Reduce = "window"
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(got) != 1 || got[0].Status != StatusOK || got[0].Frame.Totals[1] != float64(6) {
		t.Fatalf("%+v %v", got, err)
	}
	found := false
	for _, sql := range capture.queries {
		if strings.Contains(sql, "FILTER (WHERE") && !strings.Contains(sql, "time_bucket(") {
			found = true
		}
	}
	if !found {
		t.Fatal("totals did not use expression compiler")
	}
}
