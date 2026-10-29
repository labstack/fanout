package panel

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestQueryShapeMatchesValidateQueryProblems(t *testing.T) {
	queries := []struct {
		viz string
		q   Query
	}{
		{"stat", Query{From: "spans", Measures: []string{"count()"}}},
		{"gauge", Query{From: "spans", Measures: []string{"count()", "rate()"}}},
		{"timeseries", Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}},
		{"bar", Query{From: "spans", Measures: []string{"count()"}}},
		{"table", Query{From: "spans", Measures: []string{"count()"}, Sort: "wrong"}},
		{"heatmap", Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto"}},
		{"histogram", Query{From: "spans", Measures: []string{"count()"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}},
		{"scatter", Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}},
		{"state_timeline", Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Bucket: "auto", Sort: "count"}},
		{"logs", Query{From: "spans", Measures: []string{"count()"}, Sort: "wrong"}},
		{"log_patterns", Query{From: "logs", Measures: []string{"count()"}, By: []string{"service"}}},
		{"traces", Query{From: "spans", Sort: "+start", Where: []string{"service = 'api'"}}},
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
			got, _ := QueryShape(&tc.q, tc.viz, nil)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("shape = %#v; validate = %#v", got, want)
			}
		})
	}
}

func TestQueryShapeRequires(t *testing.T) {
	q := &Query{From: "spans", Measures: []string{"count()"}}
	for _, tc := range []struct {
		viz  string
		want []VizRequirement
	}{{"gauge", []VizRequirement{RequiresMinMax}}, {"state_timeline", []VizRequirement{RequiresThresholds}}, {"table", []VizRequirement{}}} {
		_, got := QueryShape(q, tc.viz, nil)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s requires %v, want %v", tc.viz, got, tc.want)
		}
	}
}

func TestQueryShapeCountIfDefaultAlias(t *testing.T) {
	q := &Query{From: "spans", Measures: []string{"count_if(status = 'STATUS_CODE_ERROR')"}, Sort: "count_if"}
	if problems, _ := QueryShape(q, "table", nil); len(problems) != 0 {
		t.Fatalf("count_if shape: %v", problems)
	}
}

func TestCompatibleVizzesOnQueryAsWritten(t *testing.T) {
	for _, tc := range []struct {
		q    Query
		want []string
	}{
		{Query{From: "spans", Where: []string{"kind = 'SPAN_KIND_SERVER'"}, Measures: []string{"count()"}, By: []string{"http_route"}}, []string{"bar", "table"}},
		{Query{From: "spans", Where: []string{"kind = 'SPAN_KIND_SERVER'"}, Measures: []string{"count()"}, By: []string{"http_route"}, Bucket: "auto"}, []string{"timeseries", "state_timeline"}},
		{Query{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'"}}, []string{"traces"}},
		{Query{From: "logs"}, []string{"logs"}},
		{Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}}, []string{"bar", "table", "log_patterns"}},
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
	q := &Query{From: "spans", Measures: []string{"count()"}, Where: []string{"service = $missing"}}
	got, _ := QueryShape(q, "table", map[string]Variable{})
	if len(got) != 1 || got[0].Path != "where" || !strings.Contains(got[0].Message, "$missing is not a dashboard variable") {
		t.Fatalf("variables = %#v", got)
	}
	if got, _ := QueryShape(q, "table", nil); len(got) != 0 {
		t.Fatalf("nil declarations: %v", got)
	}
}
