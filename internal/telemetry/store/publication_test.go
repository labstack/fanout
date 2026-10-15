package store

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestPublicationHorizonWaitsForEarlierAttempts(t *testing.T) {
	r := &Repository{}
	a := r.beginPublication(100)
	b := r.beginPublication(90)
	if a != 100 || b != 101 {
		t.Fatalf("stamps %d %d", a, b)
	}
	r.finishPublication(b, telemetry.BatchMetadata{MinIngestedNanos: b, MaxIngestedNanos: b})
	if h, p, _ := r.PublicationBounds(); h != 99 || p != 101 {
		t.Fatalf("bounds %d %d", h, p)
	}
	r.finishPublication(a, telemetry.BatchMetadata{MinIngestedNanos: a, MaxIngestedNanos: a})
	if h, p, lo := r.PublicationBounds(); h != 101 || p != 101 || lo != 100 {
		t.Fatalf("bounds %d %d %d", h, p, lo)
	}
}
func TestCommitRetryRestampsEverySignal(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	batch := testBatch()
	path := r.Parquet.BatchPath(batch.ID)
	if err := os.WriteFile(path, []byte("block rename"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(t.Context(), batch); err == nil {
		t.Fatal("blocked rename succeeded")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	failed := batch.Spans[0].IngestedAt
	if err := r.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	stamp := batch.Spans[0].IngestedAt
	if stamp <= failed || batch.Logs[0].IngestedAt != stamp || batch.Metrics[0].IngestedAt != stamp {
		t.Fatalf("retry did not restamp all rows: %#v", batch)
	}
	h, p, lo := r.PublicationBounds()
	if h != stamp || p != stamp || lo != failed {
		t.Fatalf("bounds %d %d %d stamp %d", h, p, lo, stamp)
	}
	rows, err := traceAll(r, "trace-1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("published retry: %#v %v", rows, err)
	}
}
func TestPublicationStampsStayMonotonicAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a clock ahead of wall time, then a backwards clock on reopen.
	r.lastStamp = time.Now().Add(time.Hour).UnixNano()
	batch := testBatch()
	if err := r.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	previous := batch.Spans[0].IngestedAt
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	batch.ID = "after-restart"
	if err := r.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if batch.Spans[0].IngestedAt != previous+1 {
		t.Fatalf("stamp %d after %d", batch.Spans[0].IngestedAt, previous)
	}
}

func TestCommitAdoptsPriorAttemptStamp(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	batch := testBatch()
	// A durable directory from an earlier attempt can be registered on retry.
	normalizeBatch(&batch, 100)
	if err := r.Parquet.CommitBatch(t.Context(), &telemetry.BatchMetadata{ID: batch.ID, MinIngestedNanos: 100, MaxIngestedNanos: 100}, batch.Spans, batch.Logs, batch.Metrics); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	_, published, minimum := r.PublicationBounds()
	if published != 100 || minimum != 100 {
		t.Fatalf("adopted bounds %d %d", published, minimum)
	}
}

func TestGenerationZeroInventorySurvivesRestartAndCompaction(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		batch := testBatch()
		batch.ID = fmt.Sprint("batch-", i)
		if err := r.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if r.Parquet.Generation0BatchCount() != 8 {
		t.Fatal("commits not counted")
	}
	r.Close()
	r, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Parquet.Generation0BatchCount() != 8 {
		t.Fatal("restart lost inventory")
	}
	if n, err := r.CompactParquet(t.Context(), &testParquetCompactor{}, 8); err != nil || n != 8 {
		t.Fatalf("compaction %d %v", n, err)
	}
	if r.Parquet.Generation0BatchCount() != 0 {
		t.Fatal("compaction did not retire generation zero")
	}
}

func TestFailedFirstPublicationKeepsInitialMinimum(t *testing.T) {
	r := &Repository{}
	first := r.beginPublication(100)
	r.finishPublication(first, telemetry.BatchMetadata{})
	second := r.beginPublication(200)
	r.finishPublication(second, telemetry.BatchMetadata{MinIngestedNanos: second, MaxIngestedNanos: second})
	horizon, published, minimum := r.PublicationBounds()
	if horizon != second || published != second || minimum != first {
		t.Fatalf("bounds=%d %d %d want=%d %d %d", horizon, published, minimum, second, second, first)
	}
}
