package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFind_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Spans query - empty
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}))

	// Logs query - empty
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
	if len(result.Logs) != 0 {
		t.Errorf("Logs count = %d, want 0", len(result.Logs))
	}
	if len(result.Metrics) != 0 {
		t.Errorf("Metrics count = %d, want 0", len(result.Metrics))
	}
}

func TestFind_DefaultParams(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	// Empty params should use defaults (window=15, limit=50, type=both)
	result, err := svc.Find(context.Background(), FindParams{})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if result == nil {
		t.Fatal("Find() returned nil")
	}
}

func TestFind_SpansOnly(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-123", "span-456", "api", "GET /users", 50.0, "OK", "2024-01-01T10:00:00Z", "my-scope", "1.0"))

	result, err := svc.Find(context.Background(), FindParams{Type: "spans"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 1 {
		t.Errorf("Spans count = %d, want 1", len(result.Spans))
	}
	if len(result.Logs) != 0 {
		t.Errorf("Logs count = %d, want 0", len(result.Logs))
	}
	if result.Spans[0].TraceID != "trace-123" {
		t.Errorf("Spans[0].TraceID = %q, want %q", result.Spans[0].TraceID, "trace-123")
	}
}

func TestFind_LogsOnly(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}).
			AddRow("2024-01-01T10:00:00Z", nil, "api", "ERROR", int64(17), "Something failed", "trace-789", "span-012", nil, nil))

	result, err := svc.Find(context.Background(), FindParams{Type: "logs"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Logs) != 1 {
		t.Errorf("Logs count = %d, want 1", len(result.Logs))
	}
	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
	if result.Logs[0].Severity != "ERROR" {
		t.Errorf("Logs[0].Severity = %q, want %q", result.Logs[0].Severity, "ERROR")
	}
}

func TestFind_WithQuery(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-1", "span-1", "api", "GET /users", 50.0, "OK", "2024-01-01T10:00:00Z", nil, nil))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{Query: "users"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if result == nil {
		t.Fatal("Find() returned nil")
	}
}

func TestFind_WithServiceFilter(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-1", "span-1", "api-gateway", "POST /login", 100.0, "OK", "2024-01-01T10:00:00Z", nil, nil))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{Service: "api-gateway"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 1 {
		t.Errorf("Spans count = %d, want 1", len(result.Spans))
	}
}

func TestFind_ErrorStatus(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-err", "span-err", "api", "GET /fail", 50.0, "ERROR", "2024-01-01T10:00:00Z", nil, nil))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{Status: "error"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 1 {
		t.Errorf("Spans count = %d, want 1", len(result.Spans))
	}
}

func TestFind_SlowStatus(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-slow", "span-slow", "api", "GET /heavy", 5000.0, "OK", "2024-01-01T10:00:00Z", nil, nil))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{Status: "slow"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 1 {
		t.Errorf("Spans count = %d, want 1", len(result.Spans))
	}
}

func TestFind_WithSeverityFilter(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}).
			AddRow("2024-01-01T10:00:00Z", nil, "api", "ERROR", int64(17), "Critical error", nil, nil, nil, nil))

	result, err := svc.Find(context.Background(), FindParams{Severity: []string{"ERROR", "FATAL"}})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Logs) != 1 {
		t.Errorf("Logs count = %d, want 1", len(result.Logs))
	}
}

func TestFind_WithOperation(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-1", "span-1", "api", "GET /users", 50.0, "OK", "2024-01-01T10:00:00Z", nil, nil))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{Operation: "GET /users"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if result == nil {
		t.Fatal("Find() returned nil")
	}
}

func TestFind_WithAttrs(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-1", "span-1", "api", "GET /users", 50.0, "OK", "2024-01-01T10:00:00Z", nil, nil))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{
		Attrs: map[string]string{"http.method": "GET"},
	})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if result == nil {
		t.Fatal("Find() returned nil")
	}
}

func TestFind_HasMore(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Return more spans than limit to trigger hasMore
	rows := sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"})
	for i := 0; i < 52; i++ { // Limit is 50, returning 52
		rows.AddRow("trace", "span", "api", "op", 50.0, "OK", "2024-01-01T10:00:00Z", nil, nil)
	}
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	result, err := svc.Find(context.Background(), FindParams{Limit: 50})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if !result.HasMore {
		t.Error("HasMore should be true when results exceed limit")
	}
	if len(result.Spans) != 50 {
		t.Errorf("Spans count = %d, want 50", len(result.Spans))
	}
}

func TestFind_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	// Should propagate query errors
	result, err := svc.Find(context.Background(), FindParams{})
	if err == nil {
		t.Fatal("Find() should return error on query failure")
	}
	if !strings.Contains(err.Error(), "db error") {
		t.Errorf("error should contain 'db error', got: %v", err)
	}

	// Should still return a valid (empty) result struct
	if result == nil {
		t.Fatal("Find() should return non-nil result even on error")
	}
	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
}

func TestFind_SQLEscaping(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}))

	// SQL injection attempts should be escaped
	result, err := svc.Find(context.Background(), FindParams{
		Query:   "'; DROP TABLE--",
		Service: "service'; DROP TABLE--",
	})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if result == nil {
		t.Fatal("Find() returned nil")
	}
}

