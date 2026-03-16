package mcp

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
)

// newTestServer creates an MCP Server backed by a sqlmock database
// for integration testing of handler methods.
func newTestServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.Config{
		LakeDir:   "lake",
		DefaultNS: "default",
		TenantID:  uuid.MustParse("00000000-0000-0000-0000-000000000000"),
	}

	duck := &query.Duck{DB: db}
	svc := service.New(duck, cfg)

	s := &Server{
		svc:  svc,
		duck: duck,
		cfg:  cfg,
	}
	return s, mock
}

// ---------- Overview Handler ----------

func TestOverviewHandler(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	// Overview queries service_rollup via the service layer
	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("frontend", int64(5000), sql.NullFloat64{Float64: 3.0, Valid: true},
			sql.NullFloat64{Float64: 20.0, Valid: true},
			sql.NullFloat64{Float64: 0.01, Valid: true}).
		AddRow("backend", int64(3000), sql.NullFloat64{Float64: 5.0, Valid: true},
			sql.NullFloat64{Float64: 50.0, Valid: true},
			sql.NullFloat64{Float64: 0.005, Valid: true})

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, out, err := s.overview(ctx, nil, OverviewIn{})
	if err != nil {
		t.Fatalf("overview() error = %v", err)
	}

	if out.Health.TotalServices != 2 {
		t.Errorf("Health.TotalServices = %d, want 2", out.Health.TotalServices)
	}
	if len(out.Services) != 2 {
		t.Errorf("Services count = %d, want 2", len(out.Services))
	}
	if out.Window == "" {
		t.Error("Window should not be empty")
	}
	if out.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
	if out.Health.Score <= 0 {
		t.Errorf("Health.Score = %f, should be > 0", out.Health.Score)
	}

	// Verify services are mapped correctly
	found := false
	for _, svc := range out.Services {
		if svc.Service == "frontend" {
			found = true
			if svc.Requests != 5000 {
				t.Errorf("frontend.Requests = %d, want 5000", svc.Requests)
			}
		}
	}
	if !found {
		t.Error("frontend service not found in output")
	}
}

func TestOverviewHandler_Error(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("database is down"))

	_, _, err := s.overview(ctx, nil, OverviewIn{})
	if err == nil {
		t.Fatal("overview() should return error on query failure")
	}
}

func TestOverviewHandler_WithWindow(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}))

	_, out, err := s.overview(ctx, nil, OverviewIn{Window: "1h"})
	if err != nil {
		t.Fatalf("overview() error = %v", err)
	}

	if out.Window != "1h" {
		t.Errorf("Window = %q, want %q", out.Window, "1h")
	}
}

func TestOverviewHandler_InvalidWindow(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	_, _, err := s.overview(ctx, nil, OverviewIn{Window: "banana"})
	if err == nil {
		t.Error("overview() should return error for invalid window")
	}
}

// ---------- Spans Handler ----------

func TestSpansHandler(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"trace_id", "span_id", "service", "operation", "kind",
		"start_time", "duration_ms", "status", "attributes_json",
	}).
		AddRow("trace-1", "span-1", "api", "GET /health", "SPAN_KIND_SERVER",
			"2026-03-14T12:00:00Z", 5.2, "STATUS_CODE_OK", sql.NullString{}).
		AddRow("trace-2", "span-2", "api", "POST /checkout", "SPAN_KIND_SERVER",
			"2026-03-14T12:00:01Z", 120.5, "STATUS_CODE_ERROR", sql.NullString{String: `{"http.method":"POST"}`, Valid: true})

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, out, err := s.spans(ctx, nil, SpansIn{Service: "api", Window: "15m"})
	if err != nil {
		t.Fatalf("spans() error = %v", err)
	}

	if len(out.Spans) != 2 {
		t.Errorf("Spans count = %d, want 2", len(out.Spans))
	}
	if out.TotalMatched != 2 {
		t.Errorf("TotalMatched = %d, want 2", out.TotalMatched)
	}
	if out.Returned != 2 {
		t.Errorf("Returned = %d, want 2", out.Returned)
	}
	if out.Spans[0].TraceID != "trace-1" {
		t.Errorf("Spans[0].TraceID = %q, want %q", out.Spans[0].TraceID, "trace-1")
	}
	if out.Spans[0].Service != "api" {
		t.Errorf("Spans[0].Service = %q, want %q", out.Spans[0].Service, "api")
	}
	if out.Spans[0].DurationMs != 5.2 {
		t.Errorf("Spans[0].DurationMs = %f, want 5.2", out.Spans[0].DurationMs)
	}
	// Second span should have parsed attributes
	if out.Spans[1].Attributes == nil {
		t.Error("Spans[1].Attributes should not be nil")
	} else if out.Spans[1].Attributes["http.method"] != "POST" {
		t.Errorf("Spans[1].Attributes[http.method] = %q, want %q",
			out.Spans[1].Attributes["http.method"], "POST")
	}
}

