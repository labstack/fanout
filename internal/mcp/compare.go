package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// compare - Side-by-side service comparison with multiple modes

type CompareIn struct {
	Mode     string            `json:"mode,omitempty" jsonschema:"Comparison mode: services, time, operations,default=services"`
	Services []string          `json:"services,omitempty" jsonschema:"Services to compare (2-4) for services mode"`
	Service  string            `json:"service,omitempty" jsonschema:"Service for time/operations mode"`
	Left     map[string]string `json:"left,omitempty" jsonschema:"Left side config: window (ISO range) for time mode, operation for operations mode"`
	Right    map[string]string `json:"right,omitempty" jsonschema:"Right side config: window (ISO range) for time mode, operation for operations mode"`
	Focus    []string          `json:"focus,omitempty" jsonschema:"Metrics to compare,default=[latency,errors,throughput]"`
	Window   string            `json:"window,omitempty" jsonschema:"Time window for services mode,default=1h"`
}

// CompareMetrics is the JSON-serializable representation of per-service metrics
// in the MCP compare response.
type CompareMetrics struct {
	Service    string  `json:"service"`
	Requests   int64   `json:"requests"`
	ErrorRate  float64 `json:"error_rate"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	AvgMs      float64 `json:"avg_ms"`
	ErrorCount int64   `json:"error_count"`
}

// CompareMetricDiff is the JSON-serializable representation of a metric difference
// in the MCP compare response.
type CompareMetricDiff struct {
	LeftValue                float64 `json:"left_value"`
	RightValue               float64 `json:"right_value"`
	ChangePct                float64 `json:"change_pct"`
	Direction                string  `json:"direction"` // "regression", "improvement", "stable"
	StatisticallySignificant bool    `json:"statistically_significant"`
}

type CompareOut struct {
	// Services mode (existing)
	Services []CompareMetrics `json:"services,omitempty"`
	Winner   string           `json:"winner,omitempty"`
	Summary  string           `json:"summary,omitempty"`

	// All modes
	Mode       string                       `json:"mode"`
	LeftLabel  string                        `json:"left_label,omitempty"`
	RightLabel string                        `json:"right_label,omitempty"`
	Comparison map[string]CompareMetricDiff  `json:"comparison,omitempty"`
	Verdict    string                        `json:"verdict,omitempty"`
}

func (s *Server) compare(ctx context.Context, req *mcp.CallToolRequest, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	mode := in.Mode
	if mode == "" {
		mode = "services"
	}

	switch mode {
	case "services":
		return s.compareServices(ctx, in)
	case "time":
		return s.compareTime(ctx, in)
	case "operations":
		return s.compareOperations(ctx, in)
	default:
		return nil, CompareOut{}, fmt.Errorf("invalid mode %q: must be services, time, or operations", mode)
	}
}

// compareServices handles the services-mode comparison.
func (s *Server) compareServices(ctx context.Context, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	// Parse window string; default to 1h
	windowStr := in.Window
	if windowStr == "" {
		windowStr = "1h"
	}
	tw, err := parseWindow(windowStr)
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid window: %w", err)
	}
	window := clampInt(tw.Minutes, minWindow, maxWindow, 60)

	result, err := s.svc.CompareServices(ctx, service.CompareServicesParams{
		Services: in.Services,
		Window:   window,
	})
	if err != nil {
		return nil, CompareOut{}, err
	}

	// Map service types to MCP types
	metrics := make([]CompareMetrics, len(result.Services))
	for i, m := range result.Services {
		metrics[i] = CompareMetrics{
			Service:    m.Service,
			Requests:   m.Requests,
			ErrorRate:  m.ErrorRate,
			P50Ms:      m.P50Ms,
			P95Ms:      m.P95Ms,
			AvgMs:      m.AvgMs,
			ErrorCount: m.ErrorCount,
		}
	}

	out := CompareOut{
		Mode:     "services",
		Services: metrics,
		Winner:   result.Winner,
		Summary:  result.Summary,
	}

	return nil, out, nil
}

// compareTime compares the same service across two time windows.
func (s *Server) compareTime(ctx context.Context, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	if in.Service == "" {
		return nil, CompareOut{}, fmt.Errorf("service is required for time mode")
	}
	if in.Left == nil || in.Left["window"] == "" {
		return nil, CompareOut{}, fmt.Errorf("left.window is required for time mode (ISO range: start/end)")
	}
	if in.Right == nil || in.Right["window"] == "" {
		return nil, CompareOut{}, fmt.Errorf("right.window is required for time mode (ISO range: start/end)")
	}

	leftTW, err := parseWindow(in.Left["window"])
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid left.window: %w", err)
	}
	rightTW, err := parseWindow(in.Right["window"])
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid right.window: %w", err)
	}

	leftLabel := fmt.Sprintf("Before (%s)", formatWindowLabel(leftTW))
	rightLabel := fmt.Sprintf("After (%s)", formatWindowLabel(rightTW))

	result, err := s.svc.CompareTime(ctx, service.CompareTimeParams{
		Service: in.Service,
		Left:    service.TimeRange{Start: leftTW.Start, End: leftTW.End},
		Right:   service.TimeRange{Start: rightTW.Start, End: rightTW.End},
		Focus:   in.Focus,
	})
	if err != nil {
		return nil, CompareOut{}, err
	}

	comparison := mapMetricDiffs(result.Comparison)
	verdict := service.BuildVerdict(result.Comparison)

	out := CompareOut{
		Mode:       "time",
		LeftLabel:  leftLabel,
		RightLabel: rightLabel,
		Comparison: comparison,
		Verdict:    verdict,
	}

	return nil, out, nil
}

// compareOperations compares two operations within the same service.
func (s *Server) compareOperations(ctx context.Context, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	if in.Service == "" {
		return nil, CompareOut{}, fmt.Errorf("service is required for operations mode")
	}
	if in.Left == nil || in.Left["operation"] == "" {
		return nil, CompareOut{}, fmt.Errorf("left.operation is required for operations mode")
	}
	if in.Right == nil || in.Right["operation"] == "" {
		return nil, CompareOut{}, fmt.Errorf("right.operation is required for operations mode")
	}

	leftOp := in.Left["operation"]
	rightOp := in.Right["operation"]

	windowStr := in.Window
	if windowStr == "" {
		windowStr = "1h"
	}
	tw, err := parseWindow(windowStr)
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid window: %w", err)
	}

	result, err := s.svc.CompareOperations(ctx, service.CompareOperationsParams{
		Service:        in.Service,
		LeftOperation:  leftOp,
		RightOperation: rightOp,
		Window:         tw.Minutes,
		Focus:          in.Focus,
	})
	if err != nil {
		return nil, CompareOut{}, err
	}

	comparison := mapMetricDiffs(result.Comparison)
	verdict := service.BuildVerdict(result.Comparison)

	out := CompareOut{
		Mode:       "operations",
		LeftLabel:  leftOp,
		RightLabel: rightOp,
		Comparison: comparison,
		Verdict:    verdict,
	}

	return nil, out, nil
}

// --- Presentation-level helpers (stay in MCP layer) ---

// formatWindowLabel formats a TimeWindow as a short human-readable label.
func formatWindowLabel(tw TimeWindow) string {
	start := tw.Start.Format("15:04")
	end := tw.End.Format("15:04")
	return fmt.Sprintf("%s\u2013%s", start, end)
}

// mapMetricDiffs converts service.MetricDiff map to MCP CompareMetricDiff map.
func mapMetricDiffs(src map[string]service.MetricDiff) map[string]CompareMetricDiff {
	out := make(map[string]CompareMetricDiff, len(src))
	for k, v := range src {
		out[k] = CompareMetricDiff{
			LeftValue:                v.LeftValue,
			RightValue:               v.RightValue,
			ChangePct:                v.ChangePct,
			Direction:                v.Direction,
			StatisticallySignificant: v.StatisticallySignificant,
		}
	}
	return out
}
