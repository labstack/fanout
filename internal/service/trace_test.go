package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestTrace_EmptyTraceID(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.Trace(context.Background(), "", true, 60)
	if err == nil {
		t.Error("Trace() should return error for empty trace_id")
	}
}

func TestTrace_NotFound(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}))

	result, err := svc.Trace(context.Background(), "nonexistent-trace", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
}

func TestTrace_SingleSpan(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).AddRow(
			"span-1", nil, "api-gateway", "GET /users", "SERVER", "2024-01-01T10:00:00Z",
			150.0, "OK", nil, int64(1704106800000000000), nil, nil,
			nil, nil, "otel-go", "1.0", nil,
		))

	result, err := svc.Trace(context.Background(), "trace-123", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if result.TraceID != "trace-123" {
		t.Errorf("TraceID = %q, want %q", result.TraceID, "trace-123")
	}
	if len(result.Spans) != 1 {
		t.Errorf("Spans count = %d, want 1", len(result.Spans))
	}
	if result.Spans[0].Service != "api-gateway" {
		t.Errorf("Spans[0].Service = %q, want %q", result.Spans[0].Service, "api-gateway")
	}
	if len(result.Services) != 1 {
		t.Errorf("Services count = %d, want 1", len(result.Services))
	}
}

func TestTrace_MultiSpan(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("span-1", nil, "api", "GET /users", "SERVER", "2024-01-01T10:00:00Z",
				200.0, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-2", "span-1", "user-service", "fetch-user", "CLIENT", "2024-01-01T10:00:01Z",
				50.0, "OK", nil, int64(1704106801000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-3", "span-1", "cache", "get-cache", "CLIENT", "2024-01-01T10:00:01Z",
				10.0, "OK", nil, int64(1704106801100000000), nil, nil, nil, nil, nil, nil, nil))

	result, err := svc.Trace(context.Background(), "trace-multi", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if len(result.Spans) != 3 {
		t.Errorf("Spans count = %d, want 3", len(result.Spans))
	}
	if len(result.Services) != 3 {
		t.Errorf("Services count = %d, want 3", len(result.Services))
	}
	if result.SpanCount != 3 {
		t.Errorf("SpanCount = %d, want 3", result.SpanCount)
	}
}

func TestTrace_WithError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("span-1", nil, "api", "GET /users", "SERVER", "2024-01-01T10:00:00Z",
				200.0, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-2", "span-1", "database", "SELECT", "CLIENT", "2024-01-01T10:00:01Z",
				50.0, "STATUS_CODE_ERROR", "connection refused", int64(1704106801000000000), nil, nil, nil, nil, nil, nil, nil))

	result, err := svc.Trace(context.Background(), "trace-error", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if !result.HasError {
		t.Error("HasError should be true")
	}
	if result.RootCause == nil {
		t.Fatal("RootCause should not be nil")
	}
	if result.RootCause.Reason != "error" {
		t.Errorf("RootCause.Reason = %q, want %q", result.RootCause.Reason, "error")
	}
	if result.RootCause.Service != "database" {
		t.Errorf("RootCause.Service = %q, want %q", result.RootCause.Service, "database")
	}
}

func TestTrace_SlowOperation(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("span-1", nil, "api", "POST /process", "SERVER", "2024-01-01T10:00:00Z",
				2000.0, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-2", "span-1", "processor", "heavy-compute", "INTERNAL", "2024-01-01T10:00:01Z",
				1800.0, "OK", nil, int64(1704106801000000000), nil, nil, nil, nil, nil, nil, nil))

	result, err := svc.Trace(context.Background(), "trace-slow", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if result.RootCause == nil {
		t.Fatal("RootCause should not be nil for slow trace")
	}
	if result.RootCause.Reason != "latency" {
		t.Errorf("RootCause.Reason = %q, want %q", result.RootCause.Reason, "latency")
	}
}

func TestTrace_WithLogs(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Spans query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).AddRow(
			"span-1", nil, "api", "GET /users", "SERVER", "2024-01-01T10:00:00Z",
			100.0, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil,
		))

	// Logs query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"ts", "observed_ts", "service", "severity", "severity_number", "body",
			"span_id", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("2024-01-01T10:00:00Z", nil, "api", "INFO", int64(9), "Request started", "span-1", nil, nil, nil, nil).
			AddRow("2024-01-01T10:00:01Z", nil, "api", "INFO", int64(9), "Request completed", "span-1", nil, nil, nil, nil))

	result, err := svc.Trace(context.Background(), "trace-with-logs", true, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if len(result.Logs) != 2 {
		t.Errorf("Logs count = %d, want 2", len(result.Logs))
	}
}

