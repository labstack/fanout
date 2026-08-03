package query

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/metrics"
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

func TestRunMaintenanceContinuesAfterDeleteFailure(t *testing.T) {
	metrics.DuckLakeOperationTotal.Reset()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{
		DB:  db,
		cfg: env.Config{RetentionDays: 7},
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM lake.spans WHERE COALESCE(start_time, ingested_at) < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 11))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM lake.logs WHERE COALESCE(log_time, observed_time, ingested_at) < now() - INTERVAL 7 DAY")).
		WillReturnError(errors.New("log delete failed"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM lake.metrics WHERE COALESCE(metric_time, ingested_at) < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM service_rollup WHERE bucket < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM endpoint_rollup WHERE bucket < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM edge_rollup WHERE bucket < now() - INTERVAL 7 DAY")).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_merge_adjacent_files('lake')")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_rewrite_data_files('lake')")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_expire_snapshots('lake', older_than => now() - INTERVAL 10 MINUTE)")).
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
	if got := testutil.ToFloat64(metrics.DuckLakeOperationTotal.WithLabelValues("maintenance", "error")); got != 1 {
		t.Errorf("maintenance error outcomes = %f, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

// A merge failure must not stop snapshot expiry, file cleanup, or the
// checkpoint — each compaction step degrades independently.
// Within DUCKLAKE_MAINTENANCE_EVERY_SECONDS of the last pass, runMaintenance
// must short-circuit before issuing ANY SQL — the throttle that keeps the
// retention+compaction cycle off every rollup tick.
// runMerge issues exactly one merge_adjacent_files call when due, and nothing
// on the next call within the DUCKLAKE_MERGE_EVERY_SECONDS cadence.
func TestRunMergeExecutesThenThrottles(t *testing.T) {
	metrics.DuckLakeOperationTotal.Reset()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db, cfg: env.Config{MergeEverySeconds: 60}}
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_merge_adjacent_files('lake')")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := d.runMerge(context.Background()); err != nil {
		t.Fatalf("runMerge() = %v, want nil", err)
	}
	// Second call is within the cadence → must issue no SQL.
	if err := d.runMerge(context.Background()); err != nil {
		t.Fatalf("throttled runMerge() = %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if got := testutil.ToFloat64(metrics.DuckLakeOperationTotal.WithLabelValues("merge", "success")); got != 1 {
		t.Errorf("merge success outcomes = %f, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.DuckLakeOperationTotal.WithLabelValues("merge", "throttled")); got != 1 {
		t.Errorf("merge throttled outcomes = %f, want 1", got)
	}
}

// MergeEverySeconds <= 0 disables the frequent merge pass entirely.
func TestRunMergeDisabled(t *testing.T) {
	metrics.DuckLakeOperationTotal.Reset()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db, cfg: env.Config{MergeEverySeconds: 0}}
	if err := d.runMerge(context.Background()); err != nil {
		t.Fatalf("disabled runMerge() = %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("disabled runMerge should issue no SQL: %v", err)
	}
	if got := testutil.ToFloat64(metrics.DuckLakeOperationTotal.WithLabelValues("merge", "disabled")); got != 1 {
		t.Errorf("merge disabled outcomes = %f, want 1", got)
	}
}

func TestRunMaintenanceThrottle(t *testing.T) {
	metrics.DuckLakeOperationTotal.Reset()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db, cfg: env.Config{MaintenanceEverySeconds: 3600}, lastMaintenance: time.Now()}
	if err := d.runMaintenance(context.Background()); err != nil {
		t.Fatalf("throttled runMaintenance() = %v, want nil", err)
	}
	// No expectations were registered: if it ran any SQL, this fails.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("throttled runMaintenance should issue no SQL: %v", err)
	}
	if got := testutil.ToFloat64(metrics.DuckLakeOperationTotal.WithLabelValues("maintenance", "throttled")); got != 1 {
		t.Errorf("maintenance throttled outcomes = %f, want 1", got)
	}
}

func TestRunMaintenanceContinuesAfterCompactionFailure(t *testing.T) {
	metrics.DuckLakeOperationTotal.Reset()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db, cfg: env.Config{}}

	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_merge_adjacent_files('lake')")).
		WillReturnError(errors.New("merge failed"))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_rewrite_data_files('lake')")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_expire_snapshots('lake', older_than => now() - INTERVAL 10 MINUTE)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_cleanup_old_files('lake', cleanup_all => true)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CHECKPOINT")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = d.runMaintenance(context.Background())
	if err == nil {
		t.Fatal("runMaintenance() error = nil, want merge error surfaced")
	}
	if lastOK, lastAt, lastErr := d.MaintenanceHealth(); lastErr == nil || !lastOK.IsZero() || lastAt.IsZero() {
		t.Fatalf("MaintenanceHealth() = (%v, %v, %v), want zero lastOK, non-zero lastAt, non-nil lastErr", lastOK, lastAt, lastErr)
	}
	if got := testutil.ToFloat64(metrics.DuckLakeOperationTotal.WithLabelValues("maintenance", "error")); got != 1 {
		t.Errorf("maintenance error outcomes = %f, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

// A failing rollup must not skip maintenance: file/snapshot growth is what
// makes the failing rollup heavier each retry, so compaction has to run anyway.
func TestRollupOnceRunsMaintenanceDespiteRollupFailure(t *testing.T) {
	metrics.RollupComponentTotal.Reset()
	metrics.DuckLakeOperationTotal.Reset()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db, cfg: env.Config{}}

	// All rollup transactions fail at BeginTx.
	mock.ExpectBegin().WillReturnError(errors.New("service tx failed"))
	mock.ExpectBegin().WillReturnError(errors.New("endpoint tx failed"))
	mock.ExpectBegin().WillReturnError(errors.New("edge tx failed"))
	// Maintenance still runs: compaction calls + checkpoint (RetentionDays=0
	// skips the TTL deletes).
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_merge_adjacent_files('lake')")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_rewrite_data_files('lake')")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_expire_snapshots('lake', older_than => now() - INTERVAL 10 MINUTE)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CALL ducklake_cleanup_old_files('lake', cleanup_all => true)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CHECKPOINT")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err = d.rollupOnce(context.Background())
	if err == nil {
		t.Fatal("rollupOnce() error = nil, want rollup errors surfaced")
	}
	if lastOK, lastAt, lastErr := d.MaintenanceHealth(); lastErr != nil || lastOK.IsZero() || lastAt.IsZero() {
		t.Fatalf("MaintenanceHealth() = (%v, %v, %v), want non-zero lastOK and lastAt, nil lastErr", lastOK, lastAt, lastErr)
	}
	for _, component := range []string{"service", "endpoint", "edge"} {
		if got := testutil.ToFloat64(metrics.RollupComponentTotal.WithLabelValues(component, "error")); got != 1 {
			t.Errorf("%s rollup error outcomes = %f, want 1", component, got)
		}
	}
	if got := testutil.ToFloat64(metrics.DuckLakeOperationTotal.WithLabelValues("maintenance", "success")); got != 1 {
		t.Errorf("maintenance success outcomes = %f, want 1", got)
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

			d := &Duck{DB: db, cfg: env.Config{}}
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
