package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// OverviewIn holds parameters for the overview tool.
type OverviewIn struct {
	Window         string   `json:"window,omitempty"          jsonschema:"Time window: '15m', '1h', '7d', or ISO range 'start/end'. Default: 15m"`
	Include        []string `json:"include,omitempty"         jsonschema:"Sections to include: 'health', 'services', 'issues'. Default: all"`
	SortServicesBy string   `json:"sort_services_by,omitempty" jsonschema:"Sort order: 'severity', 'error_rate', 'latency', 'throughput'. Default: severity"`
	Namespace      string   `json:"namespace,omitempty"       jsonschema:"Filter by namespace"`
	Limit          int      `json:"limit,omitempty"           jsonschema:"Max services to return. Default: 100"`
}

// OverviewHealth is the MCP output type for global health metrics.
type OverviewHealth struct {
	Score            float64        `json:"score"`
	TotalServices    int            `json:"total_services"`
	ByStatus         map[string]int `json:"by_status"`
	ThroughputPerMin float64        `json:"throughput_per_min"`
	GlobalErrorRate  float64        `json:"global_error_rate"`
	GlobalP95Ms      float64        `json:"global_p95_ms"`
}

// OverviewService is the MCP output type for per-service metrics.
type OverviewService struct {
	Service   string  `json:"service"`
	Status    string  `json:"status"`
	Requests  int64   `json:"requests"`
	ErrorRate float64 `json:"error_rate"`
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
}

// OverviewIssue is the MCP output type for a detected service issue.
type OverviewIssue struct {
	Service   string  `json:"service"`
	Issue     string  `json:"issue"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Since     string  `json:"since,omitempty"`
}

// OverviewOut is the full MCP output for the overview tool.
type OverviewOut struct {
	Timestamp string            `json:"timestamp"`
	Window    string            `json:"window"`
	Health    *OverviewHealth   `json:"health,omitempty"`
	Services  []OverviewService `json:"services,omitempty"`
	TopIssues []OverviewIssue   `json:"top_issues,omitempty"`
}

func (s *Server) overview(ctx context.Context, req *mcp.CallToolRequest, in OverviewIn) (*mcp.CallToolResult, OverviewOut, error) {
	tw, err := parseWindow(in.Window)
	if err != nil {
		return nil, OverviewOut{}, fmt.Errorf("invalid window: %w", err)
	}

	window := tw.Minutes
	if window <= 0 {
		window = defWindow
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}

	result, err := s.svc.Overview(ctx, window, in.Include, in.SortServicesBy, in.Namespace, limit)
	if err != nil {
		return nil, OverviewOut{}, fmt.Errorf("overview query failed: %w", err)
	}

	// Map service types to MCP output types
	out := OverviewOut{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Window:    tw.String(),
		Services:  make([]OverviewService, 0, len(result.Services)),
		TopIssues: make([]OverviewIssue, 0, len(result.Issues)),
	}

	includeAll := len(in.Include) == 0
	if includeAll || containsOverviewSection(in.Include, "health") {
		out.Health = &OverviewHealth{
			Score:            result.Health.Score,
			TotalServices:    result.Health.TotalServices,
			ByStatus:         result.Health.ByStatus,
			ThroughputPerMin: result.Health.ThroughputPerMin,
			GlobalErrorRate:  result.Health.GlobalErrorRate,
			GlobalP95Ms:      result.Health.GlobalP95Ms,
		}
	}

	for _, svc := range result.Services {
		out.Services = append(out.Services, OverviewService{
			Service:   svc.Service,
			Status:    svc.Status,
			Requests:  svc.Requests,
			ErrorRate: svc.ErrorRate,
			P50Ms:     svc.P50Ms,
			P95Ms:     svc.P95Ms,
		})
	}

	for _, issue := range result.Issues {
		out.TopIssues = append(out.TopIssues, OverviewIssue{
			Service:   issue.Service,
			Issue:     issue.Issue,
			Value:     issue.Value,
			Threshold: issue.Threshold,
			Since:     issue.Since,
		})
	}

	return nil, out, nil
}

// String returns a human-readable window string for output.
func (tw TimeWindow) String() string {
	minutes := tw.Minutes
	if minutes == 0 {
		return "15m"
	}
	if minutes%1440 == 0 {
		return fmt.Sprintf("%dd", minutes/1440)
	}
	if minutes%60 == 0 {
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}

func containsOverviewSection(include []string, target string) bool {
	for _, section := range include {
		if section == target {
			return true
		}
	}
	return false
}