func TestTrace_DefaultWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}))

	// Pass 0 window, should default to 1440 (24h)
	result, err := svc.Trace(context.Background(), "trace-123", false, 0)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if result == nil {
		t.Fatal("Trace() returned nil")
	}
}

func TestTrace_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	_, err := svc.Trace(context.Background(), "trace-123", false, 60)
	if err == nil {
		t.Error("Trace() should return error when query fails")
	}
}

func TestTrace_CriticalPath(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("span-1", nil, "api", "GET /data", "SERVER", "2024-01-01T10:00:00Z",
				500.0, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-2", "span-1", "db", "SELECT", "CLIENT", "2024-01-01T10:00:01Z",
				300.0, "OK", nil, int64(1704106801000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-3", "span-1", "cache", "GET", "CLIENT", "2024-01-01T10:00:01Z",
				150.0, "STATUS_CODE_ERROR", "timeout", int64(1704106801100000000), nil, nil, nil, nil, nil, nil, nil))

	result, err := svc.Trace(context.Background(), "trace-critical", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if len(result.CriticalPath) == 0 {
		t.Error("CriticalPath should not be empty")
	}
}

func TestTrace_SelfTime(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Parent span of 200ms, child span of 50ms
	// Parent self time should be 200 - 50 = 150ms
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("span-1", nil, "api", "GET /", "SERVER", "2024-01-01T10:00:00Z",
				200.0, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-2", "span-1", "db", "query", "CLIENT", "2024-01-01T10:00:01Z",
				50.0, "OK", nil, int64(1704106801000000000), nil, nil, nil, nil, nil, nil, nil))

	result, err := svc.Trace(context.Background(), "trace-selftime", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if len(result.Spans) != 2 {
		t.Fatalf("Spans count = %d, want 2", len(result.Spans))
	}

	// Find parent span and check self time
	for _, sp := range result.Spans {
		if sp.SpanID == "span-1" {
			if sp.SelfTime != 150.0 {
				t.Errorf("Parent SelfTime = %f, want 150.0", sp.SelfTime)
			}
		}
	}
}

func TestTrace_SQLEscaping(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}))

	// SQL injection attempt should be escaped
	result, err := svc.Trace(context.Background(), "'; DROP TABLE--", false, 60)
	if err != nil {
		t.Fatalf("Trace() error = %v", err)
	}

	if result.TraceID != "'; DROP TABLE--" {
		t.Errorf("TraceID not preserved: %q", result.TraceID)
	}
}

func TestCompareTrace_MatchingOperations(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Primary trace result (already fetched).
	primary := &TraceResult{
		TraceID:  "trace-abc",
		Duration: 142.5,
		Services: []string{"api", "payment"},
		Spans: []SpanInfo{
			{SpanID: "s1", Service: "api", Name: "GET /checkout", Duration: 142.5},
			{SpanID: "s2", Service: "payment", Name: "process_payment", Duration: 89.3},
		},
	}

	// Mock: CompareTrace will call Trace() for the other trace ID.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("s3", nil, "api", "GET /checkout", "SERVER", "2024-01-01T10:00:00Z",
				85.2, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("s4", "s3", "payment", "process_payment", "CLIENT", "2024-01-01T10:00:00Z",
				12.1, "OK", nil, int64(1704106800100000000), nil, nil, nil, nil, nil, nil, nil))

	cmp := svc.CompareTrace(context.Background(), primary, "trace-def", 60)
	if cmp == nil {
		t.Fatal("CompareTrace() returned nil")
	}
	if cmp.OtherTraceID != "trace-def" {
		t.Errorf("OtherTraceID = %q, want %q", cmp.OtherTraceID, "trace-def")
	}
	if cmp.OtherDurationMs != 85.2 {
		t.Errorf("OtherDurationMs = %f, want 85.2", cmp.OtherDurationMs)
	}
	if len(cmp.SpanDiffs) == 0 {
		t.Error("SpanDiffs should not be empty")
	}
}