func TestFind_ScopeInfo(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-1", "span-1", "api", "GET /users", 50.0, "OK", "2024-01-01T10:00:00Z", "otel-go", "1.2.3"))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}).
			AddRow("2024-01-01T10:00:00Z", "2024-01-01T10:00:01Z", "api", "INFO", int64(9), "log message", "trace-1", "span-1", "my-logger", "2.0"))

	result, err := svc.Find(context.Background(), FindParams{})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 1 {
		t.Fatalf("Spans count = %d, want 1", len(result.Spans))
	}
	if result.Spans[0].ScopeName != "otel-go" {
		t.Errorf("Spans[0].ScopeName = %q, want %q", result.Spans[0].ScopeName, "otel-go")
	}
	if result.Spans[0].ScopeVersion != "1.2.3" {
		t.Errorf("Spans[0].ScopeVersion = %q, want %q", result.Spans[0].ScopeVersion, "1.2.3")
	}

	if len(result.Logs) != 1 {
		t.Fatalf("Logs count = %d, want 1", len(result.Logs))
	}
	if result.Logs[0].ScopeName != "my-logger" {
		t.Errorf("Logs[0].ScopeName = %q, want %q", result.Logs[0].ScopeName, "my-logger")
	}
	if result.Logs[0].ObservedTime != "2024-01-01T10:00:01Z" {
		t.Errorf("Logs[0].ObservedTime = %q, want %q", result.Logs[0].ObservedTime, "2024-01-01T10:00:01Z")
	}
}

func TestFind_MetricsOnly(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"metric_name", "mtype", "service", "value", "ts", "unit", "description", "scope_name", "scope_version"}).
			AddRow("kafka.consumer.lag", "gauge", "kafka", 42.0, "2024-01-01T10:00:00Z", "messages", "Consumer group lag", "otel-kafka", "1.0"))

	result, err := svc.Find(context.Background(), FindParams{Type: "metrics"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
	if len(result.Logs) != 0 {
		t.Errorf("Logs count = %d, want 0", len(result.Logs))
	}
	if len(result.Metrics) != 1 {
		t.Fatalf("Metrics count = %d, want 1", len(result.Metrics))
	}
	m := result.Metrics[0]
	if m.Name != "kafka.consumer.lag" {
		t.Errorf("Metrics[0].Name = %q, want %q", m.Name, "kafka.consumer.lag")
	}
	if m.Type != "gauge" {
		t.Errorf("Metrics[0].Type = %q, want %q", m.Type, "gauge")
	}
	if m.Service != "kafka" {
		t.Errorf("Metrics[0].Service = %q, want %q", m.Service, "kafka")
	}
	if m.Value != 42.0 {
		t.Errorf("Metrics[0].Value = %f, want 42.0", m.Value)
	}
	if m.Unit != "messages" {
		t.Errorf("Metrics[0].Unit = %q, want %q", m.Unit, "messages")
	}
	if m.ScopeName != "otel-kafka" {
		t.Errorf("Metrics[0].ScopeName = %q, want %q", m.ScopeName, "otel-kafka")
	}
}

func TestFind_MetricsNullable(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"metric_name", "mtype", "service", "value", "ts", "unit", "description", "scope_name", "scope_version"}).
			AddRow("cpu.usage", "gauge", nil, 0.85, "2024-01-01T10:00:00Z", nil, nil, nil, nil))

	result, err := svc.Find(context.Background(), FindParams{Type: "metrics"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Metrics) != 1 {
		t.Fatalf("Metrics count = %d, want 1", len(result.Metrics))
	}
	if result.Metrics[0].Service != "" {
		t.Errorf("Metrics[0].Service = %q, want empty", result.Metrics[0].Service)
	}
	if result.Metrics[0].Unit != "" {
		t.Errorf("Metrics[0].Unit = %q, want empty", result.Metrics[0].Unit)
	}
}

func TestFind_All(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Spans
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "span_id", "service", "operation", "duration_ms", "status", "start_time", "scope_name", "scope_version"}).
			AddRow("trace-1", "span-1", "api", "GET /", 10.0, "OK", "2024-01-01T10:00:00Z", nil, nil))
	// Logs
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"ts", "observed_ts", "service", "severity", "severity_number", "body", "trace_id", "span_id", "scope_name", "scope_version"}).
			AddRow("2024-01-01T10:00:00Z", nil, "api", "INFO", int64(9), "hello", nil, nil, nil, nil))
	// Metrics
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"metric_name", "mtype", "service", "value", "ts", "unit", "description", "scope_name", "scope_version"}).
			AddRow("http.requests", "counter", "api", 100.0, "2024-01-01T10:00:00Z", "requests", nil, nil, nil))

	result, err := svc.Find(context.Background(), FindParams{Type: "all"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if len(result.Spans) != 1 {
		t.Errorf("Spans count = %d, want 1", len(result.Spans))
	}
	if len(result.Logs) != 1 {
		t.Errorf("Logs count = %d, want 1", len(result.Logs))
	}
	if len(result.Metrics) != 1 {
		t.Errorf("Metrics count = %d, want 1", len(result.Metrics))
	}
}

func TestFind_MetricsQueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("metrics db error"))

	result, err := svc.Find(context.Background(), FindParams{Type: "metrics"})
	if err == nil {
		t.Fatal("Find() should return error on metrics query failure")
	}
	if !strings.Contains(err.Error(), "metrics db error") {
		t.Errorf("error should contain 'metrics db error', got: %v", err)
	}
	if result == nil {
		t.Fatal("Find() should return non-nil result even on error")
	}
}
