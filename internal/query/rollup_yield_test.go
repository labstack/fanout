package query

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
)

// seedEdgeSpread writes SERVER/CLIENT pairs that share one ingested instant but
// spread their start_time over spreadMins, which is what forces the edge rollup
// to sub-window: a seed load or a backfill compresses a wide start_time range
// into a narrow ingested window.
func seedEdgeSpread(t *testing.T, d *Duck, rows, spreadMins int) {
	t.Helper()
	ctx := context.Background()
	baseTime := time.Now().UTC().Truncate(time.Minute)
	for _, spec := range []struct{ kind, span, parent, service string }{
		{"SPAN_KIND_SERVER", "parent-%d", "", "svc-a"},
		{"SPAN_KIND_CLIENT", "child-%d", "parent-%d", "svc-b"},
	} {
		parent := "NULL"
		if spec.parent != "" {
			parent = fmt.Sprintf("printf('%s', i)", spec.parent)
		}
		_, err := d.DB.ExecContext(ctx, fmt.Sprintf(`
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
			baseTime, baseTime.UnixNano(), 0, rows)
		if err != nil {
			t.Fatalf("insert %s: %v", spec.kind, err)
		}
	}
}

func newSpreadDuck(t *testing.T, rows, spreadMins int) *Duck {
	t.Helper()
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}
	d := &Duck{DB: db, cfg: config.Config{RetentionDays: 30, DuckDBMemory: "1GB"}}
	seedEdgeSpread(t, d, rows, spreadMins)
	return d
}

// A rollup pass holds the Parquet snapshot until it commits, so a publication
// that queues behind it makes every query arriving after the grace period wait
// out the rest of that pass — up to eight sub-windows of work for a request
// that needed none of it. The pass now stops at the first sub-window boundary
// once a publisher is waiting, which is free: the resume cursor already exists,
// and the ingested watermark already stays behind an unfinished window.
func TestEdgeRollupYieldsToAQueuedPublisher(t *testing.T) {
	const (
		rows       = 6_000
		spreadMins = 300
	)
	ctx := context.Background()

	undisturbed := newSpreadDuck(t, rows, spreadMins)
	if _, err := undisturbed.refreshEdgeRollup(ctx); err != nil {
		t.Fatalf("undisturbed pass: %v", err)
	}
	_, freeCursor := readEdgeRollupState(t, undisturbed)
	if freeCursor == 0 {
		t.Fatal("the undisturbed pass consumed the whole window; this seed no longer exercises sub-windowing")
	}

	yielding := newSpreadDuck(t, rows, spreadMins)
	// A publisher can only queue while something is reading, so the test holds
	// a reader of its own. The publisher then stays queued for the whole pass,
	// which is what the rollup checks at each sub-window boundary. The grace
	// period has not expired, so the pass is still admitted.
	if err := yielding.parquetMu.RLockContext(ctx); err != nil {
		t.Fatalf("hold a reader: %v", err)
	}
	queued := queuePublisher(t, yielding)

	if _, err := yielding.refreshEdgeRollup(ctx); err != nil {
		t.Fatalf("yielding pass: %v", err)
	}
	watermark, yieldCursor := readEdgeRollupState(t, yielding)
	if yieldCursor == 0 {
		t.Fatal("the pass ran the whole window with a publisher waiting on it")
	}
	if watermark != 0 {
		t.Fatalf("ingested watermark advanced to %d over a window the pass did not finish", watermark)
	}
	if yieldCursor >= freeCursor {
		t.Fatalf("yielding pass reached cursor %d, no earlier than the undisturbed %d", yieldCursor, freeCursor)
	}

	// Yielding must not stall the rollup: with the publisher gone the same
	// instance finishes the window.
	yielding.parquetMu.RUnlock()
	queued.done()
	for passes := 0; ; passes++ {
		if passes > spreadMins/30+4 {
			t.Fatal("edge rollup did not converge after yielding")
		}
		if _, err := yielding.refreshEdgeRollup(ctx); err != nil {
			t.Fatalf("catch-up pass: %v", err)
		}
		if _, cursor := readEdgeRollupState(t, yielding); cursor == 0 {
			break
		}
	}
	if watermark, _ = readEdgeRollupState(t, yielding); watermark == 0 {
		t.Fatal("watermark never advanced after the window completed")
	}
}

// A publisher that is always queued must not stop the rollup dead: every pass
// has to complete at least one sub-window, or a busy compaction cycle starves
// the cache the whole product reads from.
func TestEdgeRollupAlwaysCompletesOneSubWindow(t *testing.T) {
	ctx := context.Background()
	d := newSpreadDuck(t, 4_000, 180)
	if err := d.parquetMu.RLockContext(ctx); err != nil {
		t.Fatalf("hold a reader: %v", err)
	}
	queued := queuePublisher(t, d)

	rows, err := d.refreshEdgeRollup(ctx)
	if err != nil {
		t.Fatalf("pass with a publisher queued: %v", err)
	}
	if rows == 0 {
		t.Fatal("a pass yielded before doing any work; a busy compaction cycle would starve the cache")
	}
	d.parquetMu.RUnlock()
	queued.done()
}

// queuePublisher parks a publication in the gate's wait list and returns a
// handle that releases it. It requires a reader to already hold the snapshot,
// which is the only state in which a publisher waits rather than enters.
func queuePublisher(t *testing.T, d *Duck) (handle struct{ done func() }) {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		if err := d.parquetMu.LockContext(context.Background()); err != nil {
			close(entered)
			return
		}
		close(entered)
		<-release
		d.parquetMu.Unlock()
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !d.parquetMu.publisherQueued() {
		if time.Now().After(deadline) {
			t.Fatal("publisher never queued")
		}
		time.Sleep(time.Millisecond)
	}
	handle.done = func() {
		<-entered
		close(release)
	}
	return handle
}
