package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
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
	spans, logs, metrics := digestRows()
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

func writeDigestFile[R any](path string, s *Signal, rows []R) error {
	return writeParquet(path, s, rows, parquetPageSize, parquet.Compression(&parquet.Uncompressed))
}

// digestRows covers the writer's paths: chunks and sparse dedicated keys,
// spilled and NULL values, shared, unique and absent resources, nested
// variants, lists, signed times and every metric layout.
func digestRows() ([]Span, []Log, []Metric) {
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
	const n = 700
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
// column.
func digestMap(r *rand.Rand, scope semconv.Scope, i int) map[string]any {
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
			m[key.Name] = true // wrong type: spills
		case key.Type == semconv.Int:
			m[key.Name] = int64(r.Intn(600))
		default:
			m[key.Name] = fmt.Sprintf("%s-%d", key.Name, r.Intn(5))
		}
	}
	return m
}
