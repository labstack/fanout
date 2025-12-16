package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// status - The entry point. Zero params needed.

type StatusIn struct {
	Window int    `json:"window,omitempty" jsonschema:"Time window in minutes,default=15"`
	Format string `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
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
	Render           *render.Output `json:"render,omitempty"`
}

func (s *Server) status(ctx context.Context, req *mcp.CallToolRequest, in StatusIn) (*mcp.CallToolResult, StatusOut, error) {
	result, err := s.svc.Status(ctx, in.Window)
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

	// Render output if format requested
	format := parseFormat(in.Format)
	if format != render.Data {
		rendered := renderStatus(&out)
		out.Render = &rendered
	}

	return nil, out, nil
}

func renderStatus(s *StatusOut) render.Output {
	// Health badge
	status := "healthy"
	if !s.Healthy {
		status = "unhealthy"
	}
	badge := &render.Badge{Label: status, Status: status}

	// Metrics
	metrics := &render.Grid{
		Cols: 3,
		Items: []render.Renderer{
			&render.Metric{Label: "Throughput", Value: fmt.Sprintf("%d", s.ThroughputPerMin), Unit: "/min"},
			&render.Metric{Label: "P95 Latency", Value: fmt.Sprintf("%.1f", s.P95Ms), Unit: "ms"},
			&render.Metric{Label: "Error Rate", Value: fmt.Sprintf("%.2f", s.ErrorRate*100), Unit: "%"},
		},
	}

	// Services summary
	services := &render.Grid{
		Cols: 4,
		Items: []render.Renderer{
			&render.Metric{Label: "Total", Value: fmt.Sprintf("%d", s.Services.Total)},
			&render.Metric{Label: "Healthy", Value: fmt.Sprintf("%d", s.Services.Healthy)},
			&render.Metric{Label: "Degraded", Value: fmt.Sprintf("%d", s.Services.Degraded)},
			&render.Metric{Label: "Unhealthy", Value: fmt.Sprintf("%d", s.Services.Unhealthy)},
		},
	}

	// Top issues table
	var issueRows [][]string
	for _, ti := range s.TopIssues {
		issueRows = append(issueRows, []string{
			ti.Service,
			ti.Issue,
			fmt.Sprintf("%.2f", ti.Value),
		})
	}
	issues := &render.Table{
		Title:   "Top Issues",
		Headers: []string{"Service", "Issue", "Value"},
		Rows:    issueRows,
	}

	// Compose all
	composed := &render.Compose{
		Vertical: true,
		Items: []render.Renderer{
			badge,
			&render.Text{Content: s.Summary},
			metrics,
			&render.Panel{Title: "Services", Content: []render.Renderer{services}},
			issues,
		},
	}

	return composed.Render(render.Both)
}

func parseFormat(f string) render.Format {
	switch f {
	case "html":
		return render.HTML
	case "both":
		return render.Both
	case "data":
		return render.Data
	default:
		return render.ASCII
	}
}
