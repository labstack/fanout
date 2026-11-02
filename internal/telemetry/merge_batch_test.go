package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
