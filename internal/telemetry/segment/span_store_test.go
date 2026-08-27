// Tests use the internal package to exercise crash boundaries and corruption.
package segment

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCommitReopenAndQueries(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).UnixNano()
	rows := []Span{
		{Namespace: "default", TraceID: "trace-a", SpanID: "1", ServiceName: "api", HTTPMethod: "GET", HTTPRoute: "/users/:id", StartUnixNanos: base, EndUnixNanos: base + int64(10*time.Millisecond), DurationMS: 10, StatusCode: "OK", AttributesJSON: []byte(`{"tenant":"a"}`)},
		{Namespace: "default", TraceID: "trace-a", SpanID: "2", ParentSpanID: "1", ServiceName: "db", StartUnixNanos: base + int64(time.Millisecond), EndUnixNanos: base + int64(6*time.Millisecond), DurationMS: 5, StatusCode: "OK"},
		{Namespace: "default", TraceID: "trace-b", SpanID: "3", ServiceName: "api", HTTPMethod: "GET", HTTPRoute: "/users/:id", StartUnixNanos: base + int64(time.Minute), EndUnixNanos: base + int64(time.Minute+50*time.Millisecond), DurationMS: 50, StatusCode: "ERROR"},
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(rows[:2]); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(rows[2:]); err != nil {
		t.Fatal(err)
	}
	if got := store.RowCount(); got != 3 {
		t.Fatalf("row count = %d", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Files left by an interrupted append are not present in the committed
	// manifest and must not become visible after recovery.
	if err := os.WriteFile(filepath.Join(dir, "999.fseg.tmp"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.RowCount(); got != 3 {
		t.Fatalf("reopened row count = %d", got)
	}
	trace, err := store.Trace("trace-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(trace) != 2 || !EqualSpan(trace[0], rows[0]) || !EqualSpan(trace[1], rows[1]) {
		t.Fatalf("trace result = %#v", trace)
	}
	endpoints := store.Endpoints("default", "api", base, base+int64(5*time.Minute), 10)
	if len(endpoints) != 1 || endpoints[0].Calls != 2 || endpoints[0].Errors != 1 {
		t.Fatalf("endpoint result = %#v", endpoints)
	}
	agg, err := store.ScanService("default", "api", base, base+int64(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Calls != 2 || agg.Errors != 1 || agg.DurationMS != 60 {
		t.Fatalf("aggregate = %#v", agg)
	}
}

func TestStoreSkipsOrphanAndAdvancesSegmentID(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UnixNano()
	row := Span{TraceID: "trace", SpanID: "span", StartUnixNanos: base}
	if err := store.Append([]Span{row}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after publishing segment 2 but before its manifest commit.
	orphan := filepath.Join(dir, "00000000000000000002.fseg")
	committed := filepath.Join(dir, "00000000000000000001.fseg")
	data, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, data, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.RowCount(); got != 1 {
		t.Fatalf("orphan became visible: row count = %d", got)
	}
	if err := store.Append([]Span{row}); err != nil {
		t.Fatalf("append after orphan: %v", err)
	}
	if got := store.RowCount(); got != 2 {
		t.Fatalf("row count after append = %d", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "00000000000000000003.fseg")); err != nil {
		t.Fatalf("allocator did not advance past orphan: %v", err)
	}
}

func TestStoreCompactionPreservesRowsAndIndexes(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UnixNano()
	for batch := range 3 {
		rows := []Span{
			{TraceID: "shared", SpanID: string(rune('a' + batch)), ServiceName: "api", StartUnixNanos: base + int64(batch), StatusCode: "OK"},
			{TraceID: "other", SpanID: string(rune('x' + batch)), ServiceName: "worker", StartUnixNanos: base + int64(batch+10), StatusCode: "ERROR"},
		}
		if err := store.Append(rows); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CompactOldest(2); err != nil {
		t.Fatal(err)
	}
	if got := store.SegmentCount(); got != 2 {
		t.Fatalf("segments after compaction = %d", got)
	}
	if got := store.RowCount(); got != 6 {
		t.Fatalf("rows after compaction = %d", got)
	}
	trace, err := store.Trace("shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(trace) != 3 {
		t.Fatalf("trace rows after compaction = %d", len(trace))
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.RowCount(); got != 6 {
		t.Fatalf("reopened compacted rows = %d", got)
	}
}

func TestStoreRejectsCorruptCommittedSegment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.fseg"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MANIFEST.json"), []byte(`{"next_id":2,"files":["broken.fseg"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open succeeded with a corrupt committed segment")
	}
}

func TestOpenRejectsSegmentWithCorruptSectionOffsets(t *testing.T) {
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).UnixNano()
	rows := []Span{{Namespace: "default", TraceID: "trace-a", SpanID: "1", ServiceName: "api", StartUnixNanos: base, EndUnixNanos: base + 1, DurationMS: 1, StatusCode: "OK"}}
	corrupt := func(t *testing.T, mutate func(header []byte)) {
		t.Helper()
		dir := t.TempDir()
		store, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendID("seg-a", rows); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "seg-a.fseg")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mutate(data[:headerSize])
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil {
			t.Fatal("Open succeeded with corrupt section offsets")
		}
	}
	t.Run("rollup offset before index offset", func(t *testing.T) {
		corrupt(t, func(header []byte) {
			indexOffset := binary.LittleEndian.Uint64(header[48:56])
			binary.LittleEndian.PutUint64(header[56:64], indexOffset-1)
		})
	})
	t.Run("rollup offset beyond file size", func(t *testing.T) {
		corrupt(t, func(header []byte) {
			binary.LittleEndian.PutUint64(header[56:64], 1<<40)
		})
	})
}
