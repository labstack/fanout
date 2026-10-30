package fanql

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func mustParse(t *testing.T, text string) *queryTree {
	t.Helper()
	q, ps := parse(text, "")
	if len(ps) != 0 || q == nil {
		t.Fatalf("Parse(%q): %#v", text, ps)
	}
	return q
}

func wantProblem(t *testing.T, text, source, message, hint string) panel.Problems {
	t.Helper()
	q, ps := parse(text, source)
	if q != nil || len(ps) == 0 {
		t.Fatalf("Parse(%q) = %#v, %#v; want problem %q", text, q, ps, message)
	}
	for _, p := range ps {
		if p.Message == message {
			if p.Path != "text" || p.Span == nil || (hint != "" && p.Hint != hint) {
				t.Fatalf("Parse(%q): %#v", text, p)
			}
			return ps
		}
	}
	t.Fatalf("Parse(%q): %#v; want %q", text, ps, message)
	return nil
}

func TestParseEntriesAndGrouping(t *testing.T) {
	q := mustParse(t, "spans where a:1 b:2 (c:3 or d:4) (e:5 f:6)")
	if len(q.Where) != 4 {
		t.Fatal(q)
	}
	if _, ok := q.Where[0].(*comparison); !ok {
		t.Fatal(q.Where)
	}
	if _, ok := q.Where[1].(*comparison); !ok {
		t.Fatal(q.Where)
	}
	if x, ok := q.Where[2].(*orExpr); !ok || len(x.Terms) != 2 {
		t.Fatal(q.Where)
	}
	if x, ok := q.Where[3].(*andExpr); !ok || len(x.Terms) != 2 {
		t.Fatal(q.Where)
	}
	q = mustParse(t, "spans where a:1 or b:2")
	if len(q.Where) != 1 {
		t.Fatal(q)
	}
	if _, ok := q.Where[0].(*orExpr); !ok {
		t.Fatal(q)
	}
	q = mustParse(t, "spans where (a:1)")
	if len(q.Where) != 1 {
		t.Fatal(q)
	}
	if _, ok := q.Where[0].(*comparison); !ok {
		t.Fatal(q)
	}
	q = mustParse(t, "spans status:error | count()")
	if q.WhereWord || len(q.Where) != 1 {
		t.Fatal(q)
	}
}

func TestParseMixedAndOrIsRejectedWithFixIts(t *testing.T) {
	for _, tc := range []struct{ text, body, a, b string }{
		{"spans where a:1 b:2 or c:3", "a:1 b:2 or c:3", "a:1 (b:2 or c:3)", "(a:1 b:2) or c:3"},
		{"spans where a:1 and b:2 or c:3", "a:1 and b:2 or c:3", "a:1 and (b:2 or c:3)", "(a:1 and b:2) or c:3"},
		{"spans where a:1 or b:2 c:3", "a:1 or b:2 c:3", "a:1 or (b:2 c:3)", "(a:1 or b:2) c:3"},
		{"spans where (a:1 b:2 or c:3)", "a:1 b:2 or c:3", "a:1 (b:2 or c:3)", "(a:1 b:2) or c:3"},
		{"spans | count_if(a:1 b:2 or c:3) as x", "a:1 b:2 or c:3", "a:1 (b:2 or c:3)", "(a:1 b:2) or c:3"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			ps := wantProblem(t, tc.text, "", "mixed and/or needs parentheses", "write "+tc.a+" or "+tc.b)
			p := ps[0]
			start := strings.Index(tc.text, tc.body)
			span := panel.TextSpan{Start: start, End: start + len(tc.body)}
			if *p.Span != span || len(p.FixIts) != 2 || p.FixIts[0].Span != span || p.FixIts[1].Span != span || p.FixIts[0].Text != tc.a || p.FixIts[1].Text != tc.b {
				t.Fatal(p)
			}
		})
	}
}