func TestSpansHandler_EmptyResults(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"trace_id", "span_id", "service", "operation", "kind",
			"start_time", "duration_ms", "status", "attributes_json",
		}))

	_, out, err := s.spans(ctx, nil, SpansIn{Query: "nonexistent"})
	if err != nil {
		t.Fatalf("spans() error = %v", err)
	}

	if len(out.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(out.Spans))
	}
	if out.Suggestion == "" {
		t.Error("Suggestion should be set when no results found")
	}
}

func TestSpansHandler_InvalidWindow(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	// Invalid window returns suggestion, not error (handler catches it gracefully)
	_, out, err := s.spans(ctx, nil, SpansIn{Window: "banana"})
	if err != nil {
		t.Fatalf("spans() should not return error for invalid window, got: %v", err)
	}
	if out.Suggestion == "" {
		t.Error("Suggestion should be set for invalid window")
	}
}

func TestSpansHandler_QueryError(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("query failed"))

	_, out, err := s.spans(ctx, nil, SpansIn{Window: "15m"})
	// The spans handler catches errors and returns them as suggestions, not Go errors
	if err != nil {
		t.Fatalf("spans() should not return Go error, got: %v", err)
	}
	if out.Suggestion == "" {
		t.Error("Suggestion should be set when query fails")
	}
}

// ---------- Logs Handler ----------

func TestLogsHandler(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"time", "service", "severity", "body", "trace_id", "span_id", "attributes_json",
	}).
		AddRow("2026-03-14T12:00:00Z", "payment", "ERROR", "Connection pool exhausted",
			sql.NullString{String: "trace-abc", Valid: true},
			sql.NullString{String: "span-xyz", Valid: true},
			sql.NullString{}).
		AddRow("2026-03-14T12:00:01Z", "payment", "WARN", "Pool usage at 90%",
			sql.NullString{}, sql.NullString{}, sql.NullString{})

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, out, err := s.logs(ctx, nil, LogsIn{
		Service:  "payment",
		Severity: []string{"ERROR", "WARN"},
		Window:   "1h",
	})
	if err != nil {
		t.Fatalf("logs() error = %v", err)
	}

	if len(out.Logs) != 2 {
		t.Errorf("Logs count = %d, want 2", len(out.Logs))
	}
	if out.TotalMatched != 2 {
		t.Errorf("TotalMatched = %d, want 2", out.TotalMatched)
	}
	if out.Logs[0].Service != "payment" {
		t.Errorf("Logs[0].Service = %q, want %q", out.Logs[0].Service, "payment")
	}
	if out.Logs[0].Severity != "ERROR" {
		t.Errorf("Logs[0].Severity = %q, want %q", out.Logs[0].Severity, "ERROR")
	}
	if out.Logs[0].TraceID != "trace-abc" {
		t.Errorf("Logs[0].TraceID = %q, want %q", out.Logs[0].TraceID, "trace-abc")
	}
	if out.Logs[0].Body != "Connection pool exhausted" {
		t.Errorf("Logs[0].Body = %q", out.Logs[0].Body)
	}
}

func TestLogsHandler_EmptyResults(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{
			"time", "service", "severity", "body", "trace_id", "span_id", "attributes_json",
		}))

	_, out, err := s.logs(ctx, nil, LogsIn{Query: "nonexistent"})
	if err != nil {
		t.Fatalf("logs() error = %v", err)
	}

	if len(out.Logs) != 0 {
		t.Errorf("Logs count = %d, want 0", len(out.Logs))
	}
	if out.Suggestion == "" {
		t.Error("Suggestion should be set when no results found")
	}
}

func TestLogsHandler_QueryError(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("database error"))

	_, out, err := s.logs(ctx, nil, LogsIn{Window: "15m"})
	// Logs handler catches errors and returns suggestions
	if err != nil {
		t.Fatalf("logs() should not return Go error, got: %v", err)
	}
	if out.Suggestion == "" {
		t.Error("Suggestion should be set when query fails")
	}
}

// ---------- Topology Handler ----------

