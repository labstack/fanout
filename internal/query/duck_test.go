package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

func TestNewDuckUsesSingleConnectionPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Config{
		DataDir:        t.TempDir(),
		RollupInterval: time.Minute,
		DuckDBMemory:   "128MB",
	}

	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatalf("open telemetry repository: %v", err)
	}
	defer repository.Close()
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatalf("NewDuck() error = %v", err)
	}
	defer d.Close()

	stats := d.DB.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1 when DuckDBMaxConns is unset", stats.MaxOpenConnections)
	}
}

func TestNewDuckUsesUTCForEveryConnection(t *testing.T) {
	t.Setenv("TZ", "America/Los_Angeles")
	ctx := context.Background()
	cfg := config.Config{
		DataDir:             t.TempDir(),
		RollupInterval:      time.Minute,
		DuckDBMemory:        "128MB",
		DuckDBMaxConns:      4,
		DuckDBThreads:       2,
		MaintenanceInterval: time.Hour,
	}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatalf("open telemetry repository: %v", err)
	}
	defer repository.Close()
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatalf("NewDuck() error = %v", err)
	}
	defer d.Close()
	eventTime := time.Date(2026, 8, 27, 16, 0, 30, 0, time.UTC)
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "timezone-window", Spans: []telemetry.Span{{
		Namespace: "default", TraceID: "trace-timezone", SpanID: "span-timezone",
		ServiceName: "checkout", StartUnixNanos: eventTime.UnixNano(), DurationMS: 5,
		StatusCode: "STATUS_CODE_OK", IngestedAt: eventTime.UnixNano(),
	}}}); err != nil {
		t.Fatalf("commit timezone fixture: %v", err)
	}
	d.rollupLagNanos = 0
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("roll up timezone fixture: %v", err)
	}
	var spans int64
	if err := d.DB.QueryRowContext(ctx, `
SELECT COALESCE(SUM(spans), 0)::BIGINT
FROM service_rollup
WHERE bucket >= ? AND bucket < ?`, eventTime.Add(-time.Minute), eventTime.Add(time.Minute)).Scan(&spans); err != nil {
		t.Fatalf("query UTC rollup window: %v", err)
	}
	if spans != 1 {
		t.Fatalf("UTC rollup window spans = %d, want 1", spans)
	}

	connections := make([]*sql.Conn, 0, cfg.DuckDBMaxConns)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for range cfg.DuckDBMaxConns {
		connection, err := d.DB.Conn(ctx)
		if err != nil {
			t.Fatalf("open pooled connection: %v", err)
		}
		connections = append(connections, connection)
	}
	for i, connection := range connections {
		var zone string
		if err := connection.QueryRowContext(ctx, "SELECT current_setting('TimeZone')").Scan(&zone); err != nil {
			t.Fatalf("connection %d timezone query: %v", i, err)
		}
		if zone != "UTC" {
			t.Fatalf("connection %d timezone = %q, want UTC", i, zone)
		}
	}
}

func TestQueryContextHoldsParquetLockUntilRowsFinish(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(1))
	d := &Duck{DB: db}
	rows, err := d.QueryContext(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	lockAcquired := make(chan struct{})
	go func() {
		mustLock(&d.parquetMu)
		close(lockAcquired)
		d.parquetMu.Unlock()
	}()
	select {
	case <-lockAcquired:
		t.Fatal("Parquet write lock acquired while query rows were still open")
	case <-time.After(25 * time.Millisecond):
	}
	if !rows.Next() {
		t.Fatalf("rows.Next() = false: %v", rows.Err())
	}
	var value int
	if err := rows.Scan(&value); err != nil {
		t.Fatalf("rows.Scan: %v", err)
	}
	if rows.Next() {
		t.Fatal("rows.Next() returned an unexpected second row")
	}
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("Parquet write lock remained blocked after row iteration completed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryRowScanCancelsWhileMaintenanceWaitsForReaders(t *testing.T) {
	d := &Duck{}
	mustLock(&d.parquetMu)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := d.QueryRowScan(ctx, []any{new(int)}, "SELECT 1")
	d.parquetMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("QueryRowScan error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("QueryRowScan ignored lock deadline for %s", elapsed)
	}
}

func TestPublishParquetHonorsContext(t *testing.T) {
	d := &Duck{}
	mustRLock(t, &d.parquetMu)
	timeoutsBefore := testutil.ToFloat64(metrics.ParquetPublishTimeouts)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	called := false
	err := d.PublishParquet(ctx, func(context.Context) error {
		called = true
		return nil
	})
	d.parquetMu.RUnlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PublishParquet error = %v, want deadline exceeded", err)
	}
	if called {
		t.Fatal("publication ran after its context expired")
	}
	if got := waitingParquetWriters(&d.parquetMu); got != 0 {
		t.Fatalf("canceled publication remained queued: %d", got)
	}
	if got := testutil.ToFloat64(metrics.ParquetPublishTimeouts); got != timeoutsBefore+1 {
		t.Fatalf("publication timeouts = %v, want %v", got, timeoutsBefore+1)
	}
	if got := testutil.ToFloat64(metrics.ParquetPublishWaiters); got != 0 {
		t.Fatalf("publication waiters after timeout = %v, want 0", got)
	}
}

