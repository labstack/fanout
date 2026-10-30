package query

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestQueryShapeMatchesValidateQueryProblems(t *testing.T) {
	queries := []struct {
		viz string
		q   Spec
	}{
		{"stat", Spec{From: "spans", Measures: []string{"count()"}}},
		{"gauge", Spec{From: "spans", Measures: []string{"count()", "rate()"}}},
		{"timeseries", Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}},
		{"bar", Spec{From: "spans", Measures: []string{"count()"}}},
		{"table", Spec{From: "spans", Measures: []string{"count()"}, Sort: "wrong"}},
		{"heatmap", Spec{From: "spans", Measures: []string{"count()"}, Bucket: "auto"}},
		{"histogram", Spec{From: "spans", Measures: []string{"count()"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}},
		{"scatter", Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}},
		{"state_timeline", Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Bucket: "auto", Sort: "count"}},
		{"logs", Spec{From: "spans", Measures: []string{"count()"}, Sort: "wrong"}},
		{"log_patterns", Spec{From: "logs", Measures: []string{"count()"}, By: []string{"service"}}},
		{"traces", Spec{From: "spans", Sort: "+start", Where: []string{"service = 'api'"}}},
	}
	for _, tc := range queries {
		t.Run(tc.viz, func(t *testing.T) {
			p := Panel{ID: "test", Title: "test", Viz: tc.viz, Query: &tc.q}
			d := Dashboard{Name: "test", Panels: []Panel{p}}
			original := tc.q
			Normalize(&d)
			// Shape never supplies defaults; compare against the original query.
			tc.q = original
			d.Panels[0].Query = &tc.q
			var want Problems
			for _, p := range Validate(&d) {
				if strings.HasPrefix(p.Path, "panels[0].query.") {
					p.Path = strings.TrimPrefix(p.Path, "panels[0].query.")
					want = append(want, p)
				}
			}
			got, _ := Shape(&tc.q, tc.viz, nil)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("shape = %#v; validate = %#v", got, want)
			}
		})
	}
}

func TestQueryShapeRequires(t *testing.T) {
	q := &Spec{From: "spans", Measures: []string{"count()"}}
	for _, tc := range []struct {
		viz  string
		want []VizRequirement
	}{{"gauge", []VizRequirement{RequiresMinMax}}, {"state_timeline", []VizRequirement{RequiresThresholds}}, {"table", []VizRequirement{}}} {
		_, got := Shape(q, tc.viz, nil)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s requires %v, want %v", tc.viz, got, tc.want)
		}
	}
}

func TestQueryShapeRejectsIncompleteMeasure(t *testing.T) {
	q := &Spec{From: "spans", Measures: []string{"count() + as x"}}
	got, _ := Shape(q, "table", nil)
	if len(got) == 0 {
		t.Fatal("shape accepted an incomplete arithmetic expression")
	}
	d := Dashboard{Name: "test", Panels: []Panel{{ID: "t", Title: "t", Viz: "table", Query: q}}}
	var want Problems
	for _, p := range Validate(&d) {
		if strings.HasPrefix(p.Path, "panels[0].query.") {
			p.Path = strings.TrimPrefix(p.Path, "panels[0].query.")
			want = append(want, p)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shape = %+v; validate = %+v", got, want)
	}
}

func TestQueryShapeCountIfDefaultAlias(t *testing.T) {
	q := &Spec{From: "spans", Measures: []string{"count_if(status = 'STATUS_CODE_ERROR')"}, Sort: "count_if"}
	if problems, _ := Shape(q, "table", nil); len(problems) != 0 {
		t.Fatalf("count_if shape: %v", problems)
	}
}

func TestCompatibleVizzesOnQueryAsWritten(t *testing.T) {
	for _, tc := range []struct {
		q    Spec
		want []string
	}{
		{Spec{From: "spans", Where: []string{"kind = 'SPAN_KIND_SERVER'"}, Measures: []string{"count()"}, By: []string{"http_route"}}, []string{"bar", "table"}},
		{Spec{From: "spans", Where: []string{"kind = 'SPAN_KIND_SERVER'"}, Measures: []string{"count()"}, By: []string{"http_route"}, Bucket: "auto"}, []string{"timeseries", "state_timeline"}},
		{Spec{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'"}}, []string{"traces"}},
		{Spec{From: "logs"}, []string{"logs"}},
		{Spec{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}}, []string{"bar", "table", "log_patterns"}},
	} {
		var got []string
		for _, c := range CompatibleVizzes(&tc.q, nil) {
			got = append(got, c.Viz)
			if c.Requires == nil {
				t.Errorf("%s has null requirements", c.Viz)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%+v compatible = %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestExistingValidateBehaviourUnchanged(t *testing.T) {
	q := &Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"service = $missing"}}
	got, _ := Shape(q, "table", map[string]Variable{})
	if len(got) != 1 || got[0].Path != "where" || !strings.Contains(got[0].Message, "$missing is not a dashboard variable") {
		t.Fatalf("variables = %#v", got)
	}
	if got, _ := Shape(q, "table", nil); len(got) != 0 {
		t.Fatalf("nil declarations: %v", got)
	}
}
