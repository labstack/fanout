package service

import (
	"context"
	"errors"
	"testing"
	"time"

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
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

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
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

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
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

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
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

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
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

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

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

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
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}).
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
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))

	// This should be escaped properly
	result, err := svc.Diagnose(context.Background(), "service'; DROP TABLE--", 15, "", "")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if result.Service != "service'; DROP TABLE--" {
		t.Errorf("Service name not preserved: %q", result.Service)
	}
}

// --- DiagnoseEnhanced tests ---

// expectDiagnoseQueries sets up the 4 standard Diagnose mock queries.
func expectDiagnoseQueries(mock sqlmock.Sqlmock, spanCount int64, p50, p95, p99, errorRate float64) {
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"cnt", "p50", "p95", "p99", "error_rate"}).
			AddRow(spanCount, p50, p95, p99, errorRate))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"msg", "cnt", "trace_id"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"op", "p95", "cnt"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"dep_service", "calls", "avg_ms", "error_rate"}))
}

func TestDiagnoseEnhanced_SymptomAuto_Latency(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// High p95 -> symptom = latency
	expectDiagnoseQueries(mock, 100, 100.0, 6000.0, 12000.0, 0.01)

	// Baseline query: fewer than 3 days of data -> omit baseline
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(nil, int64(1)))

	// Change point query: no rollup data
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"}))

	// Log correlation: no logs
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}))

	result, err := svc.DiagnoseEnhanced(context.Background(), "slow-svc", 15, "auto", "", "")
	if err != nil {
		t.Fatalf("DiagnoseEnhanced() error = %v", err)
	}

	if result.SymptomDetected != "latency" {
		t.Errorf("SymptomDetected = %q, want %q", result.SymptomDetected, "latency")
	}
	// Baseline should be nil (only 1 day of data)
	if result.Baseline != nil {
		t.Errorf("Baseline should be nil with insufficient data, got %+v", result.Baseline)
	}
}

func TestDiagnoseEnhanced_SymptomAuto_Errors(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// High error rate -> symptom = errors
	expectDiagnoseQueries(mock, 100, 10.0, 50.0, 100.0, 0.15)

	// Baseline query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(nil, int64(0)))

	// Change point query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"}))

	// Log correlation
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}))

	result, err := svc.DiagnoseEnhanced(context.Background(), "error-svc", 15, "auto", "", "")
	if err != nil {
		t.Fatalf("DiagnoseEnhanced() error = %v", err)
	}

	if result.SymptomDetected != "errors" {
		t.Errorf("SymptomDetected = %q, want %q", result.SymptomDetected, "errors")
	}
}

func TestDiagnoseEnhanced_ExplicitSymptom(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	expectDiagnoseQueries(mock, 500, 10.0, 50.0, 100.0, 0.01)

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(nil, int64(0)))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"}))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}))

	result, err := svc.DiagnoseEnhanced(context.Background(), "my-svc", 15, "throughput_drop", "", "")
	if err != nil {
		t.Fatalf("DiagnoseEnhanced() error = %v", err)
	}

	// Explicit symptom overrides auto detection
	if result.SymptomDetected != "throughput_drop" {
		t.Errorf("SymptomDetected = %q, want %q", result.SymptomDetected, "throughput_drop")
	}
}

func TestDiagnoseEnhanced_WithBaseline(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	expectDiagnoseQueries(mock, 100, 100.0, 9450.0, 18000.0, 0.01)

	// Baseline query: 5 days of data, baseline p95 = 500ms
	baselineP95 := 500.0
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(baselineP95, int64(5)))

	// Change point query: no buckets
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"}))

	// Log correlation
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}))

	result, err := svc.DiagnoseEnhanced(context.Background(), "checkout", 15, "latency", "", "")
	if err != nil {
		t.Fatalf("DiagnoseEnhanced() error = %v", err)
	}

	if result.Baseline == nil {
		t.Fatal("Baseline should not be nil with sufficient data")
	}
	if result.Baseline.BaselineP95Ms != 500.0 {
		t.Errorf("BaselineP95Ms = %f, want 500.0", result.Baseline.BaselineP95Ms)
	}
	if result.Baseline.BaselineWindow != "7d" {
		t.Errorf("BaselineWindow = %q, want %q", result.Baseline.BaselineWindow, "7d")
	}
}