func TestCompareTrace_FetchError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	primary := &TraceResult{
		TraceID:  "trace-abc",
		Duration: 100.0,
		Services: []string{"api"},
		Spans:    []SpanInfo{{SpanID: "s1", Service: "api", Name: "op", Duration: 100.0}},
	}

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	cmp := svc.CompareTrace(context.Background(), primary, "trace-def", 60)
	// Should return nil on error, not panic.
	if cmp != nil {
		t.Error("CompareTrace() should return nil when other trace fetch fails")
	}
}

func TestCompareTrace_DeltaIsAbsolute(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Other trace is FASTER than primary — delta should still be positive.
	primary := &TraceResult{
		TraceID:  "trace-abc",
		Duration: 50.0,
		Services: []string{"api"},
		Spans:    []SpanInfo{{SpanID: "s1", Service: "api", Name: "op", Duration: 50.0}},
	}

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_span_id", "service", "operation", "kind", "start_time",
			"duration_ms", "status", "status_msg", "start_nano", "events_json", "links_json",
			"trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).AddRow("s2", nil, "api", "op", "SERVER", "2024-01-01T10:00:00Z",
			200.0, "OK", nil, int64(1704106800000000000), nil, nil, nil, nil, nil, nil, nil))

	cmp := svc.CompareTrace(context.Background(), primary, "trace-def", 60)
	if cmp == nil {
		t.Fatal("CompareTrace() returned nil")
	}
	if cmp.DurationDeltaMs < 0 {
		t.Errorf("DurationDeltaMs = %f, should be non-negative", cmp.DurationDeltaMs)
	}
}

func TestFetchMetricContext_NoSpans(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	result := &TraceResult{
		TraceID: "trace-abc",
		Spans:   []SpanInfo{},
	}

	mcs := svc.FetchMetricContext(context.Background(), result)
	if mcs != nil {
		t.Errorf("FetchMetricContext() should return nil for empty spans, got %v", mcs)
	}
}

func TestFetchMetricContext_NoServices(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	result := &TraceResult{
		TraceID:  "trace-abc",
		Services: []string{},
		Spans:    []SpanInfo{{SpanID: "s1", Service: "api", Name: "op", StartTime: "2024-01-01T10:00:00Z"}},
	}

	mcs := svc.FetchMetricContext(context.Background(), result)
	if mcs != nil {
		t.Errorf("FetchMetricContext() should return nil when no services, got %v", mcs)
	}
}

func TestFetchMetricContext_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	result := &TraceResult{
		TraceID:  "trace-abc",
		Services: []string{"api"},
		Spans:    []SpanInfo{{SpanID: "s1", Service: "api", Name: "op", StartTime: "2024-01-01T10:00:00Z"}},
	}

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("rollup unavailable"))

	mcs := svc.FetchMetricContext(context.Background(), result)
	// Should return nil on error, not panic.
	if mcs != nil {
		t.Errorf("FetchMetricContext() should return nil on query error, got %v", mcs)
	}
}

func TestFetchMetricContext_WithData(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	result := &TraceResult{
		TraceID:  "trace-abc",
		Services: []string{"payment"},
		Spans:    []SpanInfo{{SpanID: "s1", Service: "payment", Name: "op", StartTime: "2024-01-01T10:00:00Z"}},
	}

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "p50_ms", "p95_ms", "error_rate", "total_spans"}).
			AddRow("payment", 5200.0, 9444.0, 0.0, 60.0))

	mcs := svc.FetchMetricContext(context.Background(), result)
	if len(mcs) != 1 {
		t.Fatalf("FetchMetricContext() returned %d contexts, want 1", len(mcs))
	}
	if mcs[0].Service != "payment" {
		t.Errorf("Service = %q, want %q", mcs[0].Service, "payment")
	}
	if mcs[0].AtTraceTime.P50Ms != 5200.0 {
		t.Errorf("P50Ms = %f, want 5200.0", mcs[0].AtTraceTime.P50Ms)
	}
	if mcs[0].AtTraceTime.P95Ms != 9444.0 {
		t.Errorf("P95Ms = %f, want 9444.0", mcs[0].AtTraceTime.P95Ms)
	}
	// total_spans=60 over 5 minutes = 12 spans/min
	if mcs[0].AtTraceTime.SpansPerMin != 12.0 {
		t.Errorf("SpansPerMin = %f, want 12.0", mcs[0].AtTraceTime.SpansPerMin)
	}
}
