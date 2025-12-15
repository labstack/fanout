package mcp

import (
	"context"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// topology - Service map with health indicators

type TopologyIn struct {
	Window int `json:"window,omitempty" jsonschema:"Time window in minutes,default=60"`
}

type ServiceNode struct {
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	SpanCount int64   `json:"span_count"`
	P95Ms     float64 `json:"p95_ms"`
	ErrorRate float64 `json:"error_rate"`
}

type ServiceEdge struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	CallCount int64   `json:"call_count"`
	AvgMs     float64 `json:"avg_ms"`
	ErrorRate float64 `json:"error_rate"`
	Status    string  `json:"status"`
}

type TopologyOut struct {
	Nodes        []ServiceNode `json:"nodes"`
	Edges        []ServiceEdge `json:"edges"`
	ServiceCount int           `json:"service_count"`
	EdgeCount    int           `json:"edge_count"`
}

func (s *Server) topology(ctx context.Context, req *mcp.CallToolRequest, in TopologyIn) (*mcp.CallToolResult, TopologyOut, error) {
	result, err := s.svc.Topology(ctx, in.Window)
	if err != nil {
		return nil, TopologyOut{Nodes: []ServiceNode{}, Edges: []ServiceEdge{}}, nil
	}

	out := TopologyOut{
		Nodes:        make([]ServiceNode, 0, len(result.Nodes)),
		Edges:        make([]ServiceEdge, 0, len(result.Edges)),
		ServiceCount: len(result.Nodes),
		EdgeCount:    len(result.Edges),
	}

	for _, n := range result.Nodes {
		out.Nodes = append(out.Nodes, ServiceNode{
			Name:      n.Name,
			Status:    n.Status,
			SpanCount: n.SpanCount,
			P95Ms:     n.P95Ms,
			ErrorRate: n.ErrorRate,
		})
	}

	for _, e := range result.Edges {
		out.Edges = append(out.Edges, ServiceEdge{
			From:      e.From,
			To:        e.To,
			CallCount: e.CallCount,
			AvgMs:     e.AvgMs,
			ErrorRate: e.ErrorRate,
			Status:    service.DeriveHealth(e.ErrorRate, e.AvgMs),
		})
	}

	return nil, out, nil
}
