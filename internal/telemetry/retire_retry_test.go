package telemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A commit retried after its batch was compacted away and the batch's
// directory collected is already published: its rows live in the output.
func TestRetriedCommitOfCollectedInputIsNotRewritten(t *testing.T) {
	dir := t.TempDir()
	output, stage := stagedReplacement(t, dir, "a", "b")
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishReplacement(stage, output, []string{"a", "b"}, publishDirect); err != nil {
		t.Fatal(err)
	}
	if n, err := store.CollectRetired(); err != nil || n != 2 {
		t.Fatalf("collected %d inputs, want 2: %v", n, err)
	}
	metadata := BatchMetadata{ID: "a", MinIngestedNanos: 2, MaxIngestedNanos: 2}
	if err := store.CommitBatch(context.Background(), &metadata, []Span{{TraceID: "trace", SpanID: "a", StartUnixNanos: 1}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, rows := batchIDs(store), store.RowCount(); !slices.Equal(got, []string{"output"}) || rows != 2 {
		t.Fatalf("catalog after retried commit = %v rows %d, want output with 2 rows", got, rows)
	}
	if _, err := os.Stat(store.BatchPath("a")); !os.IsNotExist(err) {
		t.Fatalf("retried commit rewrote a collected input: %v", err)
	}
	// The same holds after a restart: the output's replaces list names it.
	reopened, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	metadata = BatchMetadata{ID: "b"}
	if err := reopened.CommitBatch(context.Background(), &metadata, []Span{{TraceID: "trace", SpanID: "b", StartUnixNanos: 2}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, rows := batchIDs(reopened), reopened.RowCount(); !slices.Equal(got, []string{"output"}) || rows != 2 {
		t.Fatalf("catalog after retried commit on reopen = %v rows %d", got, rows)
	}
}

// After a replacement whose directory fsync failed, its inputs leave the
// catalog but are not yet retired. A retried commit of one must not adopt the
// input's directory next to the output.
func TestRetriedCommitAfterReplacementSyncFailureIsNotAdopted(t *testing.T) {
	dir := t.TempDir()
	output, stage := stagedReplacement(t, dir, "a", "b")
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	originalSync := syncPublishedDirectory
	syncPublishedDirectory = func(string) error { return errors.New("injected directory sync failure") }
	t.Cleanup(func() { syncPublishedDirectory = originalSync })
	if err := store.PublishReplacement(stage, output, []string{"a", "b"}, publishDirect); err == nil {
		t.Fatal("replacement succeeded despite injected sync failure")
	}
	syncPublishedDirectory = originalSync
	metadata := BatchMetadata{ID: "a"}
	if err := store.CommitBatch(context.Background(), &metadata, []Span{{TraceID: "trace", SpanID: "a", StartUnixNanos: 1}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, rows := batchIDs(store), store.RowCount(); !slices.Equal(got, []string{"output"}) || rows != 2 {
		t.Fatalf("catalog after retried commit = %v rows %d, want output with 2 rows", got, rows)
	}
}

// Retention marks a batch durably before hiding it, so a crash before its
// directory is deleted does not bring the rows back.
func TestPrunedBatchStaysPrunedAfterACrash(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"expired", "kept"} {
		stamp := int64(1)
		if id == "kept" {
			stamp = 5
		}
		if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: id, MinIngestedNanos: stamp, MaxIngestedNanos: stamp}, []Span{{TraceID: "t", SpanID: id}}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	pin := store.Pin()
	defer store.Unpin(pin)
	if n, err := store.PruneBefore(2, 10, publishDirect); err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	// Crash: the pinned reader kept the directory in place.
	crashed, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := batchIDs(crashed); !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("catalog after crash = %v, want kept only", got)
	}
	if n, err := crashed.CollectRetired(); err != nil || n != 1 {
		t.Fatalf("collected %d pruned batches on open, want 1: %v", n, err)
	}
	if _, err := os.Stat(crashed.BatchPath("expired")); !os.IsNotExist(err) {
		t.Fatalf("pruned batch remains: %v", err)
	}
}

// A pruned compacted output retires the inputs its replaces list names, and
// collection after a crash deletes those inputs before the output.
func TestPrunedOutputRetiresItsInputsAfterACrash(t *testing.T) {
	dir := t.TempDir()
	output, stage := stagedReplacement(t, dir, "a", "b")
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pin := store.Pin()
	defer store.Unpin(pin)
	if err := store.PublishReplacement(stage, output, []string{"a", "b"}, publishDirect); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PruneBefore(2, 10, publishDirect); err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	crashed, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := batchIDs(crashed); len(got) != 0 {
		t.Fatalf("catalog after crash = %v, want empty", got)
	}
	// Removals before the output's must be durable before it is removed.
	var events []string
	original := syncRetiredDirectory
	syncRetiredDirectory = func(dir string) error {
		var present []string
		for _, id := range []string{"a", "b", "output"} {
			if _, err := os.Stat(crashed.BatchPath(id)); err == nil {
				present = append(present, id)
			}
		}
		events = append(events, filepath.Base(dir)+":"+strings.Join(present, ","))
		return original(dir)
	}
	t.Cleanup(func() { syncRetiredDirectory = original })
	if n, err := crashed.CollectRetired(); err != nil || n != 3 {
		t.Fatalf("collected %d batches, want 3: %v", n, err)
	}
	if !slices.Contains(events, "batches:output") {
		t.Fatalf("directory syncs %v; none made input removals durable before the output's", events)
	}
}

// A reader pinned after an earlier retirement still holds a later one.
func TestRetirementWaitingChecksTheNewestRetirement(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"first", "second"} {
		stamp := int64(i + 1)
		if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: id, MinIngestedNanos: stamp, MaxIngestedNanos: stamp}, []Span{{TraceID: "t", SpanID: id}}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	early := store.Pin()
	defer store.Unpin(early)
	if n, err := store.PruneBefore(2, 10, publishDirect); err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	late := store.Pin()
	defer store.Unpin(late)
	if store.RetirementWaiting(late) {
		t.Fatal("a reader pinned after the only retirement reports waiting")
	}
	if n, err := store.PruneBefore(3, 10, publishDirect); err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	if !store.RetirementWaiting(late) {
		t.Fatal("a retirement after the reader pinned does not report waiting")
	}
}
