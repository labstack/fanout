package telemetry

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestMetricSeriesEncodingCanonicalizesMapOrder(t *testing.T) {
	a := Metric{Attributes: map[string]any{"z": int64(1), "a": map[string]any{"y": true, "x": "v"}}}
	b := Metric{Attributes: map[string]any{"a": map[string]any{"x": "v", "y": true}, "z": int64(1)}}
	if !bytes.Equal(metricSeriesEncoding(a), metricSeriesEncoding(b)) || MetricSeriesID(a) != MetricSeriesID(b) {
		t.Fatal("map insertion order changed the identity")
	}
	a.Attributes, b.Attributes = nil, map[string]any{}
	if MetricSeriesID(a) != MetricSeriesID(b) {
		t.Fatal("absent and empty attributes differ")
	}
}

func TestMetricSeriesIDSurvivesParquetCompaction(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inputs := []string{}
	want := []uint64{}
	for i := range 2 {
		m := Metric{Namespace: "test", Name: "net", Type: "gauge", TimeUnixNanos: int64(i + 1), Attributes: map[string]any{"nested": []any{map[string]any{"id": int64(i)}}}}
		m.SeriesID = MetricSeriesID(m)
		id := fmt.Sprintf("series%d", i)
		if err := store.CommitBatch(t.Context(), BatchMetadata{ID: id}, nil, nil, []Metric{m}); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePublishedBatch(store.BatchPath(id)); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, filepath.Join(store.BatchPath(id), "metrics.parquet"))
		want = append(want, m.SeriesID)
	}
	output := filepath.Join(t.TempDir(), "metrics.parquet")
	if _, err := store.MergeParquet(t.Context(), "metrics", inputs, output); err != nil {
		t.Fatal(err)
	}
	rows, err := parquet.ReadFile[struct {
		SeriesID uint64 `parquet:"series_id"`
	}](output)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(want) {
		t.Fatalf("compacted rows=%d", len(rows))
	}
	for i, row := range rows {
		if row.SeriesID != want[i] {
			t.Fatalf("compacted series id=%d want %d", row.SeriesID, want[i])
		}
	}
}

func TestMetricSeriesEncodingSeparatesTypedAndNestedValues(t *testing.T) {
	values := []any{"1", true, int64(1), float64(1), []byte("1"), []any{int64(1)}, map[string]any{"x": int64(1)}, nil,
		map[string]any{"x": []any{map[string]any{"y": int64(1)}}},
		map[string]any{"x": []any{map[string]any{"y": int64(2)}}}}
	seen := map[string]bool{}
	hashes := map[uint64]bool{}
	for _, value := range values {
		m := Metric{Attributes: map[string]any{"key": value}}
		encoded, hash := string(metricSeriesEncoding(m)), MetricSeriesID(m)
		if seen[encoded] || hashes[hash] {
			t.Fatalf("identity conflated %T: %v", value, value)
		}
		seen[encoded], hashes[hash] = true, true
	}
}

func TestMetricSeriesEncodingSeparatesFieldAndContainerBoundaries(t *testing.T) {
	for _, pair := range [][2]Metric{
		{{Namespace: "ab", ServiceName: "c"}, {Namespace: "a", ServiceName: "bc"}},
		{{Namespace: "a\x00", ServiceName: "b"}, {Namespace: "a", ServiceName: "\x00b"}},
		{{Attributes: map[string]any{"a": "bc"}}, {Attributes: map[string]any{"ab": "c"}}},
		{{Attributes: map[string]any{"a": []any{"b", "c"}}}, {Attributes: map[string]any{"a": []any{"bc"}}}},
		{{Attributes: map[string]any{"x": true}}, {Resource: map[string]any{"x": true}}},
	} {
		if bytes.Equal(metricSeriesEncoding(pair[0]), metricSeriesEncoding(pair[1])) || MetricSeriesID(pair[0]) == MetricSeriesID(pair[1]) {
			t.Fatalf("field boundaries conflated: %+v", pair)
		}
	}
}
