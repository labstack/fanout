package telemetry

import (
	"github.com/parquet-go/parquet-go"
	"os"
	"path/filepath"
	"testing"
)

func TestPressureBatchKeepsFiftyThousandRowGroups(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows := make([]Log, 200_000)
	for i := range rows {
		rows[i] = Log{TimeUnixNanos: int64(i + 1), Body: "pressure"}
	}
	metadata := BatchMetadata{ID: "pressure"}
	if err := store.CommitBatch(t.Context(), &metadata, nil, rows, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(store.BatchPath(metadata.ID), "logs.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	p, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.RowGroups()) != 4 {
		t.Fatalf("row groups=%d want=4", len(p.RowGroups()))
	}
	for _, g := range p.RowGroups() {
		if g.NumRows() != 50_000 {
			t.Fatalf("rows=%d", g.NumRows())
		}
	}
}
