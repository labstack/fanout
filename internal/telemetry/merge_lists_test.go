package telemetry

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// List columns hold a row's elements as consecutive values. Compaction
// advances them by whole rows, including rows longer than its read buffer
// and rows split across reads, interleaved across inputs, and sums the
// inputs' spill counts.
func TestMergeCopiesListColumnsByWholeRows(t *testing.T) {
	dir := t.TempDir()
	var inputs []string
	var all []Metric
	for file := range 3 {
		rows := make([]Metric, 400)
		for i := range rows {
			n := (i*37 + file*11) % 301
			bounds, counts := make([]float64, n), make([]uint64, n+1)
			for j := range bounds {
				bounds[j] = float64(j)
				counts[j] = uint64(i + j)
			}
			rows[i] = Metric{Namespace: "ns", ServiceName: fmt.Sprint(i % 3), Name: "latency", Type: "histogram", Temporality: "delta",
				SeriesID: uint64(i), TimeUnixNanos: int64(file*1000 + i + 1), Histogram: Histogram{Count: uint64(n), Bounds: bounds, Counts: counts},
				Exemplars: make([]Exemplar, i%4), Resource: map[string]any{"k8s.container.restart_count": fmt.Sprint(file)}}
			for e := range rows[i].Exemplars {
				rows[i].Exemplars[e] = Exemplar{TraceID: fmt.Sprintf("%d-%d", i, e), Attributes: map[string]any{"e": int64(e)}}
			}
		}
		path := filepath.Join(dir, fmt.Sprintf("%d.parquet", file))
		if err := writeParquet(path, Metrics, rows, 1024); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
		all = append(all, inOrder(Metrics, rows)...)
	}
	output := filepath.Join(dir, "merged.parquet")
	if err := mergeParquet(context.Background(), Metrics, inputs, output, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	want := inOrder(Metrics, all)
	got := engineRows(t, output)
	if len(got) != len(want) {
		t.Fatalf("merged %d rows, want %d", len(got), len(want))
	}
	for i, row := range got {
		if row["hist_count"] != fmt.Sprint(len(want[i].Bounds)) || row["hist_bounds"] != floats(want[i].Bounds) ||
			row["exemplar_trace_ids"] != ids(want[i].Exemplars) {
			t.Fatalf("row %d: count %s bounds %.40s exemplars %s, want %d bounds %.40s exemplars %s", i,
				row["hist_count"], row["hist_bounds"], row["exemplar_trace_ids"], len(want[i].Bounds), floats(want[i].Bounds), ids(want[i].Exemplars))
		}
	}
	counts, err := fileSpillCounts(output, Metrics)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := Metrics.Column("_r_k8s_container_restart_count")
	if counts[key.Key.Bit] != 1200 {
		t.Fatalf("merged spill count = %d, want 1200", counts[key.Key.Bit])
	}
	verifier, err := NewBatchVerifier(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	if err := verifier.values(t.Context(), output, Metrics); err != nil {
		t.Fatal(err)
	}
	if err := verifyOrder(output, Metrics, len(want)); err != nil {
		t.Fatal(err)
	}
}

func floats(values []float64) string {
	out := "["
	for i, v := range values {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%.1f", v)
	}
	return out + "]"
}

func ids(exemplars []Exemplar) string {
	out := "["
	for i, e := range exemplars {
		if i > 0 {
			out += ", "
		}
		out += e.TraceID
	}
	return out + "]"
}

// The verifier rejects a dedicated column that disagrees with its map and a
// footer whose spill count disagrees with the rows.
func TestVerifierRejectsDedicatedColumnMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spans.parquet")
	// The same layout with the method column filled from elsewhere.
	layout := &Signal{Name: Spans.Name, Time: Spans.Time, Order: Spans.Order, Traces: true}
	for _, c := range Spans.Columns {
		if c.Name == "_a_http_request_method" {
			c = nullable(c.Name, String, func(*Span) (string, bool) { return "GET", true }).dict()
		}
		layout.Columns = append(layout.Columns, c)
	}
	layout.schema, layout.order = buildSchema(layout), compileOrder(layout)
	if err := writeParquet(path, layout, []Span{completeTestSpan()}, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewBatchVerifier(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	if err := verifier.values(t.Context(), path, Spans); !IsBatchCorrupt(err) {
		t.Fatalf("mismatched column: %v", err)
	}
}
