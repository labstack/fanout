package ai

import (
	"encoding/json"
	"fmt"
	"testing"
)

func wrapToolResult(tool, payload string) string {
	return fmt.Sprintf(`{"type":"%s","data":%s,"meta":{"source":"test"}}`, tool, payload)
}

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
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 block", len(blocks))
	}
	if blocks[0].Type != BlockBar {
		t.Errorf("block type = %q, want bar", blocks[0].Type)
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
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 block", len(blocks))
	}
	if blocks[0].Type != BlockBar {
		t.Errorf("block type = %q, want bar fallback", blocks[0].Type)
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

func TestBuildBlocksFromTraceResult_Waterfall(t *testing.T) {
	text := wrapToolResult("trace", `{
		"trace_id":"trace-1",
		"total_duration_ms":42,
		"span_count":2,
		"services":["api","db"],
		"has_error":false,
		"spans":[
			{"span_id":"root","service":"api","operation":"GET /users","start_time":"2026-04-13T12:00:00Z","duration_ms":40,"status":"STATUS_CODE_OK"},
			{"span_id":"child","parent_span_id":"root","service":"db","operation":"SELECT users","start_time":"2026-04-13T12:00:00Z","duration_ms":15,"status":"STATUS_CODE_OK"}
		],
		"logs":[]
	}`)

	blocks := buildBlocksFromTraceResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockTraceWaterfall {
		t.Fatalf("block type = %q, want trace_waterfall", blocks[0].Type)
	}
}

func TestBuildBlocksFromTraceResult_SkipsBadSpanTimestamp(t *testing.T) {
	text := wrapToolResult("trace", `{
		"trace_id":"trace-1",
		"total_duration_ms":42,
		"span_count":2,
		"services":["api","db"],
		"has_error":false,
		"spans":[
			{"span_id":"root","service":"api","operation":"GET /users","start_time":"2026-04-13T12:00:00Z","duration_ms":40,"status":"STATUS_CODE_OK"},
			{"span_id":"child","parent_span_id":"root","service":"db","operation":"SELECT users","start_time":"not-a-time","duration_ms":15,"status":"STATUS_CODE_OK"}
		],
		"logs":[]
	}`)

	blocks := buildBlocksFromTraceResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}

	var data TraceWaterfallData
	if err := json.Unmarshal(blocks[0].Data, &data); err != nil {
		t.Fatalf("unmarshal trace waterfall: %v", err)
	}
	if len(data.Spans) != 1 {
		t.Fatalf("span count = %d, want 1 valid span", len(data.Spans))
	}
	if data.Spans[0].ID != "root" {
		t.Errorf("span id = %q, want root", data.Spans[0].ID)
	}
}

func TestBuildBlocksFromCompare_TimeComparison(t *testing.T) {
	text := `{
		"mode":"time",
		"left_label":"Before",
		"right_label":"After",
		"comparison":{
			"latency":{"left_value":120,"right_value":180,"change_pct":50,"direction":"regression","statistically_significant":true},
			"errors":{"left_value":1,"right_value":3,"change_pct":200,"direction":"regression","statistically_significant":true}
		},
		"verdict":"Latency regressed."
	}`

	blocks := buildBlocksFromCompareResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockComparison {
		t.Fatalf("block type = %q, want comparison", blocks[0].Type)
	}

	var data ComparisonData
	if err := json.Unmarshal(blocks[0].Data, &data); err != nil {
		t.Fatalf("unmarshal comparison: %v", err)
	}
	if len(data.Metrics) != 2 {
		t.Fatalf("metrics count = %d, want 2", len(data.Metrics))
	}
	if data.Metrics[0].Label != "Latency" {
		t.Errorf("first metric label = %q, want Latency", data.Metrics[0].Label)
	}
}

func TestBuildBlocksFromOverview_NoHealth(t *testing.T) {
	text := wrapToolResult("overview", `{
		"timestamp":"2026-04-13T12:00:00Z",
		"window":"15m",
		"services":[{"service":"api","status":"healthy","requests":100,"error_rate":0.01,"p50_ms":5,"p95_ms":20}]
	}`)

	blocks := buildBlocksFromOverviewResult(text)
	if blocks != nil {
		t.Fatalf("got %v, want nil when health field is absent", blocks)
	}
}

func TestBuildBlocksFromOverview_WithHealth(t *testing.T) {
	text := wrapToolResult("overview", `{
		"timestamp":"2026-04-13T12:00:00Z",
		"window":"15m",
		"health":{"score":0.92,"total_services":12,"by_status":{"healthy":11,"degraded":1,"unhealthy":0},"throughput_per_min":1200,"global_error_rate":0.02,"global_p95_ms":250},
		"services":[]
	}`)

	blocks := buildBlocksFromOverviewResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockMetrics {
		t.Fatalf("block type = %q, want metrics", blocks[0].Type)
	}

	var data MetricsBlockData
	if err := json.Unmarshal(blocks[0].Data, &data); err != nil {
		t.Fatalf("unmarshal metrics block: %v", err)
	}
	if data.Items[0].Value != 92 {
		t.Errorf("health score value = %v, want 92", data.Items[0].Value)
	}
	if data.Items[0].Status != "ok" {
		t.Errorf("health score status = %q, want ok", data.Items[0].Status)
	}
}

