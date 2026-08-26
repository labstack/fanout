package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
	compacted, err := repository.CompactParquet(context.Background(), db, 64)
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
