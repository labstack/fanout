package panel

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type countingTableTrendEngine struct {
	Engine
	queries int
}

func (e *countingTableTrendEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	if strings.HasPrefix(text, "SELECT epoch_ms(time_bucket(") || strings.HasPrefix(text, "WITH _trend_scope AS") {
		e.queries++
	}
	return e.Engine.QueryContext(ctx, text, args...)
}

func TestM2TableFixOneTrendQuery(t *testing.T) {
	e := newFixtureExecutor(t)
	counter := &countingTableTrendEngine{Engine: e.engine}
	e.engine = counter
	d := Dashboard{Name: "Three rows", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", By: []string{"service", "http_route"}, Measures: []string{"count()", "avg(duration_ms) as latency"}}, Options: &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}, {Field: "latency", Format: "sparkline"}}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(got) != 1 || got[0].Status != StatusOK || got[0].Frame.Rows < 3 {
		t.Fatalf("table: %+v %v", got, err)
	}
	if counter.queries != 1 {
		t.Fatalf("trend queries=%d, want exactly one for %d rows", counter.queries, got[0].Frame.Rows)
	}
}

func TestM2TableFixHealthBudgetNotesEveryFrame(t *testing.T) {
	first := newFrame([]Column{{Name: "n", Type: "number", Role: "measure"}})
	first.bucketed = true
	for range 199999 {
		appendPanelRow(first, float64(1))
	}
	current := &Frame{Note: "Current scope.", Health: &HealthFrame{ErrorTrend: []float64{1, 2, 3}}}
	previous := &Frame{Note: "Previous scope.", Health: &HealthFrame{ErrorTrend: []float64{4, 5}}}
	limitBatchFrames([]Result{{Frame: first}, {Frame: current, Previous: previous}})
	for _, f := range []*Frame{current, previous} {
		if !f.Truncated || !strings.Contains(f.Note, "Health error trend was truncated to stay within the response budget.") || !strings.Contains(f.Note, "scope.") {
			t.Fatalf("missing health truncation note: %+v", f)
		}
	}
	if len(current.Health.ErrorTrend) != 1 || current.Health.ErrorTrend[0] != 3 || len(previous.Health.ErrorTrend) != 0 {
		t.Fatalf("health budget: current=%v previous=%v", current.Health.ErrorTrend, previous.Health.ErrorTrend)
	}
}

type overflowingTableTrendEngine struct{ Engine }

func (e overflowingTableTrendEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	if strings.HasPrefix(text, "SELECT epoch_ms(time_bucket(") || strings.HasPrefix(text, "WITH _trend_scope AS") {
		return e.Engine.QueryContext(ctx, "SELECT i::BIGINT, 'checkout', 1.0 FROM range(10002) t(i)")
	}
	return e.Engine.QueryContext(ctx, text, args...)
}

func TestM2TableFixTrendNotesAppend(t *testing.T) {
	for _, mode := range []string{"failure", "allocation", "query"} {
		t.Run(mode, func(t *testing.T) {
			e := newFixtureExecutor(t)
			p := Panel{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", By: []string{"service"}, Measures: []string{"count()"}}, Options: &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}}}}
			d := Dashboard{Name: "Notes", Panels: []Panel{p}}
			Normalize(&d)
			checked, problems, err := Check(t.Context(), e.engine, &d)
			if err != nil || len(problems) > 0 {
				t.Fatalf("check: %v %v", problems, err)
			}
			f := newFrame([]Column{{Name: "service", Type: "string", Role: "dimension"}, {Name: "count", Type: "number", Role: "measure"}})
			rows := 1
			if mode == "allocation" {
				rows = 400
			}
			for range rows {
				appendPanelRow(f, "checkout", float64(120))
			}
			f.Note = "Existing scope note."
			want := "Table trends are limited by the cell budget."
			switch mode {
			case "failure":
				e.engine = failingTableTrendEngine{e.engine}
				want = "Table trends unavailable."
			case "query":
				e.engine = overflowingTableTrendEngine{e.engine}
			}
			err = e.attachTableTrends(t.Context(), &p, checked, Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}, f)
			if (err != nil) != (mode == "failure") || f.Note != "Existing scope note. "+want || f.Rows != rows {
				t.Fatalf("note=%q rows=%d err=%v", f.Note, f.Rows, err)
			}
		})
	}
}

func TestM2TableFixBatchTrendNote(t *testing.T) {
	first := newFrame([]Column{{Name: "n", Type: "number", Role: "measure"}})
	first.bucketed = true
	for range 199997 {
		appendPanelRow(first, float64(1))
	}
	f := newFrame([]Column{{Name: "n", Type: "number", Role: "measure"}})
	appendPanelRow(f, float64(1))
	f.Note = "Existing note."
	f.Trends = map[string][][]any{"a": {{1, 2, 3}}, "b": {{4, 5, 6}}}
	limitBatchFrames([]Result{{Frame: first}, {Frame: f}})
	if f.Rows != 1 || !f.Truncated || f.Note != "Existing note. Some row trends were omitted to stay within the response budget." {
		t.Fatalf("trend-only budget: %+v", f)
	}
}

func TestM2TableFixServiceLinkVariableHint(t *testing.T) {
	p := Panel{Viz: "table", Options: &Options{Columns: []ColumnFormat{{Field: "service", Format: "service_link"}}}}
	var problems Problems
	validateDisplayOptions(&p, "panels[0]", nil, &problems)
	if len(problems) != 1 || problems[0].Path != "panels[0].options.columns" || problems[0].Message != "service_link requires a query or custom variable" || problems[0].Hint == "" {
		t.Fatalf("missing path/hint: %+v", problems)
	}
}

// This structural regression checks the requested single-parse invariant;
// parsing twice has identical output and cannot be caught by result assertions.
func TestM2TableFixSingleMeasureParse(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "deploy_split.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "validateDisplayOptions" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "parseMeasures" {
					count++
				}
			}
			return true
		})
	}
	if count != 1 {
		t.Fatalf("parseMeasures calls=%d, want one", count)
	}
}