func TestDiagnoseEnhanced_WithLogPatterns(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	expectDiagnoseQueries(mock, 100, 10.0, 50.0, 100.0, 0.01)

	// Baseline
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(nil, int64(0)))

	// Change point: no data
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"}))

	// Log correlation: return some patterns
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}).
			AddRow("Batch size exceeds threshold", "WARN", int64(12)).
			AddRow("Connection pool exhausted", "ERROR", int64(3)))

	result, err := svc.DiagnoseEnhanced(context.Background(), "worker", 15, "auto", "", "")
	if err != nil {
		t.Fatalf("DiagnoseEnhanced() error = %v", err)
	}

	if len(result.CorrelatedLogPatterns) != 2 {
		t.Errorf("CorrelatedLogPatterns count = %d, want 2", len(result.CorrelatedLogPatterns))
	}
	if result.CorrelatedLogPatterns[0].Pattern != "Batch size exceeds threshold" {
		t.Errorf("Pattern[0] = %q", result.CorrelatedLogPatterns[0].Pattern)
	}
	if result.CorrelatedLogPatterns[0].Severity != "WARN" {
		t.Errorf("Severity[0] = %q, want WARN", result.CorrelatedLogPatterns[0].Severity)
	}
	if result.CorrelatedLogPatterns[0].Count != 12 {
		t.Errorf("Count[0] = %d, want 12", result.CorrelatedLogPatterns[0].Count)
	}
}

func TestDiagnoseEnhanced_EmptyService(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.DiagnoseEnhanced(context.Background(), "", 15, "auto", "", "")
	if err == nil {
		t.Error("DiagnoseEnhanced() should return error for empty service")
	}
}

// --- detectSymptom tests ---

func TestDetectSymptom(t *testing.T) {
	tests := []struct {
		name      string
		errorRate float64
		p95Ms     float64
		spanCount int64
		want      string
	}{
		{"high errors", 0.15, 100.0, 500, "errors"},
		{"high latency", 0.01, 6000.0, 100, "latency"},
		{"no spans", 0.0, 0.0, 0, "throughput_drop"},
		{"normal -> latency", 0.01, 300.0, 100, "latency"},
		{"error just above threshold", 0.11, 100.0, 100, "errors"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectSymptom(tc.errorRate, tc.p95Ms, tc.spanCount)
			if got != tc.want {
				t.Errorf("detectSymptom(%v, %v, %v) = %q, want %q", tc.errorRate, tc.p95Ms, tc.spanCount, got, tc.want)
			}
		})
	}
}

// --- meanStddev tests ---

func TestMeanStddev(t *testing.T) {
	mean, stddev := meanStddev([]float64{2, 4, 4, 4, 5, 5, 7, 9})
	wantMean := 5.0
	if mean != wantMean {
		t.Errorf("mean = %f, want %f", mean, wantMean)
	}
	wantStddev := 2.0
	if stddev != wantStddev {
		t.Errorf("stddev = %f, want %f", stddev, wantStddev)
	}
}

func TestMeanStddev_Empty(t *testing.T) {
	mean, stddev := meanStddev(nil)
	if mean != 0 || stddev != 0 {
		t.Errorf("meanStddev(nil) = (%f, %f), want (0, 0)", mean, stddev)
	}
}

func TestMeanStddev_Flat(t *testing.T) {
	mean, stddev := meanStddev([]float64{5, 5, 5, 5})
	if mean != 5.0 {
		t.Errorf("mean = %f, want 5.0", mean)
	}
	if stddev != 0.0 {
		t.Errorf("stddev = %f, want 0.0", stddev)
	}
}

func TestDiagnoseEnhanced_WithChangePoints(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// 4 base Diagnose queries
	expectDiagnoseQueries(mock, 100, 100.0, 6000.0, 12000.0, 0.01)

	// Baseline query: insufficient data
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"baseline_p95", "day_count"}).
			AddRow(nil, int64(0)))

	// Change-point query: 10 rollup buckets with a clear latency spike.
	// First 9 buckets at p95=50ms, last 1 at p95=100000ms.
	// The 2-sigma detection requires: delta > 2*stddev AND series[i] > mean+2*stddev.
	// With 9 baseline values the spike must be extreme to exceed the inflated threshold.
	now := time.Now()
	cpRows := sqlmock.NewRows([]string{"bucket", "p95_ms", "error_rate", "spans"})
	for i := 9; i >= 0; i-- {
		bucket := now.Add(-time.Duration(i) * time.Minute)
		if i >= 1 {
			// First 9 buckets: low latency
			cpRows.AddRow(bucket, 50.0, 0.01, int64(100))
		} else {
			// Last bucket: extreme latency spike
			cpRows.AddRow(bucket, 100000.0, 0.01, int64(100))
		}
	}
	mock.ExpectQuery("SELECT").WillReturnRows(cpRows)

	// Log correlation query: no logs
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"pattern", "severity", "cnt"}))

	result, err := svc.DiagnoseEnhanced(context.Background(), "spike-svc", 15, "auto", "", "")
	if err != nil {
		t.Fatalf("DiagnoseEnhanced() error = %v", err)
	}

	if len(result.ChangePoints) == 0 {
		t.Error("expected at least one change point from the latency spike, got 0")
	}
}
