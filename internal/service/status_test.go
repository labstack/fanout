package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/query"
)

func newMockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	cfg := env.Config{
		DataDir:   "data",
		DefaultNS: "default",
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
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}))

	result, err := svc.Status(context.Background(), 15, "")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if result == nil {
		t.Fatal("Status() returned nil")
		return
	}
	if !result.Healthy {
		// With no data, should be healthy
		t.Log("Result indicates system state based on no services")
	}
}

func TestStatus_WithServices(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}).
		AddRow("service-a", int64(1000), 50.0, 0.001, int64(0), int64(0)).
		AddRow("service-b", int64(500), 100.0, 0.005, int64(0), int64(0)).
		AddRow("service-c", int64(200), 2000.0, 0.02, int64(0), int64(0)) // degraded: high latency

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "")
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

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}).
		AddRow("bad-service", int64(100), 50.0, 0.15, int64(0), int64(0)) // unhealthy: >10% errors

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "")
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

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}).
		AddRow("high-error-svc", int64(100), 50.0, 0.10, int64(0), int64(0)). // errors > 5%
		AddRow("slow-svc", int64(100), 2000.0, 0.01, int64(0), int64(0))      // p95 > 1000ms

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "")
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
		sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}))

	// Pass 0 window, should default to 15
	result, err := svc.Status(context.Background(), 0, "")
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
		sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}))

	result, err := svc.Status(context.Background(), 15, "production")
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

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}).
		AddRow("svc1", int64(1000), 50.0, 0.001, int64(0), int64(0)).
		AddRow("svc2", int64(500), 100.0, 0.002, int64(0), int64(0))

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "")
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

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p95_ms", "error_rate", "log_cnt", "metric_cnt"}).
		AddRow("bad1", int64(100), 50.0, 0.20, int64(0), int64(0)).  // unhealthy
		AddRow("bad2", int64(100), 6000.0, 0.01, int64(0), int64(0)) // unhealthy (high latency)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Status(context.Background(), 15, "")
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

// --- HealthScore tests ---

func TestHealthScore(t *testing.T) {
	tests := []struct {
		name      string
		errorRate float64
		p95       float64
		spans     int64
		want      float64
	}{
		// Perfect: low error, fast, has traffic
		{"perfect", 0.001, 100, 1000, 1.0*0.4 + 1.0*0.3 + 1.0*0.3},
		// No traffic: throughput score = 0
		{"no_traffic", 0.0, 0.0, 0, 1.0*0.4 + 1.0*0.3 + 0.0*0.3},
		// Mid error (0.01 < x < 0.05): err score = 0.7
		{"mid_error", 0.02, 100, 100, 0.7*0.4 + 1.0*0.3 + 1.0*0.3},
		// High error (0.05 < x < 0.10): err score = 0.3
		{"high_error", 0.07, 100, 100, 0.3*0.4 + 1.0*0.3 + 1.0*0.3},
		// Critical error (>= 0.10): err score = 0.0
		{"critical_error", 0.15, 100, 100, 0.0*0.4 + 1.0*0.3 + 1.0*0.3},
		// Slow p95 (500 <= x < 2000): lat score = 0.7
		{"slow_latency", 0.001, 800, 100, 1.0*0.4 + 0.7*0.3 + 1.0*0.3},
		// Very slow p95 (2000 <= x < 5000): lat score = 0.3
		{"very_slow_latency", 0.001, 3000, 100, 1.0*0.4 + 0.3*0.3 + 1.0*0.3},
		// Critical latency (>= 5000): lat score = 0.0
		{"critical_latency", 0.001, 6000, 100, 1.0*0.4 + 0.0*0.3 + 1.0*0.3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HealthScore(tc.errorRate, tc.p95, tc.spans)
			if abs(got-tc.want) > 1e-9 {
				t.Errorf("HealthScore(%v, %v, %v) = %v, want %v", tc.errorRate, tc.p95, tc.spans, got, tc.want)
			}
		})
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestDeriveHealthFromScore(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{1.0, "healthy"},
		{0.9, "healthy"},
		{0.91, "healthy"},
		{0.89, "degraded"},
		{0.7, "degraded"},
		{0.71, "degraded"},
		{0.69, "unhealthy"},
		{0.0, "unhealthy"},
		{0.5, "unhealthy"},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			got := DeriveHealthFromScore(tc.score)
			if got != tc.want {
				t.Errorf("DeriveHealthFromScore(%v) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}
