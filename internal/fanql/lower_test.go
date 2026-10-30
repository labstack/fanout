package fanql

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

var lowerFixtures = []struct{ text, sql string }{
	{`spans where duration>500ms`, `duration_ms > 500`},
	{`spans where value>-3`, `attributes['value'] > -3`},
	{`metrics where value>-3`, `value > -3`},
	{`spans where -duration>1s`, `(duration_ms IS NULL OR duration_ms <= 1000)`},
	{`logs where --severity:error`, `severity = 'ERROR'`},
	{`spans where -(status:error kind:server)`, `((status IS NULL OR status != 'STATUS_CODE_ERROR') OR (kind IS NULL OR kind != 'SPAN_KIND_SERVER'))`},
	{`spans where http.target:"/a:b"`, `attributes['http.target'] = '/a:b'`},
	{`logs where "http://x:8080/y"`, `contains(lower(body), lower('http://x:8080/y'))`},
	{`metrics where name:http.server.duration`, `name = 'http.server.duration'`},
	{`spans where resource.k8s.pod.name:x`, `resource['k8s.pod.name'] = 'x'`},
	{`spans where attributes['resource.k']:x`, `attributes['resource.k'] = 'x'`},
	{`spans where flag:true`, `attributes['flag'] = true`},
	{`spans where flag:"true"`, `attributes['flag'] = 'true'`},
	{`logs where service`, `contains(lower(body), lower('service'))`},
	{`logs where service:where`, `service = 'where'`},
	{`logs where body ~ "a_b"`, `contains(lower(body), lower('a_b'))`},
	{`logs where body ilike "%a_b%"`, `body ~~* '%a_b%'`},
	{`logs where body not like "GET %"`, `body !~~ 'GET %'`},
	{`logs where -body like "GET %"`, `(body IS NULL OR body !~~ 'GET %')`},
	{`logs where -body ~ "100%"`, `(body IS NULL OR NOT contains(lower(body), lower('100%')))`},
	{`spans where service in (a, b)`, `service IN ('a', 'b')`},
	{`spans where service not in (a, b)`, `service NOT IN ('a', 'b')`},
	{`spans where -service in (a, b)`, `(service IS NULL OR service NOT IN ('a', 'b'))`},
	{`spans where service in $svcs`, `service IN $svcs`},
	{`spans where a:1 or c:3`, `(attributes['a'] = 1 OR attributes['c'] = 3)`},
	{`spans where (a:1 b:2) or c:3`, `((attributes['a'] = 1 AND attributes['b'] = 2) OR attributes['c'] = 3)`},
	{`spans where (a:1 b:2)`, `(attributes['a'] = 1 AND attributes['b'] = 2)`},
	{`spans where -http_status_code>=500`, `NOT coalesce((TRY_CAST(http_status_code AS INTEGER) >= 500), FALSE)`},
	{`spans where -starts_with(http_route, "/api")`, `NOT coalesce((starts_with(http_route, '/api')), FALSE)`},
	{"logs where -`coalesce(body, '') = ''`", `NOT coalesce((coalesce(body, '') = ''), FALSE)`},
	{"logs where `coalesce(body, '``') = ''`", "coalesce(body, '`') = ''"},
	{`spans where HTTP.Target:x`, `attributes['HTTP.Target'] = 'x'`},
	{`logs where length(body) > 1000`, `length(body) > 1000`},
	{`spans where k:*`, `attributes['k'] IS NOT NULL`},
	{`spans where -user.id:*`, `attributes['user.id'] IS NULL`},
	{`spans where http_status_code>=500`, `TRY_CAST(http_status_code AS INTEGER) >= 500`},
	{`spans where http_status_code:500`, `http_status_code = '500'`},
	{`spans where start_time>"2026-10-29T10:00:00Z"`, `start_time > '2026-10-29T10:00:00Z'::TIMESTAMPTZ_NS`},
	{`logs where severity>=warn`, `severity IN ('WARN', 'ERROR', 'FATAL')`},
	{`logs where severity<error`, `severity IN ('TRACE', 'DEBUG', 'INFO', 'WARN')`},
	{`logs where severity between info and error`, `severity IN ('INFO', 'WARN', 'ERROR')`},
	{`logs where severity:warning`, `severity = 'WARN'`},
	{`logs where severity:"error"`, `severity = 'error'`},
	{`logs where severity_number>=14`, `severity_number >= 14`},
	{`spans where -http_status_code between 400 and 599`, `NOT coalesce((TRY_CAST(http_status_code AS INTEGER) BETWEEN 400 AND 599), FALSE)`},
	{`spans where http_status_code not between 400 and 599`, `TRY_CAST(http_status_code AS INTEGER) NOT BETWEEN 400 AND 599`},
	{`spans where -http_status_code not between 400 and 599`, `NOT coalesce((TRY_CAST(http_status_code AS INTEGER) NOT BETWEEN 400 AND 599), FALSE)`},
	{`spans where -http_status_code:500`, `(http_status_code IS NULL OR http_status_code != '500')`},
	{`spans where -service not in (a, b)`, `(service IS NULL OR service IN ('a', 'b'))`},
	{`spans where -service!=api`, `(service IS NULL OR service = 'api')`},
	{`logs where -body not like "GET %"`, `(body IS NULL OR body ~~ 'GET %')`},
	{`spans where -duration not between 1s and 2s`, `(duration_ms IS NULL OR duration_ms BETWEEN 1000 AND 2000)`},
	{`logs where -length(body) > 1000`, `NOT coalesce((length(body) > 1000), FALSE)`},
	{`spans where -k ~ v`, `(attributes['k'] IS NULL OR NOT contains(lower(CAST(attributes['k'] AS VARCHAR)), lower('v')))`},
	{`spans where k ~ v`, `contains(lower(CAST(attributes['k'] AS VARCHAR)), lower('v'))`},
	{`spans where -k:$v`, `(attributes['k'] IS NULL OR attributes['k'] != $v)`},
}

