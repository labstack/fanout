package query

import (
	"slices"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

// A negated term on the traces viz drops every trace with a span that matches
// the term; listing traces by any other span would keep the root the reader
// asked to exclude.
func TestTracesNegatedTermExcludesWholeTraces(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart.Add(10 * time.Minute)
	span := func(trace, id, parent, service string, ms float64, status string) telemetry.Span {
		n := at.UnixNano()
		return telemetry.Span{Namespace: "shop", ServiceName: service, TraceID: trace, SpanID: id, ParentSpanID: parent, Name: "GET /" + id, Kind: "SPAN_KIND_SERVER",
			StartUnixNanos: n, EndUnixNanos: n + int64(ms*1e6), DurationMS: ms, StatusCode: status, IngestedAt: n}
	}
	commit(t, repo, []telemetry.Span{
		// Rooted at the load generator, with a slower frontend child.
		span("mixed", "m1", "", "load-generator", 50, "STATUS_CODE_OK"),
		span("mixed", "m2", "m1", "frontend", 40, "STATUS_CODE_ERROR"),
		span("front", "f1", "", "frontend", 30, "STATUS_CODE_OK"),
		span("front", "f2", "f1", "cart", 10, "STATUS_CODE_ERROR"),
		span("load", "l1", "", "load-generator", 20, "STATUS_CODE_OK"),
	}, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, tc := range []struct {
		name, sort string
		where      []string
		want       []string
	}{
		{"negated equality", "", []string{"(service IS NULL OR service != 'load-generator')"}, []string{"front"}},
		{"negated call", "", []string{"NOT coalesce((contains(lower(service), lower('generator'))), FALSE)"}, []string{"front"}},
		{"with a positive term", "", []string{"status = 'STATUS_CODE_ERROR'", "(service IS NULL OR service != 'cart')"}, []string{"mixed"}},
		{"negated status", "", []string{"(status IS NULL OR status != 'STATUS_CODE_ERROR')"}, []string{"load"}},
		{"oldest first", "+start", []string{"(service IS NULL OR service != 'cart')"}, []string{"load", "mixed"}},
		// SQL's own != stays a span condition: a trace with any other span is listed.
		{"sql inequality", "", []string{"service != 'load-generator'"}, []string{"mixed", "front"}},
		// Only a whole negated entry excludes traces; a negation inside `or` stays per span.
		{"negation in or", "", []string{"(service = 'cart' OR (service IS NULL OR service != 'frontend'))"}, []string{"mixed", "front", "load"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Dashboard{Name: "Traces", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Traces", Viz: "traces", Query: &Spec{From: "spans", Where: tc.where, Sort: tc.sort}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			if got[0].Frame != nil {
				for _, v := range got[0].Frame.Values[0] {
					ids = append(ids, v.(string))
				}
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("traces %v, want %v (%+v)", ids, tc.want, got[0])
			}
		})
	}
}
