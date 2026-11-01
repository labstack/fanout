package query

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The Traces page lists traces, so its diagnosis names traces, counts spans
// with digit grouping, and spells stored enum values the way FanQL writes them.
func TestTracesDiagnosisWording(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Traces", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Traces", Viz: "traces",
		Query: &Spec{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'", "service = 'frontend'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	want := "No traces match this combination of filters in this time range. Without status error, 60 spans match the remaining filters. Values seen: ok (60)."
	if r := got[0]; r.Status != StatusEmpty || r.Diagnosis != want {
		t.Fatalf("diagnosis %q, want %q", r.Diagnosis, want)
	}
	if got := grouped(1234567); got != "1,234,567" {
		t.Fatalf("grouped = %q", got)
	}
}

// A diagnosis that runs out of time must not claim the range has no data.
func TestDiagnosisWithoutTimeStaysNeutral(t *testing.T) {
	e := newFixtureExecutor(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}
	for _, tc := range []struct {
		panel   Panel
		filters []Filter
		want    string
	}{
		{Panel{Viz: "logs", Query: &Spec{From: "logs"}}, []Filter{{Expr: "contains(lower(body), lower('zzqq'))"}}, "No logs match these filters in this time range."},
		{Panel{Viz: "traces", Query: &Spec{From: "spans"}}, []Filter{{Expr: "service = 'x'"}}, "No traces match these filters in this time range."},
		{Panel{Viz: "logs", Query: &Spec{From: "logs"}}, nil, "No logs match this query in this time range."},
	} {
		if got := e.diagnose(ctx, &tc.panel, tc.filters, scope); got != tc.want {
			t.Errorf("diagnosis %q, want %q", got, tc.want)
		}
	}
}

// A diagnosis names fields as FanQL spells them: duration, not duration ms.
func TestDiagnosisNamesDurationAsFanQL(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Duration", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Slow", Viz: "table",
		Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{"service = 'frontend'", "duration_ms > 9999000"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if r := got[0]; r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "Without duration filter,") {
		t.Fatalf("diagnosis %q", r.Diagnosis)
	}
}
