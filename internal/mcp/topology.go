package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/render"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// topology - Service map with health indicators

type TopologyIn struct {
	Window    int    `json:"window,omitempty" jsonschema:"Time window in minutes,default=60"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Filter by namespace"`
	TenantID  string `json:"tenant_id,omitempty" jsonschema:"Filter by tenant"`
	Format    string `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
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
	Nodes        []ServiceNode  `json:"nodes"`
	Edges        []ServiceEdge  `json:"edges"`
	ServiceCount int            `json:"service_count"`
	EdgeCount    int            `json:"edge_count"`
	Render       *render.Output `json:"render,omitempty"`
}

func (s *Server) topology(ctx context.Context, req *mcp.CallToolRequest, in TopologyIn) (*mcp.CallToolResult, TopologyOut, error) {
	window := clampInt(in.Window, minWindow, maxWindow, 60) // default 60 for topology
	result, err := s.svc.Topology(ctx, window, in.Namespace, in.TenantID)
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

	// Render output
	format := parseFormat(in.Format)
	if format != render.Data {
		rendered := renderTopology(&out)
		out.Render = &rendered
	}

	return nil, out, nil
}

func renderTopology(t *TopologyOut) render.Output {
	var items []render.Renderer

	// Summary
	healthy := 0
	degraded := 0
	unhealthy := 0
	for _, n := range t.Nodes {
		switch n.Status {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		default:
			unhealthy++
		}
	}

	summary := &render.Grid{
		Cols: 4,
		Items: []render.Renderer{
			&render.Metric{Label: "Services", Value: fmt.Sprintf("%d", t.ServiceCount)},
			&render.Badge{Label: fmt.Sprintf("%d healthy", healthy), Status: "healthy"},
			&render.Badge{Label: fmt.Sprintf("%d degraded", degraded), Status: "degraded"},
			&render.Badge{Label: fmt.Sprintf("%d unhealthy", unhealthy), Status: "unhealthy"},
		},
	}
	items = append(items, summary)

	// Services table
	var nodeRows [][]string
	for _, n := range t.Nodes {
		nodeRows = append(nodeRows, []string{
			n.Name,
			n.Status,
			fmt.Sprintf("%.1fms", n.P95Ms),
			fmt.Sprintf("%.2f%%", n.ErrorRate*100),
			fmt.Sprintf("%d", n.SpanCount),
		})
	}
	servicesTable := &render.Table{
		Title:   "Services",
		Headers: []string{"Name", "Status", "P95", "Errors", "Spans"},
		Rows:    nodeRows,
	}
	items = append(items, servicesTable)

	// Dependencies table
	if len(t.Edges) > 0 {
		var edgeRows [][]string
		for _, e := range t.Edges {
			edgeRows = append(edgeRows, []string{
				e.From,
				"→",
				e.To,
				fmt.Sprintf("%d", e.CallCount),
				fmt.Sprintf("%.1fms", e.AvgMs),
				fmt.Sprintf("%.2f%%", e.ErrorRate*100),
			})
		}
		edgesTable := &render.Table{
			Title:   "Dependencies",
			Headers: []string{"From", "", "To", "Calls", "Avg", "Errors"},
			Rows:    edgeRows,
		}
		items = append(items, edgesTable)
	}

	composed := &render.Compose{Vertical: true, Items: items}
	return composed.Render(render.Both)
}
