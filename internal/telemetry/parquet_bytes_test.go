package telemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry/semconv"
	"github.com/parquet-go/parquet-go"
)

var updateFiles = flag.Bool("update-files", false, "rewrite testdata/format*.sha256")

// Writer changes made for speed must not change what a batch stores. Each
// signal's file for a fixed set of rows must match the digest recorded in
// testdata. Pages are written uncompressed, so a codec build cannot change the
// bytes; the digest covers row order, every leaf's levels and values, page
// layout and the footer.
func TestParquetBytesMatchRecordedDigest(t *testing.T) {
	spans, logs, metrics := digestRows(700)
	dir := t.TempDir()
	write := map[string]func(string) error{
		"spans":   func(path string) error { return writeDigestFile(path, Spans, spans) },
		"logs":    func(path string) error { return writeDigestFile(path, Logs, logs) },
		"metrics": func(path string) error { return writeDigestFile(path, Metrics, metrics) },
	}
	for _, s := range Signals {
		t.Run(s.Name, func(t *testing.T) {
			path := filepath.Join(dir, s.Name+".parquet")
			if err := write[s.Name](path); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			digest := hex.EncodeToString(sum[:]) + "\n"
			golden := filepath.Join("testdata", fmt.Sprintf("format%d-%s.sha256", batchMetadataVersion, s.Name))
			if *updateFiles {
				if err := os.WriteFile(golden, []byte(digest), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update-files to record it)", err)
			}
			if digest != string(want) {
				t.Fatalf("%s file digest %s, recorded %s", s.Name, strings.TrimSpace(digest), strings.TrimSpace(string(want)))
			}
		})
	}
}

// Lanes write a file's columns in parallel, each walking every row group on
// its own. Over several row groups, the file must match the one a single lane
// writes byte for byte and keep its rows in order.
func TestLanesWriteTheSingleLaneBytes(t *testing.T) {
	rows := 2*parquetRowGroupRows + parquetRowGroupRows/2 + 3
	spans, logs, metrics := digestRows(rows)
	write := map[string]func(string) error{
		"spans":   func(path string) error { return writeParquet(path, Spans, spans, parquetPageSize) },
		"logs":    func(path string) error { return writeParquet(path, Logs, logs, parquetPageSize) },
		"metrics": func(path string) error { return writeParquet(path, Metrics, metrics, parquetPageSize) },
	}
	dir := t.TempDir()
	for _, s := range Signals {
		t.Run(s.Name, func(t *testing.T) {
			files := map[int][]byte{}
			for _, procs := range []int{1, 2, maxWriteLanes} {
				path := filepath.Join(dir, fmt.Sprintf("%s-%d.parquet", s.Name, procs))
				previous := runtime.GOMAXPROCS(procs)
				err := write[s.Name](path)
				runtime.GOMAXPROCS(previous)
				if err != nil {
					t.Fatal(err)
				}
				if err := verifyOrder(path, s, rows); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				files[procs] = data
			}
			file, err := parquet.OpenFile(bytes.NewReader(files[1]), int64(len(files[1])))
			if err != nil {
				t.Fatal(err)
			}
			if groups := len(file.RowGroups()); groups != 3 {
				t.Fatalf("%d row groups, want 3", groups)
			}
			for _, procs := range []int{2, maxWriteLanes} {
				if !bytes.Equal(files[1], files[procs]) {
					t.Fatalf("%d lanes wrote a different file than one lane", procs)
				}
			}
		})
	}
}

func writeDigestFile[R any](path string, s *Signal, rows []R) error {
	return writeParquet(path, s, rows, parquetPageSize, parquet.Compression(&parquet.Uncompressed), uncompressedSchema(s))
}

// uncompressedSchema is the signal's schema without the dense columns' own
// codec (parquetDenseCodec), so that every page takes the default codec.
func uncompressedSchema(s *Signal) *parquet.Schema {
	plain := *s
	plain.Columns = slices.Clone(s.Columns)
	for i := range plain.Columns {
		plain.Columns[i].Dense = false
	}
	return buildSchema(&plain)
}

