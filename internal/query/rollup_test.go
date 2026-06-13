package query

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/env"
)

func TestRollupOnceRebuildsAffectedServiceBuckets(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-1",
		spanID:    "span-1",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(5 * time.Second),
		duration:  50 * time.Millisecond,
		ingested:  100,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}

	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-2",
		spanID:    "span-2",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(15 * time.Second),
		duration:  70 * time.Millisecond,
		ingested:  200,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-b",
		traceID:   "trace-3",
		spanID:    "span-3",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(25 * time.Second),
		duration:  40 * time.Millisecond,
		ingested:  300,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}

	requireServiceRollupSpans(t, db, "ns-a", bucket, "checkout", 2)
	requireServiceRollupSpans(t, db, "ns-b", bucket, "checkout", 1)
	requireRowCount(t, db, `
SELECT count(*)
FROM service_rollup
WHERE bucket = ?
  AND service = 'checkout'`, bucket, 2)
}

func TestRollupOnceRebuildsAffectedEdgeBuckets(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-1",
		spanID:    "parent-1",
		service:   "frontend",
		operation: "GET /checkout",
		kind:      "SPAN_KIND_SERVER",
		start:     bucket.Add(5 * time.Second),
		duration:  10 * time.Millisecond,
		ingested:  100,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace:    "ns-a",
		traceID:      "trace-1",
		spanID:       "child-1",
		parentSpanID: "parent-1",
		service:      "payments",
		operation:    "POST charge",
		kind:         "SPAN_KIND_CLIENT",
		start:        bucket.Add(10 * time.Second),
		duration:     80 * time.Millisecond,
		ingested:     100,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}

	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-2",
		spanID:    "parent-2",
		service:   "frontend",
		operation: "GET /checkout",
		kind:      "SPAN_KIND_SERVER",
		start:     bucket.Add(20 * time.Second),
		duration:  10 * time.Millisecond,
		ingested:  200,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace:    "ns-a",
		traceID:      "trace-2",
		spanID:       "child-2",
		parentSpanID: "parent-2",
		service:      "payments",
		operation:    "POST charge",
		kind:         "SPAN_KIND_CLIENT",
		start:        bucket.Add(25 * time.Second),
		duration:     90 * time.Millisecond,
		ingested:     200,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}

	var calls int64
	err := db.QueryRow(`
SELECT calls
FROM edge_rollup
WHERE namespace = 'ns-a'
  AND bucket = ?
  AND caller = 'frontend'
  AND callee = 'payments'
  AND edge_type = 'call'`, bucket).Scan(&calls)
	if err != nil {
		t.Fatalf("query edge_rollup failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("edge_rollup calls = %d, want 2", calls)
	}
}

func TestRollupOnceIgnoresRowsWithoutBucketTimestamp(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-1",
		spanID:    "span-1",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(10 * time.Second),
		duration:  50 * time.Millisecond,
		ingested:  100,
	})

	if _, err := db.Exec(`
INSERT INTO lake.logs (
  namespace,
  log_time,
  time_unix_nano,
  severity,
  severity_number,
  body,
  service,
  ingested_at,
  ingested_unix_nano
)
VALUES ('ns-a', NULL, 0, 'INFO', 9, 'missing time', 'checkout', now(), 200)`); err != nil {
		t.Fatalf("insert lake.logs failed: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO lake.metrics (
  namespace,
  metric_time,
  time_unix_nano,
  name,
  metric_type,
  service,
  value,
  ingested_at,
  ingested_unix_nano
)
VALUES ('ns-a', NULL, 0, 'cpu.usage', 'gauge', 'checkout', 1.0, now(), 300)`); err != nil {
		t.Fatalf("insert lake.metrics failed: %v", err)
	}

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("rollupOnce failed: %v", err)
	}

	requireServiceRollupSpans(t, db, "ns-a", bucket, "checkout", 1)
	requireRowCount(t, db, `
SELECT count(*)
FROM service_rollup
WHERE namespace = 'ns-a'`, nil, 1)
}

// A backlog wider than rollupChunkNanos must be processed across passes, one
// chunk each, not in a single unbounded statement (the 2026-06-13 prod
// incident: a first-run edge rollup over 12 days spilled 375 GiB and never
// committed).
func TestRollupOnceChunksWideBacklog(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-old",
		traceID:   "trace-1",
		spanID:    "span-1",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(5 * time.Second),
		duration:  50 * time.Millisecond,
		ingested:  100,
	})
	// Ingested two chunk-widths later than span-1: outside both the first
	// pass's chunk and the second's.
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-new",
		traceID:   "trace-2",
		spanID:    "span-2",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(15 * time.Second),
		duration:  70 * time.Millisecond,
		ingested:  100 + 2*rollupChunkNanos,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}
	requireServiceRollupSpans(t, db, "ns-old", bucket, "checkout", 1)
	requireRowCount(t, db, `
SELECT count(*)
FROM service_rollup
WHERE namespace = 'ns-new'`, nil, 0)

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("third rollupOnce failed: %v", err)
	}
	requireServiceRollupSpans(t, db, "ns-new", bucket, "checkout", 1)
}

type rollupTestSpan struct {
	namespace    string
	traceID      string
	spanID       string
	parentSpanID string
	service      string
	operation    string
	kind         string
	start        time.Time
	duration     time.Duration
	ingested     int64
}

func insertRollupTestSpan(t *testing.T, db *sql.DB, span rollupTestSpan) {
	t.Helper()

	kind := span.kind
	if kind == "" {
		kind = "SPAN_KIND_SERVER"
	}
	end := span.start.Add(span.duration)

	if _, err := db.Exec(`
INSERT INTO lake.spans (
  namespace,
  trace_id,
  span_id,
  parent_span_id,
  service,
  operation,
  kind,
  start_time,
  end_time,
  start_unix_nano,
  end_unix_nano,
  duration_ms,
  status,
  ingested_at,
  ingested_unix_nano
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'STATUS_CODE_OK', ?, ?)`,
		span.namespace,
		span.traceID,
		span.spanID,
		nullIfEmpty(span.parentSpanID),
		span.service,
		span.operation,
		kind,
		span.start,
		end,
		span.start.UnixNano(),
		end.UnixNano(),
		float64(span.duration)/float64(time.Millisecond),
		time.Unix(0, span.ingested).UTC(),
		span.ingested,
	); err != nil {
		t.Fatalf("insert lake.spans failed: %v", err)
	}
}

func requireServiceRollupSpans(t *testing.T, db *sql.DB, namespace string, bucket time.Time, service string, want int64) {
	t.Helper()

	var got int64
	err := db.QueryRow(`
SELECT spans
FROM service_rollup
WHERE namespace = ?
  AND bucket = ?
  AND service = ?`, namespace, bucket, service).Scan(&got)
	if err != nil {
		t.Fatalf("query service_rollup failed: %v", err)
	}
	if got != want {
		t.Fatalf("service_rollup spans = %d, want %d", got, want)
	}
}

func requireRowCount(t *testing.T, db *sql.DB, q string, arg any, want int) {
	t.Helper()

	var got int
	var err error
	if arg == nil {
		err = db.QueryRow(q).Scan(&got)
	} else {
		err = db.QueryRow(q, arg).Scan(&got)
	}
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d", got, want)
	}
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
