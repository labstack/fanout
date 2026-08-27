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

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/telemetry"
)

type testParquetCompactor struct {
	db         *sql.DB
	publishErr error
}

func (c *testParquetCompactor) MergeParquet(ctx context.Context, signal string, inputs []string, output string) error {
	quoted := make([]string, len(inputs))
	for i, input := range inputs {
		quoted[i] = sqlQuote(input)
	}
	query := fmt.Sprintf("SELECT * FROM read_parquet([%s], union_by_name=true)", strings.Join(quoted, ","))
	if signal == "spans" {
		query += " ORDER BY _trace_hash, start_unix_nano, span_id"
	}
	_, err := c.db.ExecContext(ctx, fmt.Sprintf("COPY (%s) TO %s (FORMAT PARQUET, COMPRESSION ZSTD)", query, sqlQuote(output)))
	return err
}

func (c *testParquetCompactor) PublishParquet(publish func() error) error {
	if c.publishErr != nil {
		return c.publishErr
	}
	return publish()
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
	if err := repository.Commit(batch); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(batch); err != nil {
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
	if err := repository.Commit(Batch{ID: "oversized", Spans: spans}); err != nil {
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
	if err := repository.Commit(testBatch()); err != nil {
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
	if err := repository.Commit(old); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(newer); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.PruneParquet(500)
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
	if err := repository.Commit(batch); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.PruneParquet(500)
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
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	db := openTestDuckDB(t)
	defer db.Close()
	compacted, err := repository.CompactParquet(context.Background(), &testParquetCompactor{db: db}, 64)
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
		if err := repository.Commit(batch); err != nil {
			t.Fatal(err)
		}
	}
	db := openTestDuckDB(t)
	defer db.Close()
	compactor := &testParquetCompactor{db: db, publishErr: errors.New("publication unavailable")}
	if _, err := repository.CompactParquet(context.Background(), compactor, 64); err == nil {
		t.Fatal("compaction succeeded despite publication failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPACTION.json")); err != nil {
		t.Fatalf("pending marker: %v", err)
	}
	compactor.publishErr = nil
	if compacted, err := repository.CompactParquet(context.Background(), compactor, 64); err != nil || compacted != 0 {
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
		if err := repository.Commit(batch); err != nil {
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
	return repository.Trace(context.Background(), telemetry.TraceQuery{
		TraceID: traceID, StartNanos: -1 << 63, EndNanos: 1<<63 - 1, Limit: 500,
	})
}
