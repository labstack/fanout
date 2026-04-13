package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	mcpout "github.com/labstack/fanout/internal/mcp"
)

func buildBlocksFromToolResult(name, text string) []Block {
	switch name {
	case "trace":
		return buildBlocksFromTraceResult(text)
	case "logs":
		return buildBlocksFromLogsResult(text)
	case "compare":
		return buildBlocksFromCompareResult(text)
	case "overview":
		return buildBlocksFromOverviewResult(text)
	case "diagnose":
		return buildBlocksFromDiagnoseResult(text)
	case "spans":
		return buildBlocksFromSpansResult(text)
	case "metrics":
		return buildBlocksFromMetricsResult(text)
	case "topology":
		return buildBlocksFromTopologyResult(text)
	case "query":
		return suggestBlocksFromQueryText(text)
	default:
		slog.Debug("no block builder for tool", "tool", name)
		return nil
	}
}

func buildBlocksFromTraceResult(text string) []Block {
	var out mcpout.TraceOut
	if !unmarshalToolResult("trace", text, &out) || len(out.Spans) == 0 {
		return nil
	}

	spans := make([]TraceSpan, 0, len(out.Spans))
	var (
		base    time.Time
		baseSet bool
	)
	for _, sp := range out.Spans {
		start, ok := parseTimestamp(sp.StartTime)
		if !ok {
			slog.Warn("skipping trace span with invalid start time",
				"trace_id", out.TraceID,
				"span_id", sp.SpanID,
				"start_time", sp.StartTime)
			continue
		}
		if !baseSet {
			base = start
			baseSet = true
		}
		var parent *string
		if sp.ParentSpanID != "" {
			parent = &sp.ParentSpanID
		}
		spans = append(spans, TraceSpan{
			ID:        sp.SpanID,
			Parent:    parent,
			Service:   sp.Service,
			Operation: sp.Operation,
			Start:     start.Sub(base).Seconds() * 1000,
			Duration:  sp.DurationMs,
			Status:    normalizeStatus(sp.Status),
		})
	}
	if !baseSet || len(spans) == 0 {
		return nil
	}

	return []Block{NewBlock(BlockTraceWaterfall, TraceWaterfallData{Spans: spans})}
}

func buildBlocksFromLogsResult(text string) []Block {
	var out mcpout.LogsOut
	if !unmarshalToolResult("logs", text, &out) {
		return nil
	}

	if len(out.Logs) > 0 {
		entries := make([]LogEntry, 0, len(out.Logs))
		for _, row := range out.Logs {
			entries = append(entries, LogEntry{
				Time:     row.Time,
				Severity: row.Severity,
				Service:  row.Service,
				Body:     row.Body,
				TraceID:  row.TraceID,
			})
		}
		return []Block{NewBlock(BlockLogs, LogsBlockData{Entries: entries})}
	}

	if len(out.Groups) > 0 {
		bars := make([]BarItem, 0, len(out.Groups))
		for _, group := range out.Groups {
			bars = append(bars, BarItem{
				Label: formatGroupLabel(group.Key),
				Value: float64(group.Count),
			})
		}
		return []Block{NewBlock(BlockBar, BarBlockData{
			Title:      "Log Volume",
			Bars:       bars,
			YLabel:     "count",
			Horizontal: true,
		})}
	}

	return nil
}

