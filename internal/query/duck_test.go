package query

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/env"
)

func TestLatencyRowStruct(t *testing.T) {
	r := LatencyRow{
		ServiceName: "api-gateway",
		P95Ms:       45.5,
		ErrorRate:   0.01,
		Spans:       1000,
	}

	if r.ServiceName != "api-gateway" {
		t.Errorf("ServiceName = %q", r.ServiceName)
	}
	if r.P95Ms != 45.5 {
		t.Errorf("P95Ms = %f", r.P95Ms)
	}
	if r.ErrorRate != 0.01 {
		t.Errorf("ErrorRate = %f", r.ErrorRate)
	}
	if r.Spans != 1000 {
		t.Errorf("Spans = %d", r.Spans)
	}
}

func TestLogsSampleStruct(t *testing.T) {
	s := LogsSample{
		TS:   "2024-01-15T10:30:00Z",
		Body: "Connection failed",
		Svc:  "user-service",
		Sev:  "ERROR",
	}

	if s.TS != "2024-01-15T10:30:00Z" {
		t.Errorf("TS = %q", s.TS)
	}
	if s.Body != "Connection failed" {
		t.Errorf("Body = %q", s.Body)
	}
	if s.Svc != "user-service" {
		t.Errorf("Svc = %q", s.Svc)
	}
	if s.Sev != "ERROR" {
		t.Errorf("Sev = %q", s.Sev)
	}
}

func TestThroughputRowStruct(t *testing.T) {
	r := ThroughputRow{
		Bucket: "2024-01-15T10:00:00Z",
		Spans:  5000,
	}

	if r.Bucket != "2024-01-15T10:00:00Z" {
		t.Errorf("Bucket = %q", r.Bucket)
	}
	if r.Spans != 5000 {
		t.Errorf("Spans = %d", r.Spans)
	}
}

func TestServiceThroughputRowStruct(t *testing.T) {
	r := ServiceThroughputRow{
		Service:        "checkout",
		SpansPerMinute: 120,
	}

	if r.Service != "checkout" {
		t.Errorf("Service = %q", r.Service)
	}
	if r.SpansPerMinute != 120 {
		t.Errorf("SpansPerMinute = %d", r.SpansPerMinute)
	}
}

func TestErrorRouteStruct(t *testing.T) {
	r := ErrorRoute{
		Route: "POST /api/orders",
		Count: 42,
	}

	if r.Route != "POST /api/orders" {
		t.Errorf("Route = %q", r.Route)
	}
	if r.Count != 42 {
		t.Errorf("Count = %d", r.Count)
	}
}

func TestErrorRouteRowStruct(t *testing.T) {
	r := ErrorRouteRow{
		ServiceName: "payment-service",
		Name:        "process_payment",
		Errors:      10,
		ErrorRate:   0.05,
	}

	if r.ServiceName != "payment-service" {
		t.Errorf("ServiceName = %q", r.ServiceName)
	}
	if r.Name != "process_payment" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Errors != 10 {
		t.Errorf("Errors = %d", r.Errors)
	}
	if r.ErrorRate != 0.05 {
		t.Errorf("ErrorRate = %f", r.ErrorRate)
	}
}

func TestParquetGlob_DayLevel(t *testing.T) {
	lakeDir, err := os.MkdirTemp("", "fanout-parquetglob-dayLevel-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	// Create files spanning 2 days
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	for _, d := range []time.Time{now, yesterday} {
		dir := filepath.Join(
			lakeDir,
			"spans",
			"tenant=test-tenant",
			"namespace=default",
			d.Format("year=2006"),
			d.Format("month=01"),
			d.Format("day=02"),
			d.Format("hour=15"),
		)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "part-1.parquet"), []byte("test"), 0o644)
	}

	// Use window > 1440 to trigger day-level glob
	glob := ParquetGlob(lakeDir, "spans", "test-tenant", "default", 2880) // 48 hours
	if glob == "" {
		t.Error("ParquetGlob returned empty string")
	}
}

func TestRunMaintenanceContinuesAfterDeleteFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{
		DB:  db,
		cfg: env.Config{RetentionDays: 7},
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM lake.spans WHERE start_time < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 11))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM lake.logs WHERE log_time < now() - INTERVAL 7 DAY")).
		WillReturnError(errors.New("log delete failed"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM lake.metrics WHERE metric_time < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM service_rollup WHERE bucket < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM edge_rollup WHERE bucket < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta("CHECKPOINT")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = d.runMaintenance(context.Background())
	if err == nil {
		t.Fatal("runMaintenance() error = nil, want joined error")
	}
	if d.lastMaintenance.IsZero() {
		t.Fatal("runMaintenance() did not update lastMaintenance")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestNewDuckUsesSingleConnectionPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := env.Config{
		DataDir:      t.TempDir(),
		RollupEvery:  60,
		DuckDBMemory: "128MB",
		TenantID:     uuid.Nil,
	}

	d, err := NewDuck(ctx, cfg)
	if err != nil {
		if strings.Contains(err.Error(), "LOAD ducklake") ||
			strings.Contains(err.Error(), "LOAD sqlite") ||
			strings.Contains(err.Error(), "ATTACH") {
			t.Skipf("DuckLake extensions unavailable: %v", err)
		}
		t.Fatalf("NewDuck() error = %v", err)
	}
	defer d.Close()

	stats := d.DB.Stats()
	if stats.MaxOpenConnections != duckDBPoolSize {
		t.Fatalf("MaxOpenConnections = %d, want %d for DuckLake serialization", stats.MaxOpenConnections, duckDBPoolSize)
	}
}
