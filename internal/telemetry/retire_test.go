package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func publishDirect(publish func(context.Context) error) error { return publish(context.Background()) }

// stagedReplacement commits inputs and an output with the same rows, then
// moves the output to a staging directory as compaction would leave it.
func stagedReplacement(t *testing.T, dir string, inputs ...string) (BatchMetadata, string) {
	t.Helper()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var spans []Span
	for i, id := range inputs {
		span := []Span{{TraceID: "trace", SpanID: id, StartUnixNanos: int64(i + 1)}}
		spans = append(spans, span...)
		if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: id, MinIngestedNanos: 1, MaxIngestedNanos: 1}, span, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	output := BatchMetadata{ID: "output", MinIngestedNanos: 1, MaxIngestedNanos: 1, Generation: 1}
	if err := store.CommitBatch(context.Background(), &output, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(t.TempDir(), "output")
	if err := os.Rename(store.BatchPath("output"), stage); err != nil {
		t.Fatal(err)
	}
	return output, stage
}

func batchIDs(store *ParquetStore) []string {
	var ids []string
	for _, b := range store.BatchMetadata() {
		ids = append(ids, b.ID)
	}
	slices.Sort(ids)
	return ids
}

// A reader that captured the catalog before a replacement keeps every file it
// captured until it unpins; publication itself never waits for it.
func TestPinnedReaderSurvivesReplacement(t *testing.T) {
	dir := t.TempDir()
	output, stage := stagedReplacement(t, dir, "a", "b")
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pin := store.Pin()
	captured := store.BatchMetadata()
	if err := store.PublishReplacement(stage, output, []string{"a", "b"}, publishDirect); err != nil {
		t.Fatal(err)
	}
	if got := batchIDs(store); !slices.Equal(got, []string{"output"}) {
		t.Fatalf("catalog after publication = %v, want output only", got)
	}
	if !store.RetirementWaiting(pin) {
		t.Fatal("retired inputs do not report waiting for the pinned reader")
	}
	late := store.Pin()
	if store.RetirementWaiting(late) {
		t.Fatal("a reader pinned after publication holds retired inputs")
	}
	store.Unpin(late)
	if n, err := store.CollectRetired(); err != nil || n != 0 {
		t.Fatalf("collected %d retired inputs under a pinned reader: %v", n, err)
	}
	for _, b := range captured {
		if _, err := os.Stat(filepath.Join(store.BatchPath(b.ID), "spans.parquet")); err != nil {
			t.Fatalf("captured batch %s lost its files: %v", b.ID, err)
		}
	}
	store.Unpin(pin)
	if n, err := store.CollectRetired(); err != nil || n != 2 {
		t.Fatalf("collected %d retired inputs after the reader left, want 2: %v", n, err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := os.Stat(store.BatchPath(id)); !os.IsNotExist(err) {
			t.Fatalf("retired input %s remains: %v", id, err)
		}
	}
	if store.RetiringBatches() != 0 {
		t.Fatalf("retiring = %d after collection", store.RetiringBatches())
	}
}

// A crash after the output's rename but before its inputs are deleted leaves
// both on disk. The output's replaces list retires the inputs on open, so no
// row is counted twice and none is lost.
func TestOpenRetiresInputsLeftByACrashAfterReplacement(t *testing.T) {
	dir := t.TempDir()
	output, stage := stagedReplacement(t, dir, "a", "b")
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishReplacement(stage, output, []string{"a", "b"}, publishDirect); err != nil {
		t.Fatal(err)
	}
	// Crash: the process exits without collecting retired inputs.
	reopened, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := batchIDs(reopened); !slices.Equal(got, []string{"output"}) {
		t.Fatalf("catalog after crash = %v, want output only", got)
	}
	if rows := reopened.RowCount(); rows != 2 {
		t.Fatalf("rows after crash = %d, want 2", rows)
	}
	if n, err := reopened.CollectRetired(); err != nil || n != 2 {
		t.Fatalf("collected %d inputs on open, want 2: %v", n, err)
	}
	again, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, rows := batchIDs(again), again.RowCount(); !slices.Equal(got, []string{"output"}) || rows != 2 {
		t.Fatalf("catalog after collection = %v rows %d", got, rows)
	}
}

// A retried commit of an input that is retired but not yet deleted must not
// adopt its directory back into the catalog.
func TestRetiredBatchIsNotAdoptedByARetriedCommit(t *testing.T) {
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
	metadata := BatchMetadata{ID: "a"}
	if err := store.CommitBatch(context.Background(), &metadata, []Span{{TraceID: "trace", SpanID: "a", StartUnixNanos: 1}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := batchIDs(store); !slices.Equal(got, []string{"output"}) {
		t.Fatalf("catalog after retried commit = %v, want output only", got)
	}
}

// Deletion order keeps every replaced batch behind the batch that retires it:
// deleting a retired output first would orphan its own inputs.
func TestRetiredAtLoadOrdersInputsBeforeTheirReplacements(t *testing.T) {
	present := map[string]bool{"a": true, "b": true, "o1": true, "o2": true, "c": true}
	replaces := map[string][]string{"o1": {"a", "b"}, "o2": {"o1", "c"}}
	got := retiredAtLoad(present, replaces)
	index := map[string]int{}
	for i, id := range got {
		index[id] = i
	}
	if len(got) != 4 || index["o1"] < index["a"] || index["o1"] < index["b"] {
		t.Fatalf("retirement order = %v", got)
	}
	if _, ok := index["o2"]; ok {
		t.Fatalf("the live output was retired: %v", got)
	}
}

// Retention removes batches from the catalog at once and deletes them only
// after earlier readers leave. A crash in between republishes them, and the
// next pass prunes them again.
func TestRetentionDefersDeletionPastPinnedReaders(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: "expired", MinIngestedNanos: 1, MaxIngestedNanos: 1}, []Span{{TraceID: "t"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	pin := store.Pin()
	if n, err := store.PruneBefore(2, 10, publishDirect); err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	if len(store.BatchMetadata()) != 0 {
		t.Fatal("pruned batch remains in the catalog")
	}
	if n, err := store.CollectRetired(); err != nil || n != 0 {
		t.Fatalf("collected %d under a pinned reader: %v", n, err)
	}
	crashed, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(crashed.BatchMetadata()) != 1 {
		t.Fatal("an uncollected pruned batch did not reappear after a crash")
	}
	store.Unpin(pin)
	if n, err := store.CollectRetired(); err != nil || n != 1 {
		t.Fatalf("collected %d after the reader left: %v", n, err)
	}
	if _, err := os.Stat(store.BatchPath("expired")); !os.IsNotExist(err) {
		t.Fatalf("pruned batch remains: %v", err)
	}
}
