package fanql

import (
	"reflect"
	"testing"
)

var canonicalCases = [][2]string{
	{`spans where attributes['attributes.k']:x`, `spans where attributes['attributes.k']:x`},
	{`spans where attributes['Resource.k']:x`, `spans where attributes['Resource.k']:x`},
	{`logs where length(body)>1e3`, `logs where length(body)>1000`},
	{`spans where status ~ STATUS_CODE_ERROR`, `spans where status ~ STATUS_CODE_ERROR`},
	{`spans where status>STATUS_CODE_ERROR`, `spans where status>STATUS_CODE_ERROR`},
	{`logs where severity ~ "ERROR"`, `logs where severity ~ ERROR`},
	{`logs where body ~ 2e3`, `logs where 2e3`},
	{`logs where body ~ $term`, `logs where body ~ $term`},
	{`spans where http_status_code:2e3`, `spans where http_status_code:2e3`},
	{`logs where service:"TRUE"`, `logs where service:"TRUE"`},
	{`spans where attributes['é.x']:api`, `spans where attributes['é.x']:api`},
	{`spans where duration > 500ms`, `spans where duration>500ms`},
	{`logs where --severity:error`, `logs where severity:error`},
	{`spans where flag:true`, `spans where attributes['flag']:true`},
	{`spans where attributes.http.target:/h`, `spans where http.target:/h`},
	{`spans where attributes['resource.k']:x`, `spans where attributes['resource.k']:x`},
	{`spans status:error | count() by service`, `spans where status:error | count() by service`},
	{`spans | count() by service sort -count`, `spans | count() by service sort count`},
	{`spans | count() by service | sort count | limit 10`, `spans | count() by service sort count limit 10`},
	{`spans where k:>5`, `spans where attributes['k']>5`},
	{`logs where "limit"`, `logs where "limit"`},
	{`logs where top:x`, `logs where attributes['top']:x`},
	{`spans | count_distinct(attributes['user.id'])`, `spans | count_distinct(user.id)`},
	{`spans | count_distinct(flag)`, `spans | count_distinct(attributes['flag'])`},
	{`spans | P95(Duration)`, `spans | p95(duration)`},
	{`spans | avg(attributes.http.response_size) as sz`, `spans | avg(http.response_size) as sz`},
	{`spans where kind:server | count_if(status:error -http.target:/healthz) / count() * 100 as e`, `spans where kind:server | count_if(status:error -http.target:/healthz) / count() * 100 as e`},
	{`SPANS WHERE Service:API | COUNT() EVERY 1M`, `spans where service:API | count() every 1m`},
	{`spans where service:$service (status:error or status:unset)`, `spans where service:$service (status:error or status:unset)`},
	{`spans where (a:1 b:2)`, `spans where (attributes['a']:1 attributes['b']:2)`},
	{`spans where a:1 or b:2`, `spans where attributes['a']:1 or attributes['b']:2`},
}

func TestPrintCanonical(t *testing.T) {
	for _, tt := range canonicalCases {
		t.Run(tt[0], func(t *testing.T) {
			c, problems := Compile(tt[0], Options{})
			if len(problems) != 0 {
				t.Fatalf("compile: %+v", problems)
			}
			if c.Canonical != tt[1] {
				t.Fatalf("got %q, want %q", c.Canonical, tt[1])
			}
		})
	}
}

func TestPrintIsAFixedPoint(t *testing.T) {
	for _, tt := range canonicalCases {
		a, problems := Compile(tt[0], Options{})
		if len(problems) != 0 {
			t.Fatalf("%s: %+v", tt[0], problems)
		}
		b, problems := Compile(a.Canonical, Options{})
		if len(problems) != 0 {
			t.Fatalf("%s: %+v", a.Canonical, problems)
		}
		if b.Canonical != a.Canonical || !reflect.DeepEqual(a.Query, b.Query) {
			t.Fatalf("not a fixed point: %q -> %q; %#v != %#v", tt[0], b.Canonical, a.Query, b.Query)
		}
	}
}

func TestPrintPreservesQuotedNonFiniteNumbers(t *testing.T) {
	for _, text := range []string{
		`logs where service:"1e309"`,
		`logs where service:"-1e309"`,
		`logs where service:"1e+309"`,
		`logs where service in ("1e309", "-1e309")`,
	} {
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if len(ps) != 0 {
				t.Fatal(ps)
			}
			if c.Canonical != text {
				t.Errorf("canonical %q; want %q", c.Canonical, text)
			}
			again, ps := Compile(c.Canonical, Options{})
			if len(ps) != 0 || !reflect.DeepEqual(again.Query, c.Query) {
				t.Fatalf("recompile: %+v %v", again, ps)
			}
		})
	}
}

func TestPrintCompatibleEditPlacesBucketBeforeSortAndLimit(t *testing.T) {
	c, ps := Compile(`spans | count() by service sort count limit 10`, Options{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(c.Edits) != 1 || c.Edits[0].Text != `spans | count() by service every auto sort count limit 10` {
		t.Fatalf("edits: %+v", c.Edits)
	}
	edited, ps := Compile(c.Edits[0].Text, Options{Viz: "timeseries"})
	if len(ps) > 0 || edited.Query.Bucket != "auto" || edited.Query.Sort != "count" || edited.Query.Limit != 10 {
		t.Fatalf("applied edit: %+v %+v", edited, ps)
	}
}
