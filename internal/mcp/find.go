package mcp

import (
	"context"
	"fmt"

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
	Spans      []FoundSpan `json:"spans"`
	Logs       []FoundLog  `json:"logs"`
	SpanCount  int         `json:"span_count"`
	LogCount   int         `json:"log_count"`
	HasMore    bool        `json:"has_more"`
	Suggestion string      `json:"suggestion,omitempty"`
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

	return nil, out, nil
}
