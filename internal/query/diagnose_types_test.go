package query

import (
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

// Attribute equality is typed: a number filter misses values stored as text.
// The diagnosis says so and keeps the dotted key readable.
func TestDiagnosisNamesAttributeTypeMismatch(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	var spans []telemetry.Span
	for i := range 4 {
		id := string(rune('a' + i))
		spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: "frontend-proxy", TraceID: id, SpanID: id, Name: "GET", Kind: "SPAN_KIND_SERVER",
			StartUnixNanos: n + int64(i), EndUnixNanos: n + int64(i) + 1e6, DurationMS: 1, StatusCode: "STATUS_CODE_OK", Attributes: map[string]any{"http.status_code": "500"}, IngestedAt: n})
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Types", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "a", Title: "A", Viz: "table",
		Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{"attributes['http.status_code'] = 500"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	r := got[0]
	if r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "No spans for attribute http.status_code 500") ||
		!strings.Contains(r.Diagnosis, `4 spans store attribute http.status_code 500 as text; quote the value, "500", to match them.`) {
		t.Fatalf("diagnosis = %q", r.Diagnosis)
	}
}

// A quoted filter on a number attribute is told to drop the quotes.
func TestDiagnosisTellsQuotedFilterToMatchNumbers(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	var spans []telemetry.Span
	for i := range 3 {
		id := string(rune('a' + i))
		spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: "frontend-proxy", TraceID: id, SpanID: id, Name: "GET", Kind: "SPAN_KIND_SERVER",
			StartUnixNanos: n + int64(i), EndUnixNanos: n + int64(i) + 1e6, DurationMS: 1, StatusCode: "STATUS_CODE_OK", Attributes: map[string]any{"http.status_code": int64(308)}, IngestedAt: n})
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Types", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "a", Title: "A", Viz: "table",
		Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{"attributes['http.status_code'] = '308'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if r := got[0]; r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "3 spans store attribute http.status_code 308 as a number; write the value without quotes, 308, to match them.") {
		t.Fatalf("diagnosis = %q", r.Diagnosis)
	}
}
