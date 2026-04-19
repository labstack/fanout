package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LogsIn holds input parameters for the logs tool.
type LogsIn struct {
	Query     string            `json:"query,omitempty"     jsonschema:"Substring search on log body"`
	Severity  []string          `json:"severity,omitempty"  jsonschema:"Filter by severity: TRACE|DEBUG|INFO|WARN|ERROR|FATAL"`
	TraceID   string            `json:"trace_id,omitempty"  jsonschema:"Find logs correlated to a specific trace"`
	Service   string            `json:"service,omitempty"   jsonschema:"Filter by service"`
	Attrs     map[string]string `json:"attrs,omitempty"     jsonschema:"Attribute filters as key-value pairs"`
	GroupBy   []string          `json:"group_by,omitempty"  jsonschema:"Aggregate by: service|severity|template"`
	OrderBy   string            `json:"order_by,omitempty"  jsonschema:"Sort: time|count|severity"`
	Window    string            `json:"window,omitempty"    jsonschema:"Time window: '15m','1h','7d', or ISO range 'start/end'. Default: 15m"`
	Namespace string            `json:"namespace,omitempty" jsonschema:"Filter by namespace"`
	Limit     int               `json:"limit,omitempty"     jsonschema:"Max results (ungrouped) or groups. Default: 100"`
}

// LogRowOut is a single log entry in ungrouped results.
type LogRowOut struct {
	Time       string            `json:"time"`
	Service    string            `json:"service"`
	Severity   string            `json:"severity"`
	Body       string            `json:"body"`
	TraceID    string            `json:"trace_id,omitempty"`
	SpanID     string            `json:"span_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// LogGroupOut is one aggregation bucket in grouped log results.
type LogGroupOut struct {
	Key            map[string]string `json:"key"`
	Count          int64             `json:"count"`
	SampleBodies   []string          `json:"sample_bodies"`
	SampleTraceIDs []string          `json:"sample_trace_ids"`
}

// LogsOut is the response envelope for the logs tool.
type LogsOut struct {
	// Ungrouped
	Logs         []LogRowOut `json:"logs,omitempty"`
	TotalMatched int         `json:"total_matched,omitempty"`
	Returned     int         `json:"returned,omitempty"`

	// Grouped
	Groups      []LogGroupOut `json:"groups,omitempty"`
	TotalGroups int           `json:"total_groups,omitempty"`

	Suggestion string `json:"suggestion,omitempty"`
}

func (s *Server) logs(ctx context.Context, req *mcp.CallToolRequest, in LogsIn) (*mcp.CallToolResult, LogsOut, error) {
	tw, err := parseWindow(in.Window)
	if err != nil {
		return nil, LogsOut{Suggestion: fmt.Sprintf("Invalid window: %s", err)}, nil
	}

	limit := clampInt(in.Limit, minLimit, maxLimit, 100)

	result, err := s.svc.Logs(ctx, service.LogParams{
		Query:     in.Query,
		Severity:  in.Severity,
		TraceID:   in.TraceID,
		Service:   in.Service,
		Attrs:     in.Attrs,
		GroupBy:   in.GroupBy,
		OrderBy:   in.OrderBy,
		Window:    tw.Minutes,
		Namespace: in.Namespace,
		Limit:     limit,
	})
	if err != nil {
		slog.Warn("logs tool failed", "err", err)
		return nil, LogsOut{
			Logs:       []LogRowOut{},
			Groups:     []LogGroupOut{},
			Suggestion: fmt.Sprintf("Query failed: %s", err),
		}, nil
	}

	out := LogsOut{}

	if len(in.GroupBy) > 0 {
		out.Groups = make([]LogGroupOut, 0, len(result.Groups))
		for _, g := range result.Groups {
			out.Groups = append(out.Groups, LogGroupOut{
				Key:            g.Key,
				Count:          g.Count,
				SampleBodies:   g.SampleBodies,
				SampleTraceIDs: g.SampleTraceIDs,
			})
		}
		out.TotalGroups = result.TotalGroups
		if len(out.Groups) == 0 {
			out.Suggestion = "No log groups found. Try widening the time window or adjusting filters."
		}
	} else {
		out.Logs = make([]LogRowOut, 0, len(result.Logs))
		for _, l := range result.Logs {
			out.Logs = append(out.Logs, LogRowOut{
				Time:       l.Time,
				Service:    l.Service,
				Severity:   l.Severity,
				Body:       l.Body,
				TraceID:    l.TraceID,
				SpanID:     l.SpanID,
				Attributes: l.Attributes,
			})
		}
		out.TotalMatched = result.TotalMatched
		out.Returned = len(out.Logs)

		if len(out.Logs) == 0 {
			out.Suggestion = "No logs found. Try widening the time window or adjusting filters."
		} else if out.TotalMatched > limit {
			out.Suggestion = fmt.Sprintf("Showing %d of %d+ logs. Use filters or group_by to narrow results.", out.Returned, out.TotalMatched)
		} else if len(out.Logs) > 0 && out.Logs[0].TraceID != "" {
			out.Suggestion = fmt.Sprintf("Use trace tool with trace_id '%s' for full request context.", out.Logs[0].TraceID)
		}
	}

	return nil, out, nil
}
