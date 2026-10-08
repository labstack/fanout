package panel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const specExample = `{
  "version": 1,
  "name": "Checkout latency",
  "description": "Latency, errors and suspects for checkout.",
  "time": { "range": "1h", "refresh": "30s", "compare": "previous_period" },
  "variables": [
    { "name": "service", "kind": "query", "from": "spans", "field": "service", "default": "checkout" },
    { "name": "route", "kind": "query", "from": "spans", "field": "http_route",
      "where": ["service = $service", "kind = 'SPAN_KIND_SERVER'"], "include_all": true },
    { "name": "namespace", "kind": "constant", "value": "shop" }
  ],
  "panels": [
    {
      "id": "latency",
      "title": "Latency for $service",
      "viz": "timeseries",
      "width": 8,
      "query": {
        "from": "spans",
        "where": ["service = $service", "kind = 'SPAN_KIND_SERVER'", "http_route = $route"],
        "measures": ["p50(duration_ms)", "p95(duration_ms)", "p99(duration_ms)"],
        "bucket": "auto"
      },
      "unit": "ms",
      "thresholds": [{ "value": 1500, "status": "bad", "label": "p99 budget" }],
      "drill": "traces"
    }
  ]
}`

func decode(t *testing.T, text string) Dashboard {
	t.Helper()
	var d Dashboard
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestValidateAcceptsSpecExample(t *testing.T) {
	d := decode(t, specExample)
	Normalize(&d)
	if problems := Validate(&d); len(problems) > 0 {
		t.Fatalf("spec example rejected: %v", problems)
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	d := Dashboard{Name: " Ops ", Panels: []Panel{
		{ID: "rps", Title: "Requests", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"rate()"}}},
		{ID: "notes", Title: "Notes", Viz: "text", Content: "Hello"},
	}}
	Normalize(&d)
	if d.Version != 1 || d.Time.Range != "1h" || d.Time.Refresh != "30s" || d.Name != "Ops" {
		t.Fatalf("top-level defaults = %+v", d)
	}
	if d.Panels[0].Width != 6 || d.Panels[0].Height != "m" || d.Panels[0].Query.Bucket != "auto" {
		t.Fatalf("timeseries defaults = %+v %+v", d.Panels[0], d.Panels[0].Query)
	}
	if d.Panels[1].Width != 4 || d.Panels[1].Height != "s" {
		t.Fatalf("text defaults = %+v", d.Panels[1])
	}
}

func TestNormalizeServiceMapLargeDefault(t *testing.T) {
	d := Dashboard{Name: "Map", Panels: []Panel{{ID: "map", Title: "Map", Viz: "service_map", Query: &Query{From: "spans"}}}}
	Normalize(&d)
	if d.Panels[0].Height != "l" {
		t.Fatalf("service map height=%q", d.Panels[0].Height)
	}
}

