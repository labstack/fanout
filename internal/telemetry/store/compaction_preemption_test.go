package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestHigherGenerationMergeYieldsWithoutBlockingDebtRetirement(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seed := Batch{ID: "seed", Logs: []telemetry.Log{{Body: "typed", TimeUnixNanos: 100}}}
	if err := r.Commit(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	// Real immutable files and catalog mutations cross the same threshold as
	// ingest. Only one small log file is shared; no fake debt counters.
	for i := 0; i < telemetry.CompactionDebtLimit+telemetry.CompactionBatchLimit-2; i++ {
		id := fmt.Sprint("debt-", i)
		stage := filepath.Join(r.root, "ready", id)
		if err := os.MkdirAll(stage, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(r.Parquet.BatchPath("seed"), "logs.parquet"), filepath.Join(stage, "logs.parquet")); err != nil {
			t.Fatal(err)
		}
		m := telemetry.BatchMetadata{ID: id, Logs: 1, MinIngestedNanos: time.Now().UnixNano(), MaxIngestedNanos: time.Now().UnixNano()}
		if err := r.Parquet.PrepareReplacement(stage, m); err != nil {
			t.Fatal(err)
		}
		if err := r.Parquet.PublishReplacement(stage, m, nil, func(f func(context.Context) error) error { return f(t.Context()) }); err != nil {
			t.Fatal(err)
		}
	}
	if r.CompactionDebt() != telemetry.CompactionDebtLimit {
		t.Fatal(r.CompactionDebt())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = r.mergeCompaction(ctx, 2, 8, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, errCompactionPreempted) {
		t.Fatal(err)
	}
	if err := r.mergeCompaction(ctx, 0, 8, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatalf("generation zero canceled by debt: %v", err)
	}
	if err := r.mergeCompaction(ctx, 2, 2, func(mergeCtx context.Context) error {
		if mergeCtx != ctx {
			t.Fatal("minimum pair is still cancellable by debt")
		}
		return mergeCtx.Err()
	}); err != nil {
		t.Fatalf("minimum pair canceled by debt: %v", err)
	}
	failure := errors.New("input failure")
	if err := r.mergeCompaction(ctx, 2, 8, func(context.Context) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("lost storage error: %v", err)
	}
	// No watcher holds compactionMu, the catalog mutex, or the publish gate.
	// Ordinary compaction can retire debt immediately after the canceled work.
	if n, err := r.CompactParquet(ctx, &testParquetCompactor{}, 128); err != nil || n != 128 {
		t.Fatalf("retire: %d %v", n, err)
	}
	if _, err := r.WaitForCompaction(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.RecoverParquet(ctx, &testParquetCompactor{}); err != nil {
		t.Fatal(err)
	}
	if err := r.CleanupParquet(); err != nil {
		t.Fatal(err)
	}
	rows := r.RowCount()
	if want := uint64(telemetry.CompactionDebtLimit + telemetry.CompactionBatchLimit - 1); rows != want {
		t.Fatalf("compaction lost rows: got %d want %d", rows, want)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.RowCount() != rows || reopened.CompactionDebt() != r.CompactionDebt() {
		t.Fatal("recovery changed inventory")
	}
}

func TestHigherGenerationMergeJoinsWatcherOnCompletionAndCallerCancellation(t *testing.T) {
	r := &Repository{Parquet: &telemetry.ParquetStore{}}
	for range 20 {
		if err := r.mergeCompaction(t.Context(), 1, 8, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		err := r.mergeCompaction(ctx, 1, 8, func(ctx context.Context) error { cancel(); <-ctx.Done(); return ctx.Err() })
		cancel()
		if !errors.Is(err, context.Canceled) || errors.Is(err, errCompactionPreempted) {
			t.Fatalf("caller cancellation: %v", err)
		}
	}
}

// Repeated hard-limit crossings must walk 8 -> 4 -> 2 even though successful
// generation-0 passes run between them and clear the durable attempt record.
func TestHigherGenerationPublishesAfterRepeatedDebtPreemption(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if err := r.Commit(ctx, Batch{ID: "seed", Logs: []telemetry.Log{{Body: "progress", TimeUnixNanos: 100}}}); err != nil {
		t.Fatal(err)
	}
	stamp := r.Parquet.BatchMetadata()[0].MaxIngestedNanos
	sequence := 0
	publishBatch := func(generation uint32) {
		t.Helper()
		sequence++
		id := fmt.Sprintf("batch-%05d", sequence)
		stage := filepath.Join(r.root, "ready", id)
		if err := os.MkdirAll(stage, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(r.Parquet.BatchPath("seed"), "logs.parquet"), filepath.Join(stage, "logs.parquet")); err != nil {
			t.Fatal(err)
		}
		m := telemetry.BatchMetadata{ID: id, Generation: generation, Logs: 1, MinIngestedNanos: stamp, MaxIngestedNanos: stamp}
		if err := r.Parquet.PrepareReplacement(stage, m); err != nil {
			t.Fatal(err)
		}
		if err := r.Parquet.PublishReplacement(stage, m, nil, func(f func(context.Context) error) error { return f(ctx) }); err != nil {
			t.Fatal(err)
		}
	}
	// Keep the shared immutable seed on a day with no mergeable peers.
	stamp += int64(24 * time.Hour)
	for range 8 {
		publishBatch(1)
	}
	fillDebt := func(target int) {
		t.Helper()
		for r.CompactionDebt() < target {
			publishBatch(0)
		}
	}
	compactor := &testParquetCompactor{}
	drainDebt := func() {
		t.Helper()
		for r.CompactionDebt() >= telemetry.CompactionDebtLimit/2 {
			before := r.CompactionDebt()
			if n, err := r.CompactParquet(ctx, compactor, 128); err != nil || n != 128 {
				t.Fatalf("generation-0 retirement: %d %v", n, err)
			}
			if r.CompactionDebt() != before-128 {
				t.Fatal("generation-0 debt did not retire")
			}
		}
	}
	fillDebt(telemetry.CompactionDebtLimit/2 - 1)
	for _, want := range []int{8, 4} {
		selected := selectCompactionBatchesWithHigherCap(r.Parquet.BatchMetadata(), 8,
			compactionBatchCap(8, r.preemptedCompactionInputs), r.CompactionDebt())
		if len(selected) != want || selected[0].Generation != 1 {
			t.Fatalf("higher-generation retry: got %v, want %d generation-1 inputs", selected, want)
		}
		err := r.mergeCompaction(ctx, 1, len(selected), func(mergeCtx context.Context) error {
			fillDebt(telemetry.CompactionDebtLimit)
			<-mergeCtx.Done()
			return mergeCtx.Err()
		})
		if !errors.Is(err, errCompactionPreempted) {
			t.Fatalf("debt preemption: %v", err)
		}
		drainDebt()
		if r.preemptedCompactionInputs != want || readCompactionAttempt(r.root) != 0 {
			t.Fatal("generation-0 completion erased higher-generation retry size")
		}
	}
	// Run the real native merge with the retained pair cap. Cross the threshold
	// again before its atomic swap: the higher-generation output must publish
	// even while ingest is blocked, then generation 0 must keep retiring debt.
	pairPublisher := testParquetPublisherFunc(func(ctx context.Context, publish func(context.Context) error) error {
		fillDebt(telemetry.CompactionDebtLimit)
		return publish(ctx)
	})
	if n, err := r.CompactParquet(ctx, pairPublisher, 128); err != nil || n != 2 {
		t.Fatalf("higher-generation publication: %d %v", n, err)
	}
	found := false
	for _, batch := range r.Parquet.BatchMetadata() {
		found = found || batch.Generation == 2 && batch.Logs == 2
	}
	if !found || r.preemptedCompactionInputs != 0 || r.CompactionDebt() != telemetry.CompactionDebtLimit {
		t.Fatal("higher-generation success did not publish and reset its size")
	}
	drainDebt()
	if n, err := r.CompactParquet(ctx, compactor, 8); err != nil || n != 8 {
		t.Fatalf("restored higher-generation size: %d %v", n, err)
	}
	if r.RowCount() != uint64(sequence+1) {
		t.Fatal("preemption or publication lost rows")
	}
}
