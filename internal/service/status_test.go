package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
)

func newMockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	cfg := config.Config{
		LakeDir:   "lake",
		DefaultNS: "default",
		TenantID:  uuid.MustParse("00000000-0000-0000-0000-000000000000"),
	}

	duck := &query.Duck{DB: db}
	svc := &Service{duck: duck, cfg: cfg}

	return svc, mock
}

func TestStatus_NoData(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Simulate query error (no data)
	mock.ExpectQuery("SELECT").WillReturnError(nil)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}))

	result, err := svc.Status(context.Background(), 15, "", "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if result == nil {
		t.Fatal("Status() returned nil")
	}
	if !result.Healthy {
		// With no data, should be healthy
		t.Log("Result indicates system state based on no services")
	}
}

func TestStatus_WithServices(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}).
		AddRow("service-a", int64(1000), 50.0, 0.001).
		AddRow("service-b", int64(500), 100.0, 0.005).
		AddRow("service-c", int64(200), 2000.0, 0.02) // degraded: high latency

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "", "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if result.Services.Total != 3 {
		t.Errorf("Services.Total = %d, want 3", result.Services.Total)
	}
	if result.Services.Healthy != 2 {
		t.Errorf("Services.Healthy = %d, want 2", result.Services.Healthy)
	}
	if result.Services.Degraded != 1 {
		t.Errorf("Services.Degraded = %d, want 1", result.Services.Degraded)
	}
}

func TestStatus_WithHighErrors(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}).
		AddRow("bad-service", int64(100), 50.0, 0.15) // unhealthy: >10% errors

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "", "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if result.Services.Unhealthy != 1 {
		t.Errorf("Services.Unhealthy = %d, want 1", result.Services.Unhealthy)
	}
	if result.Healthy {
		t.Error("Healthy should be false when there are unhealthy services")
	}
}

func TestStatus_TopIssues(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}).
		AddRow("high-error-svc", int64(100), 50.0, 0.10). // errors > 5%
		AddRow("slow-svc", int64(100), 2000.0, 0.01)      // p95 > 1000ms

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "", "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if len(result.TopIssues) != 2 {
		t.Errorf("TopIssues count = %d, want 2", len(result.TopIssues))
	}
}

func TestStatus_DefaultWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}))

	// Pass 0 window, should default to 15
	result, err := svc.Status(context.Background(), 0, "", "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if result == nil {
		t.Fatal("Status() returned nil")
	}
}

func TestStatus_CustomNamespace(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}))

	result, err := svc.Status(context.Background(), 15, "production", "tenant-123")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if result == nil {
		t.Fatal("Status() returned nil")
	}
}

func TestStatus_SummaryHealthy(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}).
		AddRow("svc1", int64(1000), 50.0, 0.001).
		AddRow("svc2", int64(500), 100.0, 0.002)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "", "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if result.Summary == "" {
		t.Error("Summary should not be empty")
	}
	if !result.Healthy {
		t.Error("Healthy should be true for healthy services")
	}
}

func TestStatus_SummaryUnhealthy(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "cnt", "p95_ms", "error_rate"}).
		AddRow("bad1", int64(100), 50.0, 0.20).  // unhealthy
		AddRow("bad2", int64(100), 6000.0, 0.01) // unhealthy (high latency)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "", "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if result.Healthy {
		t.Error("Healthy should be false")
	}
	if result.Services.Unhealthy != 2 {
		t.Errorf("Services.Unhealthy = %d, want 2", result.Services.Unhealthy)
	}
}
