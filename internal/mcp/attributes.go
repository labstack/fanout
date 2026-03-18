package mcp

import (
	"context"
	"fmt"

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
	Key             string   `json:"key"`
	Count           int64    `json:"count"`
	Cardinality     int64    `json:"cardinality"`
	Samples         []string `json:"samples"`
	DiscoveryMethod string   `json:"discovery_method,omitempty"`
}

// AttributesOut is the response envelope.
type AttributesOut struct {
	Signal             string         `json:"signal"`
	TotalRows          int64          `json:"total_rows"`
	Attributes         []AttributeOut `json:"attributes"`
	ResourceAttributes []AttributeOut `json:"resource_attributes"`
	Warnings           []string       `json:"warnings,omitempty"`
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
		return nil, nil, fmt.Errorf("attributes discovery: %w", err)
	}

	out := AttributesOut{
		Signal:             result.Signal,
		TotalRows:          result.TotalRows,
		Attributes:         convertAttributes(result.Attributes),
		ResourceAttributes: convertAttributes(result.ResourceAttributes),
		Warnings:           result.Warnings,
	}

	if len(out.Attributes) == 0 && len(out.ResourceAttributes) == 0 {
		out.Suggestion = "No attributes found. Try widening the time window or removing service/operation filters."
	} else if result.Signal == "spans" {
		out.Suggestion = fmt.Sprintf("Found %d attribute(s) and %d resource attribute(s) from pre-extracted columns. Additional attributes may exist in attributes_json — use query tool with json_keys() to discover them. Use attrs={\"key\":\"value\"} on spans/logs/metrics tools to filter.", len(out.Attributes), len(out.ResourceAttributes))
	} else {
		out.Suggestion = fmt.Sprintf("Found %d attribute(s) and %d resource attribute(s) from 1000-row sample (counts are approximate). Use attrs={\"key\":\"value\"} on spans/logs/metrics tools to filter.", len(out.Attributes), len(out.ResourceAttributes))
	}

	return nil, out, nil
}

// convertAttributes maps service-layer AttributeInfo values to MCP-layer AttributeOut values.
func convertAttributes(infos []service.AttributeInfo) []AttributeOut {
	out := make([]AttributeOut, len(infos))
	for i, a := range infos {
		out[i] = AttributeOut{
			Key:             a.Key,
			Count:           a.Count,
			Cardinality:     a.Cardinality,
			Samples:         a.Samples,
			DiscoveryMethod: a.DiscoveryMethod,
		}
	}
	return out
}