func TestValidateReportsPathsAndHints(t *testing.T) {
	cases := []struct {
		name, mutate, path, contains string
	}{
		{"bad id", `{"panels":[{"id":"Latency","title":"x","viz":"stat","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].id", "lowercase"},
		{"duplicate id", `{"panels":[{"id":"a","title":"x","viz":"text","content":"x"},{"id":"a","title":"y","viz":"text","content":"y"}]}`, "panels[1].id", "duplicate"},
		{"unknown viz", `{"panels":[{"id":"a","title":"x","viz":"piechart","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].viz", "stat, gauge, timeseries, bar, table, text"},
		{"query and sql", `{"panels":[{"id":"a","title":"x","viz":"table","sql":"SELECT 1","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0]", "exactly one of query or sql"},
		{"unknown field", `{"panels":[{"id":"a","title":"x","viz":"bar","query":{"from":"spans","measures":["count()"],"by":["route"]}}]}`, "panels[0].query.by[0]", "http_route"},
		{"unknown measure", `{"panels":[{"id":"a","title":"x","viz":"stat","query":{"from":"spans","measures":["p95(duraton_ms)"]}}]}`, "panels[0].query.measures[0]", "duration_ms"},
		{"mixed units", `{"panels":[{"id":"a","title":"x","viz":"timeseries","query":{"from":"spans","measures":["p95(duration_ms)","error_rate()"]}}]}`, "panels[0].query.measures", "split"},
		{"stack percentile", `{"panels":[{"id":"a","title":"x","viz":"timeseries","options":{"style":"stacked"},"query":{"from":"spans","measures":["p95(duration_ms)"],"by":["service"]}}]}`, "panels[0].options.style", "additive"},
		{"bar needs by", `{"panels":[{"id":"a","title":"x","viz":"bar","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].query.by", "at least 1"},
		{"unknown variable", `{"panels":[{"id":"a","title":"x for $svc","viz":"stat","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].title", "$svc"},
		{"gauge bounds", `{"panels":[{"id":"a","title":"x","viz":"gauge","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0]", "min and max"},
		{"bad range", `{"time":{"range":"90m"},"panels":[{"id":"a","title":"x","viz":"text","content":"x"}]}`, "time.range", "1h"},
		{"click unknown var", `{"panels":[{"id":"a","title":"x","viz":"bar","click":{"set_variable":"route"},"query":{"from":"spans","measures":["count()"],"by":["http_route"]}}]}`, "panels[0].click.set_variable", "route"},
		{"by alias collision", `{"panels":[{"id":"a","title":"x","viz":"table","query":{"from":"spans","measures":["count()"],"by":["attributes['x']","resource['x']"]}}]}`, "panels[0].query.by[1]", "column x"},
		{"measure alias equals by", `{"panels":[{"id":"a","title":"x","viz":"bar","query":{"from":"spans","measures":["count() as service"],"by":["service"]}}]}`, "panels[0].query.measures", "rename with as"},
		{"attribute key", `{"panels":[{"id":"a","title":"x","viz":"bar","query":{"from":"spans","measures":["count()"],"by":["attributes['http.route']"]}}]}`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decode(t, `{"name":"t",`+strings.TrimPrefix(tc.mutate, "{"))
			Normalize(&d)
			problems := Validate(&d)
			if tc.path == "" {
				if len(problems) > 0 {
					t.Fatalf("unexpected problems: %v", problems)
				}
				return
			}
			for _, p := range problems {
				if p.Path == tc.path && strings.Contains(p.Message+" "+p.Hint, tc.contains) {
					return
				}
			}
			t.Fatalf("want problem at %s containing %q, got %v", tc.path, tc.contains, problems)
		})
	}
}

func TestParseSpan(t *testing.T) {
	for _, s := range []string{"90m", "6h", "2d", "30s", "400d"} {
		if _, ok := ParseSpan(s); !ok {
			t.Errorf("%q rejected", s)
		}
	}
	for _, s := range []string{"0d", "-1h", "1w", "d", "9999999999999d", "401d", "9999999999999h"} {
		if d, ok := ParseSpan(s); ok {
			t.Errorf("%q accepted as %v", s, d)
		}
	}
	if d, _ := ParseSpan("2d"); d != 48*time.Hour {
		t.Errorf("2d = %v", d)
	}
}

func TestProblemsError(t *testing.T) {
	err := Problems{{Path: "a", Message: "bad", Hint: "did you mean b?"}}
	if err.Error() != "a: bad (did you mean b?)" {
		t.Fatal(err.Error())
	}
}

func TestVizOrder(t *testing.T) {
	if len(vizOrder) != 15 {
		t.Fatalf("vizOrder has %d types, want 15", len(vizOrder))
	}
	seen := map[string]bool{}
	for _, viz := range vizOrder {
		if seen[viz] {
			t.Fatalf("duplicate %s", viz)
		}
		seen[viz] = true
		if _, ok := vizSpecs[viz]; !ok {
			t.Fatalf("unregistered %s", viz)
		}
	}
}

func TestGroupedPanelsRequireOneMeasure(t *testing.T) {
	for _, tc := range []struct {
		viz string
		by  []string
	}{
		{"timeseries", []string{"service"}}, {"bar", []string{"service", "operation"}},
	} {
		t.Run(tc.viz, func(t *testing.T) {
			d := Dashboard{Name: "Grouped", Panels: []Panel{{ID: "p", Title: "P", Viz: tc.viz, Query: &Query{From: "spans", By: tc.by, Measures: []string{"p50(duration_ms)", "p95(duration_ms)"}}}}}
			Normalize(&d)
			for _, p := range Validate(&d) {
				if p.Path == "panels[0].query.measures" && p.Message == "grouped panels show one measure" && p.Hint == "use one panel per measure, or remove by" {
					return
				}
			}
			t.Fatal("missing grouped measure problem")
		})
	}
}