func buildBlocksFromCompareResult(text string) []Block {
	var out mcpout.CompareOut
	if !unmarshalToolResult("compare", text, &out) {
		return nil
	}

	if len(out.Comparison) > 0 {
		metrics := make([]CompareMetric, 0, len(out.Comparison))
		for _, key := range orderedComparisonKeys(out.Comparison) {
			diff := out.Comparison[key]
			metrics = append(metrics, CompareMetric{
				Label:       comparisonMetricLabel(key),
				LeftValue:   diff.LeftValue,
				RightValue:  diff.RightValue,
				ChangePct:   diff.ChangePct,
				Direction:   diff.Direction,
				Significant: diff.StatisticallySignificant,
				Unit:        comparisonMetricUnit(key),
			})
		}
		return []Block{NewBlock(BlockComparison, ComparisonData{
			Mode:       out.Mode,
			LeftLabel:  out.LeftLabel,
			RightLabel: out.RightLabel,
			Metrics:    metrics,
			Verdict:    out.Verdict,
		})}
	}

	if len(out.Services) > 0 {
		rows := make([]map[string]any, 0, len(out.Services))
		for _, svc := range out.Services {
			rows = append(rows, map[string]any{
				"service":        svc.Service,
				"requests":       svc.Requests,
				"error_rate_pct": svc.ErrorRate * 100,
				"p50_ms":         svc.P50Ms,
				"p95_ms":         svc.P95Ms,
				"avg_ms":         svc.AvgMs,
				"error_count":    svc.ErrorCount,
			})
		}
		return []Block{MakeTableBlock([]TableColumn{
			{Key: "service", Label: "Service"},
			{Key: "requests", Label: "Requests", Align: "right"},
			{Key: "error_rate_pct", Label: "Error %", Align: "right"},
			{Key: "p50_ms", Label: "P50 ms", Align: "right"},
			{Key: "p95_ms", Label: "P95 ms", Align: "right"},
			{Key: "avg_ms", Label: "Avg ms", Align: "right"},
			{Key: "error_count", Label: "Errors", Align: "right"},
		}, rows)}
	}

	return nil
}

func buildBlocksFromOverviewResult(text string) []Block {
	var raw map[string]json.RawMessage
	if !unmarshalToolResult("overview", text, &raw) {
		return nil
	}
	if _, ok := raw["health"]; !ok {
		return nil
	}

	var out mcpout.OverviewOut
	if !unmarshalToolResult("overview", text, &out) || out.Health == nil {
		return nil
	}

	health := out.Health
	scorePct := health.Score * 100
	serviceStatus := "ok"
	if health.ByStatus["unhealthy"] > 0 {
		serviceStatus = "danger"
	} else if health.ByStatus["degraded"] > 0 {
		serviceStatus = "warning"
	}

	items := []MetricItem{
		{Label: "Health Score", Value: scorePct, Unit: "%", Status: healthScoreStatus(health.Score)},
		{Label: "Services", Value: float64(health.TotalServices), Unit: "", Status: serviceStatus},
		{Label: "Throughput", Value: health.ThroughputPerMin, Unit: "rpm", Status: "ok"},
		{Label: "Error Rate", Value: health.GlobalErrorRate * 100, Unit: "%", Status: errorRateStatus(health.GlobalErrorRate * 100)},
		{Label: "Global P95", Value: health.GlobalP95Ms, Unit: "ms", Status: latencyStatus(health.GlobalP95Ms)},
	}

	return []Block{MakeMetricsBlock(items)}
}

func buildBlocksFromDiagnoseResult(text string) []Block {
	var out mcpout.DiagnoseOut
	if !unmarshalToolResult("diagnose", text, &out) {
		return nil
	}

	var blocks []Block
	blocks = append(blocks, MakeMetricsBlock([]MetricItem{
		{Label: "P95", Value: out.Metrics.P95Ms, Unit: "ms", Status: latencyStatus(out.Metrics.P95Ms)},
		{Label: "P99", Value: out.Metrics.P99Ms, Unit: "ms", Status: latencyStatus(out.Metrics.P99Ms)},
		{Label: "Error Rate", Value: out.Metrics.ErrorRate * 100, Unit: "%", Status: errorRateStatus(out.Metrics.ErrorRate * 100)},
		{Label: "Requests", Value: float64(out.Metrics.Count), Unit: "", Status: throughputStatus(out.Metrics.Count)},
	}))

	if out.WindowMinutes > 0 && len(out.SlowOperations) > 0 {
		endpoints := make([]EndpointInfo, 0, len(out.SlowOperations))
		for _, op := range out.SlowOperations {
			method, path := splitMethodPath(op.Name)
			if path == "" {
				path = op.Name
			}
			endpoints = append(endpoints, EndpointInfo{
				Method:    method,
				Path:      path,
				RPM:       float64(op.Count) / float64(out.WindowMinutes),
				P50:       op.P50Ms,
				P95:       op.P95Ms,
				P99:       op.P99Ms,
				ErrorRate: op.ErrorRate * 100,
				Status:    deriveEndpointStatus(op.P95Ms, op.ErrorRate*100),
			})
		}
		blocks = append(blocks, NewBlock(BlockEndpoints, EndpointsData{Endpoints: endpoints}))
	}

	return blocks
}

