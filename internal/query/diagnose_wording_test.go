package query

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The Traces page lists traces of spans; a diagnosis that counts names spans
// throughout, groups digits, and spells stored enum values the way FanQL
// writes them.
func TestTracesDiagnosisWording(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Traces", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Traces", Viz: "traces",
		Query: &Spec{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'", "service = 'frontend'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	want := "No spans match this combination of filters in this time range. Without status error, 60 match the remaining filters. Values seen: ok (60)."
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
	if r := got[0]; r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "Without the duration filter,") {
		t.Fatalf("diagnosis %q", r.Diagnosis)
	}
}

// IDs are unique, so the most common ones say nothing: an ID filter lists no
// values seen. Other fields do, and an empty value is written as FanQL
// writes it.
func TestDiagnosisValuesSeen(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := shopSpans()
	for i := range spans {
		spans[i].HTTPRoute = ""
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Seen", Time: Time{Range: "1h"}}
	for i, where := range []string{"trace_id = 'nope'", "span_id = 'nope'", "http_route = '/missing'"} {
		d.Panels = append(d.Panels, Panel{ID: fmt.Sprint("p", i), Title: "Spans", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{where}}})
	}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got[:2] {
		if r.Status != StatusEmpty || strings.Contains(r.Diagnosis, "Values seen") {
			t.Errorf("%+v", r)
		}
	}
	if r := got[2]; r.Status != StatusEmpty || !strings.HasSuffix(r.Diagnosis, ` Values seen: "" (180).`) {
		t.Errorf("%+v", r)
	}
}

// A count of nothing is a real 0 on every viz, with the diagnosis as a hint:
// a stat answers as a table does. A grouped count with no rows has no row
// to show and stays empty.
func TestZeroCountIsZeroWithHint(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Zero", Time: Time{Range: "1h"}}
	for _, viz := range []string{"stat", "gauge", "table"} {
		d.Panels = append(d.Panels, Panel{ID: viz, Title: "Spans", Viz: viz, Query: &Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"service = 'nope'"}}})
	}
	lo, hi := 0.0, 100.0
	d.Panels[1].Min, d.Panels[1].Max = &lo, &hi
	d.Panels = append(d.Panels,
		Panel{ID: "grouped", Title: "Spans", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{"service = 'nope'"}}},
		Panel{ID: "found", Title: "Spans", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"service = 'frontend'"}}})
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	const hint = "No spans for service nope in this time range."
	for _, r := range got[:2] {
		if r.Status != StatusOK || !strings.HasPrefix(r.Diagnosis, hint) || r.Frame == nil || len(r.Frame.Totals) != 2 || r.Frame.Totals[1] != 0.0 {
			t.Errorf("%s: %+v", r.ID, r)
		}
	}
	if r := got[2]; r.Status != StatusOK || !strings.HasPrefix(r.Diagnosis, hint) || r.Frame.Rows != 1 || r.Frame.Values[0][0] != 0.0 {
		t.Errorf("table: %+v", r)
	}
	if r := got[3]; r.Status != StatusEmpty || !strings.HasPrefix(r.Diagnosis, hint) {
		t.Errorf("grouped: %+v", r)
	}
	if r := got[4]; r.Status != StatusOK || r.Diagnosis != "" {
		t.Errorf("found: %+v", r)
	}
}
