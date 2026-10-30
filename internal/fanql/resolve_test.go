package fanql

import (
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/query"
)

func resolvedText(t *testing.T, text string, opts Options) (*resolved, query.Problems) {
	t.Helper()
	q, ps := parse(text, "")
	if len(ps) != 0 {
		t.Fatalf("parse %q: %v", text, ps)
	}
	return resolve(q, opts)
}

func TestResolveSingleSegmentFieldIsColumnOrAttribute(t *testing.T) {
	for _, tt := range []struct {
		text, column, key string
		typ               query.FieldType
		notice            string
	}{
		{"spans where value>-3", "attributes", "value", query.TypeString, `"value" is not a spans column; reading attributes['value']`},
		{"metrics where value>-3", "value", "", query.TypeNumber, ""},
		{"metrics where rate:x", "attributes", "rate", query.TypeString, `"rate" is a measure, not a metrics column; reading attributes['rate']`},
		{"spans where Service:api", "service", "", query.TypeString, ""},
		{"spans where servce:api", "attributes", "servce", query.TypeString, "did you mean service?"},
	} {
		t.Run(tt.text, func(t *testing.T) {
			r, ps := resolvedText(t, tt.text, Options{})
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			f := r.Query.Where[0].(*comparison).Field.Ref
			if f.Column != tt.column || f.Key != tt.key || f.Type != tt.typ {
				t.Fatalf("ref %#v", f)
			}
			if tt.notice == "" {
				if len(r.Notices) != 0 {
					t.Fatal(r.Notices)
				}
			} else if len(r.Notices) == 0 || !strings.Contains(r.Notices[0].Message, tt.notice) {
				t.Fatal(r.Notices)
			}
		})
	}
}

