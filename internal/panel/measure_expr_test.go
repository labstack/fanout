package panel

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseMeasureKeepsSingleCalls(t *testing.T) {
	for _, tc := range []struct {
		sig, text, fn, field, alias, unit string
		q                                 float64
		additive                          bool
	}{
		{"spans", "  p95(   duration_ms   )  ", "p95", "duration_ms", "", "ms", .95, false},
		{"spans", "COUNT( )", "count", "", "", "count", 0, true},
		{"spans", "quantile(duration_ms, 0.999) as q", "quantile", "duration_ms", "q", "ms", .999, false},
		{"spans", "count_distinct(attributes['user.id'])", "count_distinct", "attributes['user.id']", "", "count", 0, false},
		{"spans", "rate()", "rate", "", "", "per_second", 0, true},
		{"spans", "share()", "share", "", "", "percent", 0, false},
		{"metrics", "last(value)", "last", "value", "", "none", 0, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			m, err := ParseMeasure(tc.sig, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			field := ""
			if m.Field != nil {
				field = m.Field.Text
			}
			if m.Func != tc.fn || field != tc.field || m.Q != tc.q || m.Alias != tc.alias || m.Unit != tc.unit || m.Additive != tc.additive || m.Text != strings.TrimSpace(tc.text) || m.Leaves != 1 || m.IsExpression() {
				t.Fatalf("got %+v field=%s", m, field)
			}
			if _, ok := m.Expr.(MeasureCall); !ok {
				t.Fatalf("call tree: %#v", m.Expr)
			}
		})
	}
}
func TestParseMeasureExpressions(t *testing.T) {
	for _, tc := range []struct {
		text, unit string
		leaves     int
	}{
		{"count_if(status = 'STATUS_CODE_ERROR') / count() * 100 as err_pct", "percent", 2},
		{"count_if(status = 'STATUS_CODE_ERROR') / count() as r", "ratio", 2},
		{"p95(duration_ms) - p50(duration_ms) as spread", "ms", 2},
		{"count() * -1 as neg", "count", 1},
		{"sum(duration_ms) / count() as x", "none", 2},
		{"count() * 0 as zero", "count", 1},
		{"count_if(a = 1 AND (b IS NULL OR b != 'x)')) as e", "count", 1},
	} {
		t.Run(tc.text, func(t *testing.T) {
			m, err := ParseMeasure("spans", tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if m.Unit != tc.unit || m.Leaves != tc.leaves || m.Additive {
				t.Fatalf("got %+v", m)
			}
		})
	}
	m, _ := ParseMeasure("spans", "count_if(status = 'STATUS_CODE_ERROR') / count() * 100 as err_pct")
	root, ok := m.Expr.(MeasureBinary)
	if !ok || root.Op != '*' || m.Alias != "err_pct" {
		t.Fatalf("%#v", m)
	}
	ratio := root.L.(MeasureBinary)
	if ratio.Op != '/' || ratio.L.(MeasureCountIf).Filter != "status = 'STATUS_CODE_ERROR'" || ratio.R.(MeasureCall).Func != "count" || root.R.(MeasureConst).Value != 100 {
		t.Fatalf("%#v", root)
	}
	neg, _ := ParseMeasure("spans", "count() * -1 as neg")
	if neg.Expr.(MeasureBinary).R.(MeasureNeg).X.(MeasureConst).Value != 1 {
		t.Fatal(neg)
	}
	quoted, _ := ParseMeasure("spans", "count_if(a = 1 AND (b IS NULL OR b != 'x)')) as e")
	if quoted.Expr.(MeasureCountIf).Filter != "a = 1 AND (b IS NULL OR b != 'x)')" {
		t.Fatal(quoted)
	}
	for _, tc := range []struct{ sig, text, want string }{
		{"spans", "count() / 2", "name this expression with as, e.g. count() / 2 as half"},
		{"spans", "share() / 2 as half", "share() cannot be combined; it is already a percentage of the scope"},
		{"spans", "1 as one", "a measure needs an aggregate; constants can only scale one, e.g. count() * 100"},
		{"spans", "1 / 2 as half", "a measure needs an aggregate; constants can only scale one, e.g. count() * 100"},
		{"spans", "-(5) as neg", "a measure needs an aggregate; constants can only scale one, e.g. count() * 100"},
		{"metrics", "count_if(name = 'x') as n", "measure arithmetic is not available for metrics"},
		{"metrics", "count() * 2 as d", "measure arithmetic is not available for metrics"},
		{"spans", "count_if() as n", "count_if needs a filter"},
		{"spans", "count_if(status = 'x'", "unclosed count_if"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			_, err := ParseMeasure(tc.sig, tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want %s", err, tc.want)
			}
		})
	}
}

