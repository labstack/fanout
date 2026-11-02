package engine

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
	"github.com/labstack/fanout/internal/engine/writegate"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

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

func TestNewDuckReconnectsInUTCAfterStartupContextCancelled(t *testing.T) {
	t.Setenv("TZ", "America/Los_Angeles")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "128MB"}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.stopMemorySampler()
	<-d.memorySamplerDone
	cancel()
	for name, db := range map[string]*sql.DB{"read": d.DB, "write": d.writeDB, "memory": d.memoryDB} {
		t.Run(name, func(t *testing.T) {
			// Retire every existing connection so this query must reconnect.
			db.SetMaxIdleConns(0)
			var timezone string
			if err := db.QueryRowContext(t.Context(), "SELECT current_setting('TimeZone')").Scan(&timezone); err != nil {
				t.Fatal(err)
			}
			if timezone != "UTC" {
				t.Fatalf("new connection timezone=%q, want UTC", timezone)
			}
		})
	}
}

// A cancelled statement's interrupt can reach the ROLLBACK that ends its read
// transaction, leaving the transaction open. The read must roll back itself
// after cancellation, and a connection whose rollback failed must leave the
// pool instead of failing every later read.
func TestReadTransactionRollbackFailureDiscardsTheConnection(t *testing.T) {
	for _, rollbackErr := range []error{nil, errors.New("INTERRUPT Error: Interrupted!")} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		mock.ExpectBegin()
		tx, err := beginRead(ctx, conn)
		if err != nil {
			t.Fatal(err)
		}
		rollback := mock.ExpectRollback()
		if rollbackErr != nil {
			rollback.WillReturnError(rollbackErr)
		}
		cancel()
		// database/sql rolls back a transaction begun with a cancelled context
		// on its own goroutine, where a failed rollback cannot be seen.
		time.Sleep(20 * time.Millisecond)
		if mock.ExpectationsWereMet() == nil {
			t.Fatal("cancellation rolled the read transaction back before its owner ended it")
		}
		endRead(conn, tx)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		want := 1
		if rollbackErr != nil {
			want = 0
		}
		if open := db.Stats().OpenConnections; open != want {
			t.Errorf("rollback error %v: %d pooled connections, want %d", rollbackErr, open, want)
		}
		_ = db.Close()
	}
}

