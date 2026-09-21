package observability

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// A paged trace must still describe the trace. Limit bounds the spans that are
// returned; it must not silently redefine what the trace contains. Reporting a
// page's service list and span count as the trace's own is the failure this
// test exists to prevent: an agent reading "1 spans across 1 services" for a
// trace whose fault lies in a service the page omitted concludes the wrong
// service is broken, and the output gives it no reason to doubt that.
func TestTracePagedSpansStillDescribeTheWholeTrace(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "paged-trace", Spans: []telemetry.Span{
		{
			Namespace: "prod", TraceID: "wide-trace", SpanID: "root", ServiceName: "cart",
			Name: "ResolveBoolean", Kind: "CLIENT",
			StartUnixNanos: start.UnixNano(), DurationMS: 2, StatusCode: "OK",
		},
		{
			Namespace: "prod", TraceID: "wide-trace", SpanID: "child", ParentSpanID: "root", ServiceName: "cart",
			Name: "POST", Kind: "CLIENT",
			StartUnixNanos: start.Add(time.Millisecond).UnixNano(), DurationMS: 1, StatusCode: "OK",
		},
		{
			Namespace: "prod", TraceID: "wide-trace", SpanID: "leaf", ParentSpanID: "child", ServiceName: "flagd",
			Name: "resolveBoolean", Kind: "SERVER",
			StartUnixNanos: start.Add(2 * time.Millisecond).UnixNano(), DurationMS: 1, StatusCode: "ERROR",
			StatusMsg: "error evaluating flag with key failedReadinessProbe",
		},
	}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("wide-trace", start, end, "prod", "prod", 1).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}))

	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "wide-trace", "", 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Data.Spans) != 1 {
		t.Fatalf("limit must still bound the returned spans: got %d", len(result.Data.Spans))
	}
	if result.Data.SpanCount != 3 {
		t.Errorf("span_count = %d, want 3: the count describes the trace, not the page", result.Data.SpanCount)
	}
	if result.Data.ServiceCount != 2 {
		t.Errorf("service_count = %d, want 2: the count describes the trace, not the page", result.Data.ServiceCount)
	}
	if !result.Data.Truncated {
		t.Error("truncated = false, want true: 1 of 3 spans were returned")
	}
	if !result.Data.HasError {
		t.Error("has_error = false, want true: the trace errors in a span the page omitted")
	}
	// Spans start at +0 (2ms), +1ms (1ms) and +2ms (1ms), so the trace runs 3ms
	// end to end -- a figure the single returned span cannot produce on its own.
	if result.Data.DurationMS != 3 {
		t.Errorf("duration_ms = %v, want 3: the duration spans the trace, not the page", result.Data.DurationMS)
	}
	if strings.Contains(result.Summary, "1 spans") || strings.Contains(result.Summary, "1 services") {
		t.Errorf("summary reports the page as the trace: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "3") {
		t.Errorf("summary omits the trace's real span count: %q", result.Summary)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// An untruncated trace must not be labelled truncated, and its summary must not
// carry a qualifier it has not earned.
func TestTraceWholeTraceIsNotReportedAsTruncated(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "whole-trace", Spans: []telemetry.Span{{
		Namespace: "prod", TraceID: "small-trace", SpanID: "root", ServiceName: "checkout",
		Name: "pay", Kind: "SERVER",
		StartUnixNanos: start.UnixNano(), DurationMS: 25, StatusCode: "OK",
	}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("small-trace", start, end, "prod", "prod", 10).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}))

	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "small-trace", "", 10)
	if err != nil {
		t.Fatal(err)
	}

	if result.Data.Truncated {
		t.Error("truncated = true for a trace returned in full")
	}
	if result.Data.SpanCount != 1 {
		t.Errorf("span_count = %d, want 1", result.Data.SpanCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
