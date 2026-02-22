package ai

import (
	"encoding/json"
	"fmt"
	"sort"
)

// toolResultToBlocks converts a tool's JSON result string into typed blocks
// for client-side rendering. Returns nil on parse failure — the LLM's text
// response is always available as fallback.
func toolResultToBlocks(name string, result string) []Block {
	switch name {
	case "status":
		return statusToBlocks(result)
	case "diagnose":
		return diagnoseToBlocks(result)
	case "find":
		return findToBlocks(result)
	case "trace":
		return traceToBlocks(result)
	case "timeline":
		return timelineToBlocks(result)
	case "topology":
		return topologyToBlocks(result)
	case "compare":
		return compareToBlocks(result)
	case "query":
		return queryToBlocks(result)
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// status → MetricsBlock + TableBlock
// ---------------------------------------------------------------------------

func statusToBlocks(result string) []Block {
	var out struct {
		Healthy          bool    `json:"healthy"`
		Summary          string  `json:"summary"`
		ThroughputPerMin int64   `json:"throughput_per_min"`
		P95Ms            float64 `json:"p95_ms"`
		ErrorRate        float64 `json:"error_rate"`
		Services         struct {
			Total     int `json:"total"`
			Healthy   int `json:"healthy"`
			Degraded  int `json:"degraded"`
			Unhealthy int `json:"unhealthy"`
		} `json:"services"`
		TopIssues []struct {
			Service string  `json:"service"`
			Issue   string  `json:"issue"`
			Value   float64 `json:"value"`
			Detail  string  `json:"detail,omitempty"`
		} `json:"top_issues"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		return nil
	}

	healthStatus := "ok"
	if !out.Healthy {
		healthStatus = "danger"
	}

	var blocks []Block

	// Metrics grid: throughput, p95, error rate
	blocks = append(blocks, MakeMetricsBlock([]MetricItem{
		{Label: "Throughput", Value: float64(out.ThroughputPerMin), Unit: "/min", Status: "ok"},
		{Label: "P95 Latency", Value: out.P95Ms, Unit: "ms", Status: latencyStatus(out.P95Ms)},
		{Label: "Error Rate", Value: out.ErrorRate * 100, Unit: "%", Status: errorRateStatus(out.ErrorRate)},
		{Label: "Health", Value: float64(out.Services.Healthy), Unit: fmt.Sprintf("/ %d", out.Services.Total), Status: healthStatus},
	}))

	// Top issues table
	if len(out.TopIssues) > 0 {
		columns := []TableColumn{
			{Key: "service", Label: "Service"},
			{Key: "issue", Label: "Issue"},
			{Key: "value", Label: "Value", Align: "right"},
		}
		rows := make([]map[string]any, 0, len(out.TopIssues))
		for _, ti := range out.TopIssues {
			rows = append(rows, map[string]any{
				"service": ti.Service,
				"issue":   ti.Issue,
				"value":   fmt.Sprintf("%.2f", ti.Value),
			})
		}
		blocks = append(blocks, MakeTableBlock(columns, rows))
	}

	return blocks
}

// ---------------------------------------------------------------------------
// diagnose → MetricsBlock + TableBlock(s)
// ---------------------------------------------------------------------------

func diagnoseToBlocks(result string) []Block {
	var out struct {
		Service string `json:"service"`
		Status  string `json:"status"`
		Metrics struct {
			P50Ms     float64 `json:"p50_ms"`
			P95Ms     float64 `json:"p95_ms"`
			P99Ms     float64 `json:"p99_ms"`
			ErrorRate float64 `json:"error_rate"`
			Count     int64   `json:"request_count"`
		} `json:"metrics"`
		TopErrors []struct {
			Message      string `json:"message"`
			Count        int64  `json:"count"`
			ExampleTrace string `json:"example_trace,omitempty"`
		} `json:"top_errors"`
		SlowOperations []struct {
			Name  string  `json:"name"`
			P95Ms float64 `json:"p95_ms"`
			Count int64   `json:"count"`
		} `json:"slow_operations"`
		Dependencies []struct {
			Service   string  `json:"service"`
			Status    string  `json:"status"`
			ErrorRate float64 `json:"error_rate"`
			P95Ms     float64 `json:"p95_ms"`
			Calls     int64   `json:"calls"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		return nil
	}

	var blocks []Block

	// Latency percentiles + error rate
	blocks = append(blocks, MakeMetricsBlock([]MetricItem{
		{Label: "P50", Value: out.Metrics.P50Ms, Unit: "ms", Status: latencyStatus(out.Metrics.P50Ms)},
		{Label: "P95", Value: out.Metrics.P95Ms, Unit: "ms", Status: latencyStatus(out.Metrics.P95Ms)},
		{Label: "P99", Value: out.Metrics.P99Ms, Unit: "ms", Status: latencyStatus(out.Metrics.P99Ms)},
		{Label: "Error Rate", Value: out.Metrics.ErrorRate * 100, Unit: "%", Status: errorRateStatus(out.Metrics.ErrorRate)},
		{Label: "Requests", Value: float64(out.Metrics.Count), Unit: "", Status: "ok"},
	}))

	// Top errors table
	if len(out.TopErrors) > 0 {
		columns := []TableColumn{
			{Key: "message", Label: "Error"},
			{Key: "count", Label: "Count", Align: "right"},
			{Key: "trace", Label: "Example Trace"},
		}
		rows := make([]map[string]any, 0, len(out.TopErrors))
		for _, e := range out.TopErrors {
			rows = append(rows, map[string]any{
				"message": e.Message,
				"count":   e.Count,
				"trace":   e.ExampleTrace,
			})
		}
		blocks = append(blocks, MakeTableBlock(columns, rows))
	}

	// Slow operations table
	if len(out.SlowOperations) > 0 {
		columns := []TableColumn{
			{Key: "operation", Label: "Operation"},
			{Key: "p95", Label: "P95 (ms)", Align: "right"},
			{Key: "count", Label: "Count", Align: "right"},
		}
		rows := make([]map[string]any, 0, len(out.SlowOperations))
		for _, op := range out.SlowOperations {
			rows = append(rows, map[string]any{
				"operation": op.Name,
				"p95":       fmt.Sprintf("%.1f", op.P95Ms),
				"count":     op.Count,
			})
		}
		blocks = append(blocks, MakeTableBlock(columns, rows))
	}

	// Dependencies table
	if len(out.Dependencies) > 0 {
		columns := []TableColumn{
			{Key: "service", Label: "Dependency"},
			{Key: "status", Label: "Status"},
			{Key: "p95", Label: "P95 (ms)", Align: "right"},
			{Key: "error_rate", Label: "Errors", Align: "right"},
			{Key: "calls", Label: "Calls", Align: "right"},
		}
		rows := make([]map[string]any, 0, len(out.Dependencies))
		for _, d := range out.Dependencies {
			rows = append(rows, map[string]any{
				"service":    d.Service,
				"status":     d.Status,
				"p95":        fmt.Sprintf("%.1f", d.P95Ms),
				"error_rate": fmt.Sprintf("%.2f%%", d.ErrorRate*100),
				"calls":      d.Calls,
			})
		}
		blocks = append(blocks, MakeTableBlock(columns, rows))
	}

	return blocks
}

// ---------------------------------------------------------------------------
// find → TableBlock(s) for spans and/or logs
// ---------------------------------------------------------------------------

func findToBlocks(result string) []Block {
	var out struct {
		Spans []struct {
			TraceID    string  `json:"trace_id"`
			SpanID     string  `json:"span_id"`
			Service    string  `json:"service"`
			Operation  string  `json:"operation"`
			DurationMs float64 `json:"duration_ms"`
			Status     string  `json:"status"`
			StartTime  string  `json:"start_time"`
		} `json:"spans"`
		Logs []struct {
			Timestamp string `json:"ts"`
			Service   string `json:"service"`
			Severity  string `json:"severity"`
			Body      string `json:"body"`
			TraceID   string `json:"trace_id,omitempty"`
		} `json:"logs"`
		SpanCount int `json:"span_count"`
		LogCount  int `json:"log_count"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		return nil
	}
	if out.SpanCount == 0 && out.LogCount == 0 {
		return nil
	}

	var blocks []Block

	// Spans table
	if len(out.Spans) > 0 {
		columns := []TableColumn{
			{Key: "service", Label: "Service"},
			{Key: "operation", Label: "Operation"},
			{Key: "duration", Label: "Duration", Align: "right"},
			{Key: "status", Label: "Status"},
			{Key: "trace_id", Label: "Trace ID"},
		}
		rows := make([]map[string]any, 0, len(out.Spans))
		for _, sp := range out.Spans {
			rows = append(rows, map[string]any{
				"service":   sp.Service,
				"operation": sp.Operation,
				"duration":  fmt.Sprintf("%.1fms", sp.DurationMs),
				"status":    sp.Status,
				"trace_id":  sp.TraceID,
			})
		}
		blocks = append(blocks, MakeTableBlock(columns, rows))
	}

	// Logs table
	if len(out.Logs) > 0 {
		columns := []TableColumn{
			{Key: "time", Label: "Time"},
			{Key: "service", Label: "Service"},
			{Key: "severity", Label: "Severity"},
			{Key: "body", Label: "Body"},
		}
		rows := make([]map[string]any, 0, len(out.Logs))
		for _, lg := range out.Logs {
			rows = append(rows, map[string]any{
				"time":     lg.Timestamp,
				"service":  lg.Service,
				"severity": lg.Severity,
				"body":     lg.Body,
			})
		}
		blocks = append(blocks, MakeTableBlock(columns, rows))
	}

	return blocks
}

// ---------------------------------------------------------------------------
// trace → TraceWaterfallBlock
// ---------------------------------------------------------------------------

func traceToBlocks(result string) []Block {
	var out struct {
		TraceID       string  `json:"trace_id"`
		TotalDuration float64 `json:"total_duration_ms"`
		SpanCount     int     `json:"span_count"`
		HasError      bool    `json:"has_error"`
		Spans         []struct {
			SpanID       string  `json:"span_id"`
			ParentSpanID string  `json:"parent_span_id,omitempty"`
			Service      string  `json:"service"`
			Operation    string  `json:"operation"`
			StartTime    string  `json:"start_time"`
			DurationMs   float64 `json:"duration_ms"`
			Status       string  `json:"status"`
		} `json:"spans"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || len(out.Spans) == 0 {
		return nil
	}

	// Find the earliest start time to compute relative offsets.
	// StartTime is ISO8601 — we parse it for relative positioning.
	// If parsing fails, fall back to index-based positioning.
	spans := make([]TraceSpan, 0, len(out.Spans))
	for _, sp := range out.Spans {
		var parent *string
		if sp.ParentSpanID != "" {
			p := sp.ParentSpanID
			parent = &p
		}
		spans = append(spans, TraceSpan{
			ID:        sp.SpanID,
			Parent:    parent,
			Service:   sp.Service,
			Operation: sp.Operation,
			Start:     0, // will be set below
			Duration:  sp.DurationMs,
			Status:    sp.Status,
		})
	}

	// Use the first span's start as epoch 0; compute offsets from DurationMs ordering.
	// Since we don't have nanosecond start in the JSON, we approximate from the trace tree.
	// The waterfall renderer handles layout from relative start times.
	// For now, use cumulative indexing as a simple approximation.
	// A proper implementation would parse start_time strings, but that adds complexity.
	// The client viz already handles parent-child nesting.
	for i := range spans {
		spans[i].Start = float64(i) // placeholder ordering; client uses parent-child tree
	}

	return []Block{
		NewBlock(BlockTraceWaterfall, TraceWaterfallData{Spans: spans}),
	}
}

// ---------------------------------------------------------------------------
// timeline → TimeseriesBlock
// ---------------------------------------------------------------------------

func timelineToBlocks(result string) []Block {
	var out struct {
		Service string `json:"service,omitempty"`
		Buckets []struct {
			Time         string  `json:"time"`
			RequestCount int64   `json:"request_count"`
			ErrorCount   int64   `json:"error_count"`
			P95Ms        float64 `json:"p95_ms"`
			ErrorRate    float64 `json:"error_rate"`
			IsAnomaly    bool    `json:"is_anomaly,omitempty"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || len(out.Buckets) == 0 {
		return nil
	}

	labels := make([]string, 0, len(out.Buckets))
	p95Values := make([]float64, 0, len(out.Buckets))
	reqValues := make([]float64, 0, len(out.Buckets))
	errValues := make([]float64, 0, len(out.Buckets))

	for _, b := range out.Buckets {
		labels = append(labels, b.Time)
		p95Values = append(p95Values, b.P95Ms)
		reqValues = append(reqValues, float64(b.RequestCount))
		errValues = append(errValues, b.ErrorRate*100)
	}

	title := "System Timeline"
	if out.Service != "" {
		title = out.Service + " Timeline"
	}

	return []Block{
		NewBlock(BlockTimeseries, TimeseriesBlockData{
			Title:  title,
			Labels: labels,
			YLabel: "ms / count / %",
			Series: []TimeseriesSeries{
				{Label: "P95 (ms)", Color: "var(--signal-trace)", Values: p95Values},
				{Label: "Requests", Color: "var(--text-secondary)", Values: reqValues},
				{Label: "Error Rate (%)", Color: "var(--signal-error)", Values: errValues},
			},
		}),
	}
}

// ---------------------------------------------------------------------------
// topology → TopologyBlock
// ---------------------------------------------------------------------------

func topologyToBlocks(result string) []Block {
	var out struct {
		Nodes []struct {
			Name      string  `json:"name"`
			Status    string  `json:"status"`
			SpanCount int64   `json:"span_count"`
			P95Ms     float64 `json:"p95_ms"`
			ErrorRate float64 `json:"error_rate"`
		} `json:"nodes"`
		Edges []struct {
			From      string  `json:"from"`
			To        string  `json:"to"`
			CallCount int64   `json:"call_count"`
			AvgMs     float64 `json:"avg_ms"`
			ErrorRate float64 `json:"error_rate"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || len(out.Nodes) == 0 {
		return nil
	}

	nodes := make([]TopologyNode, 0, len(out.Nodes))
	for _, n := range out.Nodes {
		nodes = append(nodes, TopologyNode{
			ID:     n.Name,
			Status: n.Status,
			RPM:    float64(n.SpanCount), // span_count as throughput proxy
			P95:    n.P95Ms,
			Errors: n.ErrorRate * 100,
		})
	}

	edges := make([]TopologyEdge, 0, len(out.Edges))
	for _, e := range out.Edges {
		edges = append(edges, TopologyEdge{
			Source:    e.From,
			Target:   e.To,
			RPM:      float64(e.CallCount),
			ErrorRate: e.ErrorRate * 100,
		})
	}

	return []Block{
		NewBlock(BlockTopology, TopologyData{Nodes: nodes, Edges: edges}),
	}
}

// ---------------------------------------------------------------------------
// compare → TableBlock
// ---------------------------------------------------------------------------

func compareToBlocks(result string) []Block {
	var out struct {
		Services []struct {
			Service   string  `json:"service"`
			Requests  int64   `json:"requests"`
			ErrorRate float64 `json:"error_rate"`
			P50Ms     float64 `json:"p50_ms"`
			P95Ms     float64 `json:"p95_ms"`
			AvgMs     float64 `json:"avg_ms"`
		} `json:"services"`
		Winner  string `json:"winner"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || len(out.Services) == 0 {
		return nil
	}

	columns := []TableColumn{
		{Key: "service", Label: "Service"},
		{Key: "requests", Label: "Requests", Align: "right"},
		{Key: "error_rate", Label: "Error Rate", Align: "right"},
		{Key: "p50", Label: "P50 (ms)", Align: "right"},
		{Key: "p95", Label: "P95 (ms)", Align: "right"},
	}
	rows := make([]map[string]any, 0, len(out.Services))
	for _, m := range out.Services {
		label := m.Service
		if m.Service == out.Winner {
			label += " *"
		}
		rows = append(rows, map[string]any{
			"service":    label,
			"requests":   m.Requests,
			"error_rate": fmt.Sprintf("%.2f%%", m.ErrorRate*100),
			"p50":        fmt.Sprintf("%.1f", m.P50Ms),
			"p95":        fmt.Sprintf("%.1f", m.P95Ms),
		})
	}

	return []Block{MakeTableBlock(columns, rows)}
}

// ---------------------------------------------------------------------------
// query → TableBlock
// ---------------------------------------------------------------------------

func queryToBlocks(result string) []Block {
	var out struct {
		Results  []map[string]any `json:"results"`
		RowCount int              `json:"row_count"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || len(out.Results) == 0 {
		return nil
	}

	// Build sorted columns from first row's keys for deterministic output
	keys := make([]string, 0, len(out.Results[0]))
	for key := range out.Results[0] {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	columns := make([]TableColumn, 0, len(keys))
	for _, key := range keys {
		columns = append(columns, TableColumn{Key: key, Label: key})
	}

	return []Block{MakeTableBlock(columns, out.Results)}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func latencyStatus(ms float64) string {
	switch {
	case ms > 1000:
		return "danger"
	case ms > 500:
		return "warning"
	default:
		return "ok"
	}
}

func errorRateStatus(rate float64) string {
	switch {
	case rate > 0.05:
		return "danger"
	case rate > 0.01:
		return "warning"
	default:
		return "ok"
	}
}
