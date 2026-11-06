package query

import (
	"context"
	"fmt"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"reflect"
	"testing"
	"time"
)

func TestIndexedExemplarsRankTracesAndTruncate(t *testing.T) {
	duck, repo := newTestEngine(t)
	var spans []telemetry.Span
	for i := range 21 {
		n := fixtureStart.Add(time.Minute).UnixNano() + int64(i)
		status := "STATUS_CODE_OK"
		if i == 0 {
			status = "STATUS_CODE_ERROR"
		}
		spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: "checkout", TraceID: fmt.Sprintf("trace-%02d", i), SpanID: "root", Name: "root", StartUnixNanos: n, EndUnixNanos: n + int64(i+1)*1000000, DurationMS: float64(i + 1), StatusCode: status})
	}
	// Rank a long scoped trace ahead of a trace with one slower span.
	late := fixtureStart.Add(45 * time.Minute).UnixNano()
	spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: "checkout", TraceID: "trace-01", SpanID: "late", ParentSpanID: "root", StartUnixNanos: late, EndUnixNanos: late + 1000000, DurationMS: 1})
	// A much longer trace in another namespace and a different service must not win.
	for _, s := range []struct{ ns, service string }{{"other", "checkout"}, {"shop", "other"}} {
		id := s.ns + s.service
		if s.ns == "other" {
			id = "trace-20"
		}
		spans = append(spans, telemetry.Span{Namespace: s.ns, ServiceName: s.service, TraceID: id, SpanID: "root", StartUnixNanos: fixtureStart.UnixNano(), EndUnixNanos: fixtureStart.Add(time.Hour).UnixNano(), DurationMS: 3600000, StatusCode: "STATUS_CODE_ERROR"})
	}
	commit(t, repo, spans, nil)
	if _, err := duck.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	queries := 0
	probe := exemplarProbeEngine{Engine: duck, query: func(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
		queries++
		w, ok := queryrows.ReadWindow(ctx)
		if text != queryrows.CompletedTraceQuery || !ok || w.Kind != queryrows.TraceCandidateRead || w.Namespace != "shop" || w.Service != "checkout" || args[len(args)-1] != 21 {
			t.Fatalf("unbounded candidate read: %s window=%+v args=%v", text, w, args)
		}
		return duck.QueryContext(ctx, text, args...)
	}}
	e := NewExecutor(probe, 30)
	defer e.Close()
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := exemplarDashboard("spans")
	d.Variables = []Variable{{Name: "service", Kind: "custom", Options: []string{"checkout", "other"}, IncludeAll: true, Default: AllValue}}
	d.Panels[0].Query.Where = []string{"namespace = 'shop'", "service = $service"}
	req := exemplarRequest(d)
	req.Dimensions = map[string]string{"service": "checkout"}
	req.Vars = map[string]Value{"service": {All: true}}
	got, err := e.exemplars(t.Context(), req, e.now())
	if err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("candidate statements=%d", queries)
	}
	if len(got.Traces) != 20 || !got.Truncated || got.Traces[0].TraceID != "trace-00" || got.Traces[1].TraceID != "trace-01" || got.Traces[2].TraceID != "trace-20" {
		t.Fatalf("ranking/cap: %+v", got)
	}
	for _, trace := range got.Traces {
		if trace.Namespace != "shop" || trace.Service != "checkout" {
			t.Fatalf("scope escaped: %+v", trace)
		}
	}
	// Exactly 20 distinct traces is complete.
	req.From = fixtureStart.Add(time.Minute + time.Nanosecond)
	got, err = e.exemplars(t.Context(), req, e.now())
	if err != nil || len(got.Traces) != 20 || got.Truncated {
		t.Fatalf("exact cap: %+v %v", got, err)
	}
	req.Dashboard.Panels[0].Query.Where = append(req.Dashboard.Panels[0].Query.Where, "service = 'other'")
	got, err = e.exemplars(t.Context(), req, e.now())
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("conflict escaped: %+v %v", got, err)
	}
}

