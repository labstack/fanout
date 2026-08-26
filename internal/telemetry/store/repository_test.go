package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/telemetry"
)

func testBatch() Batch {
	return Batch{
		ID:      "0198f4a0-test-batch",
		Spans:   []telemetry.Span{{Namespace: "", TraceID: "trace-1", SpanID: "span-1", ServiceName: "api", Name: "GET /", StartUnixNanos: 100, EndUnixNanos: 200, DurationMS: .0001, StatusCode: "OK", IngestedAt: 300}},
		Logs:    []telemetry.Log{{TimeUnixNanos: 110, Severity: "INFO", Body: "ready", ServiceName: "api", TraceID: "trace-1", IngestedAt: 300}},
		Metrics: []telemetry.Metric{{TimeUnixNanos: 120, Name: "requests", Type: "sum", ServiceName: "api", Value: 1, IngestedAt: 300}},
	}
}

func TestRepositoryCommitIsIdempotentAndQueryable(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	if err := repository.Commit(batch); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(batch); err != nil {
		t.Fatal(err)
	}
	if got := repository.Spans.RowCount(); got != 1 {
		t.Fatalf("span rows = %d", got)
	}
	if got := repository.Logs.RowCount(); got != 1 {
		t.Fatalf("log rows = %d", got)
	}
	if got := repository.Metrics.RowCount(); got != 1 {
		t.Fatalf("metric rows = %d", got)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, signal := range []string{"spans", "logs", "metrics"} {
		var count int
		pattern := filepath.ToSlash(filepath.Join(dir, "parquet", signal, "*.parquet"))
		if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM read_parquet(?)", pattern).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s parquet rows = %d", signal, count)
		}
	}
}

func TestRepositoryCommitIODoesNotHoldMetadataLock(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	batch := testBatch()
	batch.ID = "lock-scope-batch"
	repository.mu.Lock()
	committed := make(chan error, 1)
	go func() { committed <- repository.Commit(batch) }()
	parquet := filepath.Join(dir, "parquet", "spans", batch.ID+".parquet")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(parquet); err == nil {
			break
		}
		if time.Now().After(deadline) {
			repository.mu.Unlock()
			t.Fatal("commit projection I/O remained blocked by repository metadata lock")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-committed:
		repository.mu.Unlock()
		t.Fatalf("Commit returned before metadata publication lock was released: %v", err)
	default:
	}
	repository.mu.Unlock()
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryReplaysDurableWAL(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	if err := repository.writeWAL(batch); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Spans.RowCount() != 1 || recovered.Logs.RowCount() != 1 || recovered.Metrics.RowCount() != 1 {
		t.Fatal("WAL recovery did not restore every signal")
	}
	entries, err := filepath.Glob(filepath.Join(dir, "wal", "*.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("committed WAL files remain: %v", entries)
	}
}

func TestRepositoryRejectsLegacyDuckLakeCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ducklake.sqlite"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil || !strings.Contains(err.Error(), "clean storage.data_dir") {
		t.Fatalf("Open error = %v, want explicit clean-data-dir failure", err)
	}
}

func TestRepositoryQuarantinesCorruptWAL(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wal", "poison.wal"), []byte("not-zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(dir)
	if err != nil {
		t.Fatalf("Open with poison WAL: %v", err)
	}
	defer recovered.Close()
	if _, err := os.Stat(filepath.Join(dir, "wal", "poison.wal.corrupt")); err != nil {
		t.Fatalf("quarantined WAL missing: %v", err)
	}
}

func TestRepositoryPrunesOnlyCompleteExpiredParquetBatches(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	old := testBatch()
	old.ID = "old-batch"
	old.Spans[0].IngestedAt, old.Logs[0].IngestedAt, old.Metrics[0].IngestedAt = 100, 100, 100
	newer := testBatch()
	newer.ID = "new-batch"
	newer.Spans[0].IngestedAt, newer.Logs[0].IngestedAt, newer.Metrics[0].IngestedAt = 1000, 1000, 1000
	if err := repository.Commit(old); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(newer); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.PruneParquet(500)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed batches = %d, want 1", removed)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if _, err := os.Stat(filepath.Join(dir, "parquet", signal, "old-batch.parquet")); !os.IsNotExist(err) {
			t.Fatalf("expired %s file remains: %v", signal, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "parquet", signal, "new-batch.parquet")); err != nil {
			t.Fatalf("new %s file missing: %v", signal, err)
		}
	}
}

