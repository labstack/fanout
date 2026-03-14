package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// status - The entry point. Zero params needed.

type StatusIn struct {
	Window    int    `json:"window,omitempty" jsonschema:"Time window in minutes,default=15"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Filter by namespace"`
	TenantID  string `json:"tenant_id,omitempty" jsonschema:"Filter by tenant"`
}

type ServiceSummary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Degraded  int `json:"degraded"`
	Unhealthy int `json:"unhealthy"`
}

type TopIssue struct {
	Service string  `json:"service"`
	Issue   string  `json:"issue"`
	Value   float64 `json:"value"`
	Detail  string  `json:"detail,omitempty"`
}

type StatusOut struct {
	Healthy          bool           `json:"healthy"`
	Summary          string         `json:"summary"`
	Services         ServiceSummary `json:"services"`
	TopIssues        []TopIssue     `json:"top_issues"`
	ThroughputPerMin int64          `json:"throughput_per_min"`
	P95Ms            float64        `json:"p95_ms"`
	ErrorRate        float64        `json:"error_rate"`
}

func (s *Server) status(ctx context.Context, req *mcp.CallToolRequest, in StatusIn) (*mcp.CallToolResult, StatusOut, error) {
	window := clampInt(in.Window, minWindow, maxWindow, defWindow)
	result, err := s.svc.Status(ctx, window, in.Namespace, in.TenantID)
	if err != nil {
		return nil, StatusOut{
			Healthy:   true,
			Summary:   "Error getting status",
			Services:  ServiceSummary{},
			TopIssues: []TopIssue{},
		}, nil
	}

	// Convert service types to MCP output types
	out := StatusOut{
		Healthy:          result.Healthy,
		Summary:          result.Summary,
		ThroughputPerMin: result.ThroughputPerMin,
		P95Ms:            result.P95Ms,
		ErrorRate:        result.ErrorRate,
		Services: ServiceSummary{
			Total:     result.Services.Total,
			Healthy:   result.Services.Healthy,
			Degraded:  result.Services.Degraded,
			Unhealthy: result.Services.Unhealthy,
		},
		TopIssues: make([]TopIssue, 0, len(result.TopIssues)),
	}

	for _, ti := range result.TopIssues {
		out.TopIssues = append(out.TopIssues, TopIssue{
			Service: ti.Service,
			Issue:   ti.Issue,
			Value:   ti.Value,
			Detail:  ti.Detail,
		})
	}

	return nil, out, nil
}
