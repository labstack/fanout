package query

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestEdgeRollupBacklog is a regression test for the OOM bug in refreshEdgeRollup.
// A burst ingest that covers hours of span start_time caused the call_edges
// self-join to fan out over the full start_time range, exhausting memory at 1GB.
// The fix sub-windows execution by edgeStartChunkNanos (30 min) so the join is
// always bounded regardless of how wide the backlog's start_time range is.
func TestEdgeRollupBacklog(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}

	d := &Duck{
		DB:  db,
		cfg: config.Config{RetentionDays: 30, DuckDBMemory: "1GB"},
	}
	ctx := context.Background()

	// Insert ~400k SERVER parents + ~400k CLIENT children in bulk using DuckDB's
	// range() function. All rows share the same ingested_unix_nano (simulating a
	// burst/catch-up ingest), but start_time is spread across 180 minutes.
	// child.parent_span_id = parent.span_id, different services, same trace_id.
	const n = 400_000
	// Both inserts must share one minute boundary: they are large enough for the
	// wall clock to cross into the next minute between them on a loaded runner.
	baseTime := time.Now().UTC().Truncate(time.Minute)
	ingestedNano := baseTime.UnixNano()

	_, err := db.ExecContext(ctx, `
WITH input AS (SELECT CAST(? AS TIMESTAMP) AS base_time)
INSERT INTO telemetry.spans (
  namespace, trace_id, span_id, parent_span_id,
  service, operation, kind,
  start_time, end_time, start_unix_nano, end_unix_nano, duration_ms,
  status, ingested_at, ingested_unix_nano
)
SELECT
  'default',
  printf('trace-%d', i % 50000),
  printf('parent-%d', i),
  NULL,
  'svc-a',
  'op',
  'SPAN_KIND_SERVER',
  base_time - ((i % 180) * INTERVAL '1' MINUTE),
  base_time - ((i % 180) * INTERVAL '1' MINUTE) + INTERVAL '10' MILLISECOND,
  epoch_ns(base_time - ((i % 180) * INTERVAL '1' MINUTE)),
  epoch_ns(base_time - ((i % 180) * INTERVAL '1' MINUTE)) + 10000000,
  10.0,
  'STATUS_CODE_OK',
  base_time,
  ?
FROM range(?, ?) t(i), input`,
		baseTime, ingestedNano, 0, n)
	if err != nil {
		t.Fatalf("insert parents: %v", err)
	}

	_, err = db.ExecContext(ctx, `
WITH input AS (SELECT CAST(? AS TIMESTAMP) AS base_time)
INSERT INTO telemetry.spans (
  namespace, trace_id, span_id, parent_span_id,
  service, operation, kind,
  start_time, end_time, start_unix_nano, end_unix_nano, duration_ms,
  status, ingested_at, ingested_unix_nano
)
SELECT
  'default',
  printf('trace-%d', i % 50000),
  printf('child-%d', i),
  printf('parent-%d', i),
  'svc-b',
  'op',
  'SPAN_KIND_CLIENT',
  base_time - ((i % 180) * INTERVAL '1' MINUTE) + INTERVAL '1' MILLISECOND,
  base_time - ((i % 180) * INTERVAL '1' MINUTE) + INTERVAL '5' MILLISECOND,
  epoch_ns(base_time - ((i % 180) * INTERVAL '1' MINUTE)) + 1000000,
  epoch_ns(base_time - ((i % 180) * INTERVAL '1' MINUTE)) + 5000000,
  4.0,
  'STATUS_CODE_OK',
  base_time,
  ?
FROM range(?, ?) t(i), input`,
		baseTime, ingestedNano, 0, n)
	if err != nil {
		t.Fatalf("insert children: %v", err)
	}

	// rollupOnce must succeed. Before the fix, the call_edges join over 180
	// minutes of start_time OOMed at 1GB. The sub-windowed fix loops over
	// 30-minute slices, keeping the hash-build bounded.
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("rollupOnce() error = %v (possible OOM or sub-window bug)", err)
	}

	// At least some edge rows must have been inserted.
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM edge_rollup`).Scan(&count); err != nil {
		t.Fatalf("count edge_rollup: %v", err)
	}
	if count == 0 {
		t.Fatal("edge_rollup is empty after rollupOnce — expected cross-service edges to be recorded")
	}
	t.Logf("edge_rollup rows after wide-backlog rollup: %d", count)

	// Stronger: the number of distinct minute buckets in edge_rollup must equal
	// the number of distinct minute buckets in the inserted spans' start_times.
	// This proves sub-windowing didn't drop or duplicate any 1-minute bucket.
	var edgeBuckets, spanBuckets int64
	if err := db.QueryRowContext(ctx, `SELECT count(DISTINCT bucket) FROM edge_rollup`).Scan(&edgeBuckets); err != nil {
		t.Fatalf("count distinct edge_rollup buckets: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
SELECT count(DISTINCT date_trunc('minute', start_time))
FROM telemetry.spans`).Scan(&spanBuckets); err != nil {
		t.Fatalf("count distinct span minute buckets: %v", err)
	}
	if spanBuckets != 180 {
		t.Fatalf("spans have %d distinct minute buckets, want 180", spanBuckets)
	}
	if edgeBuckets != spanBuckets {
		t.Errorf("edge_rollup has %d distinct buckets, spans have %d distinct minute buckets — sub-windowing dropped or duplicated buckets",
			edgeBuckets, spanBuckets)
	}
	t.Logf("distinct buckets: edge_rollup=%d span_minutes=%d", edgeBuckets, spanBuckets)
}

