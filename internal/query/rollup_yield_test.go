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
func seedEdgeSpread(t *testing.T, d *Duck, baseTime time.Time, rows, spreadMins int) {
	t.Helper()
	ctx := context.Background()
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

func newSpreadDuck(t *testing.T, baseTime time.Time, rows, spreadMins int) *Duck {
	t.Helper()
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}
	d := &Duck{DB: db, writeDB: db, publicationBounds: tablePublicationBounds(db), cfg: config.Config{RetentionDays: 30, DuckDBMemory: "1GB"}}
	seedEdgeSpread(t, d, baseTime, rows, spreadMins)
	return d
}

// A fresh publisher must get the snapshot at the first resumable boundary,
// while the pass still completes at least one sub-window and can resume later.
func TestEdgeRollupYieldsToAQueuedPublisher(t *testing.T) {
	const (
		rows       = 6_000
		spreadMins = 300
	)
	ctx := context.Background()
	// Both databases need the same buckets even if setup crosses a minute.
	baseTime := time.Now().UTC().Truncate(time.Minute)

	undisturbed := newSpreadDuck(t, baseTime, rows, spreadMins)
	if _, err := undisturbed.refreshEdgeRollup(ctx); err != nil {
		t.Fatalf("undisturbed pass: %v", err)
	}
	_, freeCursor := readEdgeRollupState(t, undisturbed)
	if freeCursor == 0 {
		t.Fatal("the undisturbed pass consumed the whole window; this seed no longer exercises sub-windowing")
	}
	wantCursor := baseTime.Add(-time.Duration(spreadMins-1)*time.Minute + 30*time.Minute).UnixNano()
	if freeCursor != wantCursor {
		t.Fatalf("undisturbed pass cursor=%d, want one sub-window ending at %d", freeCursor, wantCursor)
	}

	yielding := newSpreadDuck(t, baseTime, rows, spreadMins)
	if _, err := edgePassBeforeQueuedPublisher(t, yielding, false); err != nil {
		t.Fatalf("yielding pass: %v", err)
	}
	watermark, yieldCursor := readEdgeRollupState(t, yielding)
	if yieldCursor == 0 {
		t.Fatal("the pass ran the whole window with a publisher waiting on it")
	}
	if watermark != 0 {
		t.Fatalf("ingested watermark advanced to %d over a window the pass did not finish", watermark)
	}
	if yieldCursor != freeCursor {
		t.Fatalf("queued and undisturbed passes must each commit one sub-window: queued=%d undisturbed=%d", yieldCursor, freeCursor)
	}

	// Yielding must not stall the rollup: with the publisher gone the same
	// instance finishes the window.
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

// An admitted pass completes one sub-window even if a publisher's grace
// expires while the pass is in flight.
func TestEdgeRollupAlwaysCompletesOneSubWindow(t *testing.T) {
	baseTime := time.Now().UTC().Truncate(time.Minute)
	d := newSpreadDuck(t, baseTime, 4_000, 180)
	rows, err := edgePassBeforeQueuedPublisher(t, d, true)
	if err != nil {
		t.Fatalf("pass with a publisher queued: %v", err)
	}
	if rows == 0 {
		t.Fatal("an admitted pass yielded before doing any work")
	}
}

// Hold the only SQL connection after loading admission's prerequisites. The
// pass can take its snapshot but cannot begin SQL until publication queues.
func edgePassBeforeQueuedPublisher(t *testing.T, d *Duck, overdue bool) (int64, error) {
	t.Helper()
	ctx := t.Context()
	horizon, published, minimum := d.publicationBounds()
	d.publicationBounds = func() (int64, int64, int64) { return horizon, published, minimum }
	if _, err := d.cachedRollupWatermark(ctx, edgeRollupStateKey, horizon); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Now()}
	d.parquetMu.now = clock.Now
	conn, err := d.writer().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	type result struct {
		rows int64
		err  error
	}
	finished := make(chan result, 1)
	go func() { rows, err := d.refreshEdgeRollup(ctx); finished <- result{rows, err} }()
	deadline := time.Now().Add(time.Second)
	for readers(&d.parquetMu) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("edge pass never entered its snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	publishedResult := make(chan error, 1)
	go func() { publishedResult <- d.PublishParquet(ctx, func(context.Context) error { return nil }) }()
	waitForQueuedWriter(t, &d.parquetMu)
	if overdue {
		clock.Advance(defaultWriterGrace + time.Second)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	pass := <-finished
	if err := <-publishedResult; err != nil {
		t.Fatal(err)
	}
	return pass.rows, pass.err
}

func readers(gate *parquetReadGate) int {
	gate.init()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.readers
}
