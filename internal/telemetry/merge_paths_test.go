package telemetry

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/encoding/thrift"
)

// requireFooterPaths checks that every chunk names its schema leaf and that
// the file keeps known event-time bounds equal to its inputs' union.
func requireFooterPaths(t *testing.T, s *Signal, path string, inputs []string) {
	t.Helper()
	pf := openParquetFile(t, path)
	leaves := s.schema.Columns()
	for g, group := range pf.Metadata().RowGroups {
		for i, chunk := range group.Columns {
			if !slices.Equal(chunk.MetaData.PathInSchema, leaves[i]) {
				t.Fatalf("row group %d column %d path %q; want %q", g, i, chunk.MetaData.PathInSchema, leaves[i])
			}
		}
	}
	if err := checkColumnPaths(pf, s); err != nil {
		t.Fatal(err)
	}
	if len(pf.RowGroups()) < 3 {
		t.Fatalf("%d row groups; the merge must commit several", len(pf.RowGroups()))
	}
	var want TimeRange
	for _, input := range inputs {
		r := parquetTimeRange(openParquetFile(t, input), s.Time)
		if !r.Known {
			t.Fatalf("input %s has unknown %s bounds", input, s.Time)
		}
		if !want.Known || r.MinNanos < want.MinNanos {
			want.MinNanos = r.MinNanos
		}
		if !want.Known || r.MaxNanos > want.MaxNanos {
			want.MaxNanos = r.MaxNanos
		}
		want.Known = true
	}
	if got := parquetTimeRange(pf, s.Time); got != want {
		t.Fatalf("%s bounds %+v; want %+v", s.Time, got, want)
	}
}