func TestTopologyHandler(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	// Nodes query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}).
			AddRow("api-gateway", int64(5000),
				sql.NullFloat64{Float64: 3.0, Valid: true},
				sql.NullFloat64{Float64: 50.0, Valid: true},
				sql.NullFloat64{Float64: 0.001, Valid: true},
				int64(100), int64(50)).
			AddRow("user-service", int64(3000),
				sql.NullFloat64{Float64: 5.0, Valid: true},
				sql.NullFloat64{Float64: 100.0, Valid: true},
				sql.NullFloat64{Float64: 0.005, Valid: true},
				int64(0), int64(0)))

	// Edges query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}).
			AddRow("api-gateway", "user-service", int64(2500), 25.0, 0.002, "call"))

	_, out, err := s.topology(ctx, nil, TopologyIn{})
	if err != nil {
		t.Fatalf("topology() error = %v", err)
	}

	if len(out.Nodes) != 2 {
		t.Errorf("Nodes count = %d, want 2", len(out.Nodes))
	}
	if len(out.Edges) != 1 {
		t.Errorf("Edges count = %d, want 1", len(out.Edges))
	}

	// Verify node mapping
	found := false
	for _, n := range out.Nodes {
		if n.Service == "api-gateway" {
			found = true
			if n.Requests != 5000 {
				t.Errorf("api-gateway.Requests = %d, want 5000", n.Requests)
			}
			if n.Status != "healthy" {
				t.Errorf("api-gateway.Status = %q, want %q", n.Status, "healthy")
			}
			if n.P50Ms != 3.0 {
				t.Errorf("api-gateway.P50Ms = %f, want 3.0", n.P50Ms)
			}
			if n.P95Ms != 50.0 {
				t.Errorf("api-gateway.P95Ms = %f, want 50.0", n.P95Ms)
			}
		}
	}
	if !found {
		t.Error("api-gateway node not found in output")
	}

	// Verify edge mapping
	if out.Edges[0].Source != "api-gateway" {
		t.Errorf("Edges[0].Source = %q, want %q", out.Edges[0].Source, "api-gateway")
	}
	if out.Edges[0].Target != "user-service" {
		t.Errorf("Edges[0].Target = %q, want %q", out.Edges[0].Target, "user-service")
	}
	if out.Edges[0].Calls != 2500 {
		t.Errorf("Edges[0].Calls = %d, want 2500", out.Edges[0].Calls)
	}
	if out.Edges[0].EdgeType != "call" {
		t.Errorf("Edges[0].EdgeType = %q, want %q", out.Edges[0].EdgeType, "call")
	}

	// CriticalPaths should be computed (may be empty or populated depending on edge data)
	if out.CriticalPaths == nil {
		t.Error("CriticalPaths should not be nil")
	}
}

func TestTopologyHandler_Empty(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}))

	_, out, err := s.topology(ctx, nil, TopologyIn{Window: "1h"})
	if err != nil {
		t.Fatalf("topology() error = %v", err)
	}

	if len(out.Nodes) != 0 {
		t.Errorf("Nodes count = %d, want 0", len(out.Nodes))
	}
	if len(out.Edges) != 0 {
		t.Errorf("Edges count = %d, want 0", len(out.Edges))
	}
	if out.CriticalPaths == nil {
		t.Error("CriticalPaths should not be nil (should be empty slice)")
	}
}

func TestTopologyHandler_InvalidWindow(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	_, _, err := s.topology(ctx, nil, TopologyIn{Window: "banana"})
	if err == nil {
		t.Error("topology() should return error for invalid window")
	}
}

// ---------- Diagnose Handler ----------