func TestLowerPreservesExactIntegerLiterals(t *testing.T) {
	for _, value := range []string{
		"9007199254740991", "9007199254740992", "9007199254740993",
		"-9007199254740991", "-9007199254740992", "-9007199254740993",
		"9223372036854775807", "-9223372036854775808",
	} {
		t.Run(value, func(t *testing.T) {
			text := "logs where attributes['id']:" + value
			c, ps := Compile(text, Options{})
			if len(ps) != 0 {
				t.Fatal(ps)
			}
			want := panel.Query{From: "logs", Where: []string{"attributes['id'] = " + value}}
			if !reflect.DeepEqual(c.Query, want) {
				t.Fatalf("lowered %#v; want %#v", c.Query, want)
			}
			again, ps := Compile(c.Canonical, Options{})
			if len(ps) != 0 || !reflect.DeepEqual(again.Query, want) {
				t.Fatalf("canonical %q lost integer: %+v %v", c.Canonical, again, ps)
			}
		})
	}
}

func TestLowerCountIfRawSQLComments(t *testing.T) {
	for _, body := range []string{"body = 'x' /* ) */", "body = 'x' -- )\n", "body = 'it''s )' /* ) */"} {
		text := "logs | count_if(`" + body + "`) as n"
		c, ps := Compile(text, Options{})
		if len(ps) != 0 {
			t.Fatalf("%q: %v", text, ps)
		}
		if want := "count_if(" + body + ") as n"; len(c.Query.Measures) != 1 || c.Query.Measures[0] != want {
			t.Fatalf("%q: got %#v; want %q", text, c.Query.Measures, want)
		}
		if _, err := panel.ParseMeasure(c.Query.From, c.Query.Measures[0]); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
	}
}