// A balanced expression isolates node budgets from the independent depth cap.
func balancedMeasureNodes(n int) string { return balancedMeasureTree(n, 0) }
func balancedMeasureTree(n, level int) string {
	if n == 1 {
		return "1"
	}
	left := (n - 1) / 2
	if left%2 == 0 {
		left--
	}
	op := "+"
	if level%2 != 0 {
		op = "*"
	}
	l, r := balancedMeasureTree(left, level+1), balancedMeasureTree(n-1-left, level+1)
	if op == "*" {
		if left > 1 {
			l = "(" + l + ")"
		}
		if n-1-left > 1 {
			r = "(" + r + ")"
		}
	}
	return l + op + r
}
func aggregateInBalanced(n int) string {
	return strings.Replace(balancedMeasureNodes(n), "1", "count()", 1)
}
func TestMeasureBudgets(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{aggregateInBalanced(65) + " as n", "has 65 nodes; a measure is at most 64"},
		{"count_if(" + strings.Repeat("x ", 252) + ") as n", "has 257 tokens; a measure is at most 256"},
		{strings.Repeat("(", 9) + "count()" + strings.Repeat(")", 9) + " as n", "nests 9 deep; a measure nests at most 8"},
		{"count() + ((((((((1)))))))) as n", "nests 9 deep; a measure nests at most 8"},
		{"(count()+count()+count())+(count()+count()+count())+(count()+count()+count()) as n", "has 9 aggregates; a measure has 1 to 8"},
	} {
		_, err := ParseMeasure("spans", tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("got %v want %s", err, tc.want)
		}
	}
	for _, text := range []string{strings.Repeat(" ", 10*1024) + "p95(duration_ms)", strings.Repeat("(", 8) + "count()" + strings.Repeat(")", 8)} {
		if _, err := ParseMeasure("spans", text); err != nil {
			t.Fatal(err)
		}
	}
	sig, _ := lookupSignal("spans")
	var problems Problems
	texts := []string{}
	for i, size := range []int{63, 63, 63, 63, 5} {
		texts = append(texts, fmt.Sprintf("%s as n%d", aggregateInBalanced(size), i))
	}
	parseMeasures(sig, texts, "panels[0].query.measures", &problems)
	if len(problems) != 1 || problems[0].Path != "panels[0].query.measures" || problems[0].Message != "measures have 257 nodes together; a query has at most 256" {
		t.Fatalf("%+v", problems)
	}
	problems = nil
	eight := "(count()+count())+(count()+count())+(count()+count())+(count()+count())"
	parseMeasures(sig, []string{eight + " as a", eight + " as b", eight + " as c", "count() as d"}, "m", &problems)
	if len(problems) != 1 || problems[0].Message != "measures have 25 aggregates together; a query has at most 24" {
		t.Fatalf("%+v", problems)
	}
}
func exprDashboard(measures ...string) Dashboard {
	d := Dashboard{Name: "Expressions", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "e", Title: "E", Viz: "table", Query: &Query{From: "spans", Measures: measures}}}}
	Normalize(&d)
	return d
}
func TestValidateRejectsStructuredBudgetOverrunsAtPath(t *testing.T) {
	d := exprDashboard("count()", aggregateInBalanced(65)+" as n")
	p := Validate(&d)
	if len(p) != 1 || p[0].Path != "panels[0].query.measures[1]" {
		t.Fatalf("%+v", p)
	}
	d = exprDashboard("1 as one")
	p = Validate(&d)
	if len(p) != 1 || p[0].Path != "panels[0].query.measures[0]" {
		t.Fatalf("%+v", p)
	}
}
func TestInferBetterForExpressions(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"error_rate() * 100 as e", "lower"}, {"100 - error_rate() as e", "higher"}, {"-p95(duration_ms) as e", "higher"},
		{"count_if(status = 'STATUS_CODE_ERROR') / count() as r", "lower"}, {"count_if(kind = 'SPAN_KIND_SERVER') / count() as r", ""}, {"count_if(status = 'STATUS_CODE_ERROR') as e", "lower"},
	} {
		d := exprDashboard(tc.text)
		if got := inferBetter(&d.Panels[0]); got != tc.want {
			t.Errorf("%s: %s want %s", tc.text, got, tc.want)
		}
	}
}
func FuzzParseMeasure(f *testing.F) {
	for _, s := range []string{"count()", "count_if(status = 'STATUS_CODE_ERROR') / count() * 100 as r", "-p95(duration_ms) as n", "share() / 2 as x", strings.Repeat("(", 1000)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 65536 {
			return
		}
		started := time.Now()
		m, err := ParseMeasure("spans", text)
		if time.Since(started) > 50*time.Millisecond {
			t.Errorf("parse exceeded 50ms")
		}
		if err == nil && measureDepth(m.Expr) > 8 {
			t.Errorf("depth exceeds 8: %+v", m)
		}
	})
}

func measureDepth(e MeasureExpr) int {
	switch e := e.(type) {
	case MeasureBinary:
		return 1 + max(measureDepth(e.L), measureDepth(e.R))
	case MeasureNeg:
		return 1 + measureDepth(e.X)
	}
	return 0
}