func TestResolveDottedAndPrefixedKeys(t *testing.T) {
	for _, tt := range []struct{ field, column, key string }{
		{"resource.k8s.pod.name", "resource", "k8s.pod.name"}, {"attributes.http.target", "attributes", "http.target"},
		{"attributes['resource.k']", "attributes", "resource.k"}, {"HTTP.Target", "attributes", "HTTP.Target"},
	} {
		r, ps := resolvedText(t, "spans where "+tt.field+":x", Options{})
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		f := r.Query.Where[0].(*comparison).Field.Ref
		if f.Column != tt.column || f.Key != tt.key {
			t.Fatal(f)
		}
	}
	r, ps := resolvedText(t, "spans | count_distinct(user.id), p95(resource.k)", Options{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if r.Query.Stage.Measures[0].Parsed.Field.Key != "user.id" || r.Query.Stage.Measures[1].Parsed.Field.Column != "resource" {
		t.Fatal(r.Query.Stage)
	}
	r, ps = resolvedText(t, "metrics | last(value) by resource.k8s.pod.name", Options{})
	if len(ps) > 0 || r.Query.Stage.By[0].Ref.Key != "k8s.pod.name" {
		t.Fatal(r, ps)
	}
	_, ps = resolvedText(t, "spans where x-y.z:1", Options{})
	if len(ps) == 0 || ps[0].Message != `"x-y.z" needs brackets` {
		t.Fatal(ps)
	}
}

func TestResolveVocabularies(t *testing.T) {
	for _, tt := range []struct{ text, want string }{
		{"spans where status:error", "STATUS_CODE_ERROR"}, {"spans where status:STATUS_CODE_ERROR", "STATUS_CODE_ERROR"}, {`spans where status:"error"`, "error"},
		{"spans where kind:server", "SPAN_KIND_SERVER"}, {"metrics where type:counter", "counter"}, {"logs where severity:warning", "WARN"}, {"logs where severity:Error", "ERROR"}, {"logs where severity ~ error", "error"},
	} {
		r, ps := resolvedText(t, tt.text, Options{})
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		if v := r.Query.Where[0].(*comparison).Value.Stored; v != tt.want {
			t.Fatalf("%s: %s", tt.text, v)
		}
	}
	r, ps := resolvedText(t, "spans where status in (error, ok)", Options{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if got := r.Query.Where[0].(*inExpr).Values; got[0].Stored != "STATUS_CODE_ERROR" || got[1].Stored != "STATUS_CODE_OK" {
		t.Fatal(got)
	}
	schema := &query.Schema{Signals: map[string]query.SignalSchema{"logs": {Columns: []query.ColumnSchema{{Field: query.Field{Name: "severity"}, Values: []query.Option{{Value: "ERROR"}, {Value: "INFO"}}}}}}}
	r, ps = resolvedText(t, "logs where severity:custom", Options{Schema: schema})
	if len(ps) > 0 || r.Query.Where[0].(*comparison).Value.Stored != "CUSTOM" || len(r.Notices) != 1 || r.Notices[0].Message != "not a standard level; stored levels in this window: ERROR, INFO" {
		t.Fatal(r, ps)
	}
}

func TestResolveSeverityRanges(t *testing.T) {
	for _, tt := range []struct {
		text string
		want []string
	}{
		{"severity>=warn", []string{"WARN", "ERROR", "FATAL"}}, {"severity>warn", []string{"ERROR", "FATAL"}},
		{"severity<error", []string{"TRACE", "DEBUG", "INFO", "WARN"}}, {"severity<=error", []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR"}},
		{"severity between info and error", []string{"INFO", "WARN", "ERROR"}},
	} {
		r, ps := resolvedText(t, "logs where "+tt.text, Options{})
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		var bands []string
		switch x := r.Query.Where[0].(type) {
		case *comparison:
			bands = x.Bands
		case *betweenExpr:
			bands = x.Bands
		}
		if !reflect.DeepEqual(bands, tt.want) {
			t.Fatal(bands, tt.want)
		}
	}
	r, ps := resolvedText(t, "spans where http_status_code>=warn", Options{})
	if len(ps) > 0 || r.Query.Where[0].(*comparison).Bands != nil {
		t.Fatal(r, ps)
	}
}

func TestResolveTypedLiterals(t *testing.T) {
	for _, text := range []string{"logs where severity:14", "logs where severity:true", "spans where status:500", "metrics where type:false"} {
		r, ps := resolvedText(t, text, Options{})
		if len(ps) != 0 {
			t.Fatal(text, ps)
		}
		v := r.Query.Where[0].(*comparison).Value
		if v.Stored != v.Text {
			t.Fatalf("typed literals must not be vocabulary words: %#v", v)
		}
	}
	for _, text := range []string{"spans where duration_ms>500", `spans where start_time>"2026-10-29T10:00:00Z"`, `spans where start_time>"2026-10-29 10:00:00"`} {
		_, ps := resolvedText(t, text, Options{})
		if len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	for _, tt := range []struct{ text, want string }{{"spans where http_status_code>=500", "integer"}, {"spans where http_status_code:500", ""}} {
		r, ps := resolvedText(t, tt.text, Options{})
		if len(ps) > 0 || r.Query.Where[0].(*comparison).Field.Ref.CompareAs != tt.want {
			t.Fatal(r, ps)
		}
	}
}

func TestResolveFilterFunctions(t *testing.T) {
	for _, text := range []string{`logs where length(body)>1000`, `spans where lower(service) = "api"`, `spans where starts_with(http_route, "/api")`, `logs where regexp_matches(body, "error.*")`} {
		if _, ps := resolvedText(t, text, Options{}); len(ps) != 0 {
			t.Fatal(text, ps)
		}
	}
}

func TestResolveMeasures(t *testing.T) {
	r, ps := resolvedText(t, `spans | count_distinct(flag)`, Options{})
	if len(ps) != 0 || len(r.Notices) != 1 || r.Query.Stage.Measures[0].Parsed.Field.Key != "flag" {
		t.Fatal(r, ps)
	}
	for _, text := range []string{`spans | count(*)`, `spans | quantile(duration, 0.999) as q`, `spans | count_if(status:error)`} {
		if _, ps := resolvedText(t, text, Options{}); len(ps) != 0 {
			t.Fatal(text, ps)
		}
	}
}

func TestResolveDiagnostics(t *testing.T) {
	for _, tt := range []struct{ text, want string }{
		{"spans where status:fail", `unknown status "fail"; use status:error, status:ok or status:unset`},
		{"spans where kind:http", "use kind:server"},
		{"logs where severity>fatal", "severity>fatal matches no level; use severity:fatal"},
		{"logs where severity<trace", "matches no level"},
		{"logs where severity between error and info", "between needs the lower level first: severity between info and error"},
		{"logs where severity>=custom", `"custom" has no severity rank; use trace, debug, info, warn, error or fatal`},
		{"logs where severity>=unspecified", `"unspecified" has no severity rank`},
		{"spans where http_status_code>500ms", "units apply to duration only; write http_status_code>500"},
		{"spans where duration>500", "duration needs a unit; e.g. duration>500ms"},
		{"spans where duration>-1ms", "durations are not negative"},
		{"spans where duration>1h30m", "duration needs a unit"},
		{"spans where duration_ms:true", "duration_ms needs a number"},
		{"logs where time:x", `"time" is a time column; compare it with > or <`},
		{`spans where start_time>"yesterday"`, "RFC 3339"},
		{"metrics where timeout", "free text needs a field on metrics; use name:http.server.duration"},
		{"spans where P95(Duration)", "function p95 is not allowed in a filter"},
		{"spans where starts_with(http_route, /api)", "starts_with needs a quoted string"},
		{"logs where length(body)", "length(...) needs a comparison"},
		{"spans where lower(k)", "lower(...) needs a comparison"},
		{`spans where starts_with(http_route, "/api") > 1`, "predicates are not compared"},
		{"spans | p95(service)", "p95 needs a numeric field; service is string"},
		{"spans | p95()", "p95 takes 1 argument(s), got 0; e.g. p95(duration)"},
		{"spans | count() as P95", `alias "P95" must be lowercase letters, digits and underscores`},
		{"spans | count(), count()", "two measures are named count; add distinct aliases with as"},
		{"metrics | error_rate()", "error_rate is not available for metrics"},
		{"spans | last(value)", "last is not available for spans"},
		{"spans | count_if(service:$s) as n", "variables are not allowed inside count_if"},
		{"metrics | count_if(name:x) as n", "measure arithmetic is not available for metrics"},
		{"metrics | -avg(value) as neg", "measure arithmetic is not available for metrics"},
		{"spans | quantile(duration, 2)", "quantile q must be a number between 0 and 1"},
		{"spans | count() by service sort p95", `sort "p95" must name a measure alias; aliases here: count`},
	} {
		t.Run(tt.text, func(t *testing.T) {
			_, ps := resolvedText(t, tt.text, Options{})
			if len(ps) == 0 || !strings.Contains(ps[0].Message, tt.want) || ps[0].Span == nil {
				t.Fatalf("want %q: %v", tt.want, ps)
			}
		})
	}
	// These syntax diagnostics belong to the existing parser, before resolution.
	for _, text := range []string{"spans | count_distinct(x-y)", "spans | count() by a, b, c, d"} {
		if _, ps := parse(text, ""); len(ps) == 0 {
			t.Fatal(text)
		}
	}
}

func TestResolveSortKeys(t *testing.T) {
	for _, tt := range []struct {
		text, viz string
		bad       bool
		notice    string
	}{
		{"spans sort +duration", "traces", true, ""}, {"spans sort duration", "traces", false, ""}, {"spans sort errors", "traces", false, "sort errors also filters to error spans; new queries: write status:error … sort duration"},
		{"spans sort +start", "traces", false, ""}, {"logs sort time", "logs", false, ""}, {"logs sort +time", "logs", false, ""}, {"logs sort duration", "logs", true, ""},
		{"logs | count() by body_template sort count", "log_patterns", false, ""}, {"logs | count() by body_template sort rate", "log_patterns", true, ""},
		{"spans | count() every 1m sort count", "timeseries", false, "sort does not apply to bucketed series; series are chosen by top (options)"}, {"spans | count() every 1m sort count", "", false, ""},
	} {
		r, ps := resolvedText(t, tt.text, Options{Viz: tt.viz})
		if tt.bad {
			if len(ps) == 0 {
				t.Fatal(tt.text)
			}
			continue
		}
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		if tt.notice != "" && (len(r.Notices) == 0 || r.Notices[len(r.Notices)-1].Message != tt.notice) {
			t.Fatal(r.Notices)
		}
		if tt.notice == "" && len(r.Notices) > 0 {
			t.Fatal(r.Notices)
		}
	}
}

func TestResolveVariables(t *testing.T) {
	opts := Options{Variables: []query.Variable{{Name: "service", Kind: "query"}, {Name: "svcs", Kind: "query", Multi: true}}}
	for _, tt := range []struct{ text, want string }{{"service:$region", "service, svcs"}, {"service:$svcs", "$svcs holds several values; use service in $svcs"}} {
		_, ps := resolvedText(t, "spans where "+tt.text, opts)
		if len(ps) == 0 || !strings.Contains(ps[0].Message, tt.want) {
			t.Fatal(ps)
		}
	}
	_, ps := resolvedText(t, "spans where service in $svcs", opts)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	r, ps := resolvedText(t, "spans where service:$x", Options{})
	if len(ps) > 0 || len(r.Notices) != 1 || r.Notices[0].Message != "declared variables are needed to check $x" {
		t.Fatal(r, ps)
	}
}

func TestResolveCompatibleVizzesAndEdits(t *testing.T) {
	r, ps := resolvedText(t, "spans where kind:server | count_if(http_status_code>=500) / count() * 100 as pct_5xx by http_route", Options{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	var names []string
	for _, v := range r.Compatible {
		names = append(names, v.Viz)
	}
	if !reflect.DeepEqual(names, []string{"bar", "table"}) {
		t.Fatal(r.Compatible)
	}
	if len(r.Edits) != 1 || r.Edits[0].Viz != "timeseries" || r.Edits[0].Text != "spans where kind:server | count_if(http_status_code>=500) / count() * 100 as pct_5xx by http_route every auto" {
		t.Fatal(r.Edits)
	}
	r, ps = resolvedText(t, "spans where status:error", Options{})
	if len(ps) > 0 || len(r.Compatible) != 1 || r.Compatible[0].Viz != "traces" {
		t.Fatal(r, ps)
	}
	r, ps = resolvedText(t, "spans | count()", Options{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	names = nil
	for _, v := range r.Compatible {
		names = append(names, v.Viz)
	}
	if !reflect.DeepEqual(names, []string{"stat", "gauge", "table"}) {
		t.Fatal(names)
	}
}