func TestLowerPositiveFixtures(t *testing.T) {
	for _, tt := range lowerFixtures {
		t.Run(tt.text, func(t *testing.T) {
			c, ps := Compile(tt.text, Options{})
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			want := panel.Query{From: strings.Fields(tt.text)[0], Where: []string{tt.sql}}
			if !reflect.DeepEqual(c.Query, want) {
				t.Fatalf("got %#v; want %#v", c.Query, want)
			}
		})
	}
	for _, tt := range []struct {
		text string
		want panel.Query
	}{
		{`spans | histogram(duration) by service`, panel.Query{From: "spans", Measures: []string{"count()"}, Histogram: &panel.Histogram{Field: "duration_ms", Buckets: "log2"}, By: []string{"service"}}},
		{`metrics where name:x | histogram(value) every 1m`, panel.Query{From: "metrics", Where: []string{"name = 'x'"}, Measures: []string{"count()"}, Histogram: &panel.Histogram{Field: "value", Buckets: "explicit"}, Bucket: "1m"}},
		{`spans sort errors limit 50`, panel.Query{From: "spans", Sort: "errors", Limit: 50}},
		{`spans sort duration`, panel.Query{From: "spans", Sort: "duration_ms"}},
		{`logs sort +time`, panel.Query{From: "logs", Sort: "+time"}},
	} {
		c, ps := Compile(tt.text, Options{})
		if len(ps) > 0 || !reflect.DeepEqual(c.Query, tt.want) {
			t.Fatal(tt.text, c, ps)
		}
	}
	for _, tt := range []struct{ text, measure string }{
		{`spans where kind:server | count_if(status:error) / count() * 100 as err_pct`, `count_if(status = 'STATUS_CODE_ERROR') / count() * 100 as err_pct`},
		{`spans | count()*100 as c100`, `count() * 100 as c100`},
		{`spans | count()/count() as one`, `count() / count() as one`},
		{`spans | p95(duration)-p50(duration) as spread`, `p95(duration_ms) - p50(duration_ms) as spread`},
		{`spans | count() * -1 as neg`, `count() * -1 as neg`},
		{`spans | count() * 1e3 as k`, `count() * 1000 as k`},
		{`spans | count_if(status:error -http.target:/healthz) as errs`, `count_if(status = 'STATUS_CODE_ERROR' AND (attributes['http.target'] IS NULL OR attributes['http.target'] != '/healthz')) as errs`},
		{`spans | count_if(a:1 or b:2) / count() as r`, `count_if((attributes['a'] = 1 OR attributes['b'] = 2)) / count() as r`},
		{`spans | count_distinct(user.id) by service`, `count_distinct(attributes['user.id'])`},
		{`spans | count_distinct(attributes['user.id'])`, `count_distinct(attributes['user.id'])`},
		{`spans | avg(attributes.http.response_size) as sz`, `avg(attributes['http.response_size']) as sz`},
		{`spans | p95(resource.k) as x`, `p95(resource['k']) as x`},
	} {
		c, ps := Compile(tt.text, Options{})
		if len(ps) > 0 || len(c.Query.Measures) != 1 || c.Query.Measures[0] != tt.measure {
			t.Fatal(tt.text, c, ps)
		}
	}
	for _, text := range glanceExamples {
		if _, ps := Compile(text, Options{}); len(ps) > 0 {
			t.Fatal(text, ps)
		}
	}
}

var glanceExamples = []string{
	`spans where kind:server | error_rate() by service every 1m`,
	`spans where service:checkout duration>500ms sort duration limit 50`,
	`spans where service:$service (status:error or status:unset) | count() by http_route`,
	`spans where kind:server | count_if(http_status_code>=500) / count() * 100 as pct_5xx by http_route`,
	`spans where kind:server | (count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex`,
	`spans | histogram(duration) by service`,
	`spans where service:checkout status:error -http.target:/healthz | count() by http_route`,
	`spans where user.id:* | count_distinct(user.id) by service`,
	`logs where service:api severity:error timeout sort time limit 200`,
	`logs where severity>=warn | count() by body_template sort count limit 10`,
	`metrics where name:http.server.requests | rate() by service every 1m`,
	`metrics where name:k8s.container.restarts | last(value) by resource.k8s.pod.name`,
}

