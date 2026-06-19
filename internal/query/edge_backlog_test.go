package query

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/env"
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
		DB:              db,
		cfg:             env.Config{RetentionDays: 30, DuckDBMemory: "1GB"},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	// Insert ~400k SERVER parents + ~400k CLIENT children in bulk using DuckDB's
	// range() function. All rows share the same ingested_unix_nano (simulating a
	// burst/catch-up ingest), but start_time is spread across 180 minutes.
	// child.parent_span_id = parent.span_id, different services, same trace_id.
	const n = 400_000
	ingestedNano := time.Now().UnixNano()

	_, err := db.ExecContext(ctx, `
INSERT INTO lake.spans (
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
  now() - ((i % 180) * INTERVAL '1' MINUTE),
  now() - ((i % 180) * INTERVAL '1' MINUTE) + INTERVAL '10' MILLISECOND,
  epoch_ns(now() - ((i % 180) * INTERVAL '1' MINUTE)),
  epoch_ns(now() - ((i % 180) * INTERVAL '1' MINUTE)) + 10000000,
  10.0,
  'STATUS_CODE_OK',
  now(),
  ?
FROM range(?, ?) t(i)`,
		ingestedNano, 0, n)
	if err != nil {
		t.Fatalf("insert parents: %v", err)
	}

	_, err = db.ExecContext(ctx, `
INSERT INTO lake.spans (
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
  now() - ((i % 180) * INTERVAL '1' MINUTE) + INTERVAL '1' MILLISECOND,
  now() - ((i % 180) * INTERVAL '1' MINUTE) + INTERVAL '5' MILLISECOND,
  epoch_ns(now() - ((i % 180) * INTERVAL '1' MINUTE)) + 1000000,
  epoch_ns(now() - ((i % 180) * INTERVAL '1' MINUTE)) + 5000000,
  4.0,
  'STATUS_CODE_OK',
  now(),
  ?
FROM range(?, ?) t(i)`,
		ingestedNano, 0, n)
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

}

func TestSkipRollupToLatest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := NewDuck(ctx, env.Config{DataDir: t.TempDir(), DuckDBMemory: "2GB", RetentionDays: 30})
	if err != nil {
		if strings.Contains(err.Error(), "ducklake") || strings.Contains(err.Error(), "ATTACH") {
			t.Skipf("DuckLake unavailable: %v", err)
		}
		t.Fatalf("NewDuck: %v", err)
	}
	defer d.Close()

	if _, err := d.DB.ExecContext(ctx, `
INSERT INTO lake.spans (namespace, trace_id, span_id, parent_span_id, service, kind, start_time, duration_ms, status, ingested_unix_nano)
SELECT 'default', 'tr-'||i, 'c-'||i, 'p-'||i, 'svc-'||(i%5), 'SPAN_KIND_CLIENT',
       now() - ((i % 120) * INTERVAL 1 MINUTE), 10.0, 'STATUS_CODE_OK', epoch_ns(now())
FROM range(5000) t(i)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := d.skipRollupToLatest(ctx); err != nil {
		t.Fatalf("skipRollupToLatest: %v", err)
	}
	// After skipping, the existing backlog is marked processed → rollupOnce is a no-op.
	n, err := d.rollupOnce(ctx)
	if err != nil {
		t.Fatalf("rollupOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("rollupOnce processed %d rows after skip-to-latest; want 0 (backlog should be skipped)", n)
	}
}
