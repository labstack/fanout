package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
	"github.com/parquet-go/parquet-go"
)

func TestVerifyBatchesReportsOnlyUnreadableBatches(t *testing.T) {
	root := t.TempDir()
	repository, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"good", "broken"} {
		if err := repository.Commit(context.Background(), Batch{
			ID: id, Spans: []telemetry.Span{{TraceID: id, SpanID: "span"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	corruptBatchFile(t, root, "broken", "spans.parquet")

	issues, err := VerifyBatches(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].ID != "broken" || issues[0].Err == nil {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestQuarantineBatchRefusesValidData(t *testing.T) {
	root := t.TempDir()
	repository, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), Batch{
		ID: "valid", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := QuarantineBatch(root, "valid"); err == nil || !strings.Contains(err.Error(), "valid") {
		t.Fatalf("QuarantineBatch valid error = %v", err)
	}
}

func TestQuarantineBatchSetsAsideUnreadableDataAndRestoresStartup(t *testing.T) {
	for _, file := range []string{"trace.fidx", "metadata.json"} {
		t.Run(file, func(t *testing.T) { testQuarantineRestoresStartup(t, file) })
	}
}

func testQuarantineRestoresStartup(t *testing.T, file string) {
	t.Helper()
	root := t.TempDir()
	repository, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"good", "broken"} {
		if err := repository.Commit(context.Background(), Batch{
			ID: id, Spans: []telemetry.Span{{TraceID: id, SpanID: "span"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	corruptBatchFile(t, root, "broken", file)

	destination, err := QuarantineBatch(root, "broken")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(destination), "broken.quarantined-") {
		t.Fatalf("destination = %q", destination)
	}
	if _, err := os.Stat(filepath.Join(root, "parquet", "batches", "broken.batch")); !os.IsNotExist(err) {
		t.Fatalf("authoritative batch still exists: %v", err)
	}
	if info, err := os.Stat(destination); err != nil || !info.IsDir() {
		t.Fatalf("quarantine destination = %v, %v", info, err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("startup after quarantine: %v", err)
	}
	defer reopened.Close()
	if got := reopened.RowCount(); got != 1 {
		t.Fatalf("row count after quarantine = %d, want 1", got)
	}
}

func TestSchemaMismatchCannotAuthorizeQuarantine(t *testing.T) {
	root := t.TempDir()
	repository, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(t.Context(), Batch{ID: "schema", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}}); err != nil {
		t.Fatal(err)
	}
	repository.Close()
	path := filepath.Join(root, "parquet", "batches", "schema.batch", "spans.parquet")
	if err := parquet.WriteFile(path, []struct {
		TraceID string `parquet:"trace_id"`
	}{{"trace"}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBatches(root); !errors.Is(err, telemetry.ErrUnsupportedBatchFormat) {
		t.Fatalf("verify: %v", err)
	}
	if _, err := QuarantineBatch(root, "schema"); !errors.Is(err, telemetry.ErrUnsupportedBatchFormat) {
		t.Fatalf("quarantine: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("unsupported data changed: %v", err)
	}
}

func TestQuarantineBatchRefusesLiveCompactionMembers(t *testing.T) {
	root := t.TempDir()
	repository, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), Batch{
		ID: "input", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	corruptBatchFile(t, root, "input", "spans.parquet")
	marker := compactionMarker{Output: telemetry.BatchMetadata{ID: "replacement"}, Inputs: []string{"input"}}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "COMPACTION.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := QuarantineBatch(root, "input"); err == nil || !strings.Contains(err.Error(), "live compaction") {
		t.Fatalf("QuarantineBatch live input error = %v", err)
	}
}

func TestQuarantineBatchRejectsUnsafeID(t *testing.T) {
	if _, err := QuarantineBatch(t.TempDir(), "../outside"); err == nil {
		t.Fatal("unsafe batch ID was accepted")
	}
}

func corruptBatchFile(t *testing.T, root, id, name string) {
	t.Helper()
	path := filepath.Join(root, "parquet", "batches", id+telemetry.BatchSuffix, name)
	if err := os.WriteFile(path, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestQuarantineRefusesVerifierFailureAndUnsupportedFormat(t *testing.T) {
	root := t.TempDir()
	repository, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), Batch{ID: "valid", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}}); err != nil {
		t.Fatal(err)
	}
	repository.Close()
	for _, err := range []error{context.Canceled, fmt.Errorf("native engine exhausted memory"), telemetry.ErrUnsupportedBatchFormat} {
		if _, got := quarantineBatch(root, "valid", func(string) error { return err }); got == nil {
			t.Fatalf("quarantined after %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "parquet", "batches", "valid.batch")); err != nil {
			t.Fatal(err)
		}
	}
}
