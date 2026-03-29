package ai

import (
	"encoding/json"
	"testing"
)

func TestSuggestBlocksFromRows_Heatmap(t *testing.T) {
	rows := []map[string]any{
		{"time_bucket": "2026-03-27T10:00:00", "latency_bucket": "0-10ms", "count": float64(100)},
		{"time_bucket": "2026-03-27T10:00:00", "latency_bucket": "10-50ms", "count": float64(80)},
		{"time_bucket": "2026-03-27T10:00:00", "latency_bucket": "50-100ms", "count": float64(30)},
		{"time_bucket": "2026-03-27T10:05:00", "latency_bucket": "0-10ms", "count": float64(120)},
		{"time_bucket": "2026-03-27T10:05:00", "latency_bucket": "10-50ms", "count": float64(70)},
		{"time_bucket": "2026-03-27T10:05:00", "latency_bucket": "50-100ms", "count": float64(25)},
	}

	blocks := suggestBlocksFromRows(rows)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockHeatmap {
		t.Fatalf("block type = %q, want heatmap", blocks[0].Type)
	}

	var data HeatmapBlockData
	if err := json.Unmarshal(blocks[0].Data, &data); err != nil {
		t.Fatalf("unmarshal heatmap data: %v", err)
	}
	if len(data.Times) != 2 {
		t.Errorf("times = %d, want 2", len(data.Times))
	}
	if len(data.Buckets) != 3 {
		t.Errorf("buckets = %d, want 3", len(data.Buckets))
	}
	if len(data.Values) != 2 {
		t.Errorf("values rows = %d, want 2", len(data.Values))
	}
	if len(data.Values[0]) != 3 {
		t.Errorf("values cols = %d, want 3", len(data.Values[0]))
	}
	// Check specific values
	if data.Values[0][0] != 100 {
		t.Errorf("values[0][0] = %f, want 100", data.Values[0][0])
	}
	if data.Values[1][2] != 25 {
		t.Errorf("values[1][2] = %f, want 25", data.Values[1][2])
	}
}

func TestSuggestBlocksFromRows_NoMatch(t *testing.T) {
	// Non-heatmap data: no bucket column
	rows := []map[string]any{
		{"service": "api", "error_rate": float64(0.05)},
		{"service": "web", "error_rate": float64(0.02)},
	}
	blocks := suggestBlocksFromRows(rows)
	if len(blocks) != 0 {
		t.Errorf("got %d blocks, want 0 for non-heatmap data", len(blocks))
	}
}

func TestSuggestBlocksFromRows_Empty(t *testing.T) {
	blocks := suggestBlocksFromRows(nil)
	if blocks != nil {
		t.Errorf("got %v, want nil for empty rows", blocks)
	}
}

func TestSuggestBlocksFromRows_TooFewTimeBuckets(t *testing.T) {
	// Only one time point — not enough for a heatmap
	rows := []map[string]any{
		{"time_bucket": "2026-03-27T10:00:00", "latency_bucket": "0-10ms", "count": float64(100)},
		{"time_bucket": "2026-03-27T10:00:00", "latency_bucket": "10-50ms", "count": float64(80)},
	}
	blocks := suggestBlocksFromRows(rows)
	if len(blocks) != 0 {
		t.Errorf("got %d blocks, want 0 for single time point", len(blocks))
	}
}

func TestSuggestBlocksFromQueryText_ValidEnvelope(t *testing.T) {
	envelope := `{"type":"query","data":{"results":[
		{"time_bucket":"2026-03-27T10:00:00","latency_bucket":"0-10ms","count":100},
		{"time_bucket":"2026-03-27T10:00:00","latency_bucket":"10-50ms","count":80},
		{"time_bucket":"2026-03-27T10:05:00","latency_bucket":"0-10ms","count":120},
		{"time_bucket":"2026-03-27T10:05:00","latency_bucket":"10-50ms","count":70}
	],"row_count":4},"meta":{"exec_time_ms":15}}`

	blocks := suggestBlocksFromQueryText(envelope)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockHeatmap {
		t.Errorf("block type = %q, want heatmap", blocks[0].Type)
	}
}

