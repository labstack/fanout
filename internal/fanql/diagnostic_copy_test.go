package fanql

import (
	"github.com/labstack/fanout/internal/query"
	"regexp"
	"strings"
	"testing"
)

func TestDiagnosticExamplesUseFieldVocabulary(t *testing.T) {
	for _, tt := range []struct{ input, example string }{
		{"logs where severity:", "severity:error"},
		{"logs where severity>=", "severity>=warn"},
		{"spans where kind:", "kind:server"},
		{"spans where status:", "status:error"},
		{"metrics where type:", "type:gauge"},
		{"metrics where name:", "name:http.server.duration"},
		{"logs where service:", "service:api"},
		{"spans where enabled:", "enabled:true"},
	} {
		_, ps := Compile(tt.input, Options{})
		if len(ps) != 1 || !strings.HasSuffix(ps[0].Message, "e.g. "+tt.example) {
			t.Errorf("%q: %+v", tt.input, ps)
			continue
		}
		source := strings.Fields(tt.input)[0]
		if c, ps := Compile(source+" where "+tt.example, Options{}); c == nil || len(ps) != 0 {
			t.Errorf("invalid suggested example %q: %+v", tt.example, ps)
		}
	}
}

func TestDiagnosticCopyNamesAndTime(t *testing.T) {
	for _, tt := range []struct{ input, viz, message string }{
		{"logs | count() by body_template sort rate", "log_patterns", "pattern lists sort by count only"},
		{"logs where _time:now", "", "set the time range with the time picker, not in the query"},
		{"logs since 1h", "", "set the time range with the time picker, not in the query"},
		{"logs where service:api and severity:error or body", "", "mixed and/or needs parentheses"},
	} {
		c, ps := Compile(tt.input, Options{Viz: tt.viz})
		if c != nil || len(ps) != 1 || ps[0].Message != tt.message {
			t.Errorf("%q: %+v", tt.input, ps)
		}
	}
}

func TestCorpusDiagnosticCopy(t *testing.T) {
	apiExample := regexp.MustCompile(`\b([a-z_][a-z_0-9.]*)\s*(?::|[<>!=]=?)api\b`)
	for _, fixture := range readCorpus(t) {
		if fixture.Query != nil {
			continue
		}
		_, problems := Compile(fixture.Text, Options{Viz: fixture.Viz})
		for _, p := range problems {
			copy := p.Message + " " + p.Hint
			for _, internal := range []string{"FanQL 1", "log_patterns", "state_timeline", "service_map"} {
				if strings.Contains(copy, internal) {
					t.Errorf("%q exposes %q: %s", fixture.Text, internal, copy)
				}
			}
			if _, example, ok := strings.Cut(copy, "e.g. "); ok {
				for _, match := range apiExample.FindAllStringSubmatch(example, -1) {
					if match[1] != "service" {
						t.Errorf("%q has an example from another field: %s", fixture.Text, example)
					}
				}
				if strings.Contains(example, "(api, web)") && !strings.Contains(fixture.Text, "service in") {
					t.Errorf("%q has a service-only list example: %s", fixture.Text, example)
				}
			}

		}
	}
}

func TestCompileKeepsProblemsFromIndependentClauses(t *testing.T) {
	text := "spans where service:api and status:error or timeout | count() by service every 2m sort +count desc limit 0"
	c, ps := Compile(text, Options{})
	if c != nil || len(ps) != 4 {
		t.Fatalf("independent clauses: %+v", ps)
	}
	for i, want := range []string{"mixed and/or needs parentheses", `unknown bucket "2m"`, "sort takes one direction", "limit needs a whole number"} {
		if !strings.HasPrefix(ps[i].Message, want) || ps[i].Span == nil {
			t.Errorf("problem %d: %+v", i, ps[i])
		}
		if i > 0 && ps[i].Span.Start < ps[i-1].Span.Start {
			t.Fatal("diagnostics are not in source order")
		}
	}
}

func TestMetricFreeTextDiagnosticUsesSchemaExample(t *testing.T) {
	schema := &query.Schema{Signals: map[string]query.SignalSchema{"metrics": {Metrics: []query.MetricSchema{{Name: "cpu.utilization", Count: 1}, {Name: "http.server.requests", Count: 12}}}}}
	c, ps := Compile("metrics where timeout", Options{Schema: schema})
	if c != nil || len(ps) != 1 || ps[0].Message != "free text needs a field on metrics; use name:http.server.requests" {
		t.Fatalf("metric example: %+v", ps)
	}
}

func TestCompileKeepsTheFirstGroupingRepairSpan(t *testing.T) {
	_, ps := Compile("spans where (a:1 b:2 or c:3) and d:4 or e:5", Options{})
	if len(ps) != 1 || ps[0].Span == nil || len(ps[0].FixIts) != 2 {
		t.Fatalf("grouping diagnostic: %+v", ps)
	}
	for _, fix := range ps[0].FixIts {
		if fix.Span != *ps[0].Span {
			t.Errorf("fix span %+v differs from diagnostic %+v", fix.Span, ps[0].Span)
		}
	}
}
