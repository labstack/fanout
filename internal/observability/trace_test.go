package observability

import (
	"context"
	"database/sql"
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
	mock.ExpectQuery(regexp.QuoteMeta(traceSummaryQuery)).
		WithArgs("wide-trace", start, end, "prod", "prod").
		WillReturnRows(sqlmock.NewRows([]string{"span_count", "service_count", "duration_ms", "has_error"}).
			AddRow(int64(3), int64(2), 3.0, true))
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
	mock.ExpectQuery(regexp.QuoteMeta(traceSummaryQuery)).
		WithArgs("small-trace", start, end, "prod", "prod").
		WillReturnRows(sqlmock.NewRows([]string{"span_count", "service_count", "duration_ms", "has_error"}).
			AddRow(int64(1), int64(1), 25.0, false))
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

// The sqlmock tests above prove the plumbing but never execute the SQL, so the
// aggregate's own arithmetic needs DuckDB to check it. start_time and end_time
// are TIMESTAMP (internal/query/views.go:19-20); subtracting them yields an
// INTERVAL, which is not a nanosecond count and cannot be scaled to
// milliseconds by division. Only the BIGINT *_unix_nano columns can.
func TestTraceSummaryQueryComputesDurationInMilliseconds(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE spans (
		namespace VARCHAR, trace_id VARCHAR, service VARCHAR, status VARCHAR,
		start_time TIMESTAMP, end_time TIMESTAMP,
		start_unix_nano BIGINT, end_unix_nano BIGINT)`); err != nil {
		t.Fatalf("create spans: %v", err)
	}
	base := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	// The trace spans 3ms end-to-end: first span starts at base, last ends 3ms later.
	rows := []struct {
		service string
		status  string
		startMS int64
		endMS   int64
	}{
		{"cart", "OK", 0, 3},
		{"cart", "OK", 1, 2},
		{"flagd", "ERROR", 2, 3},
	}
	for _, r := range rows {
		start := base.Add(time.Duration(r.startMS) * time.Millisecond)
		end := base.Add(time.Duration(r.endMS) * time.Millisecond)
		if _, err := db.Exec(`INSERT INTO spans VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"prod", "t1", r.service, r.status, start, end, start.UnixNano(), end.UnixNano()); err != nil {
			t.Fatalf("insert span: %v", err)
		}
	}

	var spanCount, serviceCount int64
	var durationMS float64
	var hasError bool
	if err := db.QueryRow(traceSummaryQuery, "t1", base, base.Add(time.Hour), "prod", "prod").
		Scan(&spanCount, &serviceCount, &durationMS, &hasError); err != nil {
		t.Fatalf("trace summary query: %v", err)
	}

	if spanCount != 3 {
		t.Errorf("span_count = %d, want 3", spanCount)
	}
	if serviceCount != 2 {
		t.Errorf("service_count = %d, want 2", serviceCount)
	}
	if durationMS != 3 {
		t.Errorf("duration_ms = %v, want 3", durationMS)
	}
	if !hasError {
		t.Error("has_error = false, want true")
	}
}
