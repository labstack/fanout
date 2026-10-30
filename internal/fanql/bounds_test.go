package fanql

import (
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestEachInteractiveBoundIndependently(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		compile          bool
	}{
		{"characters", strings.Repeat("a", 4001), "a query is at most 4000 characters (8192 bytes)", false},
		{"bytes", strings.Repeat("𝔘", 2049), "a query is at most 4000 characters (8192 bytes)", false},
		{"tokens", "logs where " + strings.Repeat("k:0 ", 342), "a query is at most 1024 tokens", false},
		{"nodes", "logs " + strings.Repeat("a ", 513), "a query is at most 512 nodes", false},
		{"depth", "logs where " + strings.Repeat("(", 17) + "body:x" + strings.Repeat(")", 17), "a query has at most 16 levels of nesting depth", false},
		{"entries", "logs where " + strings.Repeat("body:x ", 17), "at most 16 filters", true},
		{"entry_bytes", `logs where body:"` + strings.Repeat("x", 492) + `"`, "this filter lowers to 501 bytes; filters are at most 500", true},
		{"list", "logs where k in (" + strings.Repeat("0,", 100) + "0)", "an in list is at most 100 values", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var problems panel.Problems
			if tc.compile {
				compiled, ps := Compile(tc.text, Options{})
				if compiled != nil {
					t.Fatal("bound produced an executable query")
				}
				problems = ps
			} else {
				parsed, ps := parse(tc.text, "")
				if parsed != nil {
					t.Fatal("bound produced a parsed query")
				}
				problems = ps
			}
			if len(problems) != 1 || problems[0].Message != tc.want || problems[0].Span == nil {
				t.Fatalf("got %+v; want one positioned %q", problems, tc.want)
			}
		})
	}
}

func TestAstralCharactersSpanInCodePoints(t *testing.T) {
	const text = `logs where body:"𝔘𝔫𝔦" x:1`
	parsed, ps := parse(text, "")
	if len(ps) != 0 || parsed == nil || len(parsed.Where) != 2 {
		t.Fatal(parsed, ps)
	}
	if got := parsed.Where[1].Span(); got != (span{Start: 22, End: 25}) {
		t.Fatalf("second term span = %+v", got)
	}
	_, ps = parse(text+" +", "")
	if len(ps) != 1 || ps[0].Message != "unexpected +" || ps[0].Span == nil || *ps[0].Span != (panel.TextSpan{Start: 26, End: 27}) {
		t.Fatalf("diagnostic after astral text = %+v", ps)
	}
}

func TestParseColonRecoveryAcrossWhitespace(t *testing.T) {
	for _, text := range []string{`""A: 0:0`, `logs where attributes['user name']: 'a b':c`} {
		q, problems := parse(text, "")
		if q != nil || len(problems) == 0 {
			t.Fatalf("malformed colon input accepted: %q", text)
		}
		found := false
		for _, problem := range problems {
			if problem.Span == nil || problem.Span.Start < 0 || problem.Span.End < problem.Span.Start || problem.Span.End > len([]rune(text)) {
				t.Fatalf("invalid diagnostic span: %+v", problem)
			}
			if strings.HasPrefix(problem.Message, "unexpected : after ") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing colon diagnostic for %q: %+v", text, problems)
		}
	}
}

func TestParseQuotedTokensStayLiteral(t *testing.T) {
	for _, tc := range []struct {
		text    string
		entries int
	}{
		{`logs 0 And in`, 2},
		{`logs where 0 "in"`, 2},
		{`logs where body "="`, 2},
		{`logs where body ":"`, 2},
		{`logs where body ">"`, 2},
		{`logs where body "!="`, 2},
		{`logs where body "~"`, 2},
		{`logs where body "!~"`, 2},
		{`logs where body "between"`, 2},
		{`logs where body "like"`, 2},
		{`logs where body "not" "in"`, 3},
		{`logs where body "is" "null"`, 3},
		{`logs where body "("`, 2},
		{`logs where body ")"`, 2},
		{`logs where body "|"`, 2},
		{`logs where service:api"*"`, 2},
		{`logs where service"*"`, 2},
		{`logs where starts_with(body, "x") "="`, 2},
		{`logs where (body "(")`, 1},
		{`logs where service:">"`, 1},
		{`logs where service:"="`, 1},
		{`logs where service:"<>"`, 1},
	} {
		t.Run(tc.text, func(t *testing.T) {
			compiled, problems := Compile(tc.text, Options{})
			if len(problems) != 0 || compiled == nil {
				t.Fatalf("literal became syntax: %+v", problems)
			}
			if len(compiled.Query.Where) != tc.entries {
				t.Fatalf("got %d entries, want %d", len(compiled.Query.Where), tc.entries)
			}
			again, problems := Compile(compiled.Canonical, Options{})
			if len(problems) != 0 || again == nil {
				t.Fatalf("canonical %q: %+v", compiled.Canonical, problems)
			}
			if compiled.Canonical != again.Canonical || !reflect.DeepEqual(compiled.Query, again.Query) {
				t.Fatalf("not a fixed point: %q -> %q", compiled.Canonical, again.Canonical)
			}
		})
	}
	if q, ps := parse(`logs | count"(")`, ""); q != nil || len(ps) == 0 {
		t.Fatal("quoted parenthesis opened a measure call")
	}
}

func TestParseCallsRequireAnIdentifierAndAdjacentParenthesis(t *testing.T) {
	for _, text := range []string{`spAns 0 -(0 0)`, `logs foo (bar baz)`, `logs 0 (foo bar)`, `logs 0(foo bar)`, `logs api-worker (a b)`, `logs user.name (a b)`} {
		t.Run(text, func(t *testing.T) {
			compiled, problems := Compile(text, Options{})
			if len(problems) != 0 || compiled == nil {
				t.Fatalf("free text became a call: %+v", problems)
			}
			if len(compiled.Query.Where) != 2 {
				t.Fatalf("entry count = %d, want 2", len(compiled.Query.Where))
			}
			again, problems := Compile(compiled.Canonical, Options{})
			if len(problems) != 0 || again == nil {
				t.Fatalf("canonical %q: %+v", compiled.Canonical, problems)
			}
			if compiled.Canonical != again.Canonical || !reflect.DeepEqual(compiled.Query, again.Query) {
				t.Fatalf("not a fixed point: %q -> %q", compiled.Canonical, again.Canonical)
			}
		})
	}
	parsed, problems := parse(`logs mystery(body)`, "")
	if len(problems) != 0 || parsed == nil {
		t.Fatal(parsed, problems)
	}
	if _, ok := parsed.Where[0].(*filterCall); !ok {
		t.Fatalf("an adjacent identifier must be parsed as a call: %T", parsed.Where[0])
	}
	wantProblem(t, `logs starts_with (body, "x")`, "", "write starts_with(...) without a space", "")
}