// canonicalInput writes rows and compacts them alone, as a first generation.
func canonicalInput[R any](t *testing.T, dir, name string, s *Signal, rows []R) string {
	t.Helper()
	// Span merges write a trace index beside their output.
	dir = filepath.Join(dir, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(dir, name+"-raw.parquet")
	if err := writeParquet(raw, s, rows, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name+".parquet")
	if err := mergeParquet(t.Context(), s, []string{raw}, out, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	return out
}

func mergeForTest(t *testing.T, s *Signal, inputs []string, out string) {
	t.Helper()
	if err := mergeParquet(t.Context(), s, inputs, out, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	requireFooterPaths(t, s, out, inputs)
}

func TestMergeLogsKeepsColumnPathsAcrossGroups(t *testing.T) {
	dir := t.TempDir()
	const n = parquetRowGroupRows
	h := int64(3_600_000_000_000)
	logs := func(tm func(i int) int64) []Log {
		rows := make([]Log, n)
		for i := range rows {
			rows[i] = Log{Namespace: "ns", ServiceName: "svc", EventUnixNanos: tm(i), Body: fmt.Sprint("m", i%11), Severity: "INFO",
				Attributes: map[string]any{"k": int64(i % 3)}, Resource: map[string]any{"service.name": "svc"}}
		}
		return rows
	}
	// a and c overlap in time; b is disjoint: copied, rewritten, copied.
	inputs := []string{
		canonicalInput(t, dir, "a", Logs, logs(func(i int) int64 { return h + int64(i) })),
		canonicalInput(t, dir, "b", Logs, logs(func(i int) int64 { return 2*h + int64(i) })),
		canonicalInput(t, dir, "c", Logs, logs(func(i int) int64 { return h + int64(2*i) })),
	}
	mergeForTest(t, Logs, inputs, filepath.Join(dir, "out.parquet"))
}

func TestMergeMetricsKeepsColumnPathsAcrossGenerations(t *testing.T) {
	dir := t.TempDir()
	const n = 30_000
	h := int64(3_600_000_000_000)
	metrics := func(file, n int, series func(i int) uint64, tm func(i int) int64, ns string) []Metric {
		rows := make([]Metric, n)
		for i := range rows {
			rows[i] = Metric{Namespace: ns, ServiceName: "svc", Name: "m", Type: "gauge", SeriesID: series(i), TimeUnixNanos: tm(i), Value: float64(i),
				Attributes: map[string]any{"route": fmt.Sprint("/r", i%5), "messaging.system": "kafka"},
				Resource:   map[string]any{"service.name": "svc", "host.name": fmt.Sprint("h", file)}}
			if i%3 == 0 {
				rows[i].Type, rows[i].Temporality = "histogram", "delta"
				rows[i].Histogram = Histogram{Count: 2, Bounds: []float64{1}, Counts: []uint64{1, 1}}
			}
		}
		return rows
	}
	at := func(tm int64) func(int) int64 { return func(int) int64 { return tm } }
	gen2 := []string{
		canonicalInput(t, dir, "a", Metrics, metrics(0, n, func(i int) uint64 { return uint64(i) }, at(h+5), "ns")),
		canonicalInput(t, dir, "b", Metrics, metrics(1, n, func(i int) uint64 { return uint64(n - 1 + i) }, at(h+5), "ns")),
		canonicalInput(t, dir, "c", Metrics, metrics(2, n, func(i int) uint64 { return uint64(n + 2*i) }, at(h+5), "ns")),
		canonicalInput(t, dir, "d", Metrics, metrics(3, n, func(i int) uint64 { return uint64(i) }, func(i int) int64 { return 3*h + int64(i) }, "ns")),
	}
	out2 := filepath.Join(dir, "gen2.parquet")
	mergeForTest(t, Metrics, gen2, out2)
	e := canonicalInput(t, dir, "e", Metrics, metrics(4, 20_000, func(i int) uint64 { return uint64(3 * i) }, at(h+5), "ns"))
	out3 := filepath.Join(dir, "gen3.parquet")
	mergeForTest(t, Metrics, []string{out2, e}, out3)
}

func TestMergeSpansKeepsColumnPathsAcrossGroups(t *testing.T) {
	dir := t.TempDir()
	const n = 30_000
	spans := func(file int, trace func(i int) string, start func(i int) int64) []Span {
		rows := make([]Span, n)
		for i := range rows {
			rows[i] = Span{Namespace: "ns", TraceID: trace(i), SpanID: fmt.Sprintf("%016x", i+1+file*n), StartUnixNanos: start(i), EndUnixNanos: start(i) + 10,
				ServiceName: "svc", Name: "op", StatusCode: "STATUS_CODE_UNSET",
				Attributes: map[string]any{"k": int64(i % 4)}, Resource: map[string]any{"service.name": "svc"}}
			if i%5 == 0 {
				rows[i].Events = []Event{{TimeUnixNanos: start(i), Name: "e"}}
			}
		}
		return rows
	}
	inputs := []string{
		canonicalInput(t, dir, "a", Spans, spans(0, func(int) string { return fmt.Sprintf("%032x", 1) }, func(i int) int64 { return int64(i + 1) })),
		canonicalInput(t, dir, "b", Spans, spans(1, func(int) string { return fmt.Sprintf("%032x", 2) }, func(i int) int64 { return int64(i + 1) })),
		canonicalInput(t, dir, "c", Spans, spans(2, func(i int) string { return fmt.Sprintf("%032x", 3+i%7) }, func(i int) int64 { return int64(i + 1) })),
	}
	mergeForTest(t, Spans, inputs, filepath.Join(dir, "out.parquet"))
}

// A footer whose chunk paths do not name the schema leaves loses its
// event-time bounds silently; loading and verification must reject it.
func TestBatchRejectsFooterColumnPaths(t *testing.T) {
	root := t.TempDir()
	store, err := OpenParquetStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: "paths"}, nil, []Log{{Body: "ready", TimeUnixNanos: 1, IngestedAt: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	dir := store.BatchPath("paths")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublishedBatch(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "logs.parquet")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	size := int(binary.LittleEndian.Uint32(data[len(data)-8:]))
	start := len(data) - 8 - size
	pf, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	meta := *pf.Metadata()
	meta.RowGroups = slices.Clone(meta.RowGroups)
	columns := slices.Clone(meta.RowGroups[0].Columns)
	for i := range columns {
		if slices.Equal(columns[i].MetaData.PathInSchema, []string{Logs.Time}) {
			columns[i].MetaData.PathInSchema = []string{""}
		}
	}
	meta.RowGroups[0].Columns = columns
	footer, err := thrift.Marshal(new(thrift.CompactProtocol), &meta)
	if err != nil {
		t.Fatal(err)
	}
	bad := append(slices.Clone(data[:start]), footer...)
	bad = binary.LittleEndian.AppendUint32(bad, uint32(len(footer)))
	bad = append(bad, "PAR1"...)
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := parquetTimeRange(openParquetFile(t, path), Logs.Time); got.Known {
		t.Fatalf("tampered footer kept bounds %+v", got)
	}
	err = ValidatePublishedBatch(dir)
	if err == nil || !IsBatchCorrupt(err) || !strings.Contains(err.Error(), "path") {
		t.Fatalf("verify: %v", err)
	}
	if _, err := loadStoredBatch(dir); err == nil {
		t.Fatal("staged validation accepted a footer with a wrong column path")
	}
}