func TestSkipRollupToLatest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "2GB", RetentionDays: 30}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatalf("open telemetry repository: %v", err)
	}
	defer repository.Close()
	now := time.Now().UnixNano()
	spans := make([]telemetry.Span, 100)
	for i := range spans {
		spans[i] = telemetry.Span{Namespace: "default", TraceID: "backlog", SpanID: string(rune(i + 1)), ServiceName: "svc", Kind: "SPAN_KIND_CLIENT", StartUnixNanos: now - int64(i)*int64(time.Minute), DurationMS: 10, StatusCode: "STATUS_CODE_OK", IngestedAt: now}
	}
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "skip-backlog", Spans: spans}); err != nil {
		t.Fatalf("commit backlog: %v", err)
	}
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatalf("NewDuck: %v", err)
	}
	defer d.Close()
	if _, err := d.DB.ExecContext(ctx, `
INSERT INTO endpoint_rollup (
  namespace, bucket, service, method, path, calls, error_count, duration_count, duration_buckets
) VALUES (
  'default', date_trunc('minute', now()), 'old', 'GET', '/stale', 1, 0, 1,
  struct_pack(
    le_0_1 := 0, le_0_5 := 0, le_1 := 0, le_2_5 := 0, le_5 := 0,
    le_10 := 1, le_25 := 1, le_50 := 1, le_100 := 1, le_250 := 1,
    le_500 := 1, le_750 := 1, le_1000 := 1, le_2000 := 1,
    le_5000 := 1, le_30000 := 1, le_300000 := 1
  )
);
INSERT INTO rollup_state (cache_key, last_ingested_unix_nano, updated_at)
VALUES (?, 1, now())
ON CONFLICT (cache_key) DO UPDATE SET last_ingested_unix_nano = 1, updated_at = now()`, EndpointReadyStateKey); err != nil {
		t.Fatalf("seed prior endpoint cache state: %v", err)
	}

	if err := d.skipRollupToLatest(ctx); err != nil {
		t.Fatalf("skipRollupToLatest: %v", err)
	}
	// After skipping, the existing backlog is marked processed → rollupOnce is a no-op.
	metrics.RollupComponentTotal.Reset()
	metrics.RollupLag.Reset()
	metrics.RollupBacklogChunks.Reset()
	metrics.RollupEnabled.Reset()
	n, err := d.rollupOnce(ctx)
	if err != nil {
		t.Fatalf("rollupOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("rollupOnce processed %d rows after skip-to-latest; want 0 (backlog should be skipped)", n)
	}
	for _, component := range []string{"service", "edge"} {
		if got := testutil.ToFloat64(metrics.RollupComponentTotal.WithLabelValues(component, "noop")); got != 1 {
			t.Errorf("%s no-op outcomes = %f, want 1", component, got)
		}
		if got := testutil.ToFloat64(metrics.RollupLag.WithLabelValues(component)); got != 0 {
			t.Errorf("%s lag = %f, want 0", component, got)
		}
		if got := testutil.ToFloat64(metrics.RollupBacklogChunks.WithLabelValues(component)); got != 0 {
			t.Errorf("%s backlog chunks = %f, want 0", component, got)
		}
	}
	if got := testutil.ToFloat64(metrics.RollupComponentTotal.WithLabelValues("endpoint", "disabled")); got != 1 {
		t.Errorf("endpoint disabled outcomes = %f, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.RollupEnabled.WithLabelValues("endpoint")); got != 0 {
		t.Errorf("endpoint enabled = %f, want 0", got)
	}
	var endpointRows, endpointReady, endpointDisabled int64
	if err := d.DB.QueryRowContext(ctx, `SELECT count(*) FROM endpoint_rollup`).Scan(&endpointRows); err != nil {
		t.Fatalf("count endpoint_rollup after skip: %v", err)
	}
	if err := d.DB.QueryRowContext(ctx, `SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key = ?`, EndpointReadyStateKey).Scan(&endpointReady); err != nil {
		t.Fatalf("query endpoint ready state: %v", err)
	}
	if err := d.DB.QueryRowContext(ctx, `SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key = ?`, EndpointDisabledStateKey).Scan(&endpointDisabled); err != nil {
		t.Fatalf("query endpoint disabled state: %v", err)
	}
	if endpointRows != 0 || endpointReady != 0 || endpointDisabled == 0 {
		t.Fatalf("endpoint state after skip = rows:%d ready:%d disabled:%d, want 0/0/nonzero", endpointRows, endpointReady, endpointDisabled)
	}

	// Insert one fresh live span (ingested_unix_nano = now) and verify that
	// rollupOnce picks it up — proves the watermark didn't over-advance and
	// swallow data that arrived after the skip.
	liveTime := time.Now().Add(time.Millisecond).UnixNano()
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "skip-live", Spans: []telemetry.Span{{Namespace: "default", TraceID: "tr-live-1", SpanID: "sp-live-1", ServiceName: "svc-live", Kind: "SPAN_KIND_SERVER", StartUnixNanos: liveTime, DurationMS: 5, StatusCode: "STATUS_CODE_OK", IngestedAt: liveTime}}}); err != nil {
		t.Fatalf("commit live span: %v", err)
	}

	n2, err := d.rollupOnce(ctx)
	if err != nil {
		t.Fatalf("rollupOnce (live data): %v", err)
	}
	if n2 == 0 {
		t.Error("rollupOnce processed 0 rows for live span after skip-to-latest; watermark may have over-advanced")
	}
}

