package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

type traceParquetRow struct {
	TraceHash     uint64 `parquet:"_trace_hash"`
	StartUnixNano int64  `parquet:"start_unix_nano"`
}

func TestCompactionStreamingIndexMatchesReread(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var inputs []string
	for part := 0; part < 4; part++ {
		spans := make([]Span, 5000)
		for i := range spans {
			// Shared traces cross inputs and read buffers. Tied and signed times
			// exercise the complete sort tuple and the existing metadata bounds.
			n := part*len(spans) + i
			spans[i] = Span{Namespace: fmt.Sprint("ns-", n%3), TraceID: fmt.Sprint("trace-", n%97), SpanID: fmt.Sprint("span-", n), StartUnixNanos: int64(n%11) - 5, EndUnixNanos: int64(n%11) + 5, IngestedAt: 100, Resource: map[string]any{"bytes": []byte{0, 255}, "typed": []any{int64(n), false}}, Attributes: map[string]any{"n": int64(n)}}
		}
		id := fmt.Sprint("input-", part)
		if err := store.CommitBatch(t.Context(), BatchMetadata{ID: id}, spans, nil, nil); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, filepath.Join(store.BatchPath(id), "spans.parquet"))
	}
	dir := filepath.Join(t.TempDir(), "output"+BatchSuffix)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "spans.parquet")
	stats, err := store.MergeParquet(t.Context(), "spans", inputs, output)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 20000 || stats.MinSpanStartNanos != 1 || stats.MaxSpanStartNanos != 5 {
		t.Fatalf("merge stats=%+v", stats)
	}
	// This is the old narrow reread, retained only as an independent oracle.
	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := parquet.NewGenericReader[traceParquetRow](file)
	defer reader.Close()
	wantPath := filepath.Join(t.TempDir(), "trace.fidx")
	index, err := newTraceIndexWriter(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Abort()
	buffer := make([]traceParquetRow, 8192)
	for {
		n, err := reader.Read(buffer)
		for _, row := range buffer[:n] {
			if err := index.Append(row.TraceHash); err != nil {
				t.Fatal(err)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatal(io.ErrNoProgress)
		}
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "trace.fidx"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("streaming trace.fidx differs from reread")
	}
	metadata := BatchMetadata{ID: "output", Spans: int(stats.Rows), MinSpanStartNanos: stats.MinSpanStartNanos, MaxSpanStartNanos: stats.MaxSpanStartNanos}
	if err := store.PrepareReplacement(dir, metadata); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewBatchVerifier(t.Context(), filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	if err := verifier.Validate(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
}

type cancelMergeContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelMergeContext) Err() error {
	c.checks++
	if c.checks == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestCancelledSpanMergeRemovesOutputAndIndex(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spans := make([]Span, 5000)
	for i := range spans {
		spans[i] = Span{TraceID: fmt.Sprint("trace-", i), SpanID: fmt.Sprint(i), StartUnixNanos: int64(i), IngestedAt: 100}
	}
	if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "input"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &cancelMergeContext{Context: base, cancel: cancel}
	dir := t.TempDir()
	// Cancel between reads, after rows have reached both the output writer and
	// the index. Neither partial artifact may survive a failed merge.
	if _, err := store.MergeParquet(ctx, "spans", []string{filepath.Join(store.BatchPath("input"), "spans.parquet")}, filepath.Join(dir, "spans.parquet")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("artifacts=%v error=%v", entries, err)
	}
}
