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

// A metric aggregate over several metrics fails when run, so parse says so
// with the run's wording. Counting points, grouping by name, or one name
// literal is fine.
func TestCompileMetricAggregateNeedsOneMetric(t *testing.T) {
	const want = "metric aggregates need one metric per group"
	for _, text := range []string{
		"metrics where name in (a, b, c) | avg(value) by resource.host.name",
		"metrics | sum(value)",
		"metrics where service:cart | p95(value) every 1m",
	} {
		_, ps := Compile(text, Options{})
		if len(ps) != 1 || ps[0].Message != want || ps[0].Span == nil {
			t.Errorf("%q: %+v", text, ps)
		}
	}
	vars := []query.Variable{{Name: "one", Kind: "custom", Options: []string{"a"}}, {Name: "many", Kind: "custom", Options: []string{"a", "b"}, Multi: true}}
	for _, text := range []string{
		"metrics where name in (a, b, c) | avg(value) by name",
		"metrics where name in (a, b) | count()",
		"metrics where name:a | avg(value) by service",
		"metrics where name=a service:cart | last(value)",
		"metrics where name:$one | avg(value)",
	} {
		if c, ps := Compile(text, Options{Variables: vars}); c == nil {
			t.Errorf("%q: %+v", text, ps)
		}
	}
	for _, text := range []string{"metrics where name in $many | avg(value)", "metrics where name in $one | avg(value)", "metrics where name in (a) | avg(value)"} {
		if _, ps := Compile(text, Options{Variables: vars}); len(ps) != 1 || ps[0].Message != want {
			t.Errorf("%q: %+v", text, ps)
		}
	}
}

// A misspelled name field is why no metric is named: the problem says so
// first and offers name as a fix, rather than blaming the aggregate.
func TestCompileMetricAggregateNamesMisspelledName(t *testing.T) {
	text := "metrics where nme:container.cpu.utilization | avg(value)"
	_, ps := Compile(text, Options{})
	if len(ps) != 1 || ps[0].Message != `"nme" is not a metrics column; did you mean name?` || ps[0].Span == nil || text[ps[0].Span.Start:ps[0].Span.End] != "nme" {
		t.Fatalf("%+v", ps)
	}
	if len(ps[0].FixIts) != 1 || ps[0].FixIts[0].Text != "name" {
		t.Fatalf("fixes %+v", ps[0].FixIts)
	}
}

// A misspelled measure function offers the nearest function as a fix-it on
// its name; a percentile FanQL lacks offers swapped digits and quantile.
func TestCompileUnknownMeasureOffersFix(t *testing.T) {
	for _, tt := range []struct{ text, message string }{
		{"spans | cuont()", "unknown function cuont; did you mean count?"},
		{"spans | avgg(duration)", "unknown function avgg; did you mean avg?"},
		{"spans | cnt()", "unknown function cnt; did you mean count?"},
		{"spans | count_distnct(trace_id)", "unknown function count_distnct; did you mean count_distinct?"},
		{"spans | error_rat()", "unknown function error_rat; did you mean error_rate?"},
		{"metrics where name:a | lst(value)", "unknown function lst; did you mean last?"},
	} {
		_, ps := Compile(tt.text, Options{})
		if len(ps) != 1 || ps[0].Message != tt.message || len(ps[0].FixIts) != 1 {
			t.Errorf("%q: %+v", tt.text, ps)
			continue
		}
		fix := ps[0].FixIts[0]
		fixed := string([]rune(tt.text)[:fix.Span.Start]) + fix.Text + string([]rune(tt.text)[fix.Span.End:])
		if c, ps := Compile(fixed, Options{}); c == nil {
			t.Errorf("fix %q: %+v", fixed, ps)
		}
	}
	_, ps := Compile("spans | p59(duration)", Options{})
	if len(ps) != 1 || ps[0].Message != "unknown function p59; percentiles are p50, p75, p90, p95, p99, or write quantile(duration, 0.59)" ||
		len(ps[0].FixIts) != 2 || ps[0].FixIts[0].Text != "p95" || ps[0].FixIts[1].Text != "quantile(duration, 0.59)" {
		t.Errorf("p59: %+v", ps)
	}
	// error_rate is not offered for metrics, and nothing near zzz exists.
	_, ps = Compile("metrics where name:a | zzzzzz()", Options{})
	if len(ps) != 1 || !strings.HasPrefix(ps[0].Message, "unknown function zzzzzz; use avg, count,") || strings.Contains(ps[0].Message, "error_rate") || len(ps[0].FixIts) != 0 {
		t.Errorf("no near function: %+v", ps)
	}
}

