package fanql

import (
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/query"
)

func TestCompileRejectsReservedVariablesInEveryPosition(t *testing.T) {
	for _, text := range []string{
		"spans where $__all", "logs where -$__all", "logs where service:$__all", "spans where service in $__all",
		"spans | count_distinct($__all)", "spans | count() by $__all", "spans sort $__all", "spans | count() as $__all",
		"spans where `service = $__all`", "spans | count_if($__all)",
	} {
		c, ps := Compile(text, Options{})
		if c != nil || len(ps) == 0 {
			t.Errorf("reserved name compiled: %q %+v", text, c)
			continue
		}
		if !strings.Contains(ps[0].Message, "$__ names are reserved") {
			t.Errorf("reserved-name diagnostic for %q: %+v", text, ps)
		}
	}
}

func TestCompileMeasureFieldPunctuationHasOneDiagnostic(t *testing.T) {
	for _, tt := range []struct{ text, message string }{
		{"spans | count_distinct(x-y)", "unexpected - in count_distinct; write attributes['x-y']"},
		{"spans | avg(user.)", "unexpected . after user; write the full key, e.g. user.id"},
	} {
		c, ps := Compile(tt.text, Options{})
		if c != nil || len(ps) != 1 || ps[0].Message != tt.message {
			t.Errorf("%q: %+v", tt.text, ps)
		}
	}
}

func TestCompileSortAcceptsOnlyOneDirection(t *testing.T) {
	for _, text := range []string{"spans sort duration asc desc", "spans sort duration desc asc", "spans sort +duration desc"} {
		c, ps := Compile(text, Options{})
		if c != nil || len(ps) != 1 || ps[0].Message != "sort takes one direction" {
			t.Errorf("%q: %+v", text, ps)
		}
	}
}

func TestCompileHintsUseTheActualOperatorAndSortKey(t *testing.T) {
	for _, tt := range []struct{ text, message, hint string }{
		{"logs where body~~timeout", "~~ is SQL LIKE; use like, ilike or ~", `write body like "timeout%" for a pattern, or body ~ "timeout" for contains`},
		{"logs limit 10 sort time", "sort comes before limit: … sort time limit 10", "write logs sort time limit 10"},
		{"spans where k:$", "unexpected $", ""},
	} {
		c, ps := Compile(tt.text, Options{})
		if c != nil || len(ps) != 1 || ps[0].Message != tt.message || ps[0].Hint != tt.hint {
			t.Errorf("%q: %+v", tt.text, ps)
		}
	}
}

func TestCompileReportsIndependentProblemsInSourceOrder(t *testing.T) {
	for _, tc := range []struct {
		text     string
		messages []string
	}{
		{"spans where foo~~x and bar~~y", []string{"~~ is SQL LIKE", "~~ is SQL LIKE"}},
		{"spans where service: | avg(user.)", []string{"service: needs a value", "unexpected . after user"}},
		{"spans where foo~~x bar~~y", []string{"~~ is SQL LIKE", "~~ is SQL LIKE"}},
		{"spans where duration>5 and duration<10", []string{"duration needs a unit", "duration needs a unit"}},
	} {
		_, ps := Compile(tc.text, Options{})
		if len(ps) != len(tc.messages) {
			t.Errorf("%q: want two independent problems, got %+v", tc.text, ps)
			continue
		}
		for i, want := range tc.messages {
			if !strings.HasPrefix(ps[i].Message, want) || ps[i].Span == nil {
				t.Errorf("%q problem %d: %+v", tc.text, i, ps[i])
			}
		}
		if ps[0].Span != nil && ps[1].Span != nil && ps[0].Span.Start >= ps[1].Span.Start {
			t.Errorf("problems out of source order: %+v", ps)
		}
	}
}

func TestCompileReportsGroupingAlongsideIndependentProblem(t *testing.T) {
	_, ps := Compile("spans where a:1 b:2 or c:3 and foo~~x", Options{})
	if len(ps) != 2 || ps[0].Message != "mixed and/or needs parentheses" || ps[1].Message != "~~ is SQL LIKE; use like, ilike or ~" {
		t.Fatalf("independent grouping and operator diagnostics: %+v", ps)
	}
	if len(ps[0].FixIts) != 2 || ps[0].Span.Start >= ps[1].Span.Start {
		t.Fatalf("grouping repairs and source order: %+v", ps)
	}
}

