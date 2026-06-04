package query

import (
	"context"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/env"
)

// TestRollupWatermarkPicksUpLateLowIngestedRow reproduces the silent-data-loss
// bug: a row that commits with an ingested timestamp below a watermark already
// advanced by another signal would be excluded from the rollup forever. The
// safety lag must keep it in the reprocessed window.
func TestRollupWatermarkPicksUpLateLowIngestedRow(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}

	const lag = 2 * time.Second
	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
		rollupLagNanos:  lag.Nanoseconds(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	tNow := time.Now().UnixNano()

	// First signal commits at the high-water mark and the rollup advances.
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns", traceID: "t1", spanID: "s1", service: "alpha",
		operation: "op", start: bucket.Add(time.Second), duration: 10 * time.Millisecond,
		ingested: tNow,
	})
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce: %v", err)
	}

	// A second signal commits LATER but with a lower ingested timestamp (within
	// the lag window). Pre-fix, this was below the stored watermark and dropped.
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns", traceID: "t2", spanID: "s2", service: "beta",
		operation: "op", start: bucket.Add(time.Second), duration: 10 * time.Millisecond,
		ingested: tNow - time.Second.Nanoseconds(),
	})
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce: %v", err)
	}

	requireServiceRollupSpans(t, db, "ns", bucket, "beta", 1)

	// After the max plateaus (no new ingest), the rollup must go idle rather than
	// re-aggregating the trailing window every cycle forever.
	affected, err := d.rollupOnce(ctx)
	if err != nil {
		t.Fatalf("third rollupOnce: %v", err)
	}
	if affected != 0 {
		t.Errorf("third rollupOnce affected %d rows; want 0 (should short-circuit once ingest plateaus)", affected)
	}
}

// TestEdgeRollupWatermarkPicksUpLateChild exercises the edge rollup's safety-lag
// path (the existing edge test runs with lag disabled): a child span that commits
// after the watermark advanced, with a lower ingested timestamp, must still
// produce its dependency edge.
func TestEdgeRollupWatermarkPicksUpLateChild(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}

	const lag = 2 * time.Second
	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
		rollupLagNanos:  lag.Nanoseconds(),
	}
	ctx := context.Background()
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	tNow := time.Now().UnixNano()
	start := bucket.Add(time.Second)

	// Parent + first child commit at the high-water mark; edge advances.
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns", traceID: "tr", spanID: "p1", service: "gateway",
		operation: "op", kind: "SPAN_KIND_SERVER", start: start, duration: 10 * time.Millisecond, ingested: tNow,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns", traceID: "tr", spanID: "c1", parentSpanID: "p1", service: "orders",
		operation: "op", kind: "SPAN_KIND_CLIENT", start: start, duration: 5 * time.Millisecond, ingested: tNow,
	})
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce: %v", err)
	}

	// A late child of the same parent commits with a lower ingested timestamp.
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns", traceID: "tr", spanID: "c2", parentSpanID: "p1", service: "payments",
		operation: "op", kind: "SPAN_KIND_CLIENT", start: start, duration: 5 * time.Millisecond,
		ingested: tNow - time.Second.Nanoseconds(),
	})
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce: %v", err)
	}

	var calls int64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(calls), 0) FROM edge_rollup WHERE namespace = 'ns' AND caller = 'gateway' AND callee = 'payments'`).
		Scan(&calls); err != nil {
		t.Fatalf("query edge_rollup: %v", err)
	}
	if calls < 1 {
		t.Errorf("late child edge gateway->payments missing (calls=%d); safety lag should have caught it", calls)
	}
}