func TestBuildBlocksFromDiagnose_EndpointsUsesOperationStats(t *testing.T) {
	text := wrapToolResult("diagnose", `{
		"service":"checkout",
		"status":"degraded",
		"window_minutes":15,
		"metrics":{"p50_ms":50,"p95_ms":1200,"p99_ms":2200,"error_rate":0.03,"request_count":450},
		"top_errors":[],
		"slow_operations":[
			{"name":"GET /orders","p50_ms":80,"p95_ms":1400,"p99_ms":2400,"error_rate":0.02,"count":150}
		],
		"dependencies":[]
	}`)

	blocks := buildBlocksFromDiagnoseResult(text)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	if blocks[1].Type != BlockEndpoints {
		t.Fatalf("block type = %q, want endpoints", blocks[1].Type)
	}

	var data EndpointsData
	if err := json.Unmarshal(blocks[1].Data, &data); err != nil {
		t.Fatalf("unmarshal endpoints block: %v", err)
	}
	if len(data.Endpoints) != 1 {
		t.Fatalf("endpoints count = %d, want 1", len(data.Endpoints))
	}
	ep := data.Endpoints[0]
	if ep.Method != "GET" {
		t.Errorf("method = %q, want GET", ep.Method)
	}
	if ep.Path != "/orders" {
		t.Errorf("path = %q, want /orders", ep.Path)
	}
	if ep.RPM != 10 {
		t.Errorf("rpm = %v, want 10", ep.RPM)
	}
	if ep.ErrorRate != 2 {
		t.Errorf("error rate = %v, want 2", ep.ErrorRate)
	}
	if ep.Status != "degraded" {
		t.Errorf("status = %q, want degraded", ep.Status)
	}
}

func TestBuildBlocksFromTopology_NormalizesUnits(t *testing.T) {
	text := wrapToolResult("topology", `{
		"window_minutes":60,
		"nodes":[{"service":"api","status":"degraded","requests":120,"p50_ms":10,"p95_ms":200,"error_rate":0.02,"upstream_count":1,"downstream_count":1,"blast_radius":1}],
		"edges":[{"source":"api","target":"db","edge_type":"call","calls":60,"avg_ms":12,"error_rate":0.01}],
		"critical_paths":[["api","db"]]
	}`)

	blocks := buildBlocksFromTopologyResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}

	var data TopologyData
	if err := json.Unmarshal(blocks[0].Data, &data); err != nil {
		t.Fatalf("unmarshal topology: %v", err)
	}
	if data.Nodes[0].RPM != 2 {
		t.Errorf("node rpm = %v, want 2", data.Nodes[0].RPM)
	}
	if data.Nodes[0].Errors != 2 {
		t.Errorf("node errors = %v, want 2", data.Nodes[0].Errors)
	}
	if data.Edges[0].RPM != 1 {
		t.Errorf("edge rpm = %v, want 1", data.Edges[0].RPM)
	}
	if data.Edges[0].ErrorRate != 1 {
		t.Errorf("edge errorRate = %v, want 1", data.Edges[0].ErrorRate)
	}
}

func TestBuildBlocksFromMetrics_SparseFallsBackToTable(t *testing.T) {
	text := wrapToolResult("metrics", `{
		"series":[
			{"metric":"http.server.duration","aggregation":"avg","unit":"ms","labels":{"service":"api"},"datapoints":[
				{"time":"2026-04-13T12:00:00Z","value":10},
				{"time":"2026-04-13T12:01:00Z","value":11}
			]},
			{"metric":"http.server.duration","aggregation":"avg","unit":"ms","labels":{"service":"worker"},"datapoints":[
				{"time":"2026-04-13T12:00:00Z","value":7}
			]}
		],
		"anomalies":[]
	}`)

	blocks := buildBlocksFromMetricsResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockTable {
		t.Fatalf("block type = %q, want table fallback", blocks[0].Type)
	}
}

func TestBuildBlocksFromMetrics_AlignedSeriesBuildsTimeseries(t *testing.T) {
	text := wrapToolResult("metrics", `{
		"series":[
			{"metric":"http.server.duration","aggregation":"avg","unit":"ms","labels":{"service":"api"},"datapoints":[
				{"time":"2026-04-13T12:00:00Z","value":10},
				{"time":"2026-04-13T12:01:00Z","value":11}
			]}
		],
		"anomalies":[]
	}`)

	blocks := buildBlocksFromMetricsResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockTimeseries {
		t.Fatalf("block type = %q, want timeseries", blocks[0].Type)
	}

	var data TimeseriesBlockData
	if err := json.Unmarshal(blocks[0].Data, &data); err != nil {
		t.Fatalf("unmarshal timeseries: %v", err)
	}
	if len(data.Series) != 1 {
		t.Fatalf("series count = %d, want 1", len(data.Series))
	}
	if data.Series[0].Label != "api" {
		t.Errorf("series label = %q, want api", data.Series[0].Label)
	}
	if len(data.Labels) != 2 {
		t.Fatalf("labels count = %d, want 2", len(data.Labels))
	}
	if data.YLabel != "ms" {
		t.Errorf("yLabel = %q, want ms", data.YLabel)
	}
}

func TestSplitMethodPath_Allowlist(t *testing.T) {
	method, path := splitMethodPath("GET /users")
	if method != "GET" || path != "/users" {
		t.Fatalf("got %q %q, want GET /users", method, path)
	}

	method, path = splitMethodPath("SELECT users")
	if method != "" || path != "SELECT users" {
		t.Fatalf("got %q %q, want empty method and original path", method, path)
	}
}

func TestBuildBlocksFromLogs_Ungrouped(t *testing.T) {
	text := wrapToolResult("logs", `{
		"logs":[
			{"time":"2026-04-13T12:00:00Z","service":"api","severity":"ERROR","body":"boom","trace_id":"trace-1"}
		]
	}`)

	blocks := buildBlocksFromLogsResult(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Type != BlockLogs {
		t.Fatalf("block type = %q, want logs", blocks[0].Type)
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