func TestParseNegationNesting(t *testing.T) {
	for _, input := range []string{"logs where --severity:error", "logs where not not a:1", "logs where -not a:1"} {
		q := mustParse(t, input)
		outer, ok := q.Where[0].(*notExpr)
		if !ok {
			t.Fatal(q)
		}
		inner, ok := outer.X.(*notExpr)
		if !ok {
			t.Fatal(outer)
		}
		if _, ok := inner.X.(*comparison); !ok {
			t.Fatal(inner)
		}
	}
	q := mustParse(t, "spans where -(a:1 or b:2)")
	n, ok := q.Where[0].(*notExpr)
	if !ok || len(q.Where) != 1 {
		t.Fatal(q)
	}
	if _, ok := n.X.(*orExpr); !ok {
		t.Fatal(n)
	}
	q = mustParse(t, "spans where not a:1 b:2")
	if len(q.Where) != 2 {
		t.Fatal(q)
	}
	if _, ok := q.Where[0].(*notExpr); !ok {
		t.Fatal(q)
	}
}

func TestParseValueClassification(t *testing.T) {
	for _, tc := range []struct {
		input, text string
		kind        valueKind
	}{{"k:500", "500", numberValue}, {"k:1.5s", "1.5s", durationValue}, {"k:true", "true", booleanValue}, {"k:$v", "$v", variableValue}, {`k:"500"`, "500", stringValue}, {"value>-3", "-3", numberValue}, {"k:api", "api", wordValue}, {`start_time>"2026-10-29T10:00:00Z"`, "2026-10-29T10:00:00Z", stringValue}} {
		x := mustParse(t, "spans where "+tc.input).Where[0].(*comparison)
		if x.Value.Kind != tc.kind || x.Value.Text != tc.text {
			t.Fatal(x)
		}
	}
	if _, ok := mustParse(t, "spans where k:*").Where[0].(*existsExpr); !ok {
		t.Fatal("expected exists")
	}
	wantProblem(t, "spans where k:-foo", "", "k: needs a value; e.g. k:api", "")
}

func TestParseColonEndsWord(t *testing.T) {
	ps := wantProblem(t, "spans where http.target:/a:b", "", "unexpected : after /a", `quote the value: http.target:"/a:b"`)
	if *ps[0].Span != (panel.TextSpan{Start: 12, End: 28}) {
		t.Fatal(ps)
	}
	wantProblem(t, "logs where http://x:8080/y", "", "unexpected : after http", `quote the value: "http://x:8080/y"`)
}

