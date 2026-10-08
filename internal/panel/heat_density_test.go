package panel

import (
	"strings"
	"testing"
	"time"
)

func TestH1WidthAwareCellBuckets(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Density", Time: Time{Range: "24h"}, Panels: []Panel{
		{ID: "heat", Title: "Heat", Viz: "heatmap", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto", Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}},
		{ID: "state", Title: "State", Viz: "state_timeline", Thresholds: []Threshold{{Value: 5, Status: "bad"}}, Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Bucket: "auto"}},
		{ID: "line", Title: "Line", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto"}},
	}}
	fixed := d.Panels[0]
	fixed.ID = "fixed"
	q := *fixed.Query
	q.Bucket = "5m"
	fixed.Query = &q
	d.Panels = append(d.Panels, fixed)
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d, Widths: map[string]int{"heat": 800, "state": 800, "line": 800, "fixed": 800}})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(results)
	for _, id := range []string{"heat", "state"} {
		r := got[id]
		interval, err := time.ParseDuration(r.Interval)
		if err != nil || r.Status != StatusOK || interval <= 0 {
			t.Fatalf("%s: %+v / %v", id, r, err)
		}
		columns := 24*time.Hour/interval + 1
		if columns < 60 || columns > 90 {
			t.Errorf("%s at 800px: %d columns, want 60–90 (%s)", id, columns, r.Interval)
		}
		if !strings.Contains(r.SQL, "INTERVAL '1200 seconds'") {
			t.Errorf("%s did not execute 20m grouping: %s", id, r.SQL)
		}
	}
	if got["line"].Interval != "10m" || got["fixed"].Interval != "5m" {
		t.Errorf("line/fixed buckets changed: %s / %s", got["line"].Interval, got["fixed"].Interval)
	}
	for _, id := range []string{"heat", "state"} {
		results, err = e.Run(t.Context(), RunRequest{Dashboard: d, Panels: []string{id}, Widths: map[string]int{id: 400}})
		if err != nil || results[0].Status != StatusOK {
			t.Fatalf("narrow %s: %+v / %v", id, results, err)
		}
		interval, err := time.ParseDuration(results[0].Interval)
		if err != nil || interval <= 20*time.Minute {
			t.Errorf("narrow %s was not coarser: %s / %v", id, results[0].Interval, err)
		}
	}
}