// digestRows covers the writer's paths: chunks and sparse dedicated keys,
// converted, unconverted and NULL values, shared, unique and absent resources, nested
// variants, lists, signed times and every metric layout.
func digestRows(n int) ([]Span, []Log, []Metric) {
	r := rand.New(rand.NewSource(5))
	resources := make([]map[string]any, 4)
	for i := range resources {
		resources[i] = digestMap(r, semconv.Resource, i)
	}
	resource := func() map[string]any {
		switch n := r.Intn(20); {
		case n == 0:
			return nil
		case n == 1:
			return digestMap(r, semconv.Resource, n) // unique to its row
		default:
			return resources[n%len(resources)]
		}
	}
	id := func(n int) string {
		b := make([]byte, n/2)
		r.Read(b)
		return hex.EncodeToString(b)
	}
	spans := make([]Span, n)
	trace := id(32)
	for i := range spans {
		if r.Intn(4) == 0 {
			trace = id(32)
		}
		start := int64(1_790_000_000_000_000_000) + r.Int63n(1e12)
		spans[i] = Span{Namespace: []string{"prod", "staging"}[r.Intn(2)], TraceID: trace, SpanID: id(16), ParentSpanID: id(16),
			ServiceName: fmt.Sprintf("svc-%d", r.Intn(3)), Name: "GET /items", Kind: "SPAN_KIND_SERVER", StartUnixNanos: start,
			EndUnixNanos: start + r.Int63n(1e9), DurationMS: float64(r.Intn(1000)), StatusCode: "STATUS_CODE_OK",
			Resource: resource(), Attributes: digestMap(r, semconv.Span, i), IngestedAt: 1}
		if r.Intn(3) == 0 {
			spans[i].Events = []Event{{TimeUnixNanos: start, Name: "exception", Attributes: map[string]any{"exception.type": "E", "n": int64(i)}}, {TimeUnixNanos: start + 1, Name: "retry"}}
			spans[i].Links = []Link{{TraceID: id(32), SpanID: id(16), Attributes: map[string]any{"k": true}}}
		}
	}
	logs := make([]Log, n)
	for i := range logs {
		t := int64(1_790_000_000_000_000_000) + r.Int63n(2e12)
		if i%100 == 0 {
			t = -r.Int63n(1e12) // before 1970: buckets floor toward negative infinity
		}
		logs[i] = Log{Namespace: "prod", EventUnixNanos: t, TimeUnixNanos: t, ObservedTimeNanos: t + 1, Severity: "INFO", SeverityNumber: 9,
			Body: fmt.Sprintf("order %d accepted", r.Intn(1000)), ServiceName: fmt.Sprintf("svc-%d", r.Intn(3)), TraceID: id(32), SpanID: id(16),
			Resource: resource(), Attributes: digestMap(r, semconv.Span, i), IngestedAt: 1}
	}
	metrics := make([]Metric, n)
	for i := range metrics {
		t := int64(1_790_000_000_000_000_000) + int64(i/20)*10e9
		m := Metric{Namespace: "prod", EventUnixNanos: t, TimeUnixNanos: t, StartTimeUnixNanos: t - 3600e9, Name: fmt.Sprintf("metric.%d", i%7), Unit: "ms",
			ServiceName: fmt.Sprintf("svc-%d", i%3), Resource: resource(), Attributes: digestMap(r, semconv.Span, i), IngestedAt: 1}
		switch i % 5 {
		case 0:
			m.Type, m.Value = "gauge", r.Float64()
		case 1:
			m.Type, m.Temporality, m.Value = "counter", "cumulative", float64(i)
			m.Exemplars = []Exemplar{{TimeUnixNanos: t, Value: 1, TraceID: id(32), SpanID: id(16), Attributes: map[string]any{"a": "b"}}}
		case 2:
			m.Type, m.Temporality = "histogram", "delta"
			m.Bounds, m.Counts = []float64{0, 5, 10}, []uint64{1, 2, 3, uint64(i)}
			m.Count, m.Sum, m.HasSum = uint64(6+i), float64(i), i%2 == 0
		case 3:
			m.Type, m.Temporality = "histogram", "cumulative"
			m.Exponential, m.Scale, m.ZeroCount, m.PositiveOffset, m.PositiveCounts = true, 3, 1, -2, []uint64{1, 0, 4}
			m.NegativeCounts = []uint64{}
			m.Count, m.Min, m.Max, m.HasMin, m.HasMax = 6, -1, 9, true, true
		default:
			m.Type, m.Count, m.Sum, m.HasSum = "summary", 3, 9, true
			m.Quantiles, m.QuantileValues = []float64{0.5, 0.99}, []float64{2, 7}
		}
		m.SeriesID = MetricSeriesID(m)
		metrics[i] = m
	}
	return spans, logs, metrics
}

// digestMap holds a sparse sample of the scope's dedicated keys, most with
// their semconv type, some with another type or NULL, and keys that have no
// column. A few maps are empty or absent.
func digestMap(r *rand.Rand, scope semconv.Scope, i int) map[string]any {
	switch i % 31 {
	case 7:
		return nil
	case 8:
		return map[string]any{}
	}
	m := map[string]any{"user.id": fmt.Sprintf("u-%d", r.Intn(50))}
	if i%9 == 0 {
		m["nested"] = map[string]any{"b": []any{int64(1), "two", 3.5}, "a": map[string]any{"z": nil, "y": false}}
	}
	for _, key := range semconv.Keys {
		if key.Scope != scope || r.Intn(8) != 0 {
			continue
		}
		switch n := r.Intn(20); {
		case n == 0:
			m[key.Name] = nil
		case n == 1:
			m[key.Name] = true // text on string keys, a spill on integer keys
		case n == 2 && key.Type == semconv.Int:
			m[key.Name] = fmt.Sprint(r.Intn(600)) // an integer sent as text
		case n == 3:
			m[key.Name] = float64(r.Intn(4)) / 2 // a spill on integer keys when not integral
		case key.Type == semconv.Int:
			m[key.Name] = int64(r.Intn(600))
		default:
			m[key.Name] = fmt.Sprintf("%s-%d", key.Name, r.Intn(5))
		}
	}
	return m
}