func TestParquetWorkDoesNotWaitForDuckDBWriteGate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("COPY").WillReturnResult(sqlmock.NewResult(0, 1))
	d := &Duck{DB: db}
	release := d.writeGate.Lock(writegate.WriteRollupService)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := d.MergeParquet(ctx, "logs", []string{"/tmp/input.parquet"}, "/tmp/output.parquet"); err != nil {
		t.Fatalf("merge waited for unrelated DuckDB write gate: %v", err)
	}
	called := false
	if err := d.PublishParquet(ctx, func(context.Context) error { called = true; return nil }); err != nil {
		t.Fatalf("publication waited for unrelated DuckDB write gate: %v", err)
	}
	if !called {
		t.Fatal("publication callback did not run")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingMaintenanceDoesNotBlockNewReaders(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(1))
	d := &Duck{DB: db}
	mustRLock(t, &d.parquetMu)
	writerAcquired := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		mustLock(&d.parquetMu)
		close(writerAcquired)
		<-releaseWriter
		d.parquetMu.Unlock()
		close(writerDone)
	}()
	deadline := time.Now().Add(time.Second)
	for waitingParquetWriters(&d.parquetMu) == 0 {
		if time.Now().After(deadline) {
			d.parquetMu.RUnlock()
			t.Fatal("maintenance writer did not begin waiting")
		}
		time.Sleep(time.Millisecond)
	}
	var value int
	if err := d.QueryRowScan(context.Background(), []any{&value}, "SELECT 1"); err != nil {
		d.parquetMu.RUnlock()
		t.Fatalf("new read blocked behind waiting maintenance: %v", err)
	}
	if value != 1 {
		d.parquetMu.RUnlock()
		t.Fatalf("value = %d, want 1", value)
	}
	d.parquetMu.RUnlock()
	select {
	case <-writerAcquired:
	case <-time.After(time.Second):
		t.Fatal("maintenance writer did not run after readers drained")
	}
	close(releaseWriter)
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("maintenance writer did not release")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRollupReadLockHonorsContext(t *testing.T) {
	d := &Duck{}
	mustLock(&d.parquetMu)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := d.refreshServiceRollup(ctx)
	d.parquetMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("refreshServiceRollup error = %v, want deadline exceeded", err)
	}
}

func TestRollupAdmissionLeaseDoesNotCancelAdmittedWork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	originalLease := rollupReaderLease
	rollupReaderLease = 5 * time.Millisecond
	t.Cleanup(func() { rollupReaderLease = originalLease })

	mock.ExpectBegin()
	mock.ExpectQuery("FROM rollup_state").WithArgs(serviceRollupStateKey).
		WillReturnRows(sqlmock.NewRows([]string{"last_ingested_unix_nano"}).AddRow(100))
	mock.ExpectQuery("FROM rollup_state").WithArgs(serviceRollupRawMaxKey).
		WillReturnRows(sqlmock.NewRows([]string{"last_ingested_unix_nano"}).AddRow(100))
	mock.ExpectQuery("FROM \\(").WillDelayFor(20 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"watermark"}).AddRow(100))
	mock.ExpectCommit()

	d := &Duck{DB: db}
	if _, err := d.refreshServiceRollup(context.Background()); err != nil {
		t.Fatalf("admitted rollup was canceled by its admission lease: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestIndexedTraceReadHonorsParquetGateContext(t *testing.T) {
	repository, err := telemetrystore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	d := &Duck{repository: repository}
	mustLock(&d.parquetMu)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = d.Trace(ctx, telemetry.TraceQuery{TraceID: "trace", StartNanos: 1, EndNanos: 2, Limit: 1})
	d.parquetMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Trace error = %v, want deadline exceeded", err)
	}
}