// readEdgeRollupState returns the edge rollup's persisted watermark and its
// sub-window resume point.
func readEdgeRollupState(t *testing.T, d *Duck) (watermark, cursor int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if watermark, err = rollupWatermark(ctx, tx, edgeRollupStateKey); err != nil {
		t.Fatal(err)
	}
	if cursor, err = rollupWatermark(ctx, tx, edgeRollupSubCursorKey); err != nil {
		t.Fatal(err)
	}
	return watermark, cursor
}

// TestEdgeRollupBoundsSubWindowsPerPass pins the invariant that one rollup pass
// holds the Parquet read gate for an amount of work fixed by configuration,
// never by the shape of the data.
//
// Chunking ingested time does not provide that bound: a seed load or backfill
// compresses a wide start_time range into a narrow ingested window, so a single
// one-hour chunk can expand into an unbounded number of sub-windows and hold
// the gate for as long as that takes — starving every retention and compaction
// publication behind it. No timeout can fix that from the outside; cancelling
// mid-transaction only discards the work and retries it forever. The pass must
// instead stop at a fixed sub-window count, persist where to resume, and leave
// the ingested watermark behind until the window is finished.
func TestEdgeRollupBoundsSubWindowsPerPass(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}
	d := &Duck{DB: db, cfg: config.Config{RetentionDays: 30, DuckDBMemory: "1GB"}}
	ctx := context.Background()

	// One ingested instant, but start_time spread over 600 minutes: 20
	// sub-windows of edgeStartChunkNanos, well past maxEdgeSubWindowsPerPass.
	const (
		rows        = 12_000
		spreadMins  = 600
		wantWindows = spreadMins / 30
	)
	baseTime := time.Now().UTC().Truncate(time.Minute)
	ingestedNano := baseTime.UnixNano()
	for _, spec := range []struct{ kind, span, parent, service string }{
		{"SPAN_KIND_SERVER", "parent-%d", "", "svc-a"},
		{"SPAN_KIND_CLIENT", "child-%d", "parent-%d", "svc-b"},
	} {
		parent := "NULL"
		if spec.parent != "" {
			parent = fmt.Sprintf("printf('%s', i)", spec.parent)
		}
		_, err := db.ExecContext(ctx, fmt.Sprintf(`
WITH input AS (SELECT CAST(? AS TIMESTAMP) AS base_time)
INSERT INTO telemetry.spans (
  namespace, trace_id, span_id, parent_span_id, service, operation, kind,
  start_time, end_time, start_unix_nano, end_unix_nano, duration_ms,
  status, ingested_at, ingested_unix_nano
)
SELECT 'default', printf('trace-%%d', i %% 2000), printf('%s', i), %s, '%s', 'op', '%s',
  base_time - ((i %% %d) * INTERVAL '1' MINUTE),
  base_time - ((i %% %d) * INTERVAL '1' MINUTE) + INTERVAL '10' MILLISECOND,
  epoch_ns(base_time - ((i %% %d) * INTERVAL '1' MINUTE)),
  epoch_ns(base_time - ((i %% %d) * INTERVAL '1' MINUTE)) + 10000000,
  10.0, 'STATUS_CODE_OK', base_time, ?
FROM range(?, ?) t(i), input`,
			spec.span, parent, spec.service, spec.kind, spreadMins, spreadMins, spreadMins, spreadMins),
			baseTime, ingestedNano, 0, rows)
		if err != nil {
			t.Fatalf("insert %s: %v", spec.kind, err)
		}
	}

	// The first pass must stop short and record where to resume, leaving the
	// ingested watermark untouched so nothing in the window can be skipped.
	if _, err := d.refreshEdgeRollup(ctx); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	watermark, cursor := readEdgeRollupState(t, d)
	if cursor == 0 {
		t.Fatal("first pass consumed the whole window: the per-pass sub-window bound did not engage")
	}
	if watermark != 0 {
		t.Fatalf("ingested watermark advanced to %d over an unfinished window", watermark)
	}

	passes := 1
	for {
		if passes > wantWindows+2 {
			t.Fatal("edge rollup did not converge: passes are not making forward progress")
		}
		if _, err := d.refreshEdgeRollup(ctx); err != nil {
			t.Fatalf("pass %d: %v", passes+1, err)
		}
		passes++
		if _, cursor = readEdgeRollupState(t, d); cursor == 0 {
			break
		}
	}
	if passes < 2 {
		t.Fatalf("converged in %d passes, want the work spread over several", passes)
	}
	if watermark, _ = readEdgeRollupState(t, d); watermark == 0 {
		t.Fatal("ingested watermark never advanced after the window completed")
	}

	// Resuming must neither drop nor double-count a bucket: the aggregate has
	// to match what a single unbounded pass would have produced.
	var edgeBuckets, spanBuckets int64
	if err := db.QueryRowContext(ctx, `SELECT count(DISTINCT bucket) FROM edge_rollup`).Scan(&edgeBuckets); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `
SELECT count(DISTINCT date_trunc('minute', start_time)) FROM telemetry.spans`).Scan(&spanBuckets); err != nil {
		t.Fatal(err)
	}
	if spanBuckets != spreadMins {
		t.Fatalf("spans cover %d minute buckets, want %d", spanBuckets, spreadMins)
	}
	if edgeBuckets != spanBuckets {
		t.Fatalf("edge_rollup covers %d buckets, spans cover %d — resuming dropped or duplicated work", edgeBuckets, spanBuckets)
	}
	t.Logf("converged in %d passes over %d sub-windows; buckets edge=%d spans=%d", passes, wantWindows, edgeBuckets, spanBuckets)
}

