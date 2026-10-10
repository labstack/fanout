package panel

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMetricNameRuleUsesCheckedEqualities(t *testing.T) {
	e, _ := metricFixture(t)
	for _, tc := range []struct {
		label, where string
		by           []string
		all, multi   bool
		measure      string
		valid        bool
	}{
		{"literal", "name='net'", nil, false, false, "rate()", true},
		{"reversed_literal", "'net'=name", nil, false, false, "rate()", true},
		{"quoted_field", "\"name\"='net'", nil, false, false, "rate()", true},
		{"cast_literal", "name=CAST('net' AS VARCHAR)", nil, false, false, "rate()", true},
		{"expression_value", "name=coalesce('net','cpu')", nil, false, false, "rate()", false},
		{"cast_field", "CAST(name AS DOUBLE)=CAST($metric AS DOUBLE)", nil, false, false, "rate()", false},
		{"single", "name=$metric", nil, false, false, "rate()", true},
		{"all_option", "name=$metric", nil, true, false, "rate()", false},
		{"all_grouped", "name=$metric", []string{"name"}, true, false, "rate()", true},
		{"multi", "name IN $metric", nil, false, true, "rate()", false},
		{"no_name", "service='svc'", nil, false, false, "sum(value)", false},
		{"field_equality", "name=service", nil, false, false, "sum(value)", false},
		{"or", "name='net' OR name='cpu'", nil, false, false, "sum(value)", false},
		{"count", "service='svc'", nil, false, false, "count()", true},
		{"distinct", "service='svc'", nil, false, false, "count_distinct(name)", true},
		{"share", "service='svc'", nil, false, false, "share()", false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			d := Dashboard{Name: "Metrics", Variables: []Variable{{Name: "metric", Kind: "custom", Options: []string{"net", "cpu"}, Default: "net", IncludeAll: tc.all, Multi: tc.multi}},
				Panels: []Panel{{ID: "metric", Title: "Metric", Viz: "table", Query: &Query{From: "metrics", Where: []string{tc.where}, Measures: []string{tc.measure}, By: tc.by}}}}
			Normalize(&d)
			_, problems, err := Check(t.Context(), e.engine, &d)
			if err != nil {
				t.Fatal(err)
			}
			if (len(problems) == 0) != tc.valid {
				t.Fatalf("problems=%v", problems)
			}
			if !tc.valid {
				found := false
				for _, p := range problems {
					if p.Path == "panels[0].query.where" && strings.Contains(p.Hint, "name") {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing name hint: %v", problems)
				}
			}
			if tc.label == "all_option" {
				_, err := e.Run(t.Context(), RunRequest{Dashboard: d, Vars: map[string]Value{"metric": {All: true}}})
				var p Problems
				if !errors.As(err, &p) {
					t.Fatalf("All accepted: %v", err)
				}
			}
		})
	}
}

func TestMetricKindMismatchHasActionableDiagnosis(t *testing.T) {
	e, db := metricFixture(t)
	m := metricCounter(fixtureStart, 100)
	m.Monotonic = false
	m.Name = "system.memory.usage"
	insertMetrics(t, db, m)
	r := runMetricPanel(t, e, Panel{Viz: "timeseries", Query: &Query{From: "metrics", Where: []string{"name='system.memory.usage'"}, Measures: []string{"rate()"}, Bucket: "1m"}}, fixtureStart, fixtureStart.Add(time.Minute))
	want := "rate() needs a counter; system.memory.usage is an updown; use last(value) or avg(value)"
	if r.Status != StatusEmpty || r.Diagnosis != want {
		t.Fatalf("diagnosis=%+v", r)
	}
}

func TestMetricHistogramDiagnosisSuggestsCountWithoutAliases(t *testing.T) {
	for _, alias := range []string{"AS r", "as r"} {
		e, db := metricFixture(t)
		insertMetrics(t, db, metricHistogram(fixtureStart, "[10]", "[1,0]", 1, 5, "a"))
		r := runMetricMeasure(t, e, []string{"avg(value) " + alias}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
		if r.Status != StatusEmpty || r.Diagnosis != "avg(value) is not defined for net (histogram); use count()" {
			t.Fatalf("%s: %+v", alias, r)
		}
	}
}

func TestMetricNameInsideAndHintsAtSeparateWhereEntry(t *testing.T) {
	e, _ := metricFixture(t)
	d := Dashboard{Name: "Metrics", Panels: []Panel{{ID: "m", Title: "Metric", Viz: "table", Query: &Query{From: "metrics", Where: []string{"service='svc' AND (name='net' AND type='counter')"}, Measures: []string{"rate()"}}}}}
	Normalize(&d)
	_, problems, err := Check(t.Context(), e.engine, &d)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, "put the name filter in its own where entry") {
		t.Fatalf("problems=%v", problems)
	}
}

func TestMetricNameAllGroupsDifferentMetricsSeparately(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		name  string
		value float64
	}{{"net", 10}, {"cpu", 100}} {
		m := metricCounter(fixtureStart, p.value)
		m.Name = p.name
		m.Type = "gauge"
		m.Monotonic = false
		m.Temporality = ""
		insertMetrics(t, db, m)
	}
	start, end := fixtureStart, fixtureStart.Add(time.Minute)
	d := Dashboard{Name: "Metrics", Time: Time{From: &start, To: &end}, Variables: []Variable{{
		Name: "metric", Kind: "custom", Options: []string{"net", "cpu"}, IncludeAll: true, Default: AllValue,
	}}, Panels: []Panel{{ID: "metrics", Title: "Metrics", Viz: "table", Query: &Query{
		From: "metrics", Where: []string{"name=$metric"}, Measures: []string{"sum(value)"}, By: []string{"name"},
	}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d, Vars: map[string]Value{"metric": {All: true}}})
	if err != nil {
		t.Fatal(err)
	}
	names, values := metricValues(t, results[0], "name"), metricValues(t, results[0], "sum")
	got := map[string]any{}
	for i, name := range names {
		got[name.(string)] = values[i]
	}
	if len(got) != 2 || got["net"] != 10.0 || got["cpu"] != 100.0 {
		t.Fatalf("All groups=%v", got)
	}
}

func TestMetricTopNamesNeverCombineDifferentMetricsIntoOther(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		name  string
		value float64
	}{{"net", 10}, {"cpu", 100}, {"memory", 20}} {
		m := metricCounter(fixtureStart, p.value)
		m.Name = p.name
		m.Type, m.Monotonic, m.Temporality = "gauge", false, ""
		insertMetrics(t, db, m)
	}
	r := runMetricPanel(t, e, Panel{Viz: "timeseries", Options: &Options{Top: 1}, Query: &Query{
		From: "metrics", Measures: []string{"sum(value)"}, By: []string{"name"}, Bucket: "1m",
	}}, fixtureStart, fixtureStart.Add(time.Minute))
	names, values := metricValues(t, r, "name"), metricValues(t, r, "sum")
	if len(names) != 1 || names[0] != "cpu" || values[0] != 100.0 {
		t.Fatalf("names=%v values=%v", names, values)
	}
}
