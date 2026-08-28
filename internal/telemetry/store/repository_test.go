package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/telemetry"
)

type testParquetCompactor struct {
	publishErr error
	afterSwap  func() error
}

type testParquetPublisherFunc func(context.Context, func(context.Context) error) error

func (f testParquetPublisherFunc) PublishParquet(ctx context.Context, publish func(context.Context) error) error {
	return f(ctx, publish)
}

func (c *testParquetCompactor) PublishParquet(ctx context.Context, publish func(context.Context) error) error {
	if c.publishErr != nil {
		return c.publishErr
	}
	if err := publish(ctx); err != nil {
		return err
	}
	if c.afterSwap != nil {
		return c.afterSwap()
	}
	return nil
}

func sqlQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func testBatch() Batch {
	return Batch{
		ID: "batch-test",
		Spans: []telemetry.Span{{
			TraceID: "trace-1", SpanID: "span-1", ServiceName: "api", Name: "GET /",
			StartUnixNanos: 100, EndUnixNanos: 200, DurationMS: 0.0001, StatusCode: "OK", IngestedAt: 300,
		}},
		Logs:    []telemetry.Log{{TimeUnixNanos: 110, Severity: "INFO", Body: "ready", ServiceName: "api", TraceID: "trace-1", IngestedAt: 300}},
		Metrics: []telemetry.Metric{{TimeUnixNanos: 120, Name: "requests", Type: "sum", ServiceName: "api", Value: 1, IngestedAt: 300}},
	}
}

func TestRepositoryCommitIsIdempotentDurableAndQueryable(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	if err := repository.Commit(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), batch); err != nil {
		t.Fatalf("idempotent commit: %v", err)
	}
	if got := repository.RowCount(); got != 3 {
		t.Fatalf("rows = %d, want 3", got)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.RowCount(); got != 3 {
		t.Fatalf("reopened rows = %d, want 3", got)
	}
	spans, err := traceAll(reopened, "trace-1")
	if err != nil || len(spans) != 1 || spans[0].Namespace != "default" {
		t.Fatalf("reopened trace = %#v, %v", spans, err)
	}

	db := openTestDuckDB(t)
	defer db.Close()
	for _, signal := range []string{"spans", "logs", "metrics"} {
		var count int
		if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM read_parquet(?)", reopened.Parquet.Pattern(signal)).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", signal, err)
		}
		if count != 1 {
			t.Fatalf("%s rows = %d, want 1", signal, count)
		}
	}
}

func TestRepositoryCommitsOversizedRequestAsOneBatch(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	spans := make([]telemetry.Span, maxGroupBatchRows+1)
	for i := range spans {
		spans[i] = telemetry.Span{
			TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i),
			StartUnixNanos: int64(i + 1), EndUnixNanos: int64(i + 2), IngestedAt: 1,
		}
	}
	if err := repository.Commit(context.Background(), Batch{ID: "oversized", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	metadata := repository.Parquet.BatchMetadata()
	if len(metadata) != 1 || metadata[0].Spans != len(spans) {
		t.Fatalf("oversized batch metadata = %#v", metadata)
	}
}

func TestRepositoryHasOneAuthoritativeStorageLayout(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.Commit(context.Background(), testBatch()); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"wal", "hot", "MANIFEST.json", "MANIFEST.log", "ducklake.sqlite"} {
		if _, err := os.Stat(filepath.Join(dir, removed)); !os.IsNotExist(err) {
			t.Fatalf("removed storage artifact %s exists: %v", removed, err)
		}
	}
	entries, err := os.ReadDir(repository.Parquet.BatchPath("batch-test"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("batch contains %d files, want Parquet signals, trace index, and metadata", len(entries))
	}
}

func TestRepositoryCleansUnpublishedCompactionArtifacts(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "compaction", "orphan")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	temporaryMarker := filepath.Join(dir, "COMPACTION.json.tmp")
	if err := os.WriteFile(temporaryMarker, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for _, path := range []string{filepath.Join(dir, "compaction"), temporaryMarker} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unpublished compaction artifact remains at %s: %v", path, err)
		}
	}
}