func TestEdgeSubWindowShrinksForDenseIngest(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute)
	ingested := base.UnixNano()
	if _, err := db.ExecContext(ctx, `
WITH input AS (SELECT CAST(? AS TIMESTAMP) AS base_time)
INSERT INTO telemetry.spans (
  namespace, trace_id, span_id, service, start_time, start_unix_nano,
  ingested_at, ingested_unix_nano
)
SELECT
  'default', printf('trace-%d', i), printf('span-%d', i), 'svc',
  base_time + ((i % 30) * INTERVAL '1' MINUTE),
  epoch_ns(base_time + ((i % 30) * INTERVAL '1' MINUTE)), base_time, ?
FROM range(100) t(i), input`, base, ingested); err != nil {
		t.Fatal(err)
	}
	var affected int64
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM spans
WHERE ingested_unix_nano > ? AND ingested_unix_nano <= ?`, ingested-1, ingested).Scan(&affected); err != nil {
		t.Fatal(err)
	}
	if affected != 100 {
		t.Fatalf("affected fixture rows = %d, want 100", affected)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	subHi, err := edgeSubWindowEnd(ctx, tx, ingested-1, ingested, base, base.Add(29*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := base.Add(time.Minute); !subHi.Equal(want) {
		t.Fatalf("dense sub-window ended at %s, want %s", subHi, want)
	}
}
