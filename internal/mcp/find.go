package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/labstack/fanout/internal/render"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// find - Unified span/log search with smart defaults

type FindIn struct {
	Query     string            `json:"query,omitempty" jsonschema:"Search pattern (regex for logs, substring for spans)"`
	Service   string            `json:"service,omitempty" jsonschema:"Filter by service"`
	Operation string            `json:"operation,omitempty" jsonschema:"Filter by operation/span name"`
	Type      string            `json:"type,omitempty" jsonschema:"Signal type: spans|logs|metrics|both|all,default=both"`
	Status    string            `json:"status,omitempty" jsonschema:"Filter: error|slow|all,default=all"`
	Window    int               `json:"window,omitempty" jsonschema:"Time window in minutes,default=15"`
	Namespace string            `json:"namespace,omitempty" jsonschema:"Filter by namespace"`
	TenantID  string            `json:"tenant_id,omitempty" jsonschema:"Filter by tenant"`
	Severity  []string          `json:"severity,omitempty" jsonschema:"Log severity filter: DEBUG,INFO,WARN,ERROR,FATAL"`
	Attrs     map[string]string `json:"attrs,omitempty" jsonschema:"Attribute filters as key=value pairs (e.g. http.status_code=500)"`
	Limit     int               `json:"limit,omitempty" jsonschema:"Max results per type,default=50"`
	Format    string            `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
}

type FoundSpan struct {
	TraceID      string  `json:"trace_id"`
	SpanID       string  `json:"span_id"`
	Service      string  `json:"service"`
	Operation    string  `json:"operation"`
	DurationMs   float64 `json:"duration_ms"`
	Status       string  `json:"status"`
	StartTime    string  `json:"start_time"`
	ScopeName    string  `json:"scope_name,omitempty"`
	ScopeVersion string  `json:"scope_version,omitempty"`
}

type FoundLog struct {
	Timestamp      string `json:"ts"`
	ObservedTime   string `json:"observed_ts,omitempty"`
	Service        string `json:"service"`
	Severity       string `json:"severity"`
	SeverityNumber int32  `json:"severity_number,omitempty"`
	Body           string `json:"body"`
	TraceID        string `json:"trace_id,omitempty"`
	SpanID         string `json:"span_id,omitempty"`
	ScopeName      string `json:"scope_name,omitempty"`
	ScopeVersion   string `json:"scope_version,omitempty"`
}

type FoundMetric struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Service     string  `json:"service"`
	Value       float64 `json:"value"`
	Unit        string  `json:"unit,omitempty"`
	Time        string  `json:"time"`
	Description string  `json:"description,omitempty"`
}

type FindOut struct {
	Spans       []FoundSpan    `json:"spans"`
	Logs        []FoundLog     `json:"logs"`
	Metrics     []FoundMetric  `json:"metrics"`
	SpanCount   int            `json:"span_count"`
	LogCount    int            `json:"log_count"`
	MetricCount int            `json:"metric_count"`
	HasMore     bool           `json:"has_more"`
	Suggestion  string         `json:"suggestion,omitempty"`
	Render      *render.Output `json:"render,omitempty"`
}

func (s *Server) find(ctx context.Context, req *mcp.CallToolRequest, in FindIn) (*mcp.CallToolResult, FindOut, error) {
	window := clampInt(in.Window, minWindow, maxWindow, defWindow)
	limit := clampInt(in.Limit, minLimit, maxLimit, defLimit)

	result, err := s.svc.Find(ctx, service.FindParams{
		Query:     in.Query,
		Service:   in.Service,
		Operation: in.Operation,
		Type:      in.Type,
		Status:    in.Status,
		Window:    window,
		Namespace: in.Namespace,
		TenantID:  in.TenantID,
		Severity:  in.Severity,
		Attrs:     in.Attrs,
		Limit:     limit,
	})
	if err != nil {
		slog.Warn("find query failed", "method", "find", "err", err)
		return nil, FindOut{
			Spans:      []FoundSpan{},
			Logs:       []FoundLog{},
			Metrics:    []FoundMetric{},
			Suggestion: fmt.Sprintf("Query failed: %s", err.Error()),
		}, nil
	}

	out := FindOut{
		Spans:       make([]FoundSpan, 0, len(result.Spans)),
		Logs:        make([]FoundLog, 0, len(result.Logs)),
		Metrics:     make([]FoundMetric, 0, len(result.Metrics)),
		SpanCount:   len(result.Spans),
		LogCount:    len(result.Logs),
		MetricCount: len(result.Metrics),
		HasMore:     result.HasMore,
	}

	for _, sp := range result.Spans {
		out.Spans = append(out.Spans, FoundSpan{
			TraceID:      sp.TraceID,
			SpanID:       sp.SpanID,
			Service:      sp.Service,
			Operation:    sp.Name,
			DurationMs:   sp.Duration,
			Status:       sp.Status,
			StartTime:    sp.StartTime,
			ScopeName:    sp.ScopeName,
			ScopeVersion: sp.ScopeVersion,
		})
	}

	for _, lg := range result.Logs {
		out.Logs = append(out.Logs, FoundLog{
			Timestamp:      lg.Time,
			ObservedTime:   lg.ObservedTime,
			Service:        lg.Service,
			Severity:       lg.Severity,
			SeverityNumber: lg.SeverityNumber,
			Body:           lg.Body,
			TraceID:        lg.TraceID,
			SpanID:         lg.SpanID,
			ScopeName:      lg.ScopeName,
			ScopeVersion:   lg.ScopeVersion,
		})
	}

	for _, m := range result.Metrics {
		out.Metrics = append(out.Metrics, FoundMetric{
			Name:        m.Name,
			Type:        m.Type,
			Service:     m.Service,
			Value:       m.Value,
			Unit:        m.Unit,
			Time:        m.Time,
			Description: m.Description,
		})
	}

	// Add suggestion based on results
	if out.SpanCount > 0 && out.LogCount == 0 && out.MetricCount == 0 {
		out.Suggestion = fmt.Sprintf("Found spans. Use trace tool with trace_id '%s' for details.", out.Spans[0].TraceID)
	} else if out.LogCount > 0 && out.Logs[0].TraceID != "" {
		out.Suggestion = fmt.Sprintf("Found logs with trace context. Use trace tool with trace_id '%s'.", out.Logs[0].TraceID)
	} else if out.MetricCount > 0 && out.SpanCount == 0 && out.LogCount == 0 {
		out.Suggestion = fmt.Sprintf("Found %d metrics. Use diagnose or timeline tool for deeper analysis.", out.MetricCount)
	} else if out.SpanCount == 0 && out.LogCount == 0 && out.MetricCount == 0 {
		out.Suggestion = "No results. Try widening the time window or adjusting filters."
	}

	// Render output
	format := parseFormat(in.Format)
	if format != render.Data {
		rendered := renderFind(&out)
		out.Render = &rendered
	}

	return nil, out, nil
}

func renderFind(f *FindOut) render.Output {
	var items []render.Renderer

	// Summary
	summary := &render.Grid{
		Cols: 4,
		Items: []render.Renderer{
			&render.Metric{Label: "Spans", Value: fmt.Sprintf("%d", f.SpanCount)},
			&render.Metric{Label: "Logs", Value: fmt.Sprintf("%d", f.LogCount)},
			&render.Metric{Label: "Metrics", Value: fmt.Sprintf("%d", f.MetricCount)},
			&render.Badge{Label: moreLabel(f.HasMore), Status: moreStatus(f.HasMore)},
		},
	}
	items = append(items, summary)

	// Spans table
	if len(f.Spans) > 0 {
		var rows [][]string
		for _, sp := range f.Spans {
			rows = append(rows, []string{
				sp.Service,
				truncate(sp.Operation, 30),
				fmt.Sprintf("%.1fms", sp.DurationMs),
				sp.Status,
				truncate(sp.TraceID, 16),
			})
		}
		spansTable := &render.Table{
			Title:    "Spans",
			Headers:  []string{"Service", "Operation", "Duration", "Status", "Trace"},
			Rows:     rows,
			MaxWidth: 30,
		}
		items = append(items, spansTable)
	}

	// Logs table
	if len(f.Logs) > 0 {
		var rows [][]string
		for _, lg := range f.Logs {
			rows = append(rows, []string{
				lg.Timestamp,
				lg.Service,
				lg.Severity,
				truncate(lg.Body, 60),
			})
		}
		logsTable := &render.Table{
			Title:    "Logs",
			Headers:  []string{"Time", "Service", "Severity", "Body"},
			Rows:     rows,
			MaxWidth: 60,
		}
		items = append(items, logsTable)
	}

	// Metrics table
	if len(f.Metrics) > 0 {
		var rows [][]string
		for _, m := range f.Metrics {
			rows = append(rows, []string{
				truncate(m.Name, 30),
				m.Type,
				m.Service,
				fmt.Sprintf("%.4g", m.Value),
				m.Unit,
				m.Time,
			})
		}
		metricsTable := &render.Table{
			Title:    "Metrics",
			Headers:  []string{"Metric", "Type", "Service", "Value", "Unit", "Time"},
			Rows:     rows,
			MaxWidth: 30,
		}
		items = append(items, metricsTable)
	}

	// Suggestion
	if f.Suggestion != "" {
		items = append(items, &render.Text{Content: f.Suggestion, Style: "dim"})
	}

	composed := &render.Compose{Vertical: true, Items: items}
	return composed.Render(render.Both)
}

func moreLabel(hasMore bool) string {
	if hasMore {
		return "more available"
	}
	return "complete"
}

func moreStatus(hasMore bool) string {
	if hasMore {
		return "warning"
	}
	return "healthy"
}