func TestRepositoryPrunesOnlyExpiredBatches(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	old := testBatch()
	old.ID = "old"
	old.Spans[0].StartUnixNanos, old.Spans[0].IngestedAt = 100, 100
	old.Logs[0].TimeUnixNanos, old.Logs[0].IngestedAt = 100, 100
	old.Metrics[0].TimeUnixNanos, old.Metrics[0].IngestedAt = 100, 100
	newer := testBatch()
	newer.ID = "new"
	newer.Spans[0].StartUnixNanos, newer.Spans[0].IngestedAt = 1_000, 1_000
	newer.Logs[0].TimeUnixNanos, newer.Logs[0].IngestedAt = 1_000, 1_000
	newer.Metrics[0].TimeUnixNanos, newer.Metrics[0].IngestedAt = 1_000, 1_000
	if err := repository.Commit(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	publisher := &testParquetCompactor{afterSwap: func() error {
		if repository.compactionMu.TryLock() {
			repository.compactionMu.Unlock()
			return errors.New("retention published without holding compaction lock")
		}
		retired, err := filepath.Glob(filepath.Join(repository.Parquet.BatchesDir(), "*.retired"))
		if err != nil {
			return err
		}
		if len(retired) != 1 {
			return fmt.Errorf("retired directories during publication = %d, want 1", len(retired))
		}
		return nil
	}}
	removed, err := repository.PruneParquet(context.Background(), publisher, 500, 64)
	if err != nil || removed != 1 {
		t.Fatalf("prune = %d, %v", removed, err)
	}
	if _, err := os.Stat(repository.Parquet.BatchPath("old")); !os.IsNotExist(err) {
		t.Fatalf("expired batch remains: %v", err)
	}
	if _, err := os.Stat(repository.Parquet.BatchPath("new")); err != nil {
		t.Fatalf("current batch missing: %v", err)
	}
	if got := repository.RowCount(); got != 3 {
		t.Fatalf("retained rows = %d, want 3", got)
	}
}

func TestRepositoryDiscardsRecoverableMarkerWithoutOutput(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	batch.ID = "intact-input"
	if err := repository.Commit(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	marker := compactionMarker{
		Output: telemetry.BatchMetadata{ID: "missing-output", Generation: 1},
		Inputs: []string{batch.ID},
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDurableFile(filepath.Join(dir, "COMPACTION.json"), data); err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(repository.Parquet.BatchesDir(), batch.ID+".retired-"+marker.Output.ID)
	if err := os.Rename(repository.Parquet.BatchPath(batch.ID), retired); err != nil {
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
	if got := recovered.RowCount(); got != 3 {
		t.Fatalf("rows after marker recovery = %d, want 3", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); !os.IsNotExist(err) {
		t.Fatalf("recoverable marker remains: %v", err)
	}
}

func TestRepositoryRestoresInputsThroughPublicationGate(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	batch := testBatch()
	batch.ID = "retired-input"
	if err := repository.Commit(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	marker := compactionMarker{
		Output: telemetry.BatchMetadata{ID: "missing-output", Generation: 1},
		Inputs: []string{batch.ID},
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDurableFile(filepath.Join(dir, "COMPACTION.json"), data); err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(repository.Parquet.BatchesDir(), batch.ID+".retired-"+marker.Output.ID)
	if err := os.Rename(repository.Parquet.BatchPath(batch.ID), retired); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	publisher := testParquetPublisherFunc(func(ctx context.Context, publish func(context.Context) error) error {
		close(entered)
		<-release
		return publish(ctx)
	})
	done := make(chan error, 1)
	go func() { done <- repository.RecoverParquet(context.Background(), publisher) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not enter the publication gate")
	}
	if _, err := os.Stat(repository.Parquet.BatchPath(batch.ID)); !os.IsNotExist(err) {
		t.Fatalf("input became visible before publication: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repository.Parquet.BatchPath(batch.ID)); err != nil {
		t.Fatalf("input was not restored after publication: %v", err)
	}
}

func TestRepositoryPrunePassDrainsWithinBudgetAndOldestFirst(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for i := range 5 {
		batch := testBatch()
		batch.ID = fmt.Sprintf("expired-%d", i)
		batch.Spans[0].IngestedAt = int64(i + 1)
		batch.Logs[0].IngestedAt = int64(i + 1)
		batch.Metrics[0].IngestedAt = int64(i + 1)
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	publications := 0
	publisher := &testParquetCompactor{afterSwap: func() error {
		publications++
		return nil
	}}
	removed, err := repository.PruneParquetPass(context.Background(), publisher, 10, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 5 || publications != 3 {
		t.Fatalf("removed=%d publications=%d, want 5 across 3 bounded swaps", removed, publications)
	}
	metadata := repository.Parquet.BatchMetadata()
	if len(metadata) != 0 {
		t.Fatalf("remaining batches = %#v, want the backlog drained", metadata)
	}
}

func TestRepositoryPrunePassFinishesPublicationAfterBudget(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for i := range 4 {
		batch := testBatch()
		batch.ID = fmt.Sprintf("expired-%d", i)
		batch.Spans[0].IngestedAt = int64(i + 1)
		batch.Logs[0].IngestedAt = int64(i + 1)
		batch.Metrics[0].IngestedAt = int64(i + 1)
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	publications := 0
	publisher := &testParquetCompactor{afterSwap: func() error {
		publications++
		time.Sleep(10 * time.Millisecond)
		return nil
	}}
	removed, err := repository.PruneParquetPass(context.Background(), publisher, 10, 2, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 || publications != 1 {
		t.Fatalf("removed=%d publications=%d, want the in-flight 2-batch publication completed once", removed, publications)
	}
}

func TestRepositoryCleanupOwnsRetiredDirectories(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	retired := filepath.Join(repository.Parquet.BatchesDir(), "old.retired-compacted")
	if err := os.Mkdir(retired, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), Batch{ID: "contains.retired", Spans: []telemetry.Span{{TraceID: "trace"}}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CleanupParquet(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Fatalf("retired directory remains: %v", err)
	}
	if _, err := os.Stat(repository.Parquet.BatchPath("contains.retired")); err != nil {
		t.Fatalf("published batch with retired in its ID was removed: %v", err)
	}
}

func TestRepositoryRetentionUsesIngestTimeNotEventTime(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	batch := testBatch()
	batch.ID = "future-events"
	batch.Spans[0].StartUnixNanos = 1 << 62
	batch.Logs[0].TimeUnixNanos = 1 << 62
	batch.Metrics[0].TimeUnixNanos = 1 << 62
	batch.Spans[0].IngestedAt = 100
	batch.Logs[0].IngestedAt = 100
	batch.Metrics[0].IngestedAt = 100
	if err := repository.Commit(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.PruneParquet(context.Background(), &testParquetCompactor{}, 500, 64)
	if err != nil || removed != 1 {
		t.Fatalf("prune future-dated events = %d, %v; want one batch expired by ingest time", removed, err)
	}
}

func TestRepositoryCompactsParquetWithoutChangingRows(t *testing.T) {
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
	db := openTestDuckDB(t)
	defer db.Close()
	compactor := &testParquetCompactor{afterSwap: func() error {
		retired, err := filepath.Glob(filepath.Join(repository.Parquet.BatchesDir(), "*.retired-*"))
		if err != nil {
			return err
		}
		if len(retired) != minCompactionInputs {
			return fmt.Errorf("retired compaction inputs during publication = %d, want %d", len(retired), minCompactionInputs)
		}
		return nil
	}}
	compactCtx, cancelCompact := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCompact()
	compacted, err := repository.CompactParquet(compactCtx, compactor, minCompactionInputs)
	if err != nil {
		t.Fatal(err)
	}
	if compacted != minCompactionInputs {
		t.Fatalf("compacted inputs = %d", compacted)
	}
	metadata := repository.Parquet.BatchMetadata()
	if len(metadata) != 1 || metadata[0].Generation != 1 || metadata[0].Spans != minCompactionInputs || metadata[0].Logs != minCompactionInputs || metadata[0].Metrics != minCompactionInputs {
		t.Fatalf("compacted metadata = %#v", metadata)
	}
	if got := repository.RowCount(); got != 3*minCompactionInputs {
		t.Fatalf("compacted rows = %d", got)
	}
	spans, err := traceAll(repository, "trace-1")
	if err != nil || len(spans) != minCompactionInputs {
		t.Fatalf("compacted trace spans = %d, %v", len(spans), err)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		var count int
		if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM read_parquet(?)", repository.Parquet.Pattern(signal)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != minCompactionInputs {
			t.Fatalf("compacted %s rows = %d", signal, count)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); !os.IsNotExist(err) {
		t.Fatalf("completed compaction marker remains: %v", err)
	}
}

func TestSelectCompactionBatchesRequiresFullSmallFileGroup(t *testing.T) {
	const groupSize = 16
	batches := make([]telemetry.BatchMetadata, groupSize)
	for i := range batches {
		batches[i] = telemetry.BatchMetadata{ID: fmt.Sprintf("batch-%d", i), MaxIngestedNanos: 1, Spans: 1}
	}
	if selected := selectCompactionBatches(batches[:groupSize-1], groupSize); len(selected) != 0 {
		t.Fatalf("selected partial group of %d batches", len(selected))
	}
	if selected := selectCompactionBatches(batches, groupSize); len(selected) != groupSize {
		t.Fatalf("selected full group = %d, want %d", len(selected), groupSize)
	}
}

func TestSelectCompactionBatchesBuildsRowBoundedGroup(t *testing.T) {
	const maxBatches = 16
	batches := make([]telemetry.BatchMetadata, maxBatches)
	for i := range batches {
		batches[i] = telemetry.BatchMetadata{
			ID: fmt.Sprintf("batch-%d", i), MaxIngestedNanos: 1, Generation: 2,
			Spans: 2_000_000,
		}
	}
	selected := selectCompactionBatches(batches, maxBatches)
	if len(selected) != 12 {
		t.Fatalf("selected %d batches, want 12 below row limit", len(selected))
	}
	var rows int
	for _, batch := range selected {
		rows += batch.Spans + batch.Logs + batch.Metrics
	}
	if rows > maxCompactionRows {
		t.Fatalf("selected %d rows above limit %d", rows, maxCompactionRows)
	}
}

func TestSelectCompactionBatchesCombinesSaturatedLargeFiles(t *testing.T) {
	const maxBatches = 16
	batches := make([]telemetry.BatchMetadata, 4)
	for i := range batches {
		batches[i] = telemetry.BatchMetadata{
			ID: fmt.Sprintf("large-%d", i), MaxIngestedNanos: 1, Generation: 3,
			Spans: 6_000_000,
		}
	}
	selected := selectCompactionBatches(batches, maxBatches)
	if len(selected) != len(batches) {
		t.Fatalf("selected %d saturated large batches, want %d", len(selected), len(batches))
	}
}

func TestSelectCompactionBatchesSkipsUncompactableOlderGroup(t *testing.T) {
	batches := make([]telemetry.BatchMetadata, 2*minCompactionInputs)
	for i := range minCompactionInputs {
		batches[i] = telemetry.BatchMetadata{
			ID: fmt.Sprintf("oversized-%d", i), MaxIngestedNanos: 1,
			Spans: maxCompactionRows + 1,
		}
	}
	for i := minCompactionInputs; i < len(batches); i++ {
		batches[i] = telemetry.BatchMetadata{
			ID: fmt.Sprintf("newer-%d", i), MaxIngestedNanos: int64(24*time.Hour) + 1,
			Spans: 1,
		}
	}
	selected := selectCompactionBatches(batches, minCompactionInputs)
	if len(selected) != minCompactionInputs || !strings.HasPrefix(selected[0].ID, "newer-") {
		t.Fatalf("selection did not skip uncompactable older group: %#v", selected)
	}
}

func TestRepositoryRecoversPendingCompactionWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for i := range minCompactionInputs {
		batch := testBatch()
		batch.ID = fmt.Sprintf("pending-%d", i)
		batch.Spans[0].SpanID = fmt.Sprintf("span-%d", i)
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	compactor := &testParquetCompactor{publishErr: errors.New("publication unavailable")}
	if _, err := repository.CompactParquet(context.Background(), compactor, minCompactionInputs); err == nil {
		t.Fatal("compaction succeeded despite publication failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); err != nil {
		t.Fatalf("pending marker: %v", err)
	}
	compactor.publishErr = nil
	if compacted, err := repository.CompactParquet(context.Background(), compactor, minCompactionInputs); err != nil || compacted != 0 {
		t.Fatalf("resume compaction = %d, %v", compacted, err)
	}
	if got := repository.Parquet.BatchMetadata(); len(got) != 1 || got[0].Generation != 1 {
		t.Fatalf("recovered metadata = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); !os.IsNotExist(err) {
		t.Fatalf("recovered compaction marker remains: %v", err)
	}
}

func TestRepositoryRecoversInterruptedCompactionSwap(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range minCompactionInputs {
		batch := testBatch()
		batch.ID = fmt.Sprintf("recover-%d", i)
		batch.Spans[0].SpanID = fmt.Sprintf("span-%d", i)
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	db := openTestDuckDB(t)
	defer db.Close()
	output := telemetry.BatchMetadata{
		ID: "compact-recovery", MinIngestedNanos: 100, MaxIngestedNanos: 300, Generation: 1,
		Spans: minCompactionInputs, Logs: minCompactionInputs, Metrics: minCompactionInputs,
	}
	stage := repository.compactionStage(output.ID)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, signal := range parquetSignals {
		query := fmt.Sprintf("SELECT * FROM read_parquet(%s)", sqlQuote(repository.Parquet.Pattern(signal)))
		if signal == "spans" {
			query += " ORDER BY _trace_hash, start_unix_nano, span_id"
		}
		stmt := fmt.Sprintf("COPY (%s) TO %s (FORMAT PARQUET, COMPRESSION ZSTD)", query, sqlQuote(filepath.Join(stage, signal+".parquet")))
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Parquet.PrepareReplacement(stage, output); err != nil {
		t.Fatal(err)
	}
	inputs := make([]string, minCompactionInputs)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("recover-%d", i)
	}
	marker := compactionMarker{Output: output, Inputs: inputs}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDurableFile(filepath.Join(dir, "COMPACTION.json"), data); err != nil {
		t.Fatal(err)
	}
	// Reproduce a crash halfway through retiring inputs. Startup must finish
	// the intended publication, not expose a half-old/half-new file set.
	for _, id := range inputs[:4] {
		if err := os.Rename(repository.Parquet.BatchPath(id), filepath.Join(repository.Parquet.BatchesDir(), id+".retired-"+output.ID)); err != nil {
			t.Fatal(err)
		}
	}

	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	metadata := recovered.Parquet.BatchMetadata()
	if len(metadata) != 1 || metadata[0].ID != output.ID || recovered.RowCount() != 3*minCompactionInputs {
		t.Fatalf("recovered metadata = %#v rows=%d", metadata, recovered.RowCount())
	}
	spans, err := traceAll(recovered, "trace-1")
	if err != nil || len(spans) != minCompactionInputs {
		t.Fatalf("recovered trace spans = %d, %v", len(spans), err)
	}
	if retired, err := filepath.Glob(filepath.Join(recovered.Parquet.BatchesDir(), "*.retired*")); err != nil || len(retired) != 0 {
		t.Fatalf("retired inputs after recovery = %v, %v", retired, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); !os.IsNotExist(err) {
		t.Fatalf("compaction marker remains after recovery: %v", err)
	}
}

func openTestDuckDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func traceAll(repository *Repository, traceID string) ([]telemetry.IndexedSpan, error) {
	return repository.Parquet.Trace(context.Background(), telemetry.TraceQuery{
		TraceID: traceID, StartNanos: -1 << 63, EndNanos: 1<<63 - 1, Limit: 500,
	})
}

// seedFailedCompaction leaves a live COMPACTION.json whose recovery always
// fails, which is the state that used to latch maintenance off permanently.
func seedFailedCompaction(t *testing.T, dir string, repository *Repository) {
	t.Helper()
	for i := range minCompactionInputs {
		batch := testBatch()
		batch.ID = fmt.Sprintf("latched-%d", i)
		batch.Spans[0].SpanID = fmt.Sprintf("span-%d", i)
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	compactor := &testParquetCompactor{publishErr: errors.New("publication unavailable")}
	if _, err := repository.CompactParquet(context.Background(), compactor, minCompactionInputs); err == nil {
		t.Fatal("compaction succeeded despite publication failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); err != nil {
		t.Fatalf("expected a live marker: %v", err)
	}
}

// TestRepositoryRecoveryFailureStaysLive pins fail-closed recovery. The marker
// and rollback set remain authoritative until recovery succeeds; silently
// bypassing them can make old inputs eligible for deletion.
func TestRepositoryRecoveryFailureStaysLive(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	seedFailedCompaction(t, dir, repository)

	failing := testParquetPublisherFunc(func(context.Context, func(context.Context) error) error {
		return errors.New("publication unavailable")
	})
	for attempt := 1; attempt <= 5; attempt++ {
		if err := repository.RecoverParquet(context.Background(), failing); err == nil {
			t.Fatalf("attempt %d: recovery reported success", attempt)
		}
		if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); err != nil {
			t.Fatalf("attempt %d: live marker was removed: %v", attempt, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json.failed")); !os.IsNotExist(err) {
		t.Fatalf("recovery created a fallback marker: %v", err)
	}
}

// TestRepositoryCleanupPreservesMarkerRollbackSet pins that cleanup never
// deletes the retired inputs a pending marker still needs. Deleting one is
// unrecoverable: the input is gone and its rows were never published under the
// replacement, so the rows exist in no queryable batch at all.
func TestRepositoryCleanupPreservesMarkerRollbackSet(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	marker := compactionMarker{
		Output: telemetry.BatchMetadata{ID: "compact-live", Generation: 1},
		Inputs: []string{"input-a"},
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDurableFile(filepath.Join(dir, "COMPACTION.json"), data); err != nil {
		t.Fatal(err)
	}
	batches := repository.Parquet.BatchesDir()
	needed := filepath.Join(batches, "input-a.retired-compact-live")
	stale := filepath.Join(batches, "input-b.retired")
	for _, path := range []string{needed, stale} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := repository.CleanupParquet(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(needed); err != nil {
		t.Fatalf("cleanup deleted the rollback set of a live marker: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("cleanup left an unreferenced retired directory: %v", err)
	}
}

func TestRepositoryOpenFailsClosedOnUnrecoverableMarker(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedFailedCompaction(t, dir, repository)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	// Corrupt the staged output so recovery cannot complete or roll back.
	stage := filepath.Join(dir, "compaction")
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.WriteFile(filepath.Join(stage, entry.Name(), "metadata.json"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if reopened, err := Open(dir); err == nil {
		_ = reopened.Close()
		t.Fatal("Open succeeded with an unrecoverable marker")
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); err != nil {
		t.Fatalf("Open removed the live marker: %v", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("Open destroyed the staged output: %v", err)
	}
}

func TestRepositoryOpenFailsOnUnreadableMarker(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "COMPACTION.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	for boot := 1; boot <= 3; boot++ {
		if reopened, err := Open(dir); err == nil {
			_ = reopened.Close()
			t.Fatalf("boot %d accepted an unreadable marker", boot)
		}
		if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); err != nil {
			t.Fatalf("boot %d removed the unreadable marker: %v", boot, err)
		}
	}
}

func TestRepositoryOpenRejectsUnsafeCompactionMarker(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	marker := compactionMarker{
		Output: telemetry.BatchMetadata{ID: "../outside"},
		Inputs: []string{"input"},
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dir, "COMPACTION.json")
	if err := os.WriteFile(markerPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(dir); err == nil {
		_ = reopened.Close()
		t.Fatal("Open accepted a marker-controlled path outside storage")
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("Open removed the invalid live marker: %v", err)
	}
}

// TestUnresolvedCompactionRunbookExists keeps the startup log's runbook link
// pointing at a section that exists. The log stopped restating the rollback
// procedure because maintaining it in two places produced a string of
// revisions that corrected one copy and left the other unsafe; a link only
// removes that risk while it resolves, so the anchor is checked here rather
// than trusted.
func TestUnresolvedCompactionRunbookExists(t *testing.T) {
	const guide = "../../../site/src/content/docs/guides/troubleshoot.mdx"
	data, err := os.ReadFile(guide)
	if err != nil {
		t.Fatalf("read the runbook the startup log links to: %v", err)
	}
	_, anchor, found := strings.Cut(compactionRunbookURL, "#")
	if !found {
		t.Fatalf("runbook URL %q has no anchor", compactionRunbookURL)
	}
	var headings []string
	for _, line := range strings.Split(string(data), "\n") {
		title, isHeading := strings.CutPrefix(line, "## ")
		if !isHeading {
			continue
		}
		slug := strings.ToLower(strings.TrimSpace(title))
		slug = strings.Join(strings.Fields(slug), "-")
		headings = append(headings, slug)
		if slug == anchor {
			return
		}
	}
	t.Fatalf("runbook anchor %q is not a section in %s; sections are %v", anchor, guide, headings)
}
