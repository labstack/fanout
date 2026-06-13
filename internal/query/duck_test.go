package query

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_merge_adjacent_files('lake')")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_expire_snapshots('lake', older_than => now())")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_cleanup_old_files('lake', cleanup_all => true)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
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

// A merge failure must not stop snapshot expiry, file cleanup, or the
// checkpoint — each compaction step degrades independently.
func TestRunMaintenanceContinuesAfterCompactionFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db, cfg: env.Config{}}

	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_merge_adjacent_files('lake')")).
		WillReturnError(errors.New("merge failed"))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_expire_snapshots('lake', older_than => now())")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_cleanup_old_files('lake', cleanup_all => true)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CHECKPOINT")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = d.runMaintenance(context.Background())
	if err == nil {
		t.Fatal("runMaintenance() error = nil, want merge error surfaced")
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
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1 when DuckDBMaxConns is unset (floored for DuckLake serialization)", stats.MaxOpenConnections)
	}
}

func TestDuckDSN(t *testing.T) {
	tests := []struct {
		name    string
		mem     string
		threads int
		want    string
		wantErr bool
	}{
		{name: "zero values leave DuckDB defaults", mem: "", threads: 0, want: "/x/cache.duckdb"},
		{name: "explicit memory cap", mem: "8GB", want: "/x/cache.duckdb?memory_limit=8GB"},
		{name: "explicit threads cap", threads: 4, want: "/x/cache.duckdb?threads=4"},
		{name: "both caps", mem: "8GB", threads: 4, want: "/x/cache.duckdb?threads=4&memory_limit=8GB"},
		{name: "negative threads ignored", threads: -2, want: "/x/cache.duckdb"},
		{name: "rejects DSN metacharacters", mem: "8GB&allow_unsigned_extensions=true", wantErr: true},
		{name: "rejects quotes", mem: "8GB'", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := duckDSN("/x/cache.duckdb", tt.mem, tt.threads)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("duckDSN(%q) error = nil, want error", tt.mem)
				}
				return
			}
			if err != nil {
				t.Fatalf("duckDSN(%q) error = %v", tt.mem, err)
			}
			if got != tt.want {
				t.Errorf("duckDSN(%q) = %q, want %q", tt.mem, got, tt.want)
			}
		})
	}
}

func TestDuckDBPoolSizeConfigurable(t *testing.T) {
	if got := duckDBPoolSize(env.Config{}); got != 1 {
		t.Errorf("default pool size = %d, want 1", got)
	}
	if got := duckDBPoolSize(env.Config{DuckDBMaxConns: 0}); got != 1 {
		t.Errorf("zero pool size = %d, want floored to 1", got)
	}
	if got := duckDBPoolSize(env.Config{DuckDBMaxConns: -5}); got != 1 {
		t.Errorf("negative pool size = %d, want floored to 1", got)
	}
	if got := duckDBPoolSize(env.Config{DuckDBMaxConns: 8}); got != 8 {
		t.Errorf("configured pool size = %d, want 8", got)
	}
}