func TestIndexedExemplarsSeeTailCompactionAndRetirement(t *testing.T) {
	duck, repo := newTestEngine(t)
	// Fresh ingest stamps let the compactor select these small batches.
	at := time.Now().UTC().Add(-time.Minute)
	start, end := at.Add(-time.Minute), at.Add(time.Minute)
	for i := range 8 {
		n := at.UnixNano() + int64(i)*1000000
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("index-%d", i), Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", TraceID: "shared", SpanID: fmt.Sprint(i), ParentSpanID: "missing", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := duck.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The parentless root sorts after the children and is outside a Limit:1 page.
	n := at.Add(time.Second).UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "tail", Spans: []telemetry.Span{
		{Namespace: "shop", ServiceName: "frontend", TraceID: "shared", SpanID: "root", Name: "real root", StartUnixNanos: n, EndUnixNanos: n + 10000000, DurationMS: 10, IngestedAt: n},
		{Namespace: "shop", ServiceName: "checkout", TraceID: "new-error", SpanID: "root", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, StatusCode: "STATUS_CODE_ERROR", IngestedAt: n},
	}}); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(duck, 30)
	defer e.Close()
	d := Dashboard{Name: "Indexed", Time: Time{From: &start, To: &end}, Panels: []Panel{{ID: "p", Title: "Services", Viz: "service_map", Query: &Spec{From: "spans", Where: []string{"namespace = 'shop'"}}}}}
	req := ExemplarRequest{Dashboard: d, PanelID: "p", From: start, To: end, Dimensions: map[string]string{"service": "checkout"}}
	read := func() ExemplarResponse {
		t.Helper()
		got, err := e.exemplars(t.Context(), req, end)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	want := read()
	if len(want.Traces) != 2 || want.Traces[0].TraceID != "new-error" || want.Traces[1].Operation != "real root" || want.Traces[1].Service != "frontend" || want.Truncated {
		t.Fatalf("tail/root: %+v", want)
	}
	if _, err := duck.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := read(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cache changed results: %+v want %+v", got, want)
	}
	if n, err := repo.CompactParquet(t.Context(), duck, 8); err != nil || n != 8 {
		t.Fatalf("compact %d: %v", n, err)
	}
	if got := read(); !reflect.DeepEqual(got, want) {
		t.Fatalf("compaction changed results: %+v want %+v", got, want)
	}
	if _, err := duck.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := read(); !reflect.DeepEqual(got, want) {
		t.Fatalf("retired cache changed results: %+v", got)
	}
	if n, err := repo.PruneParquet(t.Context(), duck, end.Add(time.Minute).UnixNano(), 8); err != nil || n == 0 {
		t.Fatalf("prune %d: %v", n, err)
	}
	if got := read(); len(got.Traces) != 0 {
		t.Fatalf("retired traces visible: %+v", got)
	}
}

func TestExemplarIndexEligibility(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, tc := range []struct {
		where      []string
		dimensions map[string]string
		bucket     *SelectionBucket
		want       bool
	}{
		{nil, map[string]string{"service": "checkout"}, nil, true},
		{[]string{"service = 'checkout'"}, nil, nil, true},
		{[]string{"namespace = 'shop'"}, map[string]string{"service": "checkout"}, nil, true},
		{[]string{"status = 'STATUS_CODE_ERROR'"}, map[string]string{"service": "checkout"}, nil, false},
		{[]string{"lower(service) = 'checkout'"}, nil, nil, false},
		{nil, map[string]string{"service": "checkout"}, &SelectionBucket{Lower: 1}, false},
		{nil, map[string]string{"operation": "GET"}, nil, false},
		{nil, nil, nil, false},
	} {
		d := exemplarDashboard("spans")
		d.Panels[0].Query.Where = tc.where
		Normalize(&d)
		checked, err := e.check(t.Context(), &d)
		if err != nil {
			t.Fatal(err)
		}
		_, got, _, err := e.exemplarTraceScope(t.Context(), &d.Panels[0], checked.Filters["p"], Scope{}, tc.dimensions, tc.bucket)
		if err != nil || got != tc.want {
			t.Fatalf("where=%v dimensions=%v eligible=%v err=%v", tc.where, tc.dimensions, got, err)
		}
	}
}
