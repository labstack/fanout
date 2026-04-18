package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/query"
)

func TestHome_ReturnsServicesAndSummary(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	tracker := NewIncidentTracker()

	// Pre-tick the tracker so the degraded service has an open incident.
	tracker.Tick("svc-degraded", "degraded", 0.05, 1500.0, time.Now().Add(-2*time.Minute))
	tracker.Tick("svc-degraded", "degraded", 0.05, 1500.0, time.Now())

	// Main rollup query: 1 healthy + 1 degraded service.
	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-healthy", int64(1000), 10.0, 100.0, 0.001). // healthy: low error, fast
		AddRow("svc-degraded", int64(500), 50.0, 1500.0, 0.05)  // degraded: p95 > 1000ms + error > 1%

	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)

	// Sparklines query (for all services).
	sparkRows := sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}).
		AddRow("svc-healthy", time.Now().Add(-2*time.Minute), int64(100), 0.001).
		AddRow("svc-healthy", time.Now().Add(-1*time.Minute), int64(110), 0.001).
		AddRow("svc-degraded", time.Now().Add(-2*time.Minute), int64(50), 0.05).
		AddRow("svc-degraded", time.Now().Add(-1*time.Minute), int64(60), 0.06)

	mock.ExpectQuery("SELECT").WillReturnRows(sparkRows)

	// Top errors query (for degraded services only).
	errRows := sqlmock.NewRows([]string{"service", "message", "cnt"}).
		AddRow("svc-degraded", "connection timeout", int64(42)).
		AddRow("svc-degraded", "bad gateway", int64(10))

	mock.ExpectQuery("SELECT").WillReturnRows(errRows)

	result, err := svc.Home(context.Background(), 60, "", "", tracker)
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	if result == nil {
		t.Fatal("Home() returned nil")
	}

	// Verify summary counts.
	if result.Summary.TotalServices != 2 {
		t.Errorf("Summary.TotalServices = %d, want 2", result.Summary.TotalServices)
	}
	if result.Summary.Healthy != 1 {
		t.Errorf("Summary.Healthy = %d, want 1", result.Summary.Healthy)
	}
	if result.Summary.Degraded != 1 {
		t.Errorf("Summary.Degraded = %d, want 1", result.Summary.Degraded)
	}
	if result.Summary.Unhealthy != 0 {
		t.Errorf("Summary.Unhealthy = %d, want 0", result.Summary.Unhealthy)
	}

	// Verify incidents list has the degraded service.
	if len(result.Incidents) != 1 {
		t.Fatalf("Incidents count = %d, want 1", len(result.Incidents))
	}
	inc := result.Incidents[0]
	if inc.Service != "svc-degraded" {
		t.Errorf("Incident.Service = %q, want %q", inc.Service, "svc-degraded")
	}
	if inc.Health != "degraded" {
		t.Errorf("Incident.Health = %q, want %q", inc.Health, "degraded")
	}
	if len(inc.TopErrors) == 0 {
		t.Error("Incident.TopErrors should not be empty")
	} else if inc.TopErrors[0].Message != "connection timeout" {
		t.Errorf("TopErrors[0].Message = %q, want %q", inc.TopErrors[0].Message, "connection timeout")
	}

	// Verify healthy services list has the healthy service.
	if len(result.Services) != 1 {
		t.Fatalf("Services count = %d, want 1", len(result.Services))
	}
	if result.Services[0].Name != "svc-healthy" {
		t.Errorf("Services[0].Name = %q, want %q", result.Services[0].Name, "svc-healthy")
	}

	// Verify traffic per minute is computed.
	if result.Summary.TrafficPerMin <= 0 {
		t.Errorf("Summary.TrafficPerMin = %v, want > 0", result.Summary.TrafficPerMin)
	}

	// Verify sparklines are populated.
	if len(result.Services[0].SparklineTraffic) == 0 {
		t.Error("healthy service SparklineTraffic should not be empty")
	}
	if len(inc.SparklineErrRate) == 0 {
		t.Error("incident SparklineErrRate should not be empty")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestHome_EmptyState(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	tracker := NewIncidentTracker()

	// Main rollup query returns no rows.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}))

	result, err := svc.Home(context.Background(), 60, "", "", tracker)
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	if result == nil {
		t.Fatal("Home() returned nil")
	}

	// All counts should be zero.
	if result.Summary.TotalServices != 0 {
		t.Errorf("Summary.TotalServices = %d, want 0", result.Summary.TotalServices)
	}
	if result.Summary.Healthy != 0 {
		t.Errorf("Summary.Healthy = %d, want 0", result.Summary.Healthy)
	}
	if result.Summary.Degraded != 0 {
		t.Errorf("Summary.Degraded = %d, want 0", result.Summary.Degraded)
	}
	if result.Summary.Unhealthy != 0 {
		t.Errorf("Summary.Unhealthy = %d, want 0", result.Summary.Unhealthy)
	}

	// Slices should be non-nil but empty.
	if result.Incidents == nil {
		t.Error("Incidents should be non-nil")
	}
	if len(result.Incidents) != 0 {
		t.Errorf("Incidents count = %d, want 0", len(result.Incidents))
	}
	if result.Services == nil {
		t.Error("Services should be non-nil")
	}
	if len(result.Services) != 0 {
		t.Errorf("Services count = %d, want 0", len(result.Services))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestHome_DefaultWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}))

	// Pass window <= 0, should default to 60.
	result, err := svc.Home(context.Background(), 0, "", "", nil)
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	if result == nil {
		t.Fatal("Home() returned nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestHome_UnhealthyService(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// One unhealthy service (high error rate).
	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("bad-svc", int64(100), 50.0, 200.0, 0.15) // unhealthy: error > 10%

	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)

	// Sparklines.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))

	// Top errors for unhealthy service.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "message", "cnt"}))

	result, err := svc.Home(context.Background(), 60, "", "", nil)
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}

	if result.Summary.Unhealthy != 1 {
		t.Errorf("Summary.Unhealthy = %d, want 1", result.Summary.Unhealthy)
	}
	if len(result.Incidents) != 1 {
		t.Fatalf("Incidents count = %d, want 1", len(result.Incidents))
	}
	if result.Incidents[0].Health != "unhealthy" {
		t.Errorf("Incident.Health = %q, want %q", result.Incidents[0].Health, "unhealthy")
	}
	// Unhealthy service should not appear in healthy services list.
	if len(result.Services) != 0 {
		t.Errorf("Services count = %d, want 0 (unhealthy should not be in healthy list)", len(result.Services))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestHome_SortIncidentsWorstFirst(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Two degraded services with different severity.
	// svc-worse has higher error rate so lower health score.
	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-bad", int64(100), 50.0, 200.0, 0.15).   // unhealthy, score = 0.0*0.4+1.0*0.3+1.0*0.3 = 0.6
		AddRow("svc-worse", int64(100), 50.0, 6000.0, 0.15) // unhealthy, score = 0.0*0.4+0.0*0.3+1.0*0.3 = 0.3

	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)

	// Sparklines.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))

	// Top errors.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "message", "cnt"}))

	result, err := svc.Home(context.Background(), 60, "", "", nil)
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}

	if len(result.Incidents) != 2 {
		t.Fatalf("Incidents count = %d, want 2", len(result.Incidents))
	}

	// svc-worse should appear first (lowest score = worst first).
	if result.Incidents[0].Service != "svc-worse" {
		t.Errorf("Incidents[0].Service = %q, want %q (worst first)", result.Incidents[0].Service, "svc-worse")
	}
	if result.Incidents[1].Service != "svc-bad" {
		t.Errorf("Incidents[1].Service = %q, want %q", result.Incidents[1].Service, "svc-bad")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestHome_NilTracker(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-a", int64(200), 10.0, 80.0, 0.001)

	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))

	// No top errors query — no degraded/unhealthy services.

	result, err := svc.Home(context.Background(), 60, "", "", nil)
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	if result == nil {
		t.Fatal("Home() returned nil")
	}
	if result.Summary.TotalServices != 1 {
		t.Errorf("TotalServices = %d, want 1", result.Summary.TotalServices)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestHome_UsesCachedSnapshot(t *testing.T) {
	cacheCtx, cancel := context.WithCancel(context.Background())
	query.InitQueryCache(cacheCtx)
	t.Cleanup(func() {
		cancel()
		query.QueryCache = nil
	})

	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-a", int64(200), 10.0, 80.0, 0.001)
	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))

	if _, err := svc.Home(context.Background(), 60, "", "", nil); err != nil {
		t.Fatalf("first Home() error = %v", err)
	}
	if _, err := svc.Home(context.Background(), 60, "", "", nil); err != nil {
		t.Fatalf("second Home() error = %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