func TestRepositoryCompactsParquetBatchesWithoutChangingRows(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for i := range 8 {
		batch := testBatch()
		batch.ID = fmt.Sprintf("batch-%d", i)
		batch.Spans[0].SpanID = fmt.Sprintf("span-%d", i)
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	compacted, err := repository.CompactParquet(context.Background(), db, 64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if compacted != 8 {
		t.Fatalf("compacted batches = %d, want 8", compacted)
	}
	stats, err := repository.Parquet.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["spans"].Files != 1 {
		t.Fatalf("span files = %d, want 1", stats["spans"].Files)
	}
	pattern := filepath.ToSlash(filepath.Join(dir, "parquet", "spans", "*.parquet"))
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM read_parquet(?)", pattern).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 8 {
		t.Fatalf("compacted rows = %d, want 8", rows)
	}
}

func TestRepositoryCompactionDrainsBacklog(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for i := range 72 {
		batch := testBatch()
		batch.ID = fmt.Sprintf("backlog-%d", i)
		batch.Spans[0].SpanID = fmt.Sprintf("span-%d", i)
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	compacted, err := repository.CompactParquetBacklog(context.Background(), db, 64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if compacted <= 64 {
		t.Fatalf("compacted batches = %d, want multiple groups", compacted)
	}
	if len(repository.manifest.Batches) != 2 {
		t.Fatalf("manifest batches = %d, want 2 bounded level-1 outputs", len(repository.manifest.Batches))
	}
	for _, batch := range repository.manifest.Batches {
		if batch.Generation != 1 {
			t.Fatalf("batch %s generation = %d, want 1", batch.ID, batch.Generation)
		}
	}
	again, err := repository.CompactParquet(context.Background(), db, 64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("second compaction rewrote %d already-compacted inputs, want 0", again)
	}
}

func TestRepositoryCompactionPreservesRetentionPartitions(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	oldTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	newTime := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC).UnixNano()
	for day, timestamp := range []int64{oldTime, newTime} {
		for i := range 8 {
			batch := testBatchAt(timestamp)
			batch.ID = fmt.Sprintf("day-%d-batch-%d", day, i)
			if err := repository.Commit(batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := repository.CompactParquetBacklog(context.Background(), db, 64, nil); err != nil {
		t.Fatal(err)
	}
	if len(repository.manifest.Batches) != 2 {
		t.Fatalf("manifest batches = %d, want one output per day", len(repository.manifest.Batches))
	}
	var oldID, newID string
	for _, batch := range repository.manifest.Batches {
		switch batch.MaxNanos {
		case oldTime:
			oldID = batch.ID
		case newTime:
			newID = batch.ID
		}
	}
	removed, err := repository.PruneParquet(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || oldID == "" || newID == "" {
		t.Fatalf("removed=%d oldID=%q newID=%q, want exactly the old partition", removed, oldID, newID)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if _, err := os.Stat(filepath.Join(dir, "parquet", signal, oldID+".parquet")); !os.IsNotExist(err) {
			t.Fatalf("expired compacted %s file remains: %v", signal, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "parquet", signal, newID+".parquet")); err != nil {
			t.Fatalf("current compacted %s file missing: %v", signal, err)
		}
	}
}

func testBatchAt(timestamp int64) Batch {
	batch := testBatch()
	batch.Spans[0].StartUnixNanos = timestamp
	batch.Spans[0].EndUnixNanos = timestamp + 1
	batch.Spans[0].IngestedAt = timestamp
	batch.Logs[0].TimeUnixNanos = timestamp
	batch.Logs[0].EventUnixNanos = timestamp
	batch.Logs[0].IngestedAt = timestamp
	batch.Metrics[0].TimeUnixNanos = timestamp
	batch.Metrics[0].EventUnixNanos = timestamp
	batch.Metrics[0].IngestedAt = timestamp
	return batch
}