func TestParseFieldVsFreeText(t *testing.T) {
	for _, s := range []string{"service", "in", `"limit"`, "stats"} {
		if _, ok := mustParse(t, "logs where "+s).Where[0].(*textExpr); !ok {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"where", "join"} {
		x := mustParse(t, "logs where service:"+s).Where[0].(*comparison)
		if x.Value.Kind != wordValue || x.Value.Text != s {
			t.Fatal(x)
		}
	}
	x := mustParse(t, "logs where service in (sort, limit)").Where[0].(*inExpr)
	if len(x.Values) != 2 || x.Values[0].Text != "sort" || x.Values[1].Text != "limit" {
		t.Fatal(x)
	}
	wantProblem(t, "logs where limit 10", "", "where needs a filter", `quote "limit" to search for the word, or drop where`)
}

func TestParseOperatorsAfterField(t *testing.T) {
	for _, op := range []string{"in", "not in", "between", "not between", "is null", "is not null", "like", "ilike", "not like", "not ilike"} {
		input := "k " + op
		switch {
		case strings.HasSuffix(op, "in"):
			input += " (a, b)"
		case strings.HasSuffix(op, "between"):
			input += " 1 and 2"
		case strings.Contains(op, "like"):
			input += ` "a%"`
		}
		x := mustParse(t, "spans where "+input).Where[0]
		neg := strings.Contains(op, "not")
		switch x := x.(type) {
		case *inExpr:
			if x.Negated != neg || len(x.Values) != 2 {
				t.Fatal(x)
			}
		case *betweenExpr:
			if x.Negated != neg || x.Lo.Text != "1" || x.Hi.Text != "2" {
				t.Fatal(x)
			}
		case *nullExpr:
			if x.Negated != neg {
				t.Fatal(x)
			}
		case *likeExpr:
			if x.Negated != neg || x.Insensitive != strings.Contains(op, "ilike") || x.Pattern.Text != "a%" {
				t.Fatal(x)
			}
		default:
			t.Fatalf("%s: %T", op, x)
		}
	}
	if mustParse(t, "spans where k:>5").Where[0].(*comparison).Op != ">" {
		t.Fatal("colon comparison")
	}
	wantProblem(t, "spans where k like a%", "", "like needs a quoted pattern", "")
	wantProblem(t, "spans where duration between 1s", "", "between needs two values", "")
	x := mustParse(t, "spans where service in $svcs").Where[0].(*inExpr)
	if x.Variable != "svcs" {
		t.Fatal(x)
	}
}

func TestParseCalls(t *testing.T) {
	x := mustParse(t, `spans where starts_with(http_route, "/api")`).Where[0].(*filterCall)
	if x.Name != "starts_with" || len(x.Args) != 2 || x.Args[0].Field == nil || x.Args[0].Field.Key != "http_route" || x.Args[1].Value == nil || x.Args[1].Value.Text != "/api" {
		t.Fatal(x)
	}
	x = mustParse(t, "logs where length(body) > 1000").Where[0].(*filterCall)
	if x.Cmp == nil || x.Cmp.Op != ">" || x.Cmp.Value.Text != "1000" {
		t.Fatal(x)
	}
	wantProblem(t, `logs where starts_with (x, "y")`, "", "write starts_with(...) without a space", "")
	x = mustParse(t, "spans where P95(Duration)").Where[0].(*filterCall)
	if x.Name != "p95" {
		t.Fatal(x)
	}
}

func TestParseStageMeasuresAndArithmetic(t *testing.T) {
	q := mustParse(t, "spans | count_if(status:error) / count() * 100 as err_pct by http_route every 1m sort err_pct limit 10")
	m := q.Stage.Measures[0]
	mul, ok := m.Parsed.Expr.(panel.MeasureBinary)
	if !ok || mul.Op != '*' || m.Alias != "err_pct" {
		t.Fatal(m)
	}
	div, ok := mul.L.(panel.MeasureBinary)
	if !ok || div.Op != '/' {
		t.Fatal(mul)
	}
	if _, ok := div.L.(panel.MeasureCountIf); !ok {
		t.Fatal(div)
	}
	if div.R.(panel.MeasureCall).Func != "count" || mul.R.(panel.MeasureConst).Value != 100 {
		t.Fatal(mul)
	}
	if q.Stage.By[0].Key != "http_route" || q.Stage.Bucket != "1m" || q.Sort.Key != "err_pct" || q.Sort.Ascending || q.Limit.N != 10 {
		t.Fatal(q)
	}
	x := mustParse(t, "spans | p95(duration)-p50(duration) as spread").Stage.Measures[0].Parsed.Expr.(panel.MeasureBinary)
	if x.Op != '-' || x.L.(panel.MeasureCall).Func != "p95" || x.R.(panel.MeasureCall).Func != "p50" {
		t.Fatal(x)
	}
	x = mustParse(t, "spans | count() * -1 as neg").Stage.Measures[0].Parsed.Expr.(panel.MeasureBinary)
	if x.R.(panel.MeasureNeg).X.(panel.MeasureConst).Value != 1 {
		t.Fatal(x)
	}
	for _, s := range []string{"count()*100 as c100", "count()/count() as one", "count() * 1e3 as k", "count() * 1.5 as x", "(count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex"} {
		mustParse(t, "spans | "+s)
	}
	c := mustParse(t, "spans | count() * 1e3 as k").Stage.Measures[0].Parsed.Expr.(panel.MeasureBinary).R.(panel.MeasureConst)
	if c.Value != 1000 || c.Text != "1e3" {
		t.Fatal(c)
	}
	wantProblem(t, "spans | count() +", "", "+ needs a measure or number on both sides", "")
}

func TestParseExpressionRules(t *testing.T) {
	wantProblem(t, "spans | count() / 2", "", "name this expression with as, e.g. count() / 2 as half", "")
	wantProblem(t, "spans | share() / 2 as half", "", "share() cannot be combined; it is already a percentage of the scope", "use share() alone, or count_if(...) / count() * 100 for a custom ratio")
	for _, s := range []string{"1 as one", "1 / 2 as half", "-(5) as neg"} {
		ps := wantProblem(t, "spans | "+s, "", "a measure needs an aggregate; constants can only scale one, e.g. count() * 100", "")
		if ps[0].Span.Start != 8 {
			t.Fatal(ps)
		}
	}
	mustParse(t, "spans | count() * 0 as zero")
	wantProblem(t, "spans | histogram(duration), count()", "", "distribution counts require count(); histogram(...) stands alone", "")
	q := mustParse(t, "spans | histogram(duration) by service")
	if q.Stage.Histogram == nil || q.Stage.Histogram.Key != "duration" || len(q.Stage.By) != 1 {
		t.Fatal(q)
	}
	x := mustParse(t, "spans | count(*)").Stage.Measures[0].Parsed.Expr.(panel.MeasureCall)
	if x.Func != "count" {
		t.Fatal(x)
	}
}

func TestParseSortLimitAndSecondPipe(t *testing.T) {
	for _, tc := range []struct {
		sort string
		asc  bool
	}{{"-count", false}, {"+p95", true}, {"p95 asc", true}, {"p95 desc", false}} {
		q := mustParse(t, "spans sort "+tc.sort)
		if q.Sort.Ascending != tc.asc {
			t.Fatal(q.Sort)
		}
	}
	wantProblem(t, "spans sort -p95 asc", "", "sort takes one direction", "")
	q := mustParse(t, "spans | count() by service | sort count | limit 10")
	if q.Sort.Key != "count" || q.Limit.N != 10 {
		t.Fatal(q)
	}
	for _, tc := range []struct{ text, message, hint string }{
		{"spans | count() by service | top 3 by count", "FanQL 1 runs one aggregation stage", "remove the second |; more stages come in a later version"},
		{"spans limit 10 sort duration", "sort comes before limit: … sort duration limit 10", "write spans sort duration limit 10"},
		{"spans | count() limit 0", "limit needs a whole number from 1 to 1000; e.g. limit 50", ""},
		{"spans where a:1 where b:2", "where appears twice; combine the filters into one where", ""},
		{"spans | every 1m", "every needs measures first; write | count() every 1m", ""},
		{"spans | count() every 2m", `unknown bucket "2m"; use auto or one of 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h, 1d`, ""},
		{"spans | count() by a, b, c, d", "at most 3 grouping fields", ""},
	} {
		wantProblem(t, tc.text, "", tc.message, tc.hint)
	}
}

func TestParseSourceErrors(t *testing.T) {
	wantProblem(t, "traces where a:1", "", `unknown source "traces"`, "use spans; traces is a viz of spans")
	wantProblem(t, "from spans", "", `"from" is not FanQL; start with the source: spans where ...`, "")
	wantProblem(t, "", "", `unknown source ""`, "use spans, logs or metrics")
	q := mustParse(t, "logs")
	if q.Source != "logs" || len(q.Where) != 0 {
		t.Fatal(q)
	}
}

func TestParseSearchBarFilters(t *testing.T) {
	q, ps := parse("service:api timeout", "logs")
	if len(ps) != 0 || q == nil || q.Source != "logs" || len(q.Where) != 2 {
		t.Fatalf("%#v %#v", q, ps)
	}
	wantProblem(t, "where service:api", "logs", "the page supplies where", "")
	if q, ps := parse("| count()", "logs"); q != nil || len(ps) == 0 {
		t.Fatal(q, ps)
	}
}

func TestParseReservedAndForeignWords(t *testing.T) {
	for _, w := range strings.Fields("let join correlate compare top eval extend having fields rename over window") {
		wantProblem(t, "spans where "+w, "", fmt.Sprintf("%q is reserved for a future FanQL feature", w), "quote it to search for the word")
	}
	for _, tc := range []struct{ text, message, hint string }{
		{"spans | summarize count() by service", `"summarize" is not FanQL; write the measures after |, e.g. | count() by service`, ""},
		{`spans where service == "api"`, "== is not FanQL; use key:value or =", ""},
		{`spans where body =~ "re"`, "=~ is not FanQL; ~ is contains; use regexp_matches(field, 're') for a regular expression", ""},
		{`spans where {service="api"}`, "{ selectors are not FanQL; write labels as key:value, e.g. service:api", ""},
		{"spans where @http.target:x", `"@http.target" is not a field name; write the attribute without @: http.target`, ""},
		{"spans where !service:api", "! negation is not FanQL; write -service:api", ""},
		{"spans since 1h", "the time range is not part of FanQL 1; use the time picker (since is reserved)", ""},
		{"spans until 1h", "the time range is not part of FanQL 1; use the time picker (until is reserved)", ""},
		{"spans where service:api*", "* wildcards are not in FanQL 1", `use ~ for contains, or quote "api*"`},
	} {
		wantProblem(t, tc.text, "", tc.message, tc.hint)
	}
	if _, ok := mustParse(t, "spans where stats").Where[0].(*textExpr); !ok {
		t.Fatal("foreign free text")
	}
}

func TestParseUnclosedAndUnmatched(t *testing.T) {
	for _, tc := range []struct{ text, message string }{
		{"spans where (a:1", "unclosed (; add ) before |, sort, limit or the end"},
		{"spans where a:1)", "unmatched ); remove it or add the opening ("},
		{"spans where \"abc", `unclosed quote; close the value with "`},
		{"spans where `coalesce(a", "unclosed raw filter; close it with `"},
		{"spans where a:1 or", "or needs a filter on both sides"},
		{"spans where service:", "service: needs a value; e.g. service:api"},
		{"spans where >500ms", "a filter needs a field before >"},
		{"spans where duration>", "duration> needs a value; e.g. duration>500ms"},
	} {
		ps := wantProblem(t, tc.text, "", tc.message, "")
		if strings.Contains(tc.text, "(a:1") && *ps[0].Span != (panel.TextSpan{Start: 12, End: 13}) {
			t.Fatal(ps)
		}
	}
}

func TestParseCollectsAllProblemsInOrderCappedAt20(t *testing.T) {
	q, ps := parse("spans where "+strings.Repeat("!k:v ", 25), "")
	if q != nil || len(ps) != 21 || ps[20].Message != "more problems follow; fix these first" {
		t.Fatal(q, ps)
	}
	for i, p := range ps {
		if p.Span == nil || p.Path != "text" || (i > 0 && p.Span.Start < ps[i-1].Span.Start) {
			t.Fatal(ps)
		}
	}
	q, ps = parse("traces | stats x take 5", "")
	if q != nil || len(ps) != 3 {
		t.Fatal(q, ps)
	}
}

func TestParseBoundsAreEnforcedIndependently(t *testing.T) {
	values := make([]string, 101)
	for i := range values {
		values[i] = fmt.Sprintf("v%d", i+1)
	}
	for _, tc := range []struct{ text, fragment string }{
		{strings.Repeat("a", 4001), "a query is at most 4000 characters (8192 bytes)"},
		{strings.Repeat("界", 3000), "a query is at most 4000 characters (8192 bytes)"},
		{"spans " + strings.Repeat("a ", 1024), "tokens"},
		{"spans " + strings.Repeat("a ", 513), "nodes"},
		{"spans " + strings.Repeat("(", 17) + "a:1" + strings.Repeat(")", 17), "depth"},
		{"spans service in (" + strings.Join(values, ",") + ")", "list"},
	} {
		q, ps := parse(tc.text, "")
		if q != nil || len(ps) != 1 || !strings.Contains(ps[0].Message, tc.fragment) || ps[0].Span == nil {
			t.Fatalf("%s: %#v %#v", tc.fragment, q, ps)
		}
		if tc.fragment == "list" && string([]rune(tc.text)[ps[0].Span.Start:ps[0].Span.End]) != "v101" {
			t.Fatal(ps)
		}
	}
}

func TestParseKeywordCaseFolding(t *testing.T) {
	q := mustParse(t, "SPANS WHERE Service:API | COUNT() BY service EVERY 1M SORT COUNT LIMIT 5")
	x := q.Where[0].(*comparison)
	if q.Source != "spans" || x.Field.Key != "Service" || x.Value.Text != "API" || q.Stage.Measures[0].Parsed.Expr.(panel.MeasureCall).Func != "count" || q.Stage.Bucket != "1m" || q.Sort.Key != "count" {
		t.Fatal(q, x)
	}
}

func TestParseEntryParserIsUnbounded(t *testing.T) {
	x, ps := parseEntry(`body:"` + strings.Repeat("x", 9000) + `"`)
	if len(ps) != 0 || x == nil {
		t.Fatal(x, ps)
	}
}

func TestParseFieldsAndLiteralSpans(t *testing.T) {
	q := mustParse(t, `logs where attributes['𝔘.key']:"✓" resource.k8s.pod.name:x a.b:y`)
	fields := []fieldName{q.Where[0].(*comparison).Field, q.Where[1].(*comparison).Field, q.Where[2].(*comparison).Field}
	if fields[0].Scope != "attributes" || fields[0].Key != "𝔘.key" || !fields[0].Bracket || fields[0].Single || fields[1].Scope != "resource" || fields[1].Key != "k8s.pod.name" || fields[2].Scope != "" || fields[2].Key != "a.b" {
		t.Fatal(fields)
	}
	if !reflect.DeepEqual(q.SourceSpan, span{0, 4}) {
		t.Fatal(q)
	}
}

func TestParseRejectsMissingMeasureCommas(t *testing.T) {
	wantProblem(t, "spans | count() rate()", "", "separate measures with a comma", "")
}

func TestParseGroupingFixItsRepairLongerChains(t *testing.T) {
	for _, s := range []string{"a:1 b:2 or c:3 d:4", "a:1 or b:2 c:3 or d:4", "(a:1 b:2) c:3 or d:4"} {
		text := "spans where " + s
		ps := wantProblem(t, text, "", "mixed and/or needs parentheses", "")
		for _, fix := range ps[0].FixIts {
			r := []rune(text)
			repaired := string(r[:fix.Span.Start]) + fix.Text + string(r[fix.Span.End:])
			mustParse(t, repaired)
		}
	}
}

func TestParseApdexTree(t *testing.T) {
	q := mustParse(t, "spans | (count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex")
	var walk func(panel.MeasureExpr) (int, int)
	walk = func(x panel.MeasureExpr) (int, int) {
		switch x := x.(type) {
		case panel.MeasureBinary:
			ld, ln := walk(x.L)
			rd, rn := walk(x.R)
			return max(ld, rd) + 1, ln + rn
		case panel.MeasureNeg:
			d, n := walk(x.X)
			return d + 1, n
		case panel.MeasureCall, panel.MeasureCountIf:
			return 1, 1
		default:
			return 1, 0
		}
	}
	depth, leaves := walk(q.Stage.Measures[0].Parsed.Expr)
	if depth != 4 || leaves != 3 {
		t.Fatalf("depth %d, aggregates %d", depth, leaves)
	}
}

func TestParseFilterSignsAndFieldShapes(t *testing.T) {
	for _, tc := range []struct{ text, message string }{
		{"spans where k:+3", "unexpected +"},
		{"spans where 500>3", "a filter needs a field before >"},
		{"spans where *foo", "* wildcards are not in FanQL 1"},
		{"spans | avg(*)", "* is only allowed as the sole argument of count(*)"},
	} {
		wantProblem(t, tc.text, "", tc.message, "")
	}
	q := mustParse(t, "spans where k not a:1")
	if len(q.Where) != 2 {
		t.Fatal(q)
	}
	if _, ok := q.Where[1].(*notExpr); !ok {
		t.Fatal(q.Where)
	}
}

func TestParseForeignSyntaxInEachPosition(t *testing.T) {
	for _, s := range []string{"spans | sort by service", "spans where body::text", "spans |= err", "spans |~ err", "spans group by service", "spans facet service", "spans where ago(1h)", "spans where now()", "spans where earliest=1h", "spans where _time:x"} {
		if q, ps := parse(s, ""); q != nil || len(ps) == 0 {
			t.Fatalf("accepted %s: %#v", s, q)
		}
	}
	mustParse(t, "metrics | avg(value)")
}

func TestParseCombinedNestingBounds(t *testing.T) {
	for _, text := range []string{
		"spans where " + strings.Repeat("not ", 17) + "a:1",
		"spans where " + strings.Repeat("lower(", 17) + "body" + strings.Repeat(")", 17) + " = 'x'",
		"spans where " + strings.Repeat("(", 9) + strings.Repeat("not ", 8) + "a:1" + strings.Repeat(")", 9),
		"spans | count()" + strings.Repeat(" + 1", 17) + " as x",
	} {
		_, ps := parse(text, "")
		if len(ps) != 1 || !strings.Contains(ps[0].Message, "depth") {
			t.Fatalf("%q: %#v", text, ps)
		}
	}
	mustParse(t, "spans where "+strings.Repeat("not ", 16)+"a:1")
}

func TestParseDistributionStandsAlone(t *testing.T) {
	for _, text := range []string{"spans | histogram(duration) + count() as x", "spans | count() + histogram(duration) as x", "spans | histogram(duration), histogram(value)"} {
		wantProblem(t, text, "", "distribution counts require count(); histogram(...) stands alone", "")
	}
	wantProblem(t, "spans | histogram(duration) as x", "", "histogram(...) does not take an alias", "")
}

func TestParseListsRejectWildcards(t *testing.T) {
	wantProblem(t, "spans where service in (*)", "", "* wildcards are not in FanQL 1", "")
}
