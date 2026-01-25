package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDiagnose_EmptyService(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.Diagnose(context.Background(), "", 15, "", "")
	if err == nil {
		t.Error("Diagnose() should return error for empty service")
	}
}

func TestDiagnose_DefaultWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Main metrics query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(100), 10.0, 50.0, 100.0, 0.01))

	// Top errors query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))

	// Slow ops query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))

	// Dependencies query
	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}))

	result, err := svc.Diagnose(context.Background(), "my-service", 0, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if result.Service != "my-service" {
		t.Errorf("Service = %q, want %q", result.Service, "my-service")
	}
}

func TestDiagnose_HealthyService(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Main metrics - healthy
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(1000), 5.0, 25.0, 50.0, 0.001))

	// Top errors - none
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))

	// Slow ops - none
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))

	// Dependencies - none
	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}))

	result, err := svc.Diagnose(context.Background(), "healthy-service", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if result.Status != "healthy" {
		t.Errorf("Status = %q, want %q", result.Status, "healthy")
	}
	if result.P50Ms != 5.0 {
		t.Errorf("P50Ms = %f, want 5.0", result.P50Ms)
	}
	if result.P95Ms != 25.0 {
		t.Errorf("P95Ms = %f, want 25.0", result.P95Ms)
	}
	if result.SpanCount != 1000 {
		t.Errorf("SpanCount = %d, want 1000", result.SpanCount)
	}
}

func TestDiagnose_DegradedService(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Main metrics - degraded (high latency)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(500), 100.0, 1500.0, 3000.0, 0.02))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))
	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}))

	result, err := svc.Diagnose(context.Background(), "slow-service", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if result.Status != "degraded" {
		t.Errorf("Status = %q, want %q", result.Status, "degraded")
	}
}

func TestDiagnose_UnhealthyService(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Main metrics - unhealthy (high error rate)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(100), 10.0, 50.0, 100.0, 0.15))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))
	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}))

	result, err := svc.Diagnose(context.Background(), "bad-service", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if result.Status != "unhealthy" {
		t.Errorf("Status = %q, want %q", result.Status, "unhealthy")
	}
}

func TestDiagnose_WithTopErrors(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(100), 10.0, 50.0, 100.0, 0.05))

	// Top errors with data
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}).
			AddRow("connection timeout", int64(50), "trace-123").
			AddRow("null pointer", int64(25), "trace-456"))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))
	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}))

	result, err := svc.Diagnose(context.Background(), "error-service", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if len(result.TopErrors) != 2 {
		t.Errorf("TopErrors count = %d, want 2", len(result.TopErrors))
	}
	if result.TopErrors[0].Message != "connection timeout" {
		t.Errorf("TopErrors[0].Message = %q, want %q", result.TopErrors[0].Message, "connection timeout")
	}
	if result.TopErrors[0].Count != 50 {
		t.Errorf("TopErrors[0].Count = %d, want 50", result.TopErrors[0].Count)
	}
}

func TestDiagnose_WithSlowOps(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(100), 10.0, 50.0, 100.0, 0.01))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))

	// Slow ops with data
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}).
			AddRow("GET /api/heavy", 500.0, int64(100)).
			AddRow("POST /api/import", 800.0, int64(50)))

	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}))

	result, err := svc.Diagnose(context.Background(), "my-service", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if len(result.SlowOps) != 2 {
		t.Errorf("SlowOps count = %d, want 2", len(result.SlowOps))
	}
	if result.SlowOps[0].Name != "GET /api/heavy" {
		t.Errorf("SlowOps[0].Name = %q", result.SlowOps[0].Name)
	}
}

func TestDiagnose_WithDependencies(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(100), 10.0, 50.0, 100.0, 0.01))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))

	// Dependencies with data
	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}).
			AddRow("database", int64(500), 10.0, 0.001).
			AddRow("cache", int64(1000), 2.0, 0.0).
			AddRow("auth-service", int64(200), 25.0, 0.01))

	result, err := svc.Diagnose(context.Background(), "api-gateway", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if len(result.Dependencies) != 3 {
		t.Errorf("Dependencies count = %d, want 3", len(result.Dependencies))
	}
	if result.Dependencies[0].Service != "database" {
		t.Errorf("Dependencies[0].Service = %q", result.Dependencies[0].Service)
	}
}

func TestDiagnose_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Main query fails
	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	result, err := svc.Diagnose(context.Background(), "failing-service", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	// Should return result with unknown status
	if result.Status != "unknown" {
		t.Errorf("Status = %q, want %q", result.Status, "unknown")
	}
}

func TestDiagnose_SQLEscaping(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Service name with SQL injection attempt
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(int64(10), 5.0, 10.0, 20.0, 0.0))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))
	mock.ExpectQuery("WITH").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "p95", "error_rate"}))

	// This should be escaped properly
	result, err := svc.Diagnose(context.Background(), "service'; DROP TABLE--", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if result.Service != "service'; DROP TABLE--" {
		t.Errorf("Service name not preserved: %q", result.Service)
	}
}
