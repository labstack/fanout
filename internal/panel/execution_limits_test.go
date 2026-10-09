package panel

import (
	"context"
	"errors"
	"fmt"
	"github.com/labstack/fanout/internal/queryrows"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type slowBatchEngine struct {
	Engine
	calls atomic.Int32
}

func (e *slowBatchEngine) QueryContext(ctx context.Context, _ string, _ ...any) (queryrows.Rows, error) {
	e.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBatchDeadlineKeepsCompletedPanels(t *testing.T) {
	e := newFixtureExecutor(t)
	slow := &slowBatchEngine{Engine: e.engine}
	e.engine = slow
	e.batchTimeout = 30 * time.Millisecond
	e.timeout = 100 * time.Millisecond
	e.parallel = 1
	d := Dashboard{Name: "Deadline", Panels: []Panel{{ID: "fast", Title: "Fast", Viz: "text", Content: "done"}}}
	for _, id := range []string{"slow", "queued"} {
		d.Panels = append(d.Panels, Panel{ID: id, Title: id, Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}}})
	}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(results) != 3 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	if results[0].Status != StatusOK {
		t.Fatalf("completed=%+v", results[0])
	}
	for _, r := range results[1:] {
		assertRetryable(t, r, true)
		if r.Status != StatusError || r.Error != "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard." {
			t.Errorf("unfinished=%+v", r)
		}
	}
	if slow.calls.Load() != 1 {
		t.Errorf("started %d slow panels, want only one", slow.calls.Load())
	}
}

func TestRunPanelSelection(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d, Panels: []string{"notes", "notes"}})
	if err != nil || len(results) != 1 {
		t.Fatalf("duplicate results=%+v err=%v", results, err)
	}
	_, err = e.Run(t.Context(), RunRequest{Dashboard: d, Panels: []string{"notes", "notes", "notes", "notes", "notes", "notes"}})
	var problems Problems
	if !errors.As(err, &problems) || problems[0].Path != "panels" {
		t.Fatalf("oversized selection=%v", err)
	}
}

func TestValidationCaps(t *testing.T) {
	for _, target := range []string{"query", "variable", "sql"} {
		t.Run(target, func(t *testing.T) {
			d := shopDashboard()
			path := "panels[0].query.where"
			limit := "16"
			filters := make([]string, 17)
			for i := range filters {
				filters[i] = "service = 'checkout'"
			}
			switch target {
			case "query":
				d.Panels[0].Query.Where = filters
			case "variable":
				d.Variables[0].Where = filters
				path = "variables[0].where"
			case "sql":
				d.Panels[0].Query = nil
				d.Panels[0].SQL = "SELECT " + strings.Repeat(" ", 8001)
				path = "panels[0].sql"
				limit = "8000"
			}
			Normalize(&d)
			problems := Validate(&d)
			for _, p := range problems {
				if p.Path == path && strings.Contains(p.Message, limit) {
					return
				}
			}
			t.Fatalf("missing %s limit %s: %v", path, limit, problems)
		})
	}
}

func TestBatchCellBudget(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Budget"}
	// Wide SQL frames test the shared budget independently of the row cap.
	for i := range 40 {
		d.Panels = append(d.Panels, Panel{ID: fmt.Sprintf("p%d", i), Title: "wide", Viz: "table", SQL: "SELECT a.span_id, a.service, a.operation, a.http_route, a.status, a.duration_ms FROM spans a, spans b WHERE $__window(a.start_time)"})
	}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	cells := 0
	truncated := false
	for _, r := range results {
		if r.Frame == nil {
			t.Fatalf("frame missing: %+v", r)
		}
		cells += r.Frame.Rows*len(r.Frame.Columns) + len(r.Frame.Totals)
		truncated = truncated || r.Frame.Truncated
	}
	if cells > 200000 || !truncated {
		t.Fatalf("batch cells=%d truncated=%v", cells, truncated)
	}
}