// PromQL and Kusto get one message for the whole query, not a cascade.
func TestCompileForeignQueryLanguages(t *testing.T) {
	const promql = "PromQL is not FanQL; start with the source, e.g. metrics where name:http.server.request.duration | rate() by service"
	for _, tt := range []struct{ text, message string }{
		{"sum(rate(http_server_requests_total[5m])) by (service)", promql},
		{"http_server_duration_seconds_bucket", promql},
		{`rate(traces_span_metrics_calls_total{service="cart"}[1m])`, promql},
		{`up{job="api"}`, promql},
		{"spans | where service == 'cart' | summarize count() by service", "Kusto is not FanQL; filters follow the source and measures follow one |, e.g. spans where service:api | count() by service"},
		{"logs | where service:cart | project body", "Kusto is not FanQL; filters follow the source and measures follow one |, e.g. logs where service:api | count() by service"},
	} {
		_, ps := Compile(tt.text, Options{})
		if len(ps) != 1 || ps[0].Message != tt.message {
			t.Errorf("%q: %+v", tt.text, ps)
		}
	}
	// Only the pipe is wrong: the fix-it removes it and the rest still parses.
	_, ps := Compile("spans | where service:cart | count() by service", Options{})
	if len(ps) != 1 || ps[0].Message != "| where is not FanQL; filters follow the source, e.g. spans where service:api | count()" ||
		len(ps[0].FixIts) != 1 || ps[0].FixIts[0].Text != "where" || *ps[0].Span != (query.TextSpan{Start: 6, End: 13}) {
		t.Errorf("pipe where: %+v", ps)
	}
	if c, ps := Compile("spans where service:cart | count() by service", Options{}); c == nil {
		t.Errorf("fixed: %+v", ps)
	}
	// where after as is an alias, not a misplaced where.
	if c, ps := Compile("spans where service:a | count() as where", Options{}); c == nil {
		t.Errorf("alias where: %+v", ps)
	}
	_, ps = Compile("spans where kind:server | where service:cart | count()", Options{})
	if len(ps) != 1 || ps[0].FixIts[0].Text != "and" {
		t.Errorf("second where: %+v", ps)
	}
	// An unknown source that does not read as PromQL keeps its own message.
	if _, ps := Compile("spams | count()", Options{}); len(ps) != 1 || ps[0].Message != `unknown source "spams"` {
		t.Errorf("spams: %+v", ps)
	}
}

// Spans count code points, as the editor converts them: text before the
// fix-it with multi-byte characters does not shift it.
func TestEditSpansCountCodePoints(t *testing.T) {
	text := `spans where service:"café☕" | cuont()`
	_, ps := Compile(text, Options{})
	if len(ps) != 1 || len(ps[0].FixIts) != 1 {
		t.Fatalf("%+v", ps)
	}
	fix, r := ps[0].FixIts[0], []rune(text)
	if got := string(r[fix.Span.Start:fix.Span.End]); got != "cuont" {
		t.Fatalf("span %+v covers %q", fix.Span, got)
	}
	if got := string(r[ps[0].Span.Start:ps[0].Span.End]); got != "cuont()" {
		t.Fatalf("problem span %+v covers %q", ps[0].Span, got)
	}
}
