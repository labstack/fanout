package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/render"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// find - Unified span/log search with smart defaults

type FindIn struct {
	Query    string   `json:"query,omitempty" jsonschema:"Search pattern (regex for logs, substring for spans)"`
	Service  string   `json:"service,omitempty" jsonschema:"Filter by service"`
	Type     string   `json:"type,omitempty" jsonschema:"Signal type: spans|logs|both,default=both"`
	Status   string   `json:"status,omitempty" jsonschema:"Filter: error|slow|all,default=all"`
	Window   int      `json:"window,omitempty" jsonschema:"Time window in minutes,default=15"`
	Severity []string `json:"severity,omitempty" jsonschema:"Log severity filter: DEBUG,INFO,WARN,ERROR,FATAL"`
	Limit    int      `json:"limit,omitempty" jsonschema:"Max results per type,default=50"`
	Format   string   `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
}

type FoundSpan struct {
	TraceID    string  `json:"trace_id"`
	SpanID     string  `json:"span_id"`
	Service    string  `json:"service"`
	Operation  string  `json:"operation"`
	DurationMs float64 `json:"duration_ms"`
	Status     string  `json:"status"`
	StartTime  string  `json:"start_time"`
}

type FoundLog struct {
	Timestamp string `json:"ts"`
	Service   string `json:"service"`
	Severity  string `json:"severity"`
	Body      string `json:"body"`
	TraceID   string `json:"trace_id,omitempty"`
}

type FindOut struct {
	Spans      []FoundSpan    `json:"spans"`
	Logs       []FoundLog     `json:"logs"`
	SpanCount  int            `json:"span_count"`
	LogCount   int            `json:"log_count"`
	HasMore    bool           `json:"has_more"`
	Suggestion string         `json:"suggestion,omitempty"`
	Render     *render.Output `json:"render,omitempty"`
}

func (s *Server) find(ctx context.Context, req *mcp.CallToolRequest, in FindIn) (*mcp.CallToolResult, FindOut, error) {
	result, err := s.svc.Find(ctx, service.FindParams{
		Query:    in.Query,
		Service:  in.Service,
		Type:     in.Type,
		Status:   in.Status,
		Window:   in.Window,
		Severity: in.Severity,
		Limit:    in.Limit,
	})
	if err != nil {
		return nil, FindOut{Spans: []FoundSpan{}, Logs: []FoundLog{}}, nil
	}

	out := FindOut{
		Spans:     make([]FoundSpan, 0, len(result.Spans)),
		Logs:      make([]FoundLog, 0, len(result.Logs)),
		SpanCount: len(result.Spans),
		LogCount:  len(result.Logs),
		HasMore:   result.HasMore,
	}

	for _, sp := range result.Spans {
		out.Spans = append(out.Spans, FoundSpan{
			TraceID:    sp.TraceID,
			SpanID:     sp.SpanID,
			Service:    sp.Service,
			Operation:  sp.Name,
			DurationMs: sp.Duration,
			Status:     sp.Status,
			StartTime:  sp.StartTime,
		})
	}

	for _, lg := range result.Logs {
		out.Logs = append(out.Logs, FoundLog{
			Timestamp: lg.Time,
			Service:   lg.Service,
			Severity:  lg.Severity,
			Body:      lg.Body,
			TraceID:   lg.TraceID,
		})
	}

	// Add suggestion based on results
	if out.SpanCount > 0 && out.LogCount == 0 {
		out.Suggestion = fmt.Sprintf("Found spans. Use trace tool with trace_id '%s' for details.", out.Spans[0].TraceID)
	} else if out.LogCount > 0 && out.Logs[0].TraceID != "" {
		out.Suggestion = fmt.Sprintf("Found logs with trace context. Use trace tool with trace_id '%s'.", out.Logs[0].TraceID)
	} else if out.SpanCount == 0 && out.LogCount == 0 {
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
		Cols: 3,
		Items: []render.Renderer{
			&render.Metric{Label: "Spans", Value: fmt.Sprintf("%d", f.SpanCount)},
			&render.Metric{Label: "Logs", Value: fmt.Sprintf("%d", f.LogCount)},
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
