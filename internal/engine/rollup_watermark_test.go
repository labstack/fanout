package engine

import (
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRollupHorizonDoesNotSkipInFlightRows(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	horizon := int64(99)
	minimum := int64(200)
	d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return horizon, 200, minimum }}
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	insert := func(id string, stamp int64) {
		insertRollupTestSpan(t, db, rollupTestSpan{namespace: "ns", traceID: "trace", spanID: id, parentSpanID: "parent", service: id, start: bucket, ingested: stamp})
	}
	insertRollupTestSpan(t, db, rollupTestSpan{namespace: "ns", traceID: "trace", spanID: "parent", service: "gateway", start: bucket, ingested: 200})
	insert("later", 200)
	if _, err := d.rollupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireRowCount(t, db, "SELECT count(*) FROM service_rollup WHERE ?", true, 0)
	for _, component := range []string{"service", "edge"} {
		if got := testutil.ToFloat64(metrics.RollupWatermark.WithLabelValues(component)); got > float64(horizon)/1e9 {
			t.Fatalf("%s progress %g exceeds horizon %g", component, got, float64(horizon)/1e9)
		}
	}
	// Stamp 100 remained in flight while stamp 200 published. Once it
	// publishes, both the service and edge passes must include it.
	insert("earlier", 100)
	minimum = 100
	horizon = 200
	if _, err := d.rollupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireServiceRollupSpans(t, db, "ns", bucket, "earlier", 1)
	requireServiceRollupSpans(t, db, "ns", bucket, "later", 1)
	requireRowCount(t, db, "SELECT count(*) FROM edge_rollup WHERE caller='gateway' AND callee=?", "earlier", 1)
}

func TestRollupIdleHorizonExecutesNoSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return 100, 200, 1 }, rollupWatermarks: map[string]int64{serviceRollupStateKey: 100, edgeRollupStateKey: 100}}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.refreshEdgeRollupPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRollupWindowsPartitionSteadyIngest(t *testing.T) {
	watermark := int64(0)
	seen := make(map[int64]int)
	for horizon := int64(100); horizon <= 1000; horizon += 100 {
		start, end, _ := rollupWindow(watermark, 1, horizon)
		for row := int64(1); row <= horizon; row++ {
			if row > start && row <= end {
				seen[row]++
			}
		}
		watermark = end
	}
	for row := int64(1); row <= 1000; row++ {
		if seen[row] != 1 {
			t.Fatalf("stamp %d processed %d times", row, seen[row])
		}
	}
}

func TestRollupPicksUpRetriedCommit(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	batch := telemetrystore.Batch{ID: "retry", Spans: []telemetry.Span{{Namespace: "ns", ServiceName: "retried", TraceID: "trace", SpanID: "span", StartUnixNanos: bucket.UnixNano(), IngestedAt: 1}}}
	path := repo.Parquet.BatchPath(batch.ID)
	if err := os.WriteFile(path, []byte("block publication"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), batch); err == nil {
		t.Fatal("blocked publication succeeded")
	}
	failedStamp := batch.Spans[0].IngestedAt
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "other", Logs: []telemetry.Log{{Namespace: "ns", ServiceName: "other", TimeUnixNanos: bucket.UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.rollupWatermarks[serviceRollupStateKey] <= failedStamp {
		t.Fatal("fixture did not advance past failed stamp")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireServiceRollupSpans(t, d.DB, "ns", bucket, "retried", 1)
}

func TestRollupFailedFirstAttemptDoesNotOpenEpochWindow(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	batch := telemetrystore.Batch{ID: "first-attempt", Spans: []telemetry.Span{{Namespace: "ns", ServiceName: "first", TraceID: "trace", SpanID: "span", StartUnixNanos: bucket.UnixNano()}}}
	path := repo.Parquet.BatchPath(batch.ID)
	if err := os.WriteFile(path, []byte("block publication"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), batch); err == nil {
		t.Fatal("blocked publication succeeded")
	}
	if _, err := d.rollupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.rollupWatermarks[serviceRollupStateKey] != 0 || d.rollupWatermarks[edgeRollupStateKey] != 0 {
		t.Fatal("failed attempt opened an epoch catch-up window")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := d.rollupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireServiceRollupSpans(t, d.DB, "ns", bucket, "first", 1)
}

func TestRollupRestartRewindsFailedAttemptTail(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	future := time.Now().Add(time.Hour).UnixNano()
	batch := telemetrystore.Batch{ID: "durable", Spans: []telemetry.Span{{Namespace: "ns", ServiceName: "original", TraceID: "trace", SpanID: "span", StartUnixNanos: bucket.UnixNano(), IngestedAt: future}}}
	if err := commitRestoredFixture(repo, t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	repo, err = telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	batch.ID = "retry"
	batch.Spans[0].ServiceName = "after-restart"
	path := repo.Parquet.BatchPath(batch.ID)
	if err := os.WriteFile(path, []byte("block publication"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), batch); err == nil {
		t.Fatal("blocked publication succeeded")
	}
	failed := batch.Spans[0].IngestedAt
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.rollupWatermarks[serviceRollupStateKey] != failed {
		t.Fatal("fixture did not process the failed attempt's empty tail")
	}
	for _, v := range []struct {
		key   string
		value int64
	}{{edgeRollupSubWindowKey, failed}, {edgeRollupSubCursorKey, bucket.UnixNano()}} {
		if _, err := d.DB.Exec(`INSERT INTO rollup_state VALUES (?,?,now()) ON CONFLICT(cache_key) DO UPDATE SET last_ingested_unix_nano=excluded.last_ingested_unix_nano`, v.key, v.value); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	repo.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	repo, err = telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	d, err = NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.rollupWatermarks[serviceRollupStateKey] != future {
		t.Fatal("empty tail survived restart")
	}
	_, cursor := readEdgeRollupState(t, d)
	if cursor != 0 {
		t.Fatal("partial empty-tail cursor survived restart")
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if batch.Spans[0].IngestedAt != failed {
		t.Fatal("fixture did not reuse the failed attempt's nondurable stamp")
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireServiceRollupSpans(t, d.DB, "ns", bucket, "after-restart", 1)
}

func TestRollupIncludesRenamedFirstAttemptBeforeAdoption(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permission failure injection requires an unprivileged user")
	}
	d, repo := newBatchCacheTest(t)
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	batch := telemetrystore.Batch{ID: "first-sync-fails", Spans: []telemetry.Span{{Namespace: "ns", ServiceName: "first", TraceID: "trace", SpanID: "span", StartUnixNanos: bucket.UnixNano()}}}
	// Allow rename but deny opening the parent directory for its final fsync.
	// The stage has its own readable directory; no production fault hook is needed.
	dir := repo.Parquet.BatchesDir()
	if err := os.Chmod(dir, 0300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	err := repo.Commit(t.Context(), batch)
	if restoreErr := os.Chmod(dir, 0755); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil {
		t.Fatal("commit succeeded despite denied directory fsync")
	}
	if info, err := os.Stat(repo.Parquet.BatchPath(batch.ID)); err != nil || !info.IsDir() {
		t.Fatalf("rename did not complete: %v", err)
	}
	if len(repo.Parquet.BatchMetadata()) != 0 {
		t.Fatal("failed commit was registered")
	}
	stamp := batch.Spans[0].IngestedAt
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "second", Logs: []telemetry.Log{{Namespace: "ns", ServiceName: "other", TimeUnixNanos: bucket.UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.rollupWatermarks[serviceRollupStateKey] <= stamp {
		t.Fatal("rollup did not pass the failed stamp")
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireServiceRollupSpans(t, d.DB, "ns", bucket, "first", 1)
}
