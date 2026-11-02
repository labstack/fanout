package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestLargerLogPagesKeepWindowBufferBudget(t *testing.T) {
	for _, pageSize := range []int{128 << 10, 256 << 10} {
		for rows := int64(1); rows <= 2_000_000; rows += 997 {
			groups := min(mergeWindowRowGroups, int((rows+parquetRowGroupRows-1)/parquetRowGroupRows))
			budget := max(pageSize, groups*parquetPageSize) // at least one output group
			window := mergeLogWindowLimit(rows, pageSize)
			if window < 1 || window*pageSize > budget {
				t.Fatalf("%d rows, %d-byte pages: %d groups exceed %d-byte column budget", rows, pageSize, window, budget)
			}
		}
	}
}

// Whole log groups selected as single runs keep their compressed chunks and
// page indexes. A merge with overlapping input groups takes the regular path.
func TestMergeCopiesWholeOrderedLogGroups(t *testing.T) {
	dir := t.TempDir()
	// Use a different body encoding in the source so an accidental re-encode
	// cannot pass the compressed-byte comparison below.
	dense := *Logs
	fields := parquet.Group{}
	for _, field := range Logs.schema.Fields() {
		fields[field.Name()] = field
		if field.Name() == "body" {
			fields[field.Name()] = parquet.Compressed(field, parquetDenseCodec)
		}
	}
	dense.schema = parquet.NewSchema(Logs.schema.Name(), fields)
	var inputs []string
	for file := range 2 {
		rows := make([]Log, parquetRowGroupRows)
		for i := range rows {
			rows[i] = Log{
				EventUnixNanos: int64(file)*3_600_000_000_000 + int64(i+1),
				ServiceName:    fmt.Sprint(file), Body: fmt.Sprint("message ", i%19),
				Attributes: map[string]any{"nested": map[string]any{"number": int64(i)}},
				Resource:   map[string]any{"service.name": fmt.Sprint(file)},
			}
		}
		path := filepath.Join(dir, fmt.Sprintf("input-%d.parquet", file))
		if err := writeParquet(path, Logs, rows, parquetPageSize); err != nil {
			t.Fatal(err)
		}
		// First compact the ingest dictionaries into one shared sorted blob.
		canonical := filepath.Join(dir, fmt.Sprintf("canonical-%d.parquet", file))
		if err := mergeParquet(context.Background(), &dense, []string{path}, canonical, &ParquetMergeStats{}); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, canonical)
	}
	merge := func(name string) string {
		out := filepath.Join(dir, name)
		var stats ParquetMergeStats
		if err := mergeParquet(context.Background(), Logs, inputs, out, &stats); err != nil {
			t.Fatal(err)
		}
		if stats.Rows != 2*parquetRowGroupRows {
			t.Fatalf("rows = %d", stats.Rows)
		}
		return out
	}
	out := merge("merged.parquet")
	merged := openParquetFile(t, out)
	if len(merged.RowGroups()) != len(inputs) {
		t.Fatalf("row groups = %d", len(merged.RowGroups()))
	}
	chunkBytes := func(pf *parquet.File, group, column int) []byte {
		meta := &pf.Metadata().RowGroups[group].Columns[column].MetaData
		start := meta.DataPageOffset
		if meta.DictionaryPageOffset > 0 && meta.DictionaryPageOffset < start {
			start = meta.DictionaryPageOffset
		}
		b := make([]byte, meta.TotalCompressedSize)
		if _, err := io.ReadFull(io.NewSectionReader(pf, start, int64(len(b))), b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	for i, path := range inputs {
		source := openParquetFile(t, path)
		for col := range source.Schema().Columns() {
			if !bytes.Equal(chunkBytes(source, 0, col), chunkBytes(merged, i, col)) {
				t.Fatalf("group %d column %d was re-encoded", i, col)
			}
		}
	}
	if got := len(engineRows(t, out)); got != 2*parquetRowGroupRows {
		t.Fatalf("decoded %d rows", got)
	}
	first, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(merge("again.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("copying the same inputs produced different files")
	}
}

// A merge whose inputs need more readers than mergeReaderBudget writes its
// columns in batches over windows of row groups, reopening each reader at its
// next row in the next window. The rows must not change, list rows split
// across reads and pages included.
func TestBatchedMergeKeepsRows(t *testing.T) {
	dir := t.TempDir()
	var inputs []string
	for file := range 3 {
		rows := make([]Metric, 25_000)
		for i := range rows {
			n := (i*7 + file) % 13
			bounds, counts := make([]float64, n), make([]uint64, n+1)
			for j := range bounds {
				bounds[j] = float64(j)
				counts[j] = uint64(i + j)
			}
			rows[i] = Metric{Namespace: "ns", ServiceName: fmt.Sprint(i % 3), Name: "latency", Type: "histogram", Temporality: "delta",
				SeriesID: uint64(i % 50), TimeUnixNanos: int64(file*7 + i*3 + 1), Histogram: Histogram{Count: uint64(n), Bounds: bounds, Counts: counts},
				Exemplars:  make([]Exemplar, i%3),
				Attributes: map[string]any{"route": fmt.Sprint("/r", i%5)}, Resource: map[string]any{"host.name": fmt.Sprint("h", file)}}
			for e := range rows[i].Exemplars {
				rows[i].Exemplars[e] = Exemplar{TraceID: fmt.Sprintf("%d-%d", i, e), Attributes: map[string]any{"e": int64(e)}}
			}
		}
		path := filepath.Join(dir, fmt.Sprintf("%d.parquet", file))
		if err := writeParquet(path, Metrics, rows, 1024); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
	}
	merge := func(name string) string {
		out := filepath.Join(dir, name)
		if err := mergeParquet(context.Background(), Metrics, inputs, out, &ParquetMergeStats{}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	whole := merge("whole.parquet")
	budget, window := mergeReaderBudget, mergeWindowRowGroups
	mergeReaderBudget, mergeWindowRowGroups = 20, 1
	defer func() { mergeReaderBudget, mergeWindowRowGroups = budget, window }()
	batched := merge("batched.parquet")
	if got, want := engineRows(t, batched), engineRows(t, whole); !reflect.DeepEqual(got, want) {
		t.Fatalf("batched merge changed rows: %d rows, want %d", len(got), len(want))
	}
}

// Compacted row groups that each carry one sorted dictionary are merged
// without rewriting their values; the output is the rewrite's, byte for byte.
func TestCompactedVariantFastPathMatchesRewrite(t *testing.T) {
	dir := t.TempDir()
	compact := func(name string, offset int) string {
		var inputs []string
		for f := range 4 {
			rows := make([]Log, 30_000)
			for i := range rows {
				attributes := map[string]any{"messaging.system": "kafka", fmt.Sprint("app.k", (i+f)%6): int64(i)}
				if i%7 == 0 {
					attributes["nested"] = map[string]any{"inner": []any{map[string]any{fmt.Sprint("n", i%3): true}}}
				}
				rows[i] = Log{EventUnixNanos: int64(offset + i*8 + f), Attributes: attributes,
					Resource: map[string]any{"service.name": fmt.Sprint("svc-", i%3), fmt.Sprint("host.", (f+offset)%5): "h"}}
			}
			path := filepath.Join(dir, fmt.Sprintf("%s-%d.parquet", name, f))
			if err := writeParquet(path, Logs, rows, parquetPageSize); err != nil {
				t.Fatal(err)
			}
			inputs = append(inputs, path)
		}
		out := filepath.Join(dir, name+".parquet")
		if err := mergeParquet(context.Background(), Logs, inputs, out, &ParquetMergeStats{}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	inputs := []string{compact("a", 0), compact("b", 3)}
	merge := func(name string, keep bool) []byte {
		variantKeepSorted = keep
		defer func() { variantKeepSorted = true }()
		out := filepath.Join(dir, name)
		if err := mergeParquet(context.Background(), Logs, inputs, out, &ParquetMergeStats{}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if !bytes.Equal(merge("kept.parquet", true), merge("rewritten.parquet", false)) {
		t.Fatal("the fast path wrote a different file from the rewrite")
	}
}