// Open rows keep their snapshot pinned: a batch retired meanwhile keeps its
// files until the rows finish.
func TestQueryContextHoldsParquetPinUntilRowsFinish(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repository := emptyRepository(t)
	if err := commitRestoredFixture(repository, context.Background(), telemetrystore.Batch{ID: "expired", Spans: []telemetry.Span{{TraceID: "trace", IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(1))
	d := &Duck{DB: db, writeDB: db, repository: repository}
	rows, err := d.QueryContext(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	if n, err := repository.PruneParquet(context.Background(), d, time.Now().UnixNano(), 10); err != nil || n != 1 {
		t.Fatalf("retention while rows are open: pruned=%d err=%v", n, err)
	}
	dir := repository.Parquet.BatchPath("expired")
	if n, err := repository.CollectRetiredParquet(); err != nil || n != 0 {
		t.Fatalf("collected %d retired batches under open rows: %v", n, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("retired files removed while rows were open: %v", err)
	}
	for rows.Next() {
	}
	if n, err := repository.CollectRetiredParquet(); err != nil || n != 1 {
		t.Fatalf("collected %d retired batches after rows finished: %v", n, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("retired files remain after rows finished: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Publication never waits for readers, however long they hold a snapshot.
func TestPublishParquetDoesNotWaitForReaders(t *testing.T) {
	d := &Duck{repository: emptyRepository(t)}
	pin := d.pinParquet()
	defer d.unpinParquet(pin)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	called := false
	if err := d.PublishParquet(ctx, func(context.Context) error { called = true; return nil }); err != nil {
		t.Fatalf("publication waited for a pinned reader: %v", err)
	}
	if !called {
		t.Fatal("publication callback did not run")
	}
}

func TestParquetPublicationDoesNotWaitForDuckDBWriteGate(t *testing.T) {
	d := &Duck{}
	release := d.writeGate.Lock(writegate.RollupService)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	called := false
	if err := d.PublishParquet(ctx, func(context.Context) error { called = true; return nil }); err != nil {
		t.Fatalf("publication waited for unrelated DuckDB write gate: %v", err)
	}
	if !called {
		t.Fatal("publication callback did not run")
	}
}

func TestRollupAdmissionDoesNotCancelAdmittedWork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("FROM rollup_state").WithArgs(serviceRollupStateKey).
		WillReturnRows(sqlmock.NewRows([]string{"watermark"}).AddRow(100))
	mock.ExpectBegin()
	mock.ExpectExec("CREATE OR REPLACE TEMP TABLE service_pass_affected").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT epoch_ns").WillReturnRows(sqlmock.NewRows([]string{"lo", "hi"}).AddRow(0, 60_000_000_000))
	mock.ExpectExec("DELETE FROM service_rollup").WillDelayFor(20 * time.Millisecond).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE OR REPLACE TEMP TABLE service_pass_spans").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FROM service_pass_affected").WillReturnRows(sqlmock.NewRows([]string{"namespace", "bucket", "service"}).AddRow("ns", int64(0), "api"))
	mock.ExpectExec("INSERT INTO service_pass_spans").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("WITH span_agg").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP TABLE service_pass_spans").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP TABLE service_pass_affected").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO rollup_state").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return 200, 200, 100 }}
	if _, err := d.refreshServiceRollup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Retention publishes while a reader is pinned and the DuckDB write gate is
// held; only the rollup pruning that needs the gate waits for it.
func TestRepositoryPublicationDoesNotWaitForReadersOrDuckDBWrites(t *testing.T) {
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
	if err := commitRestoredFixture(repository, context.Background(), telemetrystore.Batch{ID: "expired", Spans: []telemetry.Span{{TraceID: "trace", IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE FROM service_rollup").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM edge_rollup").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CHECKPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	d := &Duck{DB: db, writeDB: db, repository: repository, cfg: config.Config{MaintenanceInterval: time.Nanosecond, RetentionDays: 1}}
	pin := d.pinParquet()
	release := d.writeGate.Lock(writegate.RollupService)
	done := make(chan error, 1)
	go func() { done <- d.runRepositoryMaintenance(context.Background()) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(repository.Parquet.BatchMetadata()) != 0 {
		if time.Now().After(deadline) {
			release()
			d.unpinParquet(pin)
			t.Fatal("retention waited for a pinned reader or the DuckDB write gate")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(repository.Parquet.BatchPath("expired")); err != nil {
		t.Fatalf("retention deleted files a pinned reader may open: %v", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	d.unpinParquet(pin)
	if n, err := repository.CollectRetiredParquet(); err != nil || n != 1 {
		t.Fatalf("collected %d retired batches after the reader left: %v", n, err)
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
	d := &Duck{DB: db, writeDB: db, repository: repository}
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
	d := &Duck{DB: db, writeDB: db, repository: emptyRepository(t)}
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

func (f failingPublishCompactor) PublishParquetReplacement(context.Context, telemetry.BatchMetadata, []string, func(context.Context) error) error {
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
	for _, table := range []string{"service_rollup", "edge_rollup"} {
		if _, err := db.Exec("CREATE TABLE " + table + " (bucket TIMESTAMP)"); err != nil {
			t.Fatal(err)
		}
	}
	d := &Duck{DB: db, writeDB: db, repository: repository, cfg: config.Config{RetentionDays: 1}}
	for i := range 8 {
		batch := telemetrystore.Batch{
			ID:      fmt.Sprintf("expired-%d", i),
			Spans:   []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i), StartUnixNanos: 1, IngestedAt: 1}},
			Logs:    []telemetry.Log{{Body: "old", IngestedAt: 1}},
			Metrics: []telemetry.Metric{{Name: "old", IngestedAt: 1}},
		}
		if err := commitRestoredFixture(repository, context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.CompactParquet(context.Background(), failingPublishCompactor{d}, 8); err == nil {
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
			},
			refresh: func(d *Duck, ctx context.Context) (int64, error) { return d.refreshServiceRollup(ctx) },
		},
		{
			component: "edge",
			stateReads: []stateRead{
				{edgeRollupStateKey, watermarkNanos},
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

			d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return sourceNanos, sourceNanos, 1 }, cfg: config.Config{}}
			watermarkRows := func(value int64) *sqlmock.Rows {
				return sqlmock.NewRows([]string{"last_ingested_unix_nano"}).AddRow(value)
			}

			for _, read := range tc.stateReads {
				mock.ExpectQuery("FROM rollup_state").WithArgs(read.key).
					WillReturnRows(watermarkRows(read.value))
			}
			mock.ExpectBegin()
			// Everything past this point fails, so the rollup returns an error
			// without advancing its watermark.
			if tc.component == "service" {
				mock.ExpectExec(".").WillReturnError(errors.New("rollup query failed"))
			} else {
				mock.ExpectQuery(".").WillReturnError(errors.New("rollup query failed"))
			}
			mock.ExpectRollback()

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
	d := &Duck{DB: db, writeDB: db, repository: emptyRepository(t), cfg: config.Config{MaintenanceInterval: time.Hour}}
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