func buildBlocksFromSpansResult(text string) []Block {
	var out mcpout.SpansOut
	if !unmarshalToolResult("spans", text, &out) {
		return nil
	}

	if len(out.Groups) > 0 {
		bars := make([]BarItem, 0, len(out.Groups))
		for _, group := range out.Groups {
			bars = append(bars, BarItem{
				Label: formatGroupLabel(group.Key),
				Value: float64(group.Count),
			})
		}
		return []Block{NewBlock(BlockBar, BarBlockData{
			Title:      "Span Groups",
			Bars:       bars,
			YLabel:     "count",
			Horizontal: true,
		})}
	}

	if len(out.Spans) > 0 {
		rows := make([]map[string]any, 0, len(out.Spans))
		for _, sp := range out.Spans {
			rows = append(rows, map[string]any{
				"trace_id":    sp.TraceID,
				"service":     sp.Service,
				"operation":   sp.Operation,
				"kind":        sp.Kind,
				"duration_ms": sp.DurationMs,
				"status":      normalizeStatus(sp.Status),
				"start_time":  sp.StartTime,
			})
		}
		return []Block{MakeTableBlock([]TableColumn{
			{Key: "trace_id", Label: "Trace ID"},
			{Key: "service", Label: "Service"},
			{Key: "operation", Label: "Operation"},
			{Key: "kind", Label: "Kind"},
			{Key: "duration_ms", Label: "Duration ms", Align: "right"},
			{Key: "status", Label: "Status"},
			{Key: "start_time", Label: "Start Time"},
		}, rows)}
	}

	return nil
}

func buildBlocksFromMetricsResult(text string) []Block {
	var raw map[string]json.RawMessage
	if !unmarshalToolResult("metrics", text, &raw) {
		return nil
	}

	if seriesRaw, ok := raw["series"]; ok {
		var out mcpout.MetricsQueryOut
		if !unmarshalToolResult("metrics", text, &out) {
			return nil
		}
		if block := buildMetricsTimeseriesBlock(out.Series); block != nil {
			return []Block{*block}
		}
		if rows := buildMetricsQueryRows(out.Series); len(rows) > 0 {
			return []Block{MakeTableBlock([]TableColumn{
				{Key: "metric", Label: "Metric"},
				{Key: "series", Label: "Series"},
				{Key: "time", Label: "Time"},
				{Key: "value", Label: "Value", Align: "right"},
				{Key: "unit", Label: "Unit"},
			}, rows)}
		}
		_ = seriesRaw
		return nil
	}

	if _, ok := raw["metrics"]; ok {
		var out mcpout.MetricsListOut
		if !unmarshalToolResult("metrics", text, &out) || len(out.Metrics) == 0 {
			return nil
		}
		rows := make([]map[string]any, 0, len(out.Metrics))
		for _, metric := range out.Metrics {
			rows = append(rows, map[string]any{
				"name":        metric.Name,
				"type":        metric.Type,
				"unit":        metric.Unit,
				"services":    strings.Join(metric.Services, ", "),
				"description": metric.Description,
			})
		}
		return []Block{MakeTableBlock([]TableColumn{
			{Key: "name", Label: "Metric"},
			{Key: "type", Label: "Type"},
			{Key: "unit", Label: "Unit"},
			{Key: "services", Label: "Services"},
			{Key: "description", Label: "Description"},
		}, rows)}
	}

	if block := buildGenericArrayTableBlock(raw, "histograms", "Histograms"); block != nil {
		return []Block{*block}
	}
	if block := buildGenericArrayTableBlock(raw, "exemplars", "Exemplars"); block != nil {
		return []Block{*block}
	}

	return nil
}

