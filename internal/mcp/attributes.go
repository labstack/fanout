package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AttributesIn holds input parameters for the attributes tool.
type AttributesIn struct {
	Signal    string `json:"signal,omitempty"    jsonschema:"Signal to discover: spans|logs|metrics. Default: spans"`
	Service   string `json:"service,omitempty"   jsonschema:"Filter by service"`
	Operation string `json:"operation,omitempty" jsonschema:"Filter by operation (spans only)"`
	Window    string `json:"window,omitempty"    jsonschema:"Time window: '15m','1h','7d'. Default: 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Filter by namespace"`
	Tenant    string `json:"tenant,omitempty"    jsonschema:"Filter by tenant"`
	Limit     int    `json:"limit,omitempty"     jsonschema:"Max attribute keys to return. Default: 50"`
}

// AttributeOut describes a discovered attribute.
type AttributeOut struct {
	Key         string   `json:"key"`
	Count       int64    `json:"count"`
	Cardinality int64    `json:"cardinality"`
	Samples     []string `json:"samples"`
}

// AttributesOut is the response envelope.
type AttributesOut struct {
	Signal             string         `json:"signal"`
	TotalRows          int64          `json:"total_rows"`
	Attributes         []AttributeOut `json:"attributes"`
	ResourceAttributes []AttributeOut `json:"resource_attributes"`
	Suggestion         string         `json:"suggestion,omitempty"`
}

func (s *Server) attributes(ctx context.Context, req *mcp.CallToolRequest, in AttributesIn) (*mcp.CallToolResult, any, error) {
	tw, err := parseWindow(in.Window)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid window: %w", err)
	}

	// Default to 1h for discovery (need enough data for meaningful cardinality)
	window := tw.Minutes
	if window == 0 {
		window = 60
	}

	limit := clampInt(in.Limit, 1, 200, 50)

	p := service.AttributeParams{
		Signal:    in.Signal,
		Service:   in.Service,
		Operation: in.Operation,
		Window:    window,
		Namespace: in.Namespace,
		TenantID:  in.Tenant,
		Limit:     limit,
	}

	result, err := s.svc.Attributes(ctx, p)
	if err != nil {
		slog.Warn("attributes discovery failed", "err", err)
		return nil, AttributesOut{
			Signal:             p.Signal,
			Attributes:         []AttributeOut{},
			ResourceAttributes: []AttributeOut{},
			Suggestion:         fmt.Sprintf("Discovery failed: %s", err),
		}, nil
	}

	out := AttributesOut{
		Signal:             result.Signal,
		TotalRows:          result.TotalSpans,
		Attributes:         make([]AttributeOut, 0, len(result.Attributes)),
		ResourceAttributes: make([]AttributeOut, 0, len(result.ResourceAttributes)),
	}

	for _, a := range result.Attributes {
		out.Attributes = append(out.Attributes, AttributeOut{
			Key:         a.Key,
			Count:       a.Count,
			Cardinality: a.Cardinality,
			Samples:     a.Samples,
		})
	}
	for _, a := range result.ResourceAttributes {
		out.ResourceAttributes = append(out.ResourceAttributes, AttributeOut{
			Key:         a.Key,
			Count:       a.Count,
			Cardinality: a.Cardinality,
			Samples:     a.Samples,
		})
	}

	if len(out.Attributes) == 0 && len(out.ResourceAttributes) == 0 {
		out.Suggestion = "No attributes found. Try widening the time window or removing service/operation filters."
	} else {
		out.Suggestion = fmt.Sprintf("Found %d attribute(s) and %d resource attribute(s). Use attrs={\"key\":\"value\"} on spans/logs/metrics tools to filter.", len(out.Attributes), len(out.ResourceAttributes))
	}

	return nil, out, nil
}
