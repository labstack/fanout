// Tests use the internal package to exercise crash boundaries and corruption.
package segment

import (
	"encoding/binary"
	"github.com/klauspost/compress/zstd"
	"os"
	"path/filepath"
	"strings"
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

func TestEndpointsCountCanonicalOTelErrorStatus(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).UnixNano()
	rows := []Span{
		{Namespace: "default", TraceID: "t1", SpanID: "1", ServiceName: "api", HTTPMethod: "GET", HTTPRoute: "/x", StartUnixNanos: base, EndUnixNanos: base + 1, DurationMS: 1, StatusCode: "STATUS_CODE_ERROR"},
		{Namespace: "default", TraceID: "t2", SpanID: "2", ServiceName: "api", HTTPMethod: "GET", HTTPRoute: "/x", StartUnixNanos: base + 1, EndUnixNanos: base + 2, DurationMS: 1, StatusCode: "STATUS_CODE_OK"},
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.AppendID("seg-status", rows); err != nil {
		t.Fatal(err)
	}
	endpoints := store.Endpoints("default", "api", base, base+int64(time.Minute), 10)
	if len(endpoints) != 1 {
		t.Fatalf("endpoints = %#v, want one route", endpoints)
	}
	if endpoints[0].Errors != 1 {
		t.Fatalf("Errors = %d, want 1: OTLP ingest stores Status.Code.String() as STATUS_CODE_ERROR", endpoints[0].Errors)
	}
	agg, err := store.ScanService("default", "api", base, base+int64(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Errors != 1 {
		t.Fatalf("aggregate Errors = %d, want 1", agg.Errors)
	}
}

func TestValidateSegmentSectionsRejectsOutOfBoundsDirectory(t *testing.T) {
	const size = 4096
	tests := []struct {
		name                                 string
		dirOffset, indexOffset, rollupOffset uint64
		blockCount                           uint32
	}{
		{"wrapping directory end", ^uint64(0) - uint64(0xFFFFFFFF)*blockDirSize + 1, 512, 1024, 0xFFFFFFFF},
		{"block count past index", headerSize, 512, 1024, 0xFFFFFFFF},
		{"directory before header", 0, 512, 1024, 1},
		{"sections out of order", headerSize, 2048, 1024, 1},
		{"rollups past end of file", headerSize, 512, size + 1, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSegmentSections(size, test.dirOffset, test.indexOffset, test.rollupOffset, test.blockCount); err == nil {
				t.Fatal("validateSegmentSections accepted a corrupt header")
			}
		})
	}
	if err := validateSegmentSections(size, headerSize, 512, 1024, 8); err != nil {
		t.Fatalf("validateSegmentSections rejected a sound header: %v", err)
	}
}

func TestValidateSegmentBlocksRejectsOutOfBoundsExtents(t *testing.T) {
	const size, dirOffset = 4096, uint64(2048)
	sound := []blockDir{
		{offset: headerSize, length: 512, rows: 10},
		{offset: headerSize + 512, length: 512, rows: 10},
	}
	if err := validateSegmentBlocks(size, dirOffset, sound, 20); err != nil {
		t.Fatalf("validateSegmentBlocks rejected sound blocks: %v", err)
	}
	tests := []struct {
		name   string
		blocks []blockDir
		rows   uint32
	}{
		{"length past the directory", []blockDir{{offset: headerSize, length: 4096, rows: 10}}, 10},
		{"offset inside the header", []blockDir{{offset: 0, length: 16, rows: 10}}, 10},
		{"extent wraps", []blockDir{{offset: ^uint64(0) - 8, length: 64, rows: 10}}, 10},
		{"rows disagree with the header", []blockDir{{offset: headerSize, length: 16, rows: 10}}, 11},
		{"empty block", []blockDir{{offset: headerSize, length: 0, rows: 0}}, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSegmentBlocks(size, dirOffset, test.blocks, test.rows); err == nil {
				t.Fatal("validateSegmentBlocks accepted a corrupt block directory")
			}
		})
	}
}

func TestOpenRejectsSegmentWithCorruptBlockEntry(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).UnixNano()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendID("seg-block", []Span{{Namespace: "default", TraceID: "t", SpanID: "1", ServiceName: "api", StartUnixNanos: base, EndUnixNanos: base + 1, DurationMS: 1, StatusCode: "OK"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "seg-block.fseg")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dirOffset := binary.LittleEndian.Uint64(data[40:48])
	// Claim the first block runs far past the directory it precedes.
	binary.LittleEndian.PutUint32(data[int(dirOffset)+8:], 1<<30)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Open(dir)
	if err == nil {
		t.Fatal("Open accepted a segment whose block extends past its directory")
	}
	if !strings.Contains(err.Error(), "block extends past the block directory") {
		t.Fatalf("Open error = %v, want the block-extent guard to reject it", err)
	}
}

func TestStoreAcceptsSpanWithOversizedRollupKey(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).UnixNano()
	// A pathological route must not be a deterministic publish failure: the WAL
	// would then abort every restart with no way to make progress.
	route := "/" + strings.Repeat("x", 70000)
	rows := []Span{{Namespace: "default", TraceID: "t", SpanID: "1", ServiceName: "api", HTTPMethod: "GET", HTTPRoute: route, StartUnixNanos: base, EndUnixNanos: base + 1, DurationMS: 1, StatusCode: "OK"}}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendID("seg-big-key", rows); err != nil {
		t.Fatalf("AppendID error = %v, want an oversized rollup key to be publishable", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open error = %v, want the segment to round-trip", err)
	}
	defer reopened.Close()
	endpoints := reopened.Endpoints("default", "api", base, base+int64(time.Minute), 10)
	if len(endpoints) != 1 || endpoints[0].Route != route {
		t.Fatalf("endpoints = %d entries, want the full route preserved", len(endpoints))
	}
}

func TestValidateSegmentBlocksRejectsRowsPastBlockCap(t *testing.T) {
	blocks := []blockDir{{offset: headerSize, length: 16, rows: rowsPerBlock + 1}}
	if err := validateSegmentBlocks(4096, 2048, blocks, rowsPerBlock+1); err == nil {
		t.Fatal("validateSegmentBlocks accepted a block claiming more rows than a block can hold")
	}
}

func TestSegmentDecoderRejectsOversizedFrame(t *testing.T) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	frame := encoder.EncodeAll(make([]byte, segmentDecoderMaxMemory+(1<<20)), nil)
	decoder, err := newSegmentDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	if _, err := decoder.DecodeAll(frame, nil); err == nil {
		t.Fatal("segment decoder accepted a frame declaring more memory than the product ever needs")
	}
}
