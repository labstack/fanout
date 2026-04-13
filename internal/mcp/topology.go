package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// topology - Service map with health indicators

type TopologyIn struct {
	Window          string `json:"window,omitempty" jsonschema:"Time window: '15m','1h','7d' or ISO range start/end,default=60m"`
	EdgeType        string `json:"edge_type,omitempty" jsonschema:"Filter edges by type: 'call', 'messaging', 'all' (default: all)"`
	Depth           int    `json:"depth,omitempty" jsonschema:"BFS depth limit from service (0 = unlimited)"`
	Service         string `json:"service,omitempty" jsonschema:"Focus service for depth-limited graph"`
	IncludeInactive bool   `json:"include_inactive,omitempty" jsonschema:"Include services with no recent traffic"`
	Namespace       string `json:"namespace,omitempty" jsonschema:"Filter by namespace"`
	TenantID        string `json:"tenant_id,omitempty" jsonschema:"Filter by tenant"`
}

type ServiceNode struct {
	Service         string  `json:"service"`
	Status          string  `json:"status"`
	Requests        int64   `json:"requests"`
	P50Ms           float64 `json:"p50_ms"`
	P95Ms           float64 `json:"p95_ms"`
	ErrorRate       float64 `json:"error_rate"`
	UpstreamCount   int     `json:"upstream_count"`
	DownstreamCount int     `json:"downstream_count"`
	BlastRadius     float64 `json:"blast_radius"`
}

type ServiceEdge struct {
	Source    string  `json:"source"`
	Target    string  `json:"target"`
	EdgeType  string  `json:"edge_type,omitempty"`
	Calls     int64   `json:"calls"`
	AvgMs     float64 `json:"avg_ms"`
	ErrorRate float64 `json:"error_rate"`
}

type TopologyOut struct {
	WindowMinutes int           `json:"window_minutes"`
	Nodes         []ServiceNode `json:"nodes"`
	Edges         []ServiceEdge `json:"edges"`
	CriticalPaths [][]string    `json:"critical_paths"`
}

func (s *Server) topology(ctx context.Context, req *mcp.CallToolRequest, in TopologyIn) (*mcp.CallToolResult, TopologyOut, error) {
	tw, err := parseWindow(in.Window)
	if err != nil {
		return nil, TopologyOut{}, fmt.Errorf("invalid window: %w", err)
	}
	window := clampInt(tw.Minutes, minWindow, maxWindow, 60)

	edgeType := in.EdgeType
	if edgeType == "" {
		edgeType = "all"
	}

	result, err := s.svc.TopologyWithParams(ctx, service.TopologyParams{
		Window:          window,
		EdgeType:        edgeType,
		Depth:           in.Depth,
		Service:         in.Service,
		IncludeInactive: in.IncludeInactive,
		Namespace:       in.Namespace,
		TenantID:        in.TenantID,
	})
	if err != nil {
		return nil, TopologyOut{}, fmt.Errorf("topology query failed: %w", err)
	}

	out := TopologyOut{
		WindowMinutes: window,
		Nodes:         make([]ServiceNode, 0, len(result.Nodes)),
		Edges:         make([]ServiceEdge, 0, len(result.Edges)),
		CriticalPaths: result.CriticalPaths,
	}
	if out.CriticalPaths == nil {
		out.CriticalPaths = [][]string{}
	}

	for _, n := range result.Nodes {
		out.Nodes = append(out.Nodes, ServiceNode{
			Service:         n.Name,
			Status:          n.Status,
			Requests:        n.SpanCount,
			P50Ms:           n.P50Ms,
			P95Ms:           n.P95Ms,
			ErrorRate:       n.ErrorRate,
			UpstreamCount:   n.UpstreamCount,
			DownstreamCount: n.DownstreamCount,
			BlastRadius:     n.BlastRadius,
		})
	}

	for _, e := range result.Edges {
		out.Edges = append(out.Edges, ServiceEdge{
			Source:    e.From,
			Target:    e.To,
			EdgeType:  e.EdgeType,
			Calls:     e.CallCount,
			AvgMs:     e.AvgMs,
			ErrorRate: e.ErrorRate,
		})
	}

	return nil, out, nil
}
