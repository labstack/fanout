package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSearchTraces_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "service", "namespace", "operation", "duration", "status", "ts"}))

	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if len(result.Traces) != 0 {
		t.Errorf("Traces count = %d, want 0", len(result.Traces))
	}
}

func TestSearchTraces_DefaultParams(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "service", "namespace", "operation", "duration", "status", "ts"}))

	// Empty params should use defaults
	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if result == nil {
		t.Fatal("SearchTraces() returned nil")
	}
}

func TestSearchTraces_WithResults(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "service", "namespace", "operation", "duration", "status", "ts"}).
			AddRow("trace-1", "api", "prod", "GET /users", 150.0, "OK", "2024-01-01 10:00:00").
			AddRow("trace-2", "api", "prod", "POST /users", 200.0, "STATUS_CODE_ERROR", "2024-01-01 10:01:00"))

	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if len(result.Traces) != 2 {
		t.Errorf("Traces count = %d, want 2", len(result.Traces))
	}
	if result.Traces[0].Status != "ok" {
		t.Errorf("Traces[0].Status = %q, want %q", result.Traces[0].Status, "ok")
	}
	if result.Traces[1].Status != "error" {
		t.Errorf("Traces[1].Status = %q, want %q", result.Traces[1].Status, "error")
	}
}

func TestSearchTraces_WithServiceFilter(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "service", "namespace", "operation", "duration", "status", "ts"}).
			AddRow("trace-1", "api-gateway", "", "GET /health", 50.0, "OK", "2024-01-01 10:00:00"))

	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{
		Services: []string{"api-gateway"},
	})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if len(result.Traces) != 1 {
		t.Errorf("Traces count = %d, want 1", len(result.Traces))
	}
}

func TestSearchTraces_WithStatus(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "service", "namespace", "operation", "duration", "status", "ts"}).
			AddRow("trace-err", "api", "", "POST /fail", 100.0, "ERROR", "2024-01-01 10:00:00"))

	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{
		Status: []string{"error"},
	})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if len(result.Traces) != 1 {
		t.Errorf("Traces count = %d, want 1", len(result.Traces))
	}
}

func TestSearchTraces_HasMore(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"trace_id", "service", "namespace", "operation", "duration", "status", "ts"})
	for i := 0; i < 52; i++ {
		rows.AddRow("trace", "api", "", "op", 50.0, "OK", "2024-01-01 10:00:00")
	}
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{Limit: 50})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if !result.HasMore {
		t.Error("HasMore should be true")
	}
	if len(result.Traces) != 50 {
		t.Errorf("Traces count = %d, want 50", len(result.Traces))
	}
}

func TestSearchTraces_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if len(result.Traces) != 0 {
		t.Errorf("Traces count = %d, want 0", len(result.Traces))
	}
}

func TestSearchTraces_Facets(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"trace_id", "service", "namespace", "operation", "duration", "status", "ts"}).
			AddRow("t1", "api", "", "GET /users", 50.0, "OK", "2024-01-01 10:00:00").
			AddRow("t2", "api", "", "POST /users", 100.0, "OK", "2024-01-01 10:00:00").
			AddRow("t3", "db", "", "SELECT", 20.0, "ERROR", "2024-01-01 10:00:00"))

	result, err := svc.SearchTraces(context.Background(), TraceSearchParams{})
	if err != nil {
		t.Fatalf("SearchTraces() error = %v", err)
	}

	if len(result.Facets.ByService) == 0 {
		t.Error("ByService facets should not be empty")
	}
	if len(result.Facets.ByStatus) == 0 {
		t.Error("ByStatus facets should not be empty")
	}
}

func TestComputeFacets(t *testing.T) {
	traces := []TraceRow{
		{Service: "api", Status: "ok"},
		{Service: "api", Status: "ok"},
		{Service: "db", Status: "error"},
		{Service: "cache", Status: "ok"},
	}

	facets := computeFacets(traces)

	if len(facets.ByService) != 3 {
		t.Errorf("ByService count = %d, want 3", len(facets.ByService))
	}
	// Should be sorted by count descending
	if facets.ByService[0].Value != "api" {
		t.Errorf("ByService[0].Value = %q, want %q", facets.ByService[0].Value, "api")
	}
	if facets.ByService[0].Count != 2 {
		t.Errorf("ByService[0].Count = %d, want 2", facets.ByService[0].Count)
	}

	// Status facets - error should be first
	if facets.ByStatus[0].Value != "error" {
		t.Errorf("ByStatus[0].Value = %q, want %q (error first)", facets.ByStatus[0].Value, "error")
	}
}

func TestComputeFacets_Top5Limit(t *testing.T) {
	traces := []TraceRow{
		{Service: "svc1", Status: "ok"},
		{Service: "svc2", Status: "ok"},
		{Service: "svc3", Status: "ok"},
		{Service: "svc4", Status: "ok"},
		{Service: "svc5", Status: "ok"},
		{Service: "svc6", Status: "ok"},
		{Service: "svc7", Status: "ok"},
	}

	facets := computeFacets(traces)

	if len(facets.ByService) > 5 {
		t.Errorf("ByService should be limited to 5, got %d", len(facets.ByService))
	}
}

func TestTraceDetail_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Spans query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_id", "service", "operation", "duration", "status", "status_msg", "kind", "start_nano",
			"events_json", "links_json", "trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}))

	// Logs query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"ts", "observed_ts", "service", "severity", "severity_number", "body",
			"span_id", "flags", "scope_name", "scope_version", "attributes_json",
		}))

	result, err := svc.TraceDetail(context.Background(), "nonexistent", 60, "", "")
	if err != nil {
		t.Fatalf("TraceDetail() error = %v", err)
	}

	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
}

