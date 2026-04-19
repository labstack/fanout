package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SpansIn holds input parameters for the spans tool.
type SpansIn struct {
	Query            string            `json:"query,omitempty"             jsonschema:"Substring search on operation name and status message"`
	Operation        string            `json:"operation,omitempty"         jsonschema:"Exact operation name filter"`
	Service          string            `json:"service,omitempty"           jsonschema:"Filter by service"`
	Status           string            `json:"status,omitempty"            jsonschema:"Filter: error|ok|slow|all (default all)"`
	Kind             string            `json:"kind,omitempty"              jsonschema:"Span kind: server|client|producer|consumer|internal"`
	MinDurationMs    *float64          `json:"min_duration_ms,omitempty"   jsonschema:"Minimum span duration in ms"`
	MaxDurationMs    *float64          `json:"max_duration_ms,omitempty"   jsonschema:"Maximum span duration in ms"`
	Attrs            map[string]string `json:"attrs,omitempty"             jsonschema:"Attribute filters as key-value pairs"`
	GroupBy          []string          `json:"group_by,omitempty"          jsonschema:"Aggregate by: service|operation|status|kind|http.method|http.status_code"`
	OrderBy          string            `json:"order_by,omitempty"          jsonschema:"Sort: time|duration|error_rate|count"`
	IncludeExemplars bool              `json:"include_exemplars,omitempty" jsonschema:"Include example trace IDs per group"`
	Window           string            `json:"window,omitempty"            jsonschema:"Time window: '15m','1h','7d', or ISO range 'start/end'. Default: 15m"`
	Namespace        string            `json:"namespace,omitempty"         jsonschema:"Filter by namespace"`
	Limit            int               `json:"limit,omitempty"             jsonschema:"Max results (ungrouped) or groups. Default: 100"`
}

// SpanRowOut is a single span in ungrouped results.
type SpanRowOut struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	Service    string            `json:"service"`
	Operation  string            `json:"operation"`
	Kind       string            `json:"kind,omitempty"`
	StartTime  string            `json:"start_time"`
	DurationMs float64           `json:"duration_ms"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// SpanGroupOut is one aggregation bucket in grouped results.
type SpanGroupOut struct {
	Key              map[string]string `json:"key"`
	Count            int64             `json:"count"`
	ErrorCount       int64             `json:"error_count"`
	ErrorRate        float64           `json:"error_rate"`
	P50Ms            float64           `json:"p50_ms"`
	P95Ms            float64           `json:"p95_ms"`
	P99Ms            float64           `json:"p99_ms"`
	ExemplarTraceIDs []string          `json:"exemplar_trace_ids,omitempty"`
}

// SpansOut is the response envelope for the spans tool.
type SpansOut struct {
	// Ungrouped
	Spans        []SpanRowOut `json:"spans,omitempty"`
	TotalMatched int          `json:"total_matched,omitempty"`
	Returned     int          `json:"returned,omitempty"`

	// Grouped
	Groups      []SpanGroupOut `json:"groups,omitempty"`
	TotalGroups int            `json:"total_groups,omitempty"`

	Suggestion string `json:"suggestion,omitempty"`
}

func (s *Server) spans(ctx context.Context, req *mcp.CallToolRequest, in SpansIn) (*mcp.CallToolResult, SpansOut, error) {
	tw, err := parseWindow(in.Window)
	if err != nil {
		return nil, SpansOut{Suggestion: fmt.Sprintf("Invalid window: %s", err)}, nil
	}

	limit := clampInt(in.Limit, minLimit, maxLimit, 100)

	result, err := s.svc.Spans(ctx, service.SpanParams{
		Query:            in.Query,
		Operation:        in.Operation,
		Service:          in.Service,
		Status:           in.Status,
		Kind:             in.Kind,
		MinDurationMs:    in.MinDurationMs,
		MaxDurationMs:    in.MaxDurationMs,
		Attrs:            in.Attrs,
		GroupBy:          in.GroupBy,
		OrderBy:          in.OrderBy,
		IncludeExemplars: in.IncludeExemplars,
		Window:           tw.Minutes,
		Namespace:        in.Namespace,
		Limit:            limit,
	})
	if err != nil {
		slog.Warn("spans tool failed", "err", err)
		return nil, SpansOut{
			Spans:      []SpanRowOut{},
			Groups:     []SpanGroupOut{},
			Suggestion: fmt.Sprintf("Query failed: %s", err),
		}, nil
	}

	out := SpansOut{}

	if len(in.GroupBy) > 0 {
		out.Groups = make([]SpanGroupOut, 0, len(result.Groups))
		for _, g := range result.Groups {
			out.Groups = append(out.Groups, SpanGroupOut{
				Key:              g.Key,
				Count:            g.Count,
				ErrorCount:       g.ErrorCount,
				ErrorRate:        g.ErrorRate,
				P50Ms:            g.P50Ms,
				P95Ms:            g.P95Ms,
				P99Ms:            g.P99Ms,
				ExemplarTraceIDs: g.ExemplarTraceIDs,
			})
		}
		out.TotalGroups = result.TotalGroups
		if len(out.Groups) == 0 {
			out.Suggestion = "No groups found. Try widening the time window or adjusting filters."
		}
	} else {
		out.Spans = make([]SpanRowOut, 0, len(result.Spans))
		for _, sp := range result.Spans {
			out.Spans = append(out.Spans, SpanRowOut{
				TraceID:    sp.TraceID,
				SpanID:     sp.SpanID,
				Service:    sp.Service,
				Operation:  sp.Operation,
				Kind:       sp.Kind,
				StartTime:  sp.StartTime,
				DurationMs: sp.DurationMs,
				Status:     sp.Status,
				Attributes: sp.Attributes,
			})
		}
		out.TotalMatched = result.TotalMatched
		out.Returned = len(out.Spans)

		if len(out.Spans) == 0 {
			out.Suggestion = "No spans found. Try widening the time window or adjusting filters."
		} else if out.TotalMatched > limit {
			out.Suggestion = fmt.Sprintf("Showing %d of %d+ spans. Use filters or group_by to narrow results.", out.Returned, out.TotalMatched)
		} else if len(out.Spans) > 0 {
			out.Suggestion = fmt.Sprintf("Use trace tool with trace_id '%s' for full request context.", out.Spans[0].TraceID)
		}
	}

	return nil, out, nil
}
