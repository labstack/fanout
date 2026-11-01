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
		d.Panels = append(d.Panels, Panel{ID: fmt.Sprint("p", i), Title: "Spans", Viz: "stat", Query: &Spec{From: "spans", Measures: []string{"count()"}, Where: []string{where}}})
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

// A count of nothing is an empty panel on every viz: a table answers as a
// stat does, with the diagnosis, rather than with a bare zero.
func TestZeroCountIsEmptyOnEveryViz(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Zero", Time: Time{Range: "1h"}}
	for _, viz := range []string{"stat", "table"} {
		d.Panels = append(d.Panels, Panel{ID: viz, Title: "Spans", Viz: viz, Query: &Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"service = 'nope'"}}})
	}
	d.Panels = append(d.Panels, Panel{ID: "found", Title: "Spans", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"service = 'frontend'"}}})
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got[:2] {
		if r.Status != StatusEmpty || !strings.HasPrefix(r.Diagnosis, "No spans for service nope in this time range.") {
			t.Errorf("%s: %+v", r.ID, r)
		}
	}
	if r := got[2]; r.Status != StatusOK {
		t.Errorf("%+v", r)
	}
}