func TestCompileMissingValuePreservesFollowingFilters(t *testing.T) {
	for _, text := range []string{
		"spans where service: and env:x",
		"spans where service: or env:x",
		"spans where service: not env:x",
		"service: env:",
		"service: env:x",
		"logs where service: api:x",
	} {
		options := Options{}
		if strings.HasPrefix(text, "service:") {
			options.Source = "spans"
		}
		_, ps := Compile(text, options)
		want := 1
		if text == "service: env:" {
			want = 2
		}
		if len(ps) != want || ps[0].Message != "service: needs a value; e.g. service:api" || ps[0].Span.Start != strings.Index(text, "service:") {
			t.Errorf("%q: %+v", text, ps)
		}
		if len(ps) == 2 && ps[1].Message != "env: needs a value; e.g. env:value" {
			t.Errorf("next filter: %+v", ps)
		}
	}
}

func TestCompileReportsProblemAfterNestedGrouping(t *testing.T) {
	_, ps := Compile("spans where (a:1 b:2 or c:3) and foo~~x", Options{})
	if len(ps) != 2 || len(ps[0].FixIts) != 2 || ps[1].Message != "~~ is SQL LIKE; use like, ilike or ~" {
		t.Fatalf("independent problem after grouping: %+v", ps)
	}
}

func TestCompileReportsIndependentSiblingGroupingProblems(t *testing.T) {
	_, ps := Compile("spans where (a:1 b:2 or c:3) and (d:4 e:5 or f:6)", Options{})
	if len(ps) != 2 {
		t.Fatalf("independent grouping diagnostics: %+v", ps)
	}
	for _, p := range ps {
		if p.Message != "mixed and/or needs parentheses" || len(p.FixIts) != 2 {
			t.Fatalf("grouping repair: %+v", p)
		}
	}
}

func TestCompileReportsSyntaxAndFieldProblemsInOnePass(t *testing.T) {
	_, ps := Compile("spans where opération:café 日本 kind:server or status:error", Options{})
	if len(ps) != 2 || ps[0].Message != "mixed and/or needs parentheses" || ps[1].Message != `"opération" needs brackets` {
		t.Fatalf("%+v", ps)
	}
	_, ps = Compile("from spans where service:cart", Options{})
	if len(ps) != 1 || len(ps[0].FixIts) != 1 || ps[0].FixIts[0].Text != "spans" {
		t.Fatalf("from cascades or lacks a fix-it: %+v", ps)
	}
}

func TestCompileQueryNoVizAcceptsIsAProblem(t *testing.T) {
	_, ps := Compile("spans | p95(duration), count() every 5m", Options{})
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "different units") || ps[0].Span == nil || ps[0].Span.Start != 8 || ps[0].Span.End != 30 {
		t.Fatalf("%+v", ps)
	}
	if c, ps := Compile("spans | p95(duration), p99(duration) every 5m", Options{}); c == nil || len(ps) != 0 || c.Compatible[0].Viz != "timeseries" {
		t.Fatalf("same-unit measures: %+v", ps)
	}
}

func TestCompileNoticesWithoutViz(t *testing.T) {
	c, ps := Compile("spans | count() by service every 5m limit 2", Options{})
	if len(ps) != 0 || len(c.Notices) != 1 || c.Notices[0].Path != "limit" {
		t.Fatalf("bucketed limit: %+v %+v", c, ps)
	}
	c, ps = Compile("spans where $x", Options{})
	if len(ps) != 0 || c.Canonical != "spans where operation ~ $x" || len(c.Notices) != 1 || c.Notices[0].Message != "declared variables are needed to check $x" {
		t.Fatalf("variable free text: %+v %+v", c, ps)
	}
	if _, ps := Compile("logs where $x", Options{Variables: []query.Variable{{Name: "service"}}}); len(ps) != 1 || !strings.Contains(ps[0].Message, "$x") {
		t.Fatalf("undeclared variable free text: %+v", ps)
	}
}

// Problems without a span sort after every located problem.
func TestSortProblemsPutsUnlocatedLast(t *testing.T) {
	problems := query.Problems{
		{Message: "five", Span: &query.TextSpan{Start: 5, End: 6}},
		{Message: "none"},
		{Message: "one", Span: &query.TextSpan{Start: 1, End: 2}},
	}
	sortProblems(problems)
	var got []string
	for _, p := range problems {
		got = append(got, p.Message)
	}
	if strings.Join(got, ",") != "one,five,none" {
		t.Fatalf("order %v", got)
	}
}
