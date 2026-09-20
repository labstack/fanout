package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A merge that dies takes the process with it and leaves nothing behind saying
// so: the completion marker is written after PrepareReplacement, so an
// out-of-memory kill mid-merge is indistinguishable from never having started.
// The next start then reselects the same group by the same rules and dies the
// same way. Recording the attempt before the merge is what turns that loop
// into a descent.
func TestCompactionBatchCapHalvesAfterAnInterruptedPass(t *testing.T) {
	tests := []struct {
		name        string
		maxBatches  int
		lastAttempt int
		want        int
	}{
		{name: "no previous attempt leaves the ceiling alone", maxBatches: 128, lastAttempt: 0, want: 128},
		{name: "an interrupted pass halves it", maxBatches: 128, lastAttempt: 128, want: 64},
		{name: "halving compounds across restarts", maxBatches: 128, lastAttempt: 64, want: 32},
		{name: "never below a pair, which always merges", maxBatches: 128, lastAttempt: 2, want: 2},
		{name: "an attempt larger than the ceiling still halves from the ceiling", maxBatches: 16, lastAttempt: 128, want: 8},
		{name: "a nonsensical attempt does not raise the ceiling", maxBatches: 16, lastAttempt: -5, want: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := compactionBatchCap(test.maxBatches, test.lastAttempt); got != test.want {
				t.Errorf("compactionBatchCap(%d, %d) = %d, want %d",
					test.maxBatches, test.lastAttempt, got, test.want)
			}
		})
	}
}

// The record has to survive a kill -9, so it is a file rather than process
// state, and a missing or unreadable one must read as "no attempt" rather than
// stopping compaction: a corrupt byte here should cost a larger merge, not the
// ability to compact at all.
func TestCompactionAttemptRoundTripsAndToleratesDamage(t *testing.T) {
	root := t.TempDir()

	if got := readCompactionAttempt(root); got != 0 {
		t.Errorf("readCompactionAttempt on a clean root = %d, want 0", got)
	}

	if err := writeCompactionAttempt(root, 32); err != nil {
		t.Fatalf("writeCompactionAttempt: %v", err)
	}
	if got := readCompactionAttempt(root); got != 32 {
		t.Errorf("readCompactionAttempt after write = %d, want 32", got)
	}

	if err := os.WriteFile(filepath.Join(root, compactionAttemptFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readCompactionAttempt(root); got != 0 {
		t.Errorf("readCompactionAttempt on damaged file = %d, want 0", got)
	}

	if err := clearCompactionAttempt(root); err != nil {
		t.Fatalf("clearCompactionAttempt: %v", err)
	}
	if got := readCompactionAttempt(root); got != 0 {
		t.Errorf("readCompactionAttempt after clear = %d, want 0", got)
	}
	if err := clearCompactionAttempt(root); err != nil {
		t.Errorf("clearCompactionAttempt must be idempotent, got %v", err)
	}
}

// End to end: a record left by a pass that never returned must shrink the next
// one, and a pass that does return must clear it so the group can grow back.
// Without the second half the first kill would permanently halve compaction.
func TestCompactParquetShrinksAfterAnInterruptedPassAndRecovers(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for i := range minCompactionInputs {
		batch := testBatch()
		batch.ID = fmt.Sprintf("batch-%d", i)
		batch.Spans[0].SpanID = fmt.Sprintf("span-%d", i)
		batch.Spans[0].StartUnixNanos = int64(100 + i)
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}

	// Stand in for a process killed mid-merge: the record is on disk, the
	// completion marker never got written.
	if err := writeCompactionAttempt(dir, minCompactionInputs); err != nil {
		t.Fatal(err)
	}

	compactor := &testParquetCompactor{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	compacted, err := repository.CompactParquet(ctx, compactor, minCompactionInputs)
	if err != nil {
		t.Fatalf("CompactParquet after an interrupted pass: %v", err)
	}
	if compacted == 0 {
		t.Fatal("compacted nothing; an interrupted pass must shrink the group, not stop compaction")
	}
	if compacted >= minCompactionInputs {
		t.Errorf("compacted %d inputs, want fewer than the %d the interrupted pass tried",
			compacted, minCompactionInputs)
	}
	if left := readCompactionAttempt(dir); left != 0 {
		t.Errorf("attempt record still reads %d after a completed pass; the group could never grow back", left)
	}
}