func TestSeriesRowCap(t *testing.T) {
	// Many groups in each time bucket exceed the per-frame row limit.
	duck, repo := newTestEngine(t)
	spans := shopSpans()
	base := spans[0]
	for i := range 4000 {
		sp := base
		sp.SpanID = fmt.Sprintf("extra%d", i)
		sp.TraceID = sp.SpanID
		sp.ServiceName = fmt.Sprintf("svc%d", i%20)
		sp.StartUnixNanos = fixtureStart.Add(time.Duration(i/20) * 10 * time.Second).UnixNano()
		spans = append(spans, sp)
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := shopDashboard()
	d.Panels = d.Panels[:1]
	d.Panels[0].Query.Where = nil
	d.Panels[0].Query.By = []string{"service"}
	d.Panels[0].Query.Bucket = "10s"
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if f := results[0].Frame; f.Rows <= 1000 || f.Truncated {
		t.Fatalf("series rows=%d truncated=%v", f.Rows, f.Truncated)
	}
}

type finalCountingEngine struct {
	Engine
	calls int
}

func (e *finalCountingEngine) QueryContext(ctx context.Context, q string, args ...any) (queryrows.Rows, error) {
	e.calls++
	return e.Engine.QueryContext(ctx, q, args...)
}
func TestBarSkipsTotalsAndGaugeSkipsComparison(t *testing.T) {
	e := newFixtureExecutor(t)
	e.parallel = 1
	engine := &finalCountingEngine{Engine: e.engine}
	e.engine = engine
	d := shopDashboard()
	d.Panels = []Panel{d.Panels[2], d.Panels[1]}
	d.Panels[1].Viz = "gauge"
	lo, hi := 0.0, 1000.0
	d.Panels[1].Min = &lo
	d.Panels[1].Max = &hi
	compare := true
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d, Compare: &compare})
	if err != nil {
		t.Fatal(err)
	}
	if engine.calls != 3 {
		t.Errorf("queries=%d want 3", engine.calls)
	}
	for _, r := range results {
		if r.Previous != nil || (r.ID == d.Panels[0].ID && len(r.Frame.Totals) != 0) || (r.ID == d.Panels[1].ID && len(r.Frame.Totals) == 0) {
			t.Errorf("unused frames=%+v", r)
		}
	}
}

func TestEmptyMultiSelectMatchesNothing(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Variables[0].Multi = true
	for i := range d.Panels {
		if d.Panels[i].Query != nil {
			for j := range d.Panels[i].Query.Where {
				d.Panels[i].Query.Where[j] = strings.ReplaceAll(d.Panels[i].Query.Where[j], "service = $service", "service IN $service")
			}
		}
	}
	d.Variables[1].Where = []string{"service IN $service"}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d, Panels: []string{"latency"}, Vars: map[string]Value{"service": {Values: []string{}}}})
	if err != nil || results[0].Status != StatusEmpty {
		t.Fatalf("empty selection=%+v err=%v", results, err)
	}
}

func TestPanelErrorsRedactPaths(t *testing.T) {
	for _, path := range []string{"/srv/data/telemetry/a.parquet", "/tmp/duckdb.db", "C:\\data\\telemetry\\a.parquet"} {
		r := failed(Result{ID: "a"}, fmt.Errorf("IO Error: cannot open '%s'", path), context.Canceled)
		if strings.Contains(r.Error, path) || !strings.Contains(r.Error, "<path>") {
			t.Errorf("leaked path: %s", r.Error)
		}
	}
}

func TestNormalizeBetter(t *testing.T) {
	for _, tc := range []struct{ measure, filter, want string }{
		{"p95(duration_ms)", "", "lower"}, {"error_rate()", "", "lower"}, {"count()", "status = 'STATUS_CODE_ERROR'", "lower"},
		{"count()", "status != 'STATUS_CODE_ERROR'", "higher"}, {"count()", "NOT (status = 'STATUS_CODE_ERROR')", "higher"}, {"rate()", "", "higher"}, {"count()", "", "higher"}, {"count_distinct(service)", "", ""},
	} {
		d := Dashboard{Panels: []Panel{{Viz: "stat", Query: &Query{From: "spans", Measures: []string{tc.measure}}}}}
		if tc.filter != "" {
			d.Panels[0].Query.Where = []string{tc.filter}
		}
		Normalize(&d)
		if d.Panels[0].Better != "" {
			t.Fatal("Normalize persisted a direction")
		}
		if inferBetter(&d.Panels[0]) != tc.want {
			t.Errorf("%s %s better=%q want=%q", tc.measure, tc.filter, inferBetter(&d.Panels[0]), tc.want)
		}
	}
}
