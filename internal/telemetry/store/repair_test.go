package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
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
	corruptBatchFile(t, root, "broken", "trace.fidx")

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