func TestRepositoryPublicationDoesNotWaitForDuckDBWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository, err := telemetrystore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "expired", Spans: []telemetry.Span{{TraceID: "trace", IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE FROM service_rollup").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM endpoint_rollup").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM edge_rollup").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CHECKPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	d := &Duck{DB: db, repository: repository, cfg: config.Config{MaintenanceInterval: time.Nanosecond, RetentionDays: 1}}
	mustRLock(t, &d.parquetMu)
	release := d.writeGate.Lock(writegate.WriteRollupService)
	done := make(chan error, 1)
	go func() { done <- d.runRepositoryMaintenance(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for waitingParquetWriters(&d.parquetMu) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if waiting := waitingParquetWriters(&d.parquetMu); waiting != 1 {
		d.parquetMu.RUnlock()
		release()
		t.Fatalf("publication waiters = %d, want 1 while DuckDB write gate is independently held", waiting)
	}
	d.parquetMu.RUnlock()
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceRetriesRetiredDirectoryCleanup(t *testing.T) {
	repository, err := telemetrystore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	retired := filepath.Join(repository.Parquet.BatchesDir(), "stale.retired-old")
	if err := os.Mkdir(retired, 0o755); err != nil {
		t.Fatal(err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("CHECKPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	d := &Duck{DB: db, repository: repository}
	if err := d.runRepositoryMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Fatalf("retired directory remains after maintenance: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceTracksConsecutiveFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for range 3 {
		mock.ExpectExec("CHECKPOINT").WillReturnError(errors.New("injected checkpoint failure"))
	}
	mock.ExpectExec("CHECKPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	d := &Duck{DB: db}
	for range 3 {
		if err := d.runRepositoryMaintenance(context.Background()); err == nil {
			t.Fatal("maintenance succeeded despite checkpoint failure")
		}
	}
	_, _, failures, _ := d.MaintenanceHealth()
	if failures != 3 {
		t.Fatalf("consecutive failures = %d, want 3", failures)
	}
	if err := d.runRepositoryMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, failures, _ = d.MaintenanceHealth()
	if failures != 0 {
		t.Fatalf("consecutive failures after recovery = %d, want 0", failures)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type failingPublishCompactor struct{ *Duck }

func (f failingPublishCompactor) PublishParquet(context.Context, func(context.Context) error) error {
	return errors.New("injected publication failure")
}

func TestMaintenanceRecoversCompactionBeforeRetention(t *testing.T) {
	repository, err := telemetrystore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	db := openTestDuck(t)
	defer db.Close()
	for _, table := range []string{"service_rollup", "endpoint_rollup", "edge_rollup"} {
		if _, err := db.Exec("CREATE TABLE " + table + " (bucket TIMESTAMP)"); err != nil {
			t.Fatal(err)
		}
	}
	d := &Duck{DB: db, repository: repository, cfg: config.Config{RetentionDays: 1}}
	for i := range 8 {
		batch := telemetrystore.Batch{
			ID:      fmt.Sprintf("expired-%d", i),
			Spans:   []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i), StartUnixNanos: 1, IngestedAt: 1}},
			Logs:    []telemetry.Log{{Body: "old", IngestedAt: 1}},
			Metrics: []telemetry.Metric{{Name: "old", IngestedAt: 1}},
		}
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.CompactParquet(context.Background(), failingPublishCompactor{d}, 64); err == nil {
		t.Fatal("compaction unexpectedly published")
	}
	if err := d.runRepositoryMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := repository.RowCount(); got != 0 {
		t.Fatalf("retention resurrected pending compaction rows: %d", got)
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
	if got := duckDBPoolSize(config.Config{}); got != 1 {
		t.Errorf("default pool size = %d, want 1", got)
	}
	if got := duckDBPoolSize(config.Config{DuckDBMaxConns: 0}); got != 1 {
		t.Errorf("zero pool size = %d, want floored to 1", got)
	}
	if got := duckDBPoolSize(config.Config{DuckDBMaxConns: -5}); got != 1 {
		t.Errorf("negative pool size = %d, want floored to 1", got)
	}
	if got := duckDBPoolSize(config.Config{DuckDBMaxConns: 8}); got != 8 {
		t.Errorf("configured pool size = %d, want 8", got)
	}
}

func TestParseDuckBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"6.4 GiB", 6871947673, true},
		{"512.0 MiB", 512 << 20, true},
		{"1024 bytes", 1024, true},
		{"8 GiB", 8 << 30, true},
		{"1000 MB", 1_000_000_000, true},
		{"0", 0, true},
		{"", 0, false},
		{"GiB", 0, false},
		{"6.4 PiB", 0, false},
	}
	for _, c := range cases {
		got, ok := parseDuckBytes(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseDuckBytes(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// A rollup that fails after reading the source tip must still publish lag.
// Otherwise fanout_rollup_lag_seconds freezes at its last healthy value during
// exactly the outage it exists to expose, and a lag alert never fires.
// The publish-lag-on-error defer is copy-pasted into all three rollup
// components, so it is covered for all three. A wrong component constant or a
// missed sourceMax assignment in one copy freezes fanout_rollup_lag_seconds at
// its last healthy value during exactly the outage the gauge exists to expose,
// and the lag alert never fires.
func TestFailedRollupStillPublishesLag(t *testing.T) {
	const (
		watermarkNanos = int64(1_000 * 1e9)
		sourceNanos    = int64(1_120 * 1e9) // 120s of uncovered ingest
	)

	type stateRead struct {
		key   string
		value int64
	}
	for _, tc := range []struct {
		component string
		// Each component reads a different set of rollup_state keys before it
		// reaches the source tip, in this order.
		stateReads []stateRead
		refresh    func(*Duck, context.Context) (int64, error)
	}{
		{
			component: "service",
			stateReads: []stateRead{
				{serviceRollupStateKey, watermarkNanos},
				{serviceRollupRawMaxKey, watermarkNanos},
			},
			refresh: func(d *Duck, ctx context.Context) (int64, error) { return d.refreshServiceRollup(ctx) },
		},
		{
			component: "endpoint",
			stateReads: []stateRead{
				{EndpointDisabledStateKey, 0},
				{EndpointRollupStateKey, watermarkNanos},
				{endpointRollupRawMaxKey, watermarkNanos},
				{endpointBackfillStateKey, 0},
			},
			refresh: func(d *Duck, ctx context.Context) (int64, error) { return d.refreshEndpointRollup(ctx) },
		},
		{
			component: "edge",
			stateReads: []stateRead{
				{edgeRollupStateKey, watermarkNanos},
				{edgeRollupRawMaxKey, watermarkNanos},
			},
			refresh: func(d *Duck, ctx context.Context) (int64, error) { return d.refreshEdgeRollup(ctx) },
		},
	} {
		t.Run(tc.component, func(t *testing.T) {
			metrics.RollupComponentTotal.Reset()
			metrics.RollupLag.Reset()
			metrics.RollupSourceMax.Reset()
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			defer db.Close()

			d := &Duck{DB: db, cfg: config.Config{}}
			watermarkRows := func(value int64) *sqlmock.Rows {
				return sqlmock.NewRows([]string{"last_ingested_unix_nano"}).AddRow(value)
			}

			mock.ExpectBegin()
			for _, read := range tc.stateReads {
				mock.ExpectQuery("FROM rollup_state").WithArgs(read.key).
					WillReturnRows(watermarkRows(read.value))
			}
			mock.ExpectQuery("FROM spans").
				WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(sourceNanos))
			// Everything past this point fails, so the rollup returns an error
			// without advancing its watermark.
			mock.ExpectQuery(".").
				WillReturnError(errors.New("rollup query failed"))

			if _, err := tc.refresh(d, context.Background()); err == nil {
				t.Fatalf("refresh %s rollup: error = nil, want the injected failure surfaced", tc.component)
			}
			if got := testutil.ToFloat64(metrics.RollupComponentTotal.WithLabelValues(tc.component, "error")); got != 1 {
				t.Fatalf("%s rollup error outcomes = %f, want 1", tc.component, got)
			}
			if got := testutil.ToFloat64(metrics.RollupLag.WithLabelValues(tc.component)); got != 120 {
				t.Errorf("%s rollup lag = %f seconds, want 120 — a failed rollup must still report how far behind it is", tc.component, got)
			}
			if got := testutil.ToFloat64(metrics.RollupSourceMax.WithLabelValues(tc.component)); got != float64(sourceNanos)/1e9 {
				t.Errorf("%s rollup source tip = %f, want %f", tc.component, got, float64(sourceNanos)/1e9)
			}
		})
	}
}

func TestMaintenanceRunsOnEveryTick(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("CHECKPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CHECKPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	d := &Duck{DB: db, cfg: config.Config{MaintenanceInterval: time.Hour}}
	if err := d.runRepositoryMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := d.lastMaintenanceAt
	if err := d.runRepositoryMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !d.lastMaintenanceAt.After(first) {
		t.Fatal("second maintenance pass was throttled; the ticker is the only intended throttle")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