func buildBlocksFromTopologyResult(text string) []Block {
	var out mcpout.TopologyOut
	if !unmarshalToolResult("topology", text, &out) || len(out.Nodes) == 0 || out.WindowMinutes <= 0 {
		return nil
	}

	minutes := float64(out.WindowMinutes)
	nodes := make([]TopologyNode, 0, len(out.Nodes))
	for _, node := range out.Nodes {
		nodes = append(nodes, TopologyNode{
			ID:     node.Service,
			Status: node.Status,
			RPM:    float64(node.Requests) / minutes,
			P95:    node.P95Ms,
			Errors: node.ErrorRate * 100,
		})
	}

	edges := make([]TopologyEdge, 0, len(out.Edges))
	for _, edge := range out.Edges {
		edges = append(edges, TopologyEdge{
			Source:    edge.Source,
			Target:    edge.Target,
			RPM:       float64(edge.Calls) / minutes,
			ErrorRate: edge.ErrorRate * 100,
		})
	}

	return []Block{NewBlock(BlockTopology, TopologyData{Nodes: nodes, Edges: edges})}
}

// suggestBlocksFromQueryText parses a query tool's JSON result envelope
// and suggests visualization blocks based on the result shape.
// Returns nil if no blocks are appropriate.
func suggestBlocksFromQueryText(text string) []Block {
	// Parse the query result envelope: {"type":"query","data":{"results":[...],...},...}
	var envelope struct {
		Type string `json:"type"`
		Data struct {
			Results []map[string]any `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		slog.Warn("failed to parse query tool result for block builder", "tool", "query", "err", err)
		return nil
	}
	return suggestBlocksFromRows(envelope.Data.Results)
}

// suggestBlocksFromRows analyzes query result rows and builds appropriate
// visualization blocks. Currently detects heatmap patterns (time x bucket x count).
func suggestBlocksFromRows(results []map[string]any) []Block {
	if len(results) == 0 {
		return nil
	}

	cols := columnNames(results)

	// Heatmap: time-like + bucket/category + count columns
	if tc, bc, vc := detectHeatmapCols(cols, results); tc != "" && bc != "" && vc != "" {
		if b := buildHeatmapBlock(results, tc, bc, vc); b != nil {
			return []Block{*b}
		}
	}

	if tc, sc, vc := detectTimeseriesCols(cols, results); tc != "" && vc != "" {
		if b := buildTimeseriesBlock(results, tc, sc, vc); b != nil {
			return []Block{*b}
		}
	}

	if lc, vc := detectBarCols(cols, results); lc != "" && vc != "" {
		if b := buildBarBlock(results, lc, vc); b != nil {
			return []Block{*b}
		}
	}

	return []Block{buildQueryTableBlock(results)}
}

// columnNames returns sorted column names from the first result row.
func columnNames(results []map[string]any) []string {
	if len(results) == 0 {
		return nil
	}
	cols := make([]string, 0, len(results[0]))
	for k := range results[0] {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

// detectHeatmapCols looks for time, bucket, and count columns in query results.
// Returns empty strings if the pattern doesn't match.
func detectHeatmapCols(cols []string, results []map[string]any) (timeCol, bucketCol, valueCol string) {
	for _, c := range cols {
		cl := strings.ToLower(c)
		if timeCol == "" && isTimeLikeCol(cl) && isTimeLikeValues(results, c) {
			timeCol = c
			continue
		}
		if bucketCol == "" && isBucketLikeCol(cl) && isStringCol(results, c) && !isTimeLikeValues(results, c) {
			bucketCol = c
			continue
		}
		if valueCol == "" && isCountLikeCol(cl) && isNumericCol(results, c) {
			valueCol = c
		}
	}
	return
}

func detectTimeseriesCols(cols []string, results []map[string]any) (timeCol, seriesCol, valueCol string) {
	for _, c := range cols {
		cl := strings.ToLower(c)
		if timeCol == "" && isTimeLikeCol(cl) && isTimeLikeValues(results, c) {
			timeCol = c
			continue
		}
		if valueCol == "" && isNumericCol(results, c) {
			valueCol = c
			continue
		}
		if seriesCol == "" && isStringCol(results, c) && !isTimeLikeValues(results, c) {
			seriesCol = c
		}
	}
	return
}

func detectBarCols(cols []string, results []map[string]any) (labelCol, valueCol string) {
	for _, c := range cols {
		if labelCol == "" && isStringCol(results, c) && !isTimeLikeValues(results, c) {
			labelCol = c
			continue
		}
		if valueCol == "" && isNumericCol(results, c) {
			valueCol = c
		}
	}
	return
}

func isTimeLikeCol(cl string) bool {
	return cl == "bucket" || cl == "time" || cl == "time_bucket" ||
		cl == "ts" || cl == "timestamp" ||
		strings.Contains(cl, "time") || strings.HasSuffix(cl, "_bucket")
}

func isBucketLikeCol(cl string) bool {
	return cl == "latency_bucket" || cl == "bucket_label" ||
		cl == "duration_bucket" || cl == "range" ||
		strings.Contains(cl, "bucket") || strings.Contains(cl, "range") ||
		strings.Contains(cl, "band")
}

func isCountLikeCol(cl string) bool {
	return cl == "count" || cl == "cnt" || cl == "value" || cl == "total" || cl == "n"
}

func isTimeLikeValues(results []map[string]any, col string) bool {
	if len(results) == 0 {
		return false
	}
	s, ok := results[0][col].(string)
	if !ok {
		return false
	}
	// ISO timestamps: "2026-03-27T10:00:00" or "2026-03-27 10:00:00"
	return len(s) >= 10 && ((len(s) >= 11 && s[10] == 'T') || (s[4] == '-' && s[7] == '-'))
}

func isStringCol(results []map[string]any, col string) bool {
	if len(results) == 0 {
		return false
	}
	_, ok := results[0][col].(string)
	return ok
}

func isNumericCol(results []map[string]any, col string) bool {
	if len(results) == 0 {
		return false
	}
	switch results[0][col].(type) {
	case float64, int64, int, json.Number:
		return true
	}
	return false
}

func buildTimeseriesBlock(results []map[string]any, timeCol, seriesCol, valueCol string) *Block {
	if len(results) < 2 {
		return nil
	}

	type point struct {
		series string
		time   string
		value  float64
	}

	var (
		points       []point
		times        []string
		timeIndex    = map[string]int{}
		seriesLabels []string
		seriesIndex  = map[string]int{}
	)

	for _, row := range results {
		t := fmt.Sprint(row[timeCol])
		s := "value"
		if seriesCol != "" {
			s = fmt.Sprint(row[seriesCol])
		}
		if _, ok := timeIndex[t]; !ok {
			timeIndex[t] = len(times)
			times = append(times, t)
		}
		if _, ok := seriesIndex[s]; !ok {
			seriesIndex[s] = len(seriesLabels)
			seriesLabels = append(seriesLabels, s)
		}
		points = append(points, point{series: s, time: t, value: toFloat64(row[valueCol])})
	}

	if len(times) < 2 {
		return nil
	}

	grid := make([][]float64, len(seriesLabels))
	filled := make([][]bool, len(seriesLabels))
	for i := range grid {
		grid[i] = make([]float64, len(times))
		filled[i] = make([]bool, len(times))
	}

	for _, p := range points {
		si := seriesIndex[p.series]
		ti := timeIndex[p.time]
		if filled[si][ti] {
			return nil
		}
		grid[si][ti] = p.value
		filled[si][ti] = true
	}

	for i := range filled {
		for j := range filled[i] {
			if !filled[i][j] {
				return nil
			}
		}
	}

	labels := make([]string, len(times))
	for i, t := range times {
		labels[i] = shortenTimestamp(t)
	}

	series := make([]TimeseriesSeries, 0, len(seriesLabels))
	for i, label := range seriesLabels {
		series = append(series, TimeseriesSeries{
			Label:  label,
			Values: grid[i],
		})
	}

	block := NewBlock(BlockTimeseries, TimeseriesBlockData{
		Title:  "Time Series",
		Series: series,
		Labels: labels,
		YLabel: valueCol,
	})
	return &block
}

func buildBarBlock(results []map[string]any, labelCol, valueCol string) *Block {
	if len(results) < 2 {
		return nil
	}

	bars := make([]BarItem, 0, len(results))
	for _, row := range results {
		bars = append(bars, BarItem{
			Label: fmt.Sprint(row[labelCol]),
			Value: toFloat64(row[valueCol]),
		})
	}

	block := NewBlock(BlockBar, BarBlockData{
		Title:      humanizeColumn(valueCol),
		Bars:       bars,
		YLabel:     valueCol,
		Horizontal: len(results) > 6,
	})
	return &block
}

func buildQueryTableBlock(results []map[string]any) Block {
	cols := columnNames(results)
	columns := make([]TableColumn, 0, len(cols))
	for _, col := range cols {
		align := ""
		if isNumericCol(results, col) {
			align = "right"
		}
		columns = append(columns, TableColumn{
			Key:   col,
			Label: humanizeColumn(col),
			Align: align,
		})
	}
	return MakeTableBlock(columns, results)
}

// buildHeatmapBlock pivots flat rows (time, bucket, value) into a 2D heatmap block.
func buildHeatmapBlock(results []map[string]any, timeCol, bucketCol, valueCol string) *Block {
	// Collect unique times and buckets preserving insertion order
	timeSet := map[string]int{}
	bucketSet := map[string]int{}
	var times []string
	var bucketLabels []string

	for _, row := range results {
		t := fmt.Sprint(row[timeCol])
		b := fmt.Sprint(row[bucketCol])
		if _, ok := timeSet[t]; !ok {
			timeSet[t] = len(times)
			times = append(times, t)
		}
		if _, ok := bucketSet[b]; !ok {
			bucketSet[b] = len(bucketLabels)
			bucketLabels = append(bucketLabels, b)
		}
	}

	if len(times) < 2 || len(bucketLabels) < 2 {
		return nil // Not enough data for a meaningful heatmap
	}

	// Build 2D values array: values[timeIdx][bucketIdx]
	values := make([][]float64, len(times))
	for i := range values {
		values[i] = make([]float64, len(bucketLabels))
	}

	for _, row := range results {
		t := fmt.Sprint(row[timeCol])
		b := fmt.Sprint(row[bucketCol])
		ti := timeSet[t]
		bi := bucketSet[b]
		values[ti][bi] = toFloat64(row[valueCol])
	}

	// Convert bucket labels to numeric values for the schema (buckets is []float64).
	// Try to parse boundaries from labels like "0-10ms", "10-50ms", ">1s".
	// Fall back to sequential indices if parsing fails.
	bucketNums := make([]float64, len(bucketLabels))
	parsed := 0
	for i, label := range bucketLabels {
		if v, ok := parseBucketBoundary(label); ok {
			bucketNums[i] = v
			parsed++
		}
	}
	if parsed < len(bucketLabels)/2 {
		// Parsing didn't work for most labels — use indices
		for i := range bucketLabels {
			bucketNums[i] = float64(i)
		}
	}

	// Shorten time labels for display
	shortTimes := make([]string, len(times))
	for i, t := range times {
		shortTimes[i] = shortenTimestamp(t)
	}

	data := HeatmapBlockData{
		Title:   "Latency Distribution",
		Buckets: bucketNums,
		Times:   shortTimes,
		Values:  values,
	}

	b := NewBlock(BlockHeatmap, data)
	slog.Info("suggested heatmap block from query results",
		"times", len(times), "buckets", len(bucketLabels))
	return &b
}

// parseBucketBoundary extracts a numeric boundary from a bucket label string.
// Examples: "0-10ms" → 10, "10-50ms" → 50, "500ms-1s" → 1000, ">1s" → 5000.
// Returns (value, true) on success.
func parseBucketBoundary(label string) (float64, bool) {
	label = strings.TrimSpace(label)

	// Handle ">Xs" or ">=Xs" patterns
	if strings.HasPrefix(label, ">") {
		s := strings.TrimLeft(label, ">= ")
		if v, unit, ok := parseValueWithUnit(s); ok {
			return applyUnit(v, unit) * 5, true // Scale up overflow bucket
		}
		return 0, false
	}

	// Handle "A-Bms" or "A-Bs" range patterns
	// Strip the unit suffix first
	unit := ""
	stripped := label
	if strings.HasSuffix(stripped, "ms") {
		unit = "ms"
		stripped = strings.TrimSuffix(stripped, "ms")
	} else if strings.HasSuffix(stripped, "s") {
		unit = "s"
		stripped = strings.TrimSuffix(stripped, "s")
	}

	parts := strings.SplitN(stripped, "-", 2)
	if len(parts) == 2 {
		if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
			return applyUnit(v, unit), true
		}
	}

	// Try parsing as a plain number
	if v, err := strconv.ParseFloat(label, 64); err == nil {
		return v, true
	}

	return 0, false
}

// parseValueWithUnit splits "10ms" or "1s" into (value, unit).
func parseValueWithUnit(s string) (float64, string, bool) {
	if strings.HasSuffix(s, "ms") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "ms"), 64)
		return v, "ms", err == nil
	}
	if strings.HasSuffix(s, "s") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
		return v, "s", err == nil
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, "", err == nil
}

func applyUnit(v float64, unit string) float64 {
	switch unit {
	case "s":
		return v * 1000 // Convert to ms
	default:
		return v
	}
}

// shortenTimestamp shortens ISO timestamps to "HH:MM" for display.
func shortenTimestamp(t string) string {
	// "2026-03-27T10:00:00" → "10:00"
	// "2026-03-27 10:00:00" → "10:00"
	if len(t) >= 16 {
		if t[10] == 'T' || t[10] == ' ' {
			return t[11:16]
		}
	}
	return t
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func parseTimestamp(value string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func normalizeStatus(status string) string {
	status = strings.TrimPrefix(status, "STATUS_CODE_")
	switch strings.ToLower(status) {
	case "ok":
		return "ok"
	case "error":
		return "error"
	default:
		return strings.ToLower(status)
	}
}

func orderedComparisonKeys(m map[string]mcpout.CompareMetricDiff) []string {
	order := []string{"latency", "errors", "throughput"}
	var keys []string
	seen := map[string]bool{}
	for _, key := range order {
		if _, ok := m[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	for key := range m {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys[orderIntersectionCount(order, m):])
	return keys
}

func orderIntersectionCount(order []string, m map[string]mcpout.CompareMetricDiff) int {
	count := 0
	for _, key := range order {
		if _, ok := m[key]; ok {
			count++
		}
	}
	return count
}

func comparisonMetricLabel(key string) string {
	switch key {
	case "latency":
		return "Latency"
	case "errors":
		return "Errors"
	case "throughput":
		return "Throughput"
	default:
		return humanizeColumn(key)
	}
}

func comparisonMetricUnit(key string) string {
	switch key {
	case "latency":
		return "ms"
	case "errors":
		return "%"
	case "throughput":
		return "rpm"
	default:
		return ""
	}
}

func healthScoreStatus(score float64) string {
	switch {
	case score >= 0.85:
		return "ok"
	case score >= 0.70:
		return "warning"
	default:
		return "danger"
	}
}

func errorRateStatus(ratePct float64) string {
	switch {
	case ratePct > 5:
		return "danger"
	case ratePct > 1:
		return "warning"
	default:
		return "ok"
	}
}

func latencyStatus(ms float64) string {
	switch {
	case ms > 5000:
		return "danger"
	case ms > 1000:
		return "warning"
	default:
		return "ok"
	}
}

func throughputStatus(count int64) string {
	if count == 0 {
		return "warning"
	}
	return "ok"
}

func splitMethodPath(name string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(name), " ", 2)
	if len(parts) == 2 {
		method := strings.ToUpper(parts[0])
		if validHTTPMethod(method) {
			return method, parts[1]
		}
	}
	return "", strings.TrimSpace(name)
}

func deriveEndpointStatus(p95Ms, errorRatePct float64) string {
	switch {
	case errorRatePct > 5 || p95Ms > 5000:
		return "unhealthy"
	case errorRatePct > 1 || p95Ms > 1000:
		return "degraded"
	default:
		return "healthy"
	}
}

func buildMetricsTimeseriesBlock(series []mcpout.MetricSeriesOut) *Block {
	if len(series) == 0 || len(series[0].Datapoints) == 0 {
		return nil
	}

	rawLabels := make([]string, len(series[0].Datapoints))
	labels := make([]string, len(series[0].Datapoints))
	for i, dp := range series[0].Datapoints {
		rawLabels[i] = dp.Time
		labels[i] = shortenTimestamp(dp.Time)
	}

	metricCount := distinctMetricCount(series)
	built := make([]TimeseriesSeries, 0, len(series))
	for _, item := range series {
		if len(item.Datapoints) != len(rawLabels) {
			return nil
		}
		values := make([]float64, len(item.Datapoints))
		for i, dp := range item.Datapoints {
			if dp.Time != rawLabels[i] {
				return nil
			}
			values[i] = dp.Value
		}
		built = append(built, TimeseriesSeries{
			Label:  metricSeriesLabel(item, metricCount > 1),
			Values: values,
		})
	}

	title := "Metrics"
	if metricCount == 1 {
		title = series[0].Metric
	}
	block := NewBlock(BlockTimeseries, TimeseriesBlockData{
		Title:  title,
		Series: built,
		Labels: labels,
		YLabel: sharedMetricUnit(series),
	})
	return &block
}

func buildMetricsQueryRows(series []mcpout.MetricSeriesOut) []map[string]any {
	rows := make([]map[string]any, 0)
	multiMetric := distinctMetricCount(series) > 1
	for _, item := range series {
		label := metricSeriesLabel(item, multiMetric)
		for _, dp := range item.Datapoints {
			rows = append(rows, map[string]any{
				"metric": item.Metric,
				"series": label,
				"time":   dp.Time,
				"value":  dp.Value,
				"unit":   item.Unit,
			})
		}
	}
	return rows
}

func metricSeriesLabel(item mcpout.MetricSeriesOut, includeMetric bool) string {
	var parts []string
	if includeMetric {
		parts = append(parts, item.Metric)
	}
	keys := make([]string, 0, len(item.Labels))
	for key := range item.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "service" {
			parts = append(parts, item.Labels[key])
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, item.Labels[key]))
	}
	if len(parts) == 0 {
		return item.Metric
	}
	return strings.Join(parts, " ")
}

func distinctMetricCount(series []mcpout.MetricSeriesOut) int {
	seen := map[string]struct{}{}
	for _, item := range series {
		seen[item.Metric] = struct{}{}
	}
	return len(seen)
}

func sharedMetricUnit(series []mcpout.MetricSeriesOut) string {
	if len(series) == 0 {
		return ""
	}
	unit := series[0].Unit
	for _, item := range series[1:] {
		if item.Unit != unit {
			return ""
		}
	}
	return unit
}

func buildGenericArrayTableBlock(raw map[string]json.RawMessage, field, _ string) *Block {
	data, ok := raw[field]
	if !ok {
		return nil
	}
	var rows []map[string]any
	if !unmarshalToolResultBytes(field, data, &rows) || len(rows) == 0 {
		return nil
	}
	block := buildQueryTableBlock(rows)
	return &block
}

func formatGroupLabel(key map[string]string) string {
	if len(key) == 0 {
		return "group"
	}
	keys := make([]string, 0, len(key))
	for part := range key {
		keys = append(keys, part)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, part := range keys {
		parts = append(parts, key[part])
	}
	return strings.Join(parts, " | ")
}

func humanizeColumn(col string) string {
	parts := strings.Fields(strings.ReplaceAll(col, "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(parts, " ")
}

func unmarshalToolResult(tool, text string, dst any) bool {
	return unmarshalToolResultBytes(tool, []byte(text), dst)
}

func unmarshalToolResultBytes(tool string, data []byte, dst any) bool {
	payload := unwrapToolResultData(data)
	if err := json.Unmarshal(payload, dst); err != nil {
		slog.Warn("failed to parse tool result for block builder", "tool", tool, "err", err)
		return false
	}
	return true
}

func unwrapToolResultData(data []byte) []byte {
	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return data
	}
	payload := bytes.TrimSpace(envelope.Data)
	if envelope.Type == "" || len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		return data
	}
	return payload
}

func validHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}
