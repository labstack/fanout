package panel

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestM2ItemsExecute(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = []Panel{
		{ID: "scatter", Title: "Mean and tail latency", Viz: "scatter", Query: &Query{From: "spans", Measures: []string{"avg(duration_ms)", "p95(duration_ms)"}, By: []string{"http_route", "service"}}, Options: &Options{XScale: "log", YScale: "linear"}},
		{ID: "states", Title: "Latency states", Viz: "state_timeline", Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Bucket: "5m"}, Thresholds: []Threshold{{Value: 500, Status: "bad"}}},
	}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != "ok" || got[0].Frame.Rows != 3 || len(got[0].Frame.Columns) != 4 {
		t.Fatalf("scatter: %+v", got[0])
	}
	if got[1].Status != "ok" || got[1].Frame.Rows != 24 || got[1].Better != "lower" {
		t.Fatalf("timeline: %+v", got[1])
	}
}
func TestM2TimelineBudget(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := []telemetry.Span{}
	for m := range 1440 {
		for s := range 20 {
			n := fixtureStart.Add(time.Duration(m) * time.Minute).UnixNano()
			spans = append(spans, telemetry.Span{TraceID: fmt.Sprint(m, s), SpanID: "s", ServiceName: fmt.Sprint(s), StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(24 * time.Hour) }
	d := Dashboard{Name: "States", Time: Time{Range: "24h"}, Panels: []Panel{{ID: "states", Title: "States", Viz: "state_timeline", Options: &Options{Top: 6}, Thresholds: []Threshold{{Value: 5, Status: "bad"}}, Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Bucket: "1m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if f == nil || !f.Truncated || f.Rows*len(f.Columns) > analysisCellLimit {
		t.Fatalf("budget: %+v", got)
	}
	for _, v := range f.Values[1] {
		if v == "Other" {
			t.Fatal("item identities merged")
		}
	}
	original := d.Panels[0]
	d.Panels = nil
	for i := 0; i < 11; i++ {
		copy := original
		copy.ID = fmt.Sprintf("bounded_%d", i)
		d.Panels = append(d.Panels, copy)
	}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	cells := 0
	for _, r := range got {
		if r.Frame != nil {
			cells += r.Frame.Rows * len(r.Frame.Columns)
		}
	}
	if cells > 200000 {
		t.Fatalf("batch has %d cells", cells)
	}

}
func TestM2ItemsValidation(t *testing.T) {
	mixed := Panel{ID: "s", Title: "S", Viz: "scatter", XUnit: "count", Unit: "ms", Query: &Query{From: "spans", Measures: []string{"count()", "p95(duration_ms)"}, By: []string{"service"}}}
	d := Dashboard{Name: "Mixed", Panels: []Panel{mixed}}
	Normalize(&d)
	if got := Validate(&d); len(got) != 0 {
		t.Fatalf("mixed-axis scatter rejected: %+v", got)
	}
	cases := []struct {
		panel Panel
		want  Problem
	}{
		{Panel{ID: "s", Title: "S", Viz: "state_timeline", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}, Problem{Path: "panels[0].thresholds", Message: "state_timeline requires thresholds", Hint: "set a warn or bad boundary for the selected measure"}},
		{Panel{ID: "s", Title: "S", Viz: "scatter", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}, Problem{Path: "panels[0].query.measures", Message: "scatter requires exactly two measures", Hint: "select x and y measures in that order"}},
		{Panel{ID: "s", Title: "S", Viz: "bar", XUnit: "count", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}, Problem{Path: "panels[0].x_unit", Message: "x_unit applies only to scatter", Hint: "remove x_unit or use scatter"}},
	}
	unknown := mixed
	unknown.XUnit = "rows"
	cases = append(cases, struct {
		panel Panel
		want  Problem
	}{unknown, Problem{Path: "panels[0].x_unit", Message: "unknown x unit", Hint: "use one of " + strings.Join(unitNames(), ", ")}})
	for _, tc := range cases {
		d := Dashboard{Name: "Rule", Panels: []Panel{tc.panel}}
		Normalize(&d)
		got := Validate(&d)
		if !slices.Contains(got, tc.want) {
			t.Fatalf("got %+v want %+v", got, tc.want)
		}
	}
}
func TestM2TimelineSharePartitionsByBucket(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Share", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "state_timeline", Thresholds: []Threshold{{Value: 50, Status: "warn"}}, Query: &Query{From: "spans", Measures: []string{"share()"}, By: []string{"service"}, Bucket: "5m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame == nil {
		t.Fatalf("share: %+v %v", got, err)
	}
	sums := map[any]float64{}
	f := got[0].Frame
	for i, bucket := range f.Values[0] {
		sums[bucket] += f.Values[2][i].(float64)
	}
	for bucket, sum := range sums {
		if math.Abs(sum-100) > 1e-9 {
			t.Fatalf("bucket %v share=%v", bucket, sum)
		}
	}
}

func TestM2TimelineShareBeforeTopN(t *testing.T) {
	engine, repo := newTestEngine(t)
	const buckets = 3
	var spans []telemetry.Span
	for bucket := range buckets {
		for service := range 12 {
			for event := range 12 - service {
				n := fixtureStart.Add(time.Duration(bucket) * 5 * time.Minute).UnixNano()
				spans = append(spans, telemetry.Span{TraceID: fmt.Sprintf("%d_%d_%d", bucket, service, event), SpanID: "s", ServiceName: fmt.Sprintf("svc%02d", service), StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n})
			}
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Share", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "state_timeline", Thresholds: []Threshold{{Value: 50, Status: "warn"}}, Query: &Query{From: "spans", Measures: []string{"share()"}, By: []string{"service"}, Bucket: "5m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("share: %+v %v", got, err)
	}
	f := got[0].Frame
	if f.Rows != buckets*8 {
		t.Fatalf("rows = %d, want %d", f.Rows, buckets*8)
	}
	sums := map[any]float64{}
	counts := map[any]int{}
	for i, bucket := range f.Values[0] {
		sums[bucket] += f.Values[2][i].(float64)
		counts[bucket]++
		if service := f.Values[1][i].(string); service >= "svc08" {
			t.Errorf("non-top service returned: %s", service)
		}
	}
	if len(sums) != buckets {
		t.Fatalf("buckets = %d, want %d", len(sums), buckets)
	}
	for bucket, sum := range sums {
		if counts[bucket] != 8 || sum >= 100 || math.Abs(sum-100.0*68/78) > 1e-9 {
			t.Errorf("bucket %v: rows=%d share=%v, want 8 rows and %v", bucket, counts[bucket], sum, 100.0*68/78)
		}
	}
}

func TestM2ItemsUnitNormalization(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{ServiceName: "svc", SeverityNumber: 9, EventUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, tc := range []struct {
		name, x, y, wantX, wantY string
	}{
		{"inferred", "", "", "", ""},
		{"explicit_none", "none", "none", "none", "none"},
		{"explicit_units", "count", "count", "count", "count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Dashboard{Name: "Units", Panels: []Panel{{ID: "s", Title: "S", Viz: "scatter", XUnit: tc.x, Unit: tc.y, Query: &Query{From: "logs", Measures: []string{"avg(severity_number)", "max(severity_number)"}, By: []string{"service"}}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || got[0].Status != StatusOK {
				t.Fatalf("scatter: %+v %v", got, err)
			}
			cols := got[0].Frame.Columns
			if cols[1].Unit != tc.wantX || cols[2].Unit != tc.wantY {
				t.Errorf("units = %+v, want x=%q y=%q", cols, tc.wantX, tc.wantY)
			}
		})
	}
	for _, unit := range []string{"", "none", "count"} {
		t.Run("timeline/"+unit, func(t *testing.T) {
			d := Dashboard{Name: "Units", Panels: []Panel{{ID: "s", Title: "S", Viz: "state_timeline", Unit: unit, Thresholds: []Threshold{{Value: 10, Status: "warn"}}, Query: &Query{From: "logs", Measures: []string{"avg(severity_number)"}, By: []string{"service"}, Bucket: "5m"}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || got[0].Status != StatusOK {
				t.Fatalf("timeline: %+v %v", got, err)
			}
			if got := got[0].Frame.Columns[2].Unit; got != unit {
				t.Errorf("unit = %q, want %q", got, unit)
			}
		})
	}
}

func TestM2ItemsScatterAxisUnits(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, tc := range []struct {
		name, x, y, wantX, wantY string
	}{
		{"inferred", "", "", "count", "ms"},
		{"x_override", "none", "", "none", "ms"},
		{"y_override", "", "ns", "count", "ns"},
		{"both_overrides", "none", "ns", "none", "ns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Dashboard{Name: "Axes", Panels: []Panel{{ID: "s", Title: "S", Viz: "scatter", XUnit: tc.x, Unit: tc.y, Query: &Query{From: "spans", Measures: []string{"count()", "p95(duration_ms)"}, By: []string{"service"}}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || got[0].Status != StatusOK {
				t.Fatalf("scatter: %+v %v", got, err)
			}
			cols := got[0].Frame.Columns
			if cols[1].Name != "count" || cols[1].Unit != tc.wantX || cols[2].Name != "p95" || cols[2].Unit != tc.wantY {
				t.Fatalf("axes: %+v", cols)
			}
		})
	}
}

func TestM2ItemsOptionsValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		panel Panel
		paths []string
	}{
		{"invalid_scales", Panel{Viz: "scatter", Options: &Options{XScale: "sqrt", YScale: "sqrt"}, Query: &Query{From: "spans", Measures: []string{"count()", "rate()"}, By: []string{"service"}}}, []string{"options.x_scale", "options.y_scale"}},
		{"bar_scales", Panel{Viz: "bar", Options: &Options{XScale: "log", YScale: "linear"}, Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}, []string{"options.x_scale", "options.y_scale"}},
		{"text_axes", Panel{Viz: "text", Content: "Notes", XUnit: "count", Options: &Options{XScale: "log", YScale: "linear"}}, []string{"x_unit", "options.x_scale", "options.y_scale"}},
		{"timeline_style_and_sort", Panel{Viz: "state_timeline", Options: &Options{Style: "stacked"}, Thresholds: []Threshold{{Value: 5, Status: "bad"}}, Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Sort: "p95"}}, []string{"options.style", "query.sort"}},
		{"sql_scatter", Panel{Viz: "scatter", SQL: "SELECT service, count(*) FROM spans WHERE $__window(start_time) GROUP BY 1"}, []string{"query"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.panel.ID, tc.panel.Title = "p", "P"
			d := Dashboard{Name: "Validation", Panels: []Panel{tc.panel}}
			Normalize(&d)
			got := Validate(&d)
			for _, path := range tc.paths {
				if !slices.ContainsFunc(got, func(p Problem) bool { return p.Path == "panels[0]."+path }) {
					t.Errorf("missing %s: %+v", path, got)
				}
			}
		})
	}
}

func TestM2ItemsLogsUseRedactedSource(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{Body: "token=bodysecret failed", BodyTemplate: "token=templatesecret failed", EventUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, viz := range []string{"scatter", "state_timeline"} {
		for _, field := range []string{"body", "body_template"} {
			t.Run(viz+"/"+field, func(t *testing.T) {
				p := Panel{ID: "p", Title: "P", Viz: viz, Query: &Query{From: "logs", Measures: []string{"count()", "rate()"}, By: []string{field}}}
				if viz == "state_timeline" {
					p.Query.Measures = []string{"count()"}
					p.Query.Bucket = "5m"
					p.Thresholds = []Threshold{{Value: 5, Status: "bad"}}
				}
				d := Dashboard{Name: "Logs", Panels: []Panel{p}}
				got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
				if err != nil || got[0].Status != StatusOK {
					t.Fatalf("logs: %+v %v", got, err)
				}
				data, err := json.Marshal(got)
				if err != nil || strings.Contains(string(data), "bodysecret") || strings.Contains(string(data), "templatesecret") || !strings.Contains(string(data), "[REDACTED]") {
					t.Fatalf("redaction: %s %v", data, err)
				}
				p.Query.Where = []string{field + " LIKE '%secret%'"}
				got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
				if err != nil || got[0].Status != StatusEmpty {
					t.Fatalf("raw search matched: %+v %v", got, err)
				}
			})
		}
	}
}

func TestM2TimelinePreservesUnknownBuckets(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, []telemetry.Span{{TraceID: "t", SpanID: "s", ServiceName: "svc", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n}}, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Unknown", Panels: []Panel{{ID: "s", Title: "S", Viz: "state_timeline", Thresholds: []Threshold{{Value: 5, Status: "bad"}}, Query: &Query{From: "spans", Measures: []string{"avg(attributes['missing'])"}, By: []string{"service"}, Bucket: "5m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("timeline: %+v %v", got, err)
	}
	f := got[0].Frame
	if f.Rows != 1 || f.Values[0][0] != fixtureStart.UnixMilli() || f.Values[2][0] != nil {
		t.Fatalf("null or absent bucket became healthy: %+v", f)
	}
}

func TestM2TimelineBudgetKeepsNewestCompleteBuckets(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "States", Panels: []Panel{{ID: "s", Title: "S", Viz: "state_timeline", Thresholds: []Threshold{{Value: 500, Status: "bad"}}, Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Bucket: "5m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("timeline: %+v %v", got, err)
	}
	// Leave nine response cells: only one complete two-item bucket fits.
	filler := newFrame(make([]Column, 200))
	filler.Rows = 999
	for c := range filler.Values {
		filler.Values[c] = make([]any, filler.Rows)
	}
	filler.Totals = make([]any, 191)
	results := []Result{{Frame: filler}, got[0]}
	limitBatchFrames(results)
	f := results[1].Frame
	latest := fixtureStart.Add(55 * time.Minute).UnixMilli()
	if !f.Truncated || f.Rows != 2 || f.Values[0][0] != latest || f.Values[0][1] != latest {
		t.Fatalf("budget dropped newest data or split an item bucket: %+v", f)
	}
	d.Panels[0].Query.Limit = 5
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("limited timeline: %+v %v", got, err)
	}
	f = got[0].Frame
	if !f.Truncated || f.Rows != 4 || f.Values[0][0] != fixtureStart.Add(50*time.Minute).UnixMilli() || f.Values[0][3] != latest {
		t.Fatalf("row limit did not preserve complete newest buckets: %+v", f)
	}
}
