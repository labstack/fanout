package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTraceIndexLookupKeepsEntriesOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.fidx")
	rows := make([]spanParquetRow, 10_000)
	for i := range rows {
		rows[i].TraceHash = uint64(i * 2)
	}
	if err := writeTraceIndex(path, rows); err != nil {
		t.Fatal(err)
	}
	index, err := loadTraceIndex(path, len(rows))
	if err != nil {
		t.Fatal(err)
	}
	if index.entries != uint64(len(rows)) || index.path != path {
		t.Fatalf("index metadata = %#v", index)
	}
	for _, hash := range []uint64{0, 9_998, 19_998} {
		match, found, err := index.Lookup(hash)
		if err != nil || !found || match.hash != hash || match.row != hash/2 || match.count != 1 {
			t.Fatalf("lookup(%d) = %#v, %v, %v", hash, match, found, err)
		}
	}
	if _, found, err := index.Lookup(9_999); err != nil || found {
		t.Fatalf("missing lookup = %v, %v", found, err)
	}
}

func TestTraceIndexRejectsNoncontiguousEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.fidx")
	rows := []spanParquetRow{{TraceHash: 1}, {TraceHash: 2}}
	if err := writeTraceIndex(path, rows); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The second entry must begin at row one; force it to row two.
	if _, err := file.WriteAt([]byte{2}, traceIndexHeaderSize+traceIndexEntrySize+8); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTraceIndex(path, len(rows)); err == nil {
		t.Fatal("loadTraceIndex accepted a noncontiguous range")
	}
}
