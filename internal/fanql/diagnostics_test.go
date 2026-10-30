package fanql

import (
	"strings"
	"testing"
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
