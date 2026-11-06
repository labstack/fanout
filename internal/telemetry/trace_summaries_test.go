package telemetry

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/zeebo/xxh3"
)

func TestTraceSummariesResolveCollisionsNamespacesAndWindowBounds(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	start := int64(time.Second)
	end := start + int64(10*time.Millisecond)
	hash := xxh3.HashString("shared")
	for i := range 3 {
		dir := t.TempDir()
		n := start + int64(i)*int64(time.Millisecond)
		parent := "root"
		if i == 2 {
			parent = ""
		}
		status := "STATUS_CODE_OK"
		if i == 1 {
			status = "STATUS_CODE_ERROR"
		}
		rows := []indexedSpanRow{
			{TraceID: "shared", SpanID: "before", StartUnixNano: start - 1, Status: "STATUS_CODE_ERROR", DurationMS: 100},
			{TraceID: "shared", SpanID: fmt.Sprint(i), ParentSpanID: parent, Service: fmt.Sprint(i % 2), StartUnixNano: n, DurationMS: 1},
			{Namespace: "b", TraceID: "shared", SpanID: fmt.Sprint(i), Service: "worker", StartUnixNano: n + 100, DurationMS: 3, Status: status},
			{TraceID: "collision", SpanID: "wrong", Service: "wrong", StartUnixNano: n + 200, DurationMS: 100, Status: "STATUS_CODE_ERROR"},
			{TraceID: "shared", SpanID: "at-end", StartUnixNano: end, DurationMS: 100, Status: "STATUS_CODE_ERROR"},
			{Namespace: "b", TraceID: "shared", SpanID: "after-end", StartUnixNano: end + 1, DurationMS: 100},
		}
		if err := parquet.WriteFile(filepath.Join(dir, "spans.parquet"), rows); err != nil {
			t.Fatal(err)
		}
		// Inject a collision: both IDs occupy one time-ordered hash range. This
		// avoids a production hash hook and exercises the public summary read.
		hashes := make([]uint64, len(rows))
		for j := range hashes {
			hashes[j] = hash
		}
		path := filepath.Join(dir, "trace.fidx")
		if err := writeTraceIndex(path, hashes); err != nil {
			t.Fatal(err)
		}
		index, err := loadTraceIndex(path, len(rows))
		if err != nil {
			t.Fatal(err)
		}
		store.batches[fmt.Sprint(i)] = &storedBatch{dir: dir, traces: index}
	}
	ids := []TraceIdentity{{Namespace: "", TraceID: "shared"}, {Namespace: "b", TraceID: "shared"}, {Namespace: "missing", TraceID: "shared"}, {TraceID: "absent"}}
	got, err := store.TraceSummaries(t.Context(), ids, start, end)
	if err != nil {
		t.Fatal(err)
	}
	a, b := got[0], got[1]
	if a.Spans != 3 || a.Services != 2 || a.HasError || a.DurationMS != 3 || a.MinStartNano != start || a.MaxEndNano != start+int64(3*time.Millisecond) || a.Root == nil || a.Root.SpanID != "2" || a.Root.TraceID != "shared" || a.Root.Namespace != "" {
		t.Fatalf("empty namespace: %+v root=%+v", a, a.Root)
	}
	if b.Spans != 3 || b.Services != 1 || !b.HasError || b.DurationMS != 5 || b.MinStartNano != start+100 || b.MaxEndNano != start+100+int64(5*time.Millisecond) || b.Root == nil || b.Root.SpanID != "0" || b.Root.Namespace != "b" {
		t.Fatalf("namespace b: %+v root=%+v", b, b.Root)
	}
	for _, missing := range got[2:] {
		if missing != (TraceTotals{}) {
			t.Fatalf("missing identity: %+v", missing)
		}
	}
}