func TestLowerEntryBoundaries(t *testing.T) {
	for _, tt := range []struct {
		text string
		want []string
	}{
		{`spans where a:1 b:2 (c:3 or d:4) (e:5 f:6)`, []string{`attributes['a'] = 1`, `attributes['b'] = 2`, `(attributes['c'] = 3 OR attributes['d'] = 4)`, `(attributes['e'] = 5 AND attributes['f'] = 6)`}},
		{`spans where service:$service status:error`, []string{`service = $service`, `status = 'STATUS_CODE_ERROR'`}},
		{`spans where (service:$service status:error)`, []string{`(service = $service AND status = 'STATUS_CODE_ERROR')`}},
		{`spans where a:1 (b:2 or c:3)`, []string{`attributes['a'] = 1`, `(attributes['b'] = 2 OR attributes['c'] = 3)`}},
		{`spans where not a:1 b:2`, []string{`(attributes['a'] IS NULL OR attributes['a'] != 1)`, `attributes['b'] = 2`}},
		{`spans where duration between 1s and 2s status:error`, []string{`duration_ms BETWEEN 1000 AND 2000`, `status = 'STATUS_CODE_ERROR'`}},
		{`spans where service:$service (status:error or status:unset)`, []string{`service = $service`, `(status = 'STATUS_CODE_ERROR' OR status = 'STATUS_CODE_UNSET')`}},
	} {
		c, ps := Compile(tt.text, Options{})
		if len(ps) > 0 || !reflect.DeepEqual(c.Query.Where, tt.want) {
			t.Fatal(tt.text, c, ps)
		}
	}
}

func TestLowerStructuredBounds(t *testing.T) {
	text := "spans where " + strings.Repeat("a:1 ", 16) + "z:2"
	q, ps := Parse(text, "")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	c, ps := Compile(text, Options{})
	if c != nil || len(ps) != 1 || ps[0].Message != "at most 16 filters" || *ps[0].Span != *textSpan(q.Where[16].Span()) {
		t.Fatal(c, ps)
	}
	text = `logs where attributes['long']:"` + strings.Repeat("x", 480) + `"`
	c, ps = Compile(text, Options{})
	want := fmt.Sprintf("this filter lowers to %d bytes; filters are at most 500", len(`attributes['long'] = '`+strings.Repeat("x", 480)+`'`))
	if c != nil || len(ps) != 1 || ps[0].Message != want {
		t.Fatal(c, ps, want)
	}
	if c, ps := Compile(text, Options{Viz: "logs"}); c != nil || len(ps) != 1 || ps[0].Message != want {
		t.Fatal("the selected viz must not duplicate the lowering bound", c, ps)
	}
	// One hundred quoted one-letter strings require 519 SQL bytes, so use
	// short numbers to exercise the 100-value limit independently of bytes.
	text = "spans where k in (" + strings.TrimSuffix(strings.Repeat("0,", 100), ",") + ")"
	if _, ps = Compile(text, Options{}); len(ps) > 0 {
		t.Fatal(ps)
	}
}

func TestLowerSearchBarMode(t *testing.T) {
	c, ps := Compile("service:api timeout", Options{Source: "logs"})
	want := panel.Query{From: "logs", Where: []string{`service = 'api'`, `contains(lower(body), lower('timeout'))`}}
	if len(ps) > 0 || !reflect.DeepEqual(c.Query, want) {
		t.Fatal(c, ps)
	}
}
func TestLowerCanonicalWhereIsIrrelevantToMeaning(t *testing.T) {
	a, ps := Compile("spans status:error", Options{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	b, ps := Compile("spans where status:error", Options{})
	if len(ps) > 0 || !reflect.DeepEqual(a.Query, b.Query) {
		t.Fatal(a, b, ps)
	}
}
func TestCompileReturnsNoQueryOnProblems(t *testing.T) {
	c, ps := Compile("spans where a:1 b:2 or c:3 | p99(duration) by http_route every 1m", Options{})
	if c != nil || len(ps) != 1 {
		t.Fatal(c, ps)
	}
}
