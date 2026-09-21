package telemetry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// _schema.batch is an empty Parquet batch whose only job is to carry this
// binary's column list: internal/query derives every view's explicit
// read_parquet(schema=MAP{...}) from it, so a column missing there is a column
// missing from the view.
//
// It was written once, on the first boot into an empty data directory, and
// never revisited. Every deployment since has been reading the column list of
// whichever build happened to create the directory. Adding a column to
// spanParquetRow would have shipped a binary whose views cannot bind it --
// CreateViews fails, NewDuck returns an error, the process does not start, and
// restarting does not help because the stale file is still there.
//
// The direction that matters is an OLD file and a NEW binary, so that is what
// this sets up. A test that writes a fresh _schema.batch and reads it back
// passes against the broken code.
func TestSchemaBatchTracksTheRunningBinary(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	spansFile := filepath.Join(dir, "batches", SchemaBatch, "spans.parquet")
	current := parquetColumns(t, spansFile)
	if len(current) < 10 {
		t.Fatalf("only %d columns in a freshly written schema batch: %v", len(current), current)
	}

	// An older build's _schema.batch: same file, fewer columns.
	type oldSpanRow struct {
		Namespace string `parquet:"namespace"`
		TraceID   string `parquet:"trace_id"`
		SpanID    string `parquet:"span_id"`
	}
	if err := os.Remove(spansFile); err != nil {
		t.Fatal(err)
	}
	if err := writeTypedParquet(spansFile, []oldSpanRow{}, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	if got := parquetColumns(t, spansFile); len(got) != 3 {
		t.Fatalf("fixture is wrong: wrote %d columns, want 3", len(got))
	}

	reopened, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatalf("reopen against an older schema batch: %v", err)
	}
	defer reopened.Close()

	got := parquetColumns(t, spansFile)
	if len(got) != len(current) {
		t.Errorf("after reopening, the schema batch has %d columns, want this binary's %d\n got:  %v\n want: %v",
			len(got), len(current), got, current)
	}
}

func parquetColumns(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	parquetFile, err := parquet.OpenFile(file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for _, field := range parquetFile.Schema().Fields() {
		columns = append(columns, field.Name())
	}
	return columns
}
