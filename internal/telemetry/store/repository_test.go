package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/klauspost/compress/zstd"
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
	for _, signal := range []string{"logs", "metrics"} {
		if _, err := os.Stat(filepath.Join(dir, "hot", signal)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unused hot %s copy exists: %v", signal, err)
		}
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

func TestRepositoryStageDoesNotWaitForProjectionCommitLock(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	repository.commitMu.Lock()
	staged := make(chan error, 1)
	go func() {
		staged <- repository.Stage(Batch{ID: "independent-stage", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}})
	}()
	select {
	case err := <-staged:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		repository.commitMu.Unlock()
		t.Fatal("WAL staging waited for projection commit I/O")
	}
	repository.commitMu.Unlock()
}

func TestCompactHotDoesNotTakeRepositoryIngestOrReadLocks(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	repository.hotMu.Lock()
	repository.commitMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := repository.CompactHot(2)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		repository.commitMu.Unlock()
		repository.hotMu.Unlock()
		t.Fatal("hot compaction acquired a repository-wide ingest or read lock")
	}
	repository.commitMu.Unlock()
	repository.hotMu.Unlock()
}

func TestRepositoryManifestJournalReplaysAndRepairsPartialTail(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		batch := testBatch()
		batch.ID = fmt.Sprintf("journal-%d", i)
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	var snapshot repositoryManifest
	data, err := os.ReadFile(filepath.Join(dir, "MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Batches) != 0 {
		t.Fatalf("per-commit path rewrote manifest snapshot with %d batches", len(snapshot.Batches))
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "MANIFEST.log")
	journal, err := os.OpenFile(journalPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.WriteString(`{"epoch":1,"batch":`); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.manifest.Batches) != 3 || !reopened.batchConsumed("journal-2") {
		t.Fatalf("journal replay batches = %#v", reopened.manifest.Batches)
	}
	repaired, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(repaired), `"batch":`) && !strings.HasSuffix(string(repaired), "}\n") {
		t.Fatalf("partial journal tail was not truncated: %q", repaired)
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
	if recovered.Spans.RowCount() != 1 {
		t.Fatal("WAL recovery did not restore the span index")
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if _, err := os.Stat(filepath.Join(dir, "parquet", signal, batch.ID+".parquet")); err != nil {
			t.Fatalf("WAL recovery did not restore %s parquet: %v", signal, err)
		}
	}
	entries, err := filepath.Glob(filepath.Join(dir, "wal", "*.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("committed WAL files remain: %v", entries)
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

func TestRepositoryPersistsHotPruneBoundary(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	batch.ID = "hot-boundary"
	batch.Spans = []telemetry.Span{{TraceID: "trace-boundary", SpanID: "span", StartUnixNanos: 300}}
	if err := repository.Commit(batch); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PruneHot(250); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	spans, cutoff, err := reopened.HotTrace("trace-boundary")
	if err != nil {
		t.Fatal(err)
	}
	if cutoff != 250 || len(spans) != 1 {
		t.Fatalf("cutoff=%d spans=%d, want cutoff 250 and one retained boundary span", cutoff, len(spans))
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
	if _, err := repository.PruneHot(50); err != nil {
		t.Fatal(err)
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
	if repository.manifest.HotCutoffNanos != 50 {
		t.Fatalf("hot cutoff after compaction = %d, want 50", repository.manifest.HotCutoffNanos)
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

func TestRepositorySkipsStaleWALForCompactedBatch(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var stale Batch
	for i := range 8 {
		batch := testBatch()
		batch.ID = fmt.Sprintf("replay-source-%d", i)
		batch.Spans[0].SpanID = fmt.Sprintf("span-%d", i)
		if i == 0 {
			stale = batch
		}
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CompactParquet(context.Background(), db, 64, nil); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repository.writeWAL(stale); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	recovered, err := Open(dir)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	defer recovered.Close()
	defer db.Close()
	if _, err := os.Stat(filepath.Join(dir, "wal", stale.ID+".wal")); !os.IsNotExist(err) {
		t.Fatalf("consumed WAL remains after recovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "parquet", "spans", stale.ID+".parquet")); !os.IsNotExist(err) {
		t.Fatalf("consumed source parquet was resurrected: %v", err)
	}
	pattern := filepath.ToSlash(filepath.Join(dir, "parquet", "spans", "*.parquet"))
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM read_parquet(?)", pattern).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 8 {
		t.Fatalf("rows after stale WAL recovery = %d, want 8", rows)
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

func TestRepositoryCompactionRecoveryRestoresRetiredInputsWhenStageMissing(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	marker := compactionMarker{
		ID:         "compact-recovery",
		Inputs:     []string{"recovery-a", "recovery-b"},
		Signals:    parquetSignals[:],
		MinNanos:   100,
		MaxNanos:   120,
		Generation: 1,
	}
	for _, id := range marker.Inputs {
		batch := testBatch()
		batch.ID = id
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	stageDir := filepath.Join(dir, marker.ID)
	if err := os.Mkdir(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, signal := range marker.Signals {
		if signal != "spans" {
			data, err := os.ReadFile(filepath.Join(dir, "parquet", signal, marker.Inputs[0]+".parquet"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stageDir, signal+".parquet"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range marker.Inputs {
			input := filepath.Join(dir, "parquet", signal, id+".parquet")
			if err := os.Rename(input, input+".retired-"+marker.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDurableFile(filepath.Join(dir, "COMPACTION.json"), data); err != nil {
		t.Fatal(err)
	}
	if err := repository.recoverCompaction(); err == nil || !strings.Contains(err.Error(), "missing required spans output") {
		t.Fatalf("recover compaction error = %v, want missing spans output", err)
	}
	for _, signal := range marker.Signals {
		for _, id := range marker.Inputs {
			input := filepath.Join(dir, "parquet", signal, id+".parquet")
			if _, err := os.Stat(input); err != nil {
				t.Fatalf("restored %s input %s: %v", signal, id, err)
			}
			if _, err := os.Stat(input + ".retired-" + marker.ID); !os.IsNotExist(err) {
				t.Fatalf("retired %s input %s remains: %v", signal, id, err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "parquet", signal, marker.ID+".parquet")); !os.IsNotExist(err) {
			t.Fatalf("unexpected compacted %s output: %v", signal, err)
		}
	}
	if len(repository.manifest.Batches) != len(marker.Inputs) {
		t.Fatalf("manifest batches = %d, want original %d", len(repository.manifest.Batches), len(marker.Inputs))
	}
}

func TestRepositoryCompactionRollsBackMidSwapFailure(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	marker := compactionMarker{ID: "compact-rollback", Inputs: []string{"rollback-a", "rollback-b"}, Signals: []string{"spans", "logs"}, MinNanos: 100, MaxNanos: 120, Generation: 1}
	for _, id := range marker.Inputs {
		batch := testBatch()
		batch.ID = id
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	stageDir := filepath.Join(dir, marker.ID)
	if err := os.Mkdir(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, signal := range marker.Signals {
		data, err := os.ReadFile(filepath.Join(repository.Parquet.Dir(), signal, marker.Inputs[0]+".parquet"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stageDir, signal+".parquet"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	originalRename := renameCompactionFile
	renameCompactionFile = func(oldPath, newPath string) error {
		if oldPath == filepath.Join(stageDir, "logs.parquet") {
			return errors.New("injected log publish failure")
		}
		return os.Rename(oldPath, newPath)
	}
	defer func() { renameCompactionFile = originalRename }()
	if err := repository.completeCompaction(marker); err == nil || !strings.Contains(err.Error(), "injected log publish failure") {
		t.Fatalf("complete compaction error = %v", err)
	}
	for _, signal := range marker.Signals {
		for _, id := range marker.Inputs {
			input := filepath.Join(repository.Parquet.Dir(), signal, id+".parquet")
			if _, err := os.Stat(input); err != nil {
				t.Fatalf("restored %s input %s: %v", signal, id, err)
			}
			if _, err := os.Stat(input + ".retired-" + marker.ID); !os.IsNotExist(err) {
				t.Fatalf("retired %s input %s remains: %v", signal, id, err)
			}
		}
		if _, err := os.Stat(filepath.Join(repository.Parquet.Dir(), signal, marker.ID+".parquet")); !os.IsNotExist(err) {
			t.Fatalf("partial compacted %s output remains: %v", signal, err)
		}
		if _, err := os.Stat(filepath.Join(stageDir, signal+".parquet")); err != nil {
			t.Fatalf("restaged %s output: %v", signal, err)
		}
	}
}

func TestRepositoryRefusesToOverwritePendingCompactionMarker(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	markerPath := filepath.Join(dir, "COMPACTION.json")
	original := []byte(`{"id":"compact-pending"}`)
	if err := writeDurableFile(markerPath, original); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := repository.CompactParquet(context.Background(), db, 64, nil); err == nil || !strings.Contains(err.Error(), "must recover") {
		t.Fatalf("CompactParquet error = %v, want pending-marker refusal", err)
	}
	got, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("pending marker was overwritten: %q", got)
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

func TestNormalizeBatchBackfillsSpanStartFromIngestedAt(t *testing.T) {
	batch := Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span", IngestedAt: 12345}}}
	normalizeBatch(&batch)
	if got := batch.Spans[0].StartUnixNanos; got != 12345 {
		t.Fatalf("StartUnixNanos = %d, want ingested-at fallback 12345", got)
	}
}

func TestCompactionSourcesDropInheritedLedger(t *testing.T) {
	selected := []batchMetadata{
		{ID: "raw-1"},
		{ID: "out-1", Sources: []string{"old-a", "old-b"}},
		{ID: "raw-2"},
	}
	got := compactionSources(selected)
	want := []string{"raw-1", "raw-2"}
	if len(got) != len(want) {
		t.Fatalf("compactionSources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("compactionSources = %v, want %v", got, want)
		}
	}
}

func TestRecoverQuarantinesBatchThatCanNeverApply(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A batch ID the segment stores can never accept: it decodes cleanly, so
	// only an apply attempt can reject it, and it will do so on every boot.
	poison := testBatch()
	poison.ID = "poison batch"
	if err := repository.writeWAL(poison); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open error = %v, want the unappliable batch quarantined instead of a boot loop", err)
	}
	defer reopened.Close()
	entries, err := os.ReadDir(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatal(err)
	}
	corrupt, live := 0, 0
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), ".corrupt"):
			corrupt++
		case strings.HasSuffix(entry.Name(), ".wal"):
			live++
		}
	}
	if corrupt != 1 || live != 0 {
		t.Fatalf("quarantined = %d, live = %d, want the poison WAL renamed aside", corrupt, live)
	}
}

func TestRecoverRetainsWALWhenApplyFailsFromEnvironment(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the directory permissions this test relies on")
	}
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	if err := repository.Stage(batch); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	// Hot segments still accept the batch, so replay publishes a projection
	// prefix and then fails on Parquet: an environmental failure that a later
	// healthy boot must be able to finish.
	parquetSpans := filepath.Join(dir, "parquet", "spans")
	if err := os.Chmod(parquetSpans, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parquetSpans, 0o755) })
	if _, err := Open(dir); err == nil {
		t.Fatal("Open succeeded although replay could not publish the batch")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".corrupt") {
			t.Fatalf("environmental failure quarantined %s; a healthy restart can no longer finish the batch", entry.Name())
		}
	}
	if err := os.Chmod(parquetSpans, 0o755); err != nil {
		t.Fatal(err)
	}
	healthy, err := Open(dir)
	if err != nil {
		t.Fatalf("Open error = %v, want the retained WAL to replay once the environment recovered", err)
	}
	defer healthy.Close()
	if got := healthy.Spans.RowCount(); got != uint64(len(batch.Spans)) {
		t.Fatalf("replayed spans = %d, want %d", got, len(batch.Spans))
	}
}

func TestStageRejectsBatchTheSegmentStoresCannotAccept(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	batch := testBatch()
	batch.ID = "poison batch"
	if err := repository.Stage(batch); err == nil {
		t.Fatal("Stage accepted a batch ID no projection can ever publish")
	}
}

func TestWALDecoderRejectsOversizedFrame(t *testing.T) {
	const testLimit = 64 << 10
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	frame := encoder.EncodeAll(make([]byte, testLimit+(1<<10)), nil)
	decoder, err := newWALDecoderWithLimit(testLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	if _, err := decoder.DecodeAll(frame, nil); err == nil {
		t.Fatal("WAL decoder accepted a frame declaring more memory than any batch needs")
	}
}

func TestWALWriterRejectsBatchLargerThanDecoderBudget(t *testing.T) {
	batch := Batch{ID: "large", Logs: []telemetry.Log{{Body: strings.Repeat("x", 2048)}}}
	if _, err := encodeWALBatch(batch, 1024); err == nil {
		t.Fatal("WAL writer produced a frame its paired decoder budget could not reopen")
	}
}

func TestCompactHotCompactsSpanIndexAndPreservesRows(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for i := range 6 {
		now := int64(100 + i)
		batch := Batch{
			ID:      fmt.Sprintf("batch-%d", i),
			Spans:   []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i), StartUnixNanos: now, IngestedAt: now}},
			Logs:    []telemetry.Log{{Body: fmt.Sprintf("log-%d", i), EventUnixNanos: now, IngestedAt: now}},
			Metrics: []telemetry.Metric{{Name: "requests", EventUnixNanos: now, IngestedAt: now}},
		}
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.CompactHot(3); err != nil {
		t.Fatal(err)
	}
	if got := repository.Spans.SegmentCount(); got != 2 {
		t.Fatalf("span segments = %d, want 2 compacted files", got)
	}
	if repository.Spans.RowCount() != 6 {
		t.Fatal("hot span compaction changed row counts")
	}
}

func TestOpenQuarantinesCorruptDisposableHotTier(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := Batch{ID: "committed", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span", StartUnixNanos: 100, IngestedAt: 100}}}
	if err := repository.Commit(batch); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hot", "spans", "committed.fseg"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open failed on disposable hot corruption: %v", err)
	}
	defer reopened.Close()
	if got := reopened.Spans.RowCount(); got != 0 {
		t.Fatalf("rebuilt hot rows = %d, want empty acceleration tier", got)
	}
	if reopened.manifest.HotCutoffNanos == 0 {
		t.Fatal("rebuilt hot tier did not move the authoritative boundary to Parquet")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "hot.corrupt-*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("hot quarantine paths = %v, err = %v", matches, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "parquet", "spans", "committed.parquet")); err != nil {
		t.Fatalf("authoritative Parquet was not preserved: %v", err)
	}
}