func TestDiagnoseHandler(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	// Main metrics query (Diagnose uses QueryRowContext)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(1000), 5.0, 25.0, 50.0, 0.001))

	// Top errors query (operation, message, exception_type, count, trace_id)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"operation", "message", "exception_type", "cnt", "trace_id"}).
			AddRow("GET /api/users", "connection timeout", "ConnectionError", int64(10), "trace-err-1"))

	// Slow ops query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}).
			AddRow("GET /api/heavy", 500.0, int64(100)))

	// Dependencies query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}).
			AddRow("database", int64(500), 10.0, 0.001))

	// Baseline query (DiagnoseEnhanced calls queryBaseline)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(sql.NullFloat64{Float64: 20.0, Valid: true}, int64(5)))

	// Change points query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"}))

	// Correlated logs query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}))

	// Suggested traces query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"tid"}))

	_, out, err := s.diagnose(ctx, nil, DiagnoseIn{Service: "api", Window: "15m"})
	if err != nil {
		t.Fatalf("diagnose() error = %v", err)
	}

	if out.Service != "api" {
		t.Errorf("Service = %q, want %q", out.Service, "api")
	}
	if out.Status != "healthy" {
		t.Errorf("Status = %q, want %q", out.Status, "healthy")
	}
	if out.Metrics.P50Ms != 5.0 {
		t.Errorf("Metrics.P50Ms = %f, want 5.0", out.Metrics.P50Ms)
	}
	if out.Metrics.P95Ms != 25.0 {
		t.Errorf("Metrics.P95Ms = %f, want 25.0", out.Metrics.P95Ms)
	}
	if out.Metrics.P99Ms != 50.0 {
		t.Errorf("Metrics.P99Ms = %f, want 50.0", out.Metrics.P99Ms)
	}
	if out.Metrics.Count != 1000 {
		t.Errorf("Metrics.Count = %d, want 1000", out.Metrics.Count)
	}
	if out.Metrics.ErrorRate != 0.001 {
		t.Errorf("Metrics.ErrorRate = %f, want 0.001", out.Metrics.ErrorRate)
	}

	// Verify top errors mapped correctly
	if len(out.TopErrors) != 1 {
		t.Errorf("TopErrors count = %d, want 1", len(out.TopErrors))
	} else {
		if out.TopErrors[0].Message != "connection timeout" {
			t.Errorf("TopErrors[0].Message = %q", out.TopErrors[0].Message)
		}
		if out.TopErrors[0].Count != 10 {
			t.Errorf("TopErrors[0].Count = %d, want 10", out.TopErrors[0].Count)
		}
		if out.TopErrors[0].ExampleTrace != "trace-err-1" {
			t.Errorf("TopErrors[0].ExampleTrace = %q", out.TopErrors[0].ExampleTrace)
		}
	}

	// Verify slow ops mapped correctly
	if len(out.SlowOperations) != 1 {
		t.Errorf("SlowOperations count = %d, want 1", len(out.SlowOperations))
	} else if out.SlowOperations[0].Name != "GET /api/heavy" {
		t.Errorf("SlowOperations[0].Name = %q", out.SlowOperations[0].Name)
	}

	// Verify dependencies mapped correctly
	if len(out.Dependencies) != 1 {
		t.Errorf("Dependencies count = %d, want 1", len(out.Dependencies))
	} else {
		if out.Dependencies[0].Service != "database" {
			t.Errorf("Dependencies[0].Service = %q", out.Dependencies[0].Service)
		}
		if out.Dependencies[0].Calls != 500 {
			t.Errorf("Dependencies[0].Calls = %d, want 500", out.Dependencies[0].Calls)
		}
	}

	// Verify baseline was mapped
	if out.Metrics.ComparisonToBaseline == nil {
		t.Error("ComparisonToBaseline should not be nil when baseline data exists")
	} else if out.Metrics.ComparisonToBaseline.BaselineP95Ms != 20.0 {
		t.Errorf("BaselineP95Ms = %f, want 20.0", out.Metrics.ComparisonToBaseline.BaselineP95Ms)
	}
}

func TestDiagnoseHandler_MissingService(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	_, _, err := s.diagnose(ctx, nil, DiagnoseIn{Service: "", Window: "15m"})
	if err == nil {
		t.Error("diagnose() should return error when service is empty")
	}
}

func TestDiagnoseHandler_DegradedService(t *testing.T) {
	s, mock := newTestServer(t)
	ctx := context.Background()

	// Main metrics - degraded (high latency)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(500), 100.0, 1500.0, 3000.0, 0.02))

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"operation", "message", "exception_type", "cnt", "trace_id"}))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"op", "p95", "cnt"}))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

	// Baseline
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(sql.NullFloat64{}, int64(0)))

	// Change points
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"}))

	// Correlated logs
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}))

	// Suggested traces
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"tid"}))

	_, out, err := s.diagnose(ctx, nil, DiagnoseIn{Service: "slow-svc", Window: "15m"})
	if err != nil {
		t.Fatalf("diagnose() error = %v", err)
	}

	if out.Status != "degraded" {
		t.Errorf("Status = %q, want %q", out.Status, "degraded")
	}
	if out.Metrics.P95Ms != 1500.0 {
		t.Errorf("Metrics.P95Ms = %f, want 1500.0", out.Metrics.P95Ms)
	}
}

func TestDiagnoseHandler_InvalidWindow(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	_, _, err := s.diagnose(ctx, nil, DiagnoseIn{Service: "api", Window: "banana"})
	if err == nil {
		t.Error("diagnose() should return error for invalid window")
	}
}
