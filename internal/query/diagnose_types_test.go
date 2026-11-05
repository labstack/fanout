package query

import (
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
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

// A standard key reads typed, so no other form of the literal matches more
// rows and the diagnosis offers none.
func TestDiagnosisOffersNoTypeFormForStandardKeys(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	var spans []telemetry.Span
	for i := range 4 {
		id := string(rune('a' + i))
		spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: "frontend-proxy", TraceID: id, SpanID: id, Name: "GET", Kind: "SPAN_KIND_SERVER",
			StartUnixNanos: n + int64(i), EndUnixNanos: n + int64(i) + 1e6, DurationMS: 1, StatusCode: "STATUS_CODE_OK", Attributes: map[string]any{"http.route": "500", "http.response.status_code": "200"}, IngestedAt: n})
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, filter := range []string{"attributes['http.route'] = 500", "attributes['http.response.status_code'] = 500"} {
		d := Dashboard{Name: "Types", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "a", Title: "A", Viz: "table",
			Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{filter}}}}}
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil {
			t.Fatal(err)
		}
		if r := got[0]; strings.Contains(r.Diagnosis, "quote the value") || strings.Contains(r.Diagnosis, "without quotes") {
			t.Fatalf("%s: diagnosis = %q", filter, r.Diagnosis)
		}
	}
}

// A misspelled metric name is answered with the nearest name in the window.
func TestDiagnosisSuggestsNearestMetricName(t *testing.T) {
	engine, repo := newTestEngine(t)
	var metrics []telemetry.Metric
	for i, name := range []string{"container.cpu.utilization", "container.memory.usage", "process.cpu.time"} {
		n := fixtureStart.Add(time.Duration(i) * time.Minute).UnixNano()
		metrics = append(metrics, telemetry.Metric{Namespace: "shop", ServiceName: "cart", Name: name, Type: "gauge", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, Value: 1})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "names", Metrics: metricSeriesIDs(metrics)}); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Names", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "a", Title: "A", Viz: "table",
		Query: &Spec{From: "metrics", Measures: []string{"avg(value)"}, By: []string{"service"}, Where: []string{"name = 'container.cpu.utilizaton'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if r := got[0]; r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "Did you mean container.cpu.utilization?") {
		t.Fatalf("diagnosis = %q", r.Diagnosis)
	}
}

// Near misses are for names: a number or a short literal is no typo.
func TestDiagnosisSuggestsOnlyNamedNearMisses(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	var spans []telemetry.Span
	for i, svc := range []string{"ab", "abc"} {
		id := string(rune('a' + i))
		spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: svc, TraceID: id, SpanID: id, Name: "GET", Kind: "SPAN_KIND_SERVER",
			StartUnixNanos: n + int64(i), EndUnixNanos: n + int64(i) + 1e6, DurationMS: 1, StatusCode: "STATUS_CODE_OK", Attributes: map[string]any{"http.response.status_code": int64(500)}, IngestedAt: n})
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, where := range []string{"attributes['http.response.status_code'] = 503", "service = 'a'", "service = 'abd'"} {
		d := Dashboard{Name: "Near", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "a", Title: "A", Viz: "table",
			Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{where}}}}}
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil {
			t.Fatal(err)
		}
		suggests := strings.Contains(got[0].Diagnosis, "Did you mean")
		if want := where == "service = 'abd'"; suggests != want {
			t.Errorf("%s: %q", where, got[0].Diagnosis)
		}
	}
}
