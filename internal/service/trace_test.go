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