func TestTraceDetail_WithSpans(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"span_id", "parent_id", "service", "operation", "duration", "status", "status_msg", "kind", "start_nano",
			"events_json", "links_json", "trace_state", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("span-1", "", "api", "GET /users", 200.0, "OK", "", "SERVER", int64(1000000000), nil, nil, nil, nil, nil, nil, nil).
			AddRow("span-2", "span-1", "db", "SELECT", 50.0, "OK", "", "CLIENT", int64(1050000000), nil, nil, nil, nil, nil, nil, nil))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"ts", "observed_ts", "service", "severity", "severity_number", "body",
			"span_id", "flags", "scope_name", "scope_version", "attributes_json",
		}))

	result, err := svc.TraceDetail(context.Background(), "trace-123", 60, "", "")
	if err != nil {
		t.Fatalf("TraceDetail() error = %v", err)
	}

	if len(result.Spans) != 2 {
		t.Errorf("Spans count = %d, want 2", len(result.Spans))
	}
	if result.RootService != "api" {
		t.Errorf("RootService = %q, want %q", result.RootService, "api")
	}
	if result.SpanCount != 2 {
		t.Errorf("SpanCount = %d, want 2", result.SpanCount)
	}
}

func TestSearchLogs_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"ts", "observed_ts", "service", "namespace", "severity", "severity_number", "body",
			"trace_id", "span_id", "flags", "scope_name", "scope_version", "attributes_json",
		}))

	result, err := svc.SearchLogs(context.Background(), LogSearchParams{})
	if err != nil {
		t.Fatalf("SearchLogs() error = %v", err)
	}

	if len(result.Logs) != 0 {
		t.Errorf("Logs count = %d, want 0", len(result.Logs))
	}
}

func TestSearchLogs_WithResults(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"ts", "observed_ts", "service", "namespace", "severity", "severity_number", "body",
			"trace_id", "span_id", "flags", "scope_name", "scope_version", "attributes_json",
		}).
			AddRow("2024-01-01T10:00:00Z", nil, "api", "prod", "INFO", int64(9), "Request received", "trace-1", nil, nil, nil, nil, nil).
			AddRow("2024-01-01T10:00:01Z", nil, "api", "prod", "ERROR", int64(17), "Request failed", "trace-1", nil, nil, nil, nil, nil))

	result, err := svc.SearchLogs(context.Background(), LogSearchParams{})
	if err != nil {
		t.Fatalf("SearchLogs() error = %v", err)
	}

	if len(result.Logs) != 2 {
		t.Errorf("Logs count = %d, want 2", len(result.Logs))
	}
}

func TestSearchLogs_HasMore(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{
		"ts", "observed_ts", "service", "namespace", "severity", "severity_number", "body",
		"trace_id", "span_id", "flags", "scope_name", "scope_version", "attributes_json",
	})
	for i := 0; i < 102; i++ {
		rows.AddRow("2024-01-01T10:00:00Z", nil, "api", "", "INFO", int64(9), "msg", "", nil, nil, nil, nil, nil)
	}
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.SearchLogs(context.Background(), LogSearchParams{Limit: 100})
	if err != nil {
		t.Fatalf("SearchLogs() error = %v", err)
	}

	if !result.HasMore {
		t.Error("HasMore should be true")
	}
}

func TestServiceTrends_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "cnt"}))

	result, err := svc.ServiceTrends(context.Background(), 60, "", "")
	if err != nil {
		t.Fatalf("ServiceTrends() error = %v", err)
	}

	if len(result) != 0 {
		t.Errorf("Trends count = %d, want 0", len(result))
	}
}

func TestServiceTrends_WithData(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "cnt"}).
			AddRow("api", "2024-01-01 10:00:00", int64(100)).
			AddRow("api", "2024-01-01 10:05:00", int64(150)).
			AddRow("db", "2024-01-01 10:00:00", int64(50)))

	result, err := svc.ServiceTrends(context.Background(), 60, "", "")
	if err != nil {
		t.Fatalf("ServiceTrends() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("Trends count = %d, want 2", len(result))
	}
	if len(result["api"]) != 2 {
		t.Errorf("api trend points = %d, want 2", len(result["api"]))
	}
}

func TestMetrics_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"metric_name", "mtype", "cnt", "avg_val", "min_val", "max_val", "services"}))

	result, err := svc.Metrics(context.Background(), MetricsParams{})
	if err != nil {
		t.Fatalf("Metrics() error = %v", err)
	}

	if len(result.Metrics) != 0 {
		t.Errorf("Metrics count = %d, want 0", len(result.Metrics))
	}
}

func TestMetrics_WithData(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"metric_name", "mtype", "cnt", "avg_val", "min_val", "max_val", "services"}).
			AddRow("http.requests", "counter", int64(1000), 1.0, 1.0, 1.0, nil))

	// Sparklines query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"metric_name", "bucket", "avg_val"}).
			AddRow("http.requests", "2024-01-01 10:00:00", 100.0).
			AddRow("http.requests", "2024-01-01 10:05:00", 150.0))

	result, err := svc.Metrics(context.Background(), MetricsParams{})
	if err != nil {
		t.Fatalf("Metrics() error = %v", err)
	}

	if len(result.Metrics) != 1 {
		t.Errorf("Metrics count = %d, want 1", len(result.Metrics))
	}
	if result.Metrics[0].Name != "http.requests" {
		t.Errorf("Metrics[0].Name = %q, want %q", result.Metrics[0].Name, "http.requests")
	}
}