func TestSuggestBlocksFromQueryText_InvalidJSON(t *testing.T) {
	blocks := suggestBlocksFromQueryText("not json")
	if blocks != nil {
		t.Errorf("got %v, want nil for invalid JSON", blocks)
	}
}

func TestSuggestBlocksFromQueryText_NoResults(t *testing.T) {
	blocks := suggestBlocksFromQueryText(`{"type":"query","data":{"results":[],"row_count":0}}`)
	if blocks != nil {
		t.Errorf("got %v, want nil for empty results", blocks)
	}
}

func TestParseBucketBoundary(t *testing.T) {
	tests := []struct {
		label string
		want  float64
		ok    bool
	}{
		{"0-10ms", 10, true},
		{"10-50ms", 50, true},
		{"50-100ms", 100, true},
		{"100-500ms", 500, true},
		{"500-1000ms", 1000, true},
		{">1s", 5000, true},    // 1s = 1000ms, overflow scaled x5
		{">5s", 25000, true},   // 5s = 5000ms, overflow scaled x5
		{">500ms", 2500, true}, // 500ms, overflow scaled x5
		{"100", 100, true},     // plain number
		{"unknown", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got, ok := parseBucketBoundary(tt.label)
			if ok != tt.ok {
				t.Errorf("parseBucketBoundary(%q) ok = %v, want %v", tt.label, ok, tt.ok)
				return
			}
			if ok && got != tt.want {
				t.Errorf("parseBucketBoundary(%q) = %v, want %v", tt.label, got, tt.want)
			}
		})
	}
}

func TestShortenTimestamp(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"2026-03-27T10:05:00", "10:05"},
		{"2026-03-27 10:05:00", "10:05"},
		{"10:05", "10:05"},
		{"short", "short"},
	}
	for _, tt := range tests {
		got := shortenTimestamp(tt.input)
		if got != tt.want {
			t.Errorf("shortenTimestamp(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDetectHeatmapCols_VariousColumnNames(t *testing.T) {
	// Test with "bucket" as time column and "range" as bucket column
	rows := []map[string]any{
		{"bucket": "2026-03-27T10:00:00", "range": "0-10ms", "cnt": float64(10)},
	}
	tc, bc, vc := detectHeatmapCols(columnNames(rows), rows)
	if tc != "bucket" {
		t.Errorf("timeCol = %q, want bucket", tc)
	}
	if bc != "range" {
		t.Errorf("bucketCol = %q, want range", bc)
	}
	if vc != "cnt" {
		t.Errorf("valueCol = %q, want cnt", vc)
	}
}

func TestDetectHeatmapCols_NoMatch(t *testing.T) {
	rows := []map[string]any{
		{"service": "api", "p95": float64(42)},
	}
	tc, bc, vc := detectHeatmapCols(columnNames(rows), rows)
	if tc != "" || bc != "" || vc != "" {
		t.Errorf("expected no match, got timeCol=%q bucketCol=%q valueCol=%q", tc, bc, vc)
	}
}

func TestColumnNames_Sorted(t *testing.T) {
	rows := []map[string]any{
		{"z": 1, "a": 2, "m": 3},
	}
	cols := columnNames(rows)
	if len(cols) != 3 || cols[0] != "a" || cols[1] != "m" || cols[2] != "z" {
		t.Errorf("columnNames = %v, want [a m z]", cols)
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want float64
	}{
		{"float64", float64(42.5), 42.5},
		{"int64", int64(10), 10},
		{"int", int(5), 5},
		{"string", "abc", 0},
		{"nil", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat64(tt.v)
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %f, want %f", tt.v, got, tt.want)
			}
		})
	}
}
