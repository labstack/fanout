package engine

import (
	"context"
	"errors"
	"github.com/labstack/fanout/internal/readpressure"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"testing"
	"time"
)

func TestReadCacheYieldsBetweenCompleteBatches(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	for _, id := range []string{"one", "two"} {
		logs := make([]telemetry.Log, readCacheRowBudget+1)
		for i := range logs {
			logs[i] = telemetry.Log{TimeUnixNanos: int64(i + 1), ServiceName: "api"}
		}
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Logs: logs}); err != nil {
			t.Fatal(err)
		}
	}
	checks := 0
	d.SetReadPressure(func() bool {
		checks++
		var n int
		if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&n); err != nil || n != 1 {
			t.Errorf("previous cache chunk not committed: %d %v", n, err)
		}
		return false
	})
	for range 2 {
		if _, err := d.RefreshReadCaches(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if checks != 1 {
		t.Fatalf("pressure checked %d times for two chunks", checks)
	}
	var n int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("cache pass skipped batches: %d %v", n, err)
	}
}

func TestAffectedKeysYieldsEveryChunk(t *testing.T) {
	db := openTestDuck(t)
	var signal readpressure.Signal
	checks, runs := 0, 0
	signal.Set(func() bool { checks++; return false })
	ctx := readpressure.WithSignal(t.Context(), &signal)
	err := forAffectedKeys(ctx, db, `SELECT i FROM range(1001) t(i)`, nil, "i", "?", func(string, []any) error { runs++; return nil })
	if err != nil || checks != 1 || runs != 2 {
		t.Fatalf("checks=%d runs=%d err=%v", checks, runs, err)
	}
}
func TestBackgroundSlotSharedAndCancellable(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	release, err := d.lockBackground(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := d.RefreshReadCaches(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cache bypassed shared slot: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel2()
	if _, err := d.refreshServiceRollup(ctx2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rollup bypassed shared slot: %v", err)
	}
	release()
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestVersionRollupRunsInRollupTick(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), resourceBatch("version", []int64{time.Now().UnixNano()}, map[string]any{"service.version": "v1"})); err != nil {
		t.Fatal(err)
	}
	checks := 0
	d.SetReadPressure(func() bool { checks++; return false })
	if _, err := d.rollupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&n); err != nil || n != 1 || checks == 0 {
		t.Fatalf("version tick markers=%d checks=%d %v", n, checks, err)
	}
}
