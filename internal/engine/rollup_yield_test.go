package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/config"
	"github.com/labstack/pitot/internal/telemetry"
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

// Retired files waiting for the pass get its snapshot at the first resumable
// boundary, while the pass still completes at least one sub-window and can
// resume later.
func TestEdgeRollupYieldsToWaitingRetirement(t *testing.T) {
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
	yielding.snapshotWanted = func(telemetry.SnapshotPin) bool { return true }
	n, err := yielding.refreshEdgeRollup(ctx)
	if err != nil {
		t.Fatalf("yielding pass: %v", err)
	}
	if n == 0 {
		t.Fatal("a pass yielded before doing any work")
	}
	watermark, yieldCursor := readEdgeRollupState(t, yielding)
	if yieldCursor == 0 {
		t.Fatal("the pass ran the whole window with retired files waiting on it")
	}
	if watermark != 0 {
		t.Fatalf("ingested watermark advanced to %d over a window the pass did not finish", watermark)
	}
	if yieldCursor != freeCursor {
		t.Fatalf("yielding and undisturbed passes must each commit one sub-window: yielding=%d undisturbed=%d", yieldCursor, freeCursor)
	}

	// Yielding must not stall the rollup: once nothing waits, the same
	// instance finishes the window.
	yielding.snapshotWanted = nil
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
