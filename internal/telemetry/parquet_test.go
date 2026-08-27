package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParquetBatchStagingIsInvisibleUntilPublish(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "batch-1"
	if err := store.StageBatch(id, []Span{{Namespace: "default", TraceID: "trace", SpanID: "span"}}, []Log{{Namespace: "default", Body: "body"}}, []Metric{{Namespace: "default", Name: "metric"}}); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		final := filepath.Join(store.Dir(), signal, id+".parquet")
		if _, err := os.Stat(final); !os.IsNotExist(err) {
			t.Fatalf("%s final file visible before publish: %v", signal, err)
		}
		if _, err := os.Stat(final + ".pending"); err != nil {
			t.Fatalf("%s pending file: %v", signal, err)
		}
	}
	rollback, err := store.PublishBatch(id, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if _, err := os.Stat(filepath.Join(store.Dir(), signal, id+".parquet")); err != nil {
			t.Fatalf("%s final file: %v", signal, err)
		}
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		final := filepath.Join(store.Dir(), signal, id+".parquet")
		if _, err := os.Stat(final); !os.IsNotExist(err) {
			t.Fatalf("%s final file visible after rollback: %v", signal, err)
		}
		if _, err := os.Stat(final + ".pending"); err != nil {
			t.Fatalf("%s restored pending file: %v", signal, err)
		}
	}
	if err := store.DiscardBatch(id); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if _, err := os.Stat(filepath.Join(store.Dir(), signal, id+".parquet.pending")); !os.IsNotExist(err) {
			t.Fatalf("%s pending file remained after discard: %v", signal, err)
		}
	}
}
