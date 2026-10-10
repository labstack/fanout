package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Single-pass assertions exercise the same native transaction as the tick,
// without invoking its catch-up loop.
func (d *Duck) refreshEdgeRollup(ctx context.Context) (int64, error) {
	n, _, err := d.refreshEdgeRollupPass(ctx)
	return n, err
}

func TestEdgeRollupTickDrainsMultiHourBacklog(t *testing.T) {
	for _, step := range []time.Duration{0, 20 * time.Minute} {
		t.Run(step.String(), func(t *testing.T) {
			d, repo := versionEngine(t)
			d.cfg.RollupInterval = time.Minute
			now := time.Now().UTC().Truncate(time.Minute)
			base := now.Add(-4 * time.Hour)
			seedAnalyticalBacklog(t, repo, base, now.Add(-4*time.Hour).UnixNano(), step)
			if _, err := d.rollupOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			watermark, cursor := readEdgeRollupState(t, d)
			source := now.Add(-4*time.Hour + 11*step).UnixNano()
			want := source - d.rollupSafetyLagNanos()
			if cursor != 0 || watermark != want {
				t.Fatalf("one tick did not drain the backlog: watermark=%d cursor=%d want=%d", watermark, cursor, want)
			}
			assertBacklogEdges(t, d)
			// The unchanged plateau rule completes the safety-lag tail on the
			// next tick, rather than bypassing the lag inside this tick.
			if _, err := d.rollupOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if watermark, cursor := readEdgeRollupState(t, d); watermark != source || cursor != 0 {
				t.Fatalf("plateau tick: watermark=%d cursor=%d want=%d", watermark, cursor, source)
			}
			assertBacklogEdges(t, d)
			if n, err := d.refreshEdgeRollup(t.Context()); err != nil || n != 0 {
				t.Fatalf("caught-up edge pass: rows=%d err=%v", n, err)
			}
		})
	}
}

func TestEdgeRollupInitialProgressUsesIngestWindow(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().Truncate(time.Minute)
	seedAnalyticalBacklog(t, repo, now.Add(-3*time.Hour), now.UnixNano(), 0)
	if _, err := d.refreshEdgeRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.RollupBacklogChunks.WithLabelValues("edge")); got != 1 {
		t.Fatalf("initial backlog includes time before the first ingested row: chunks=%v want=1", got)
	}
	watermark, cursor := readEdgeRollupState(t, d)
	if watermark != 0 || cursor == 0 {
		t.Fatalf("progress reporting changed durable partial-window state: watermark=%d cursor=%d", watermark, cursor)
	}
}

func TestEdgeRollupTickReleasesGateBetweenSubWindows(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().Truncate(time.Minute)
	base := now.Add(-3 * time.Hour)
	seedAnalyticalBacklog(t, repo, base, now.UnixNano(), 0)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	passes := 0
	n, err := drainEdgeRollup(ctx, time.Minute/2, func(ctx context.Context) (int64, bool, error) {
		rows, pending, err := d.refreshEdgeRollupPass(ctx)
		if err != nil {
			return rows, pending, err
		}
		passes++
		watermark, cursor := readEdgeRollupState(t, d)
		if passes == 1 && (watermark != 0 || cursor != base.Add(30*time.Minute).UnixNano()) {
			t.Fatalf("first pass exceeded one sub-window: watermark=%d cursor=%d", watermark, cursor)
		}
		// Barrier on every pass: the real anomaly writer must commit before
		// the tick starts its next pass. Holding the gate across passes would
		// deadlock this native write until the test's context expires.
		written := make(chan error, 1)
		go func() {
			written <- d.RecordAnomalies(ctx, []annotations.Anomaly{{Namespace: "ns-a", Service: "gateway", Kind: "latency", From: base, To: base.Add(time.Minute), Title: "Backlog", Severity: "warning"}}, now)
		}()
		if err := <-written; err != nil {
			return rows, pending, err
		}
		if w, c := readEdgeRollupState(t, d); w != watermark || c != cursor {
			t.Fatal("edge cursor moved while the anomaly writer committed")
		}
		return rows, pending, nil
	})
	if err != nil || n == 0 || passes < 6 {
		t.Fatalf("tick did not advance many passes: rows=%d passes=%d err=%v", n, passes, err)
	}
	watermark, cursor := readEdgeRollupState(t, d)
	if watermark != now.UnixNano()-d.rollupSafetyLagNanos() || cursor != 0 {
		t.Fatalf("tick left a backlog: watermark=%d cursor=%d", watermark, cursor)
	}
	assertBacklogEdges(t, d)
	var anomalies int
	if err := d.DB.QueryRowContext(ctx, `SELECT count(*) FROM anomaly_log`).Scan(&anomalies); err != nil || anomalies != 1 {
		t.Fatalf("anomaly not visible: rows=%d err=%v", anomalies, err)
	}
}

func TestEdgeRollupTickBudgetStopsAtCommittedPass(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().Truncate(time.Minute)
	base := now.Add(-3 * time.Hour)
	seedAnalyticalBacklog(t, repo, base, now.UnixNano(), 0)
	// One nanosecond is spent before the first native pass finishes. The pass
	// must commit, but a second pass cannot start after the tick's budget.
	if _, err := drainEdgeRollup(t.Context(), time.Nanosecond, d.refreshEdgeRollupPass); err != nil {
		t.Fatal(err)
	}
	watermark, cursor := readEdgeRollupState(t, d)
	if watermark != 0 || cursor != base.Add(30*time.Minute).UnixNano() {
		t.Fatalf("budget failed to stop at the first committed pass: watermark=%d cursor=%d", watermark, cursor)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := drainEdgeRollup(ctx, time.Minute/2, d.refreshEdgeRollupPass); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled tick: %v", err)
	}
	if w, c := readEdgeRollupState(t, d); w != watermark || c != cursor {
		t.Fatal("canceled tick changed committed progress")
	}
	if _, err := drainEdgeRollup(t.Context(), time.Minute/2, d.refreshEdgeRollupPass); err != nil {
		t.Fatal(err)
	}
	assertBacklogEdges(t, d)
}

func TestEdgeRollupTickContinuesPastEmptyPendingPass(t *testing.T) {
	passes := 0
	n, err := drainEdgeRollup(t.Context(), time.Minute/2, func(context.Context) (int64, bool, error) {
		passes++
		if passes == 1 {
			return 0, true, nil
		}
		return 3, false, nil
	})
	if err != nil || n != 3 || passes != 2 {
		t.Fatalf("empty pending pass stopped catch-up: rows=%d passes=%d err=%v", n, passes, err)
	}
}

func TestEdgeRollupTickPreservesPublicationLag(t *testing.T) {
	d, repo := versionEngine(t)
	d.cfg.RollupInterval = time.Minute
	now := time.Now().UTC().Truncate(time.Minute)
	seedAnalyticalBacklog(t, repo, now.Add(-3*time.Hour), now.UnixNano(), 0)
	if _, err := d.rollupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	watermark, cursor := readEdgeRollupState(t, d)
	if want := now.UnixNano() - d.rollupSafetyLagNanos(); watermark != want || cursor != 0 {
		t.Fatalf("tick consumed the publication lag: watermark=%d cursor=%d want=%d", watermark, cursor, want)
	}
	base := now.Add(-3 * time.Hour).UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "late-edge", Spans: []telemetry.Span{{Namespace: "ns-a", ServiceName: "late-child", TraceID: "0", SpanID: "late", ParentSpanID: "s0", Kind: "SPAN_KIND_CLIENT", StartUnixNanos: base, EndUnixNanos: base + int64(time.Millisecond), DurationMS: 1, IngestedAt: now.Add(-time.Second).UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.rollupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	var calls int
	if err := d.DB.QueryRowContext(t.Context(), `SELECT coalesce(sum(calls),0) FROM edge_rollup WHERE namespace='ns-a' AND callee='late-child'`).Scan(&calls); err != nil || calls != 1 {
		t.Fatalf("late child lost after catch-up: calls=%d err=%v", calls, err)
	}
}
