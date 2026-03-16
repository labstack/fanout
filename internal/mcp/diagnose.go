package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// diagnose - Service deep-dive with root cause analysis

type DiagnoseIn struct {
	Service   string `json:"service" jsonschema:"Service name to diagnose"`
	Window    string `json:"window,omitempty" jsonschema:"Time window: duration (15m, 1h, 7d) or ISO range,default=15m"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Filter by namespace"`
	TenantID  string `json:"tenant_id,omitempty" jsonschema:"Filter by tenant"`
	Symptom   string `json:"symptom,omitempty" jsonschema:"Focus diagnosis on: latency, errors, throughput_drop, or auto (default)"`
}

type ServiceMetrics struct {
	P50Ms                float64             `json:"p50_ms"`
	P95Ms                float64             `json:"p95_ms"`
	P99Ms                float64             `json:"p99_ms"`
	ErrorRate            float64             `json:"error_rate"`
	Count                int64               `json:"request_count"`
	ComparisonToBaseline *BaselineComparison `json:"comparison_to_baseline,omitempty"`
}

// BaselineComparison compares current P95 against historical same-time-of-day averages.
type BaselineComparison struct {
	P95Ratio       float64 `json:"p95_ratio"`
	BaselineP95Ms  float64 `json:"baseline_p95_ms"`
	BaselineWindow string  `json:"baseline_window"`
}

// ChangePoint represents a statistically significant metric jump.
type ChangePoint struct {
	Time   string  `json:"time"`
	Metric string  `json:"metric"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
}

// LogPattern describes a recurring log message pattern near a change point.
type LogPattern struct {
	Pattern  string `json:"pattern"`
	Count    int64  `json:"count"`
	Severity string `json:"severity"`
}

type ErrorDetail struct {
	Operation     string `json:"operation"`
	Message       string `json:"message"`
	ExceptionType string `json:"exception_type,omitempty"`
	Count         int64  `json:"count"`
	ExampleTrace  string `json:"example_trace,omitempty"`
}

type SlowOperation struct {
	Name  string  `json:"name"`
	P95Ms float64 `json:"p95_ms"`
	Count int64   `json:"count"`
}

type Dependency struct {
	Service   string  `json:"service"`
	Status    string  `json:"status"`
	ErrorRate float64 `json:"error_rate"`
	AvgMs     float64 `json:"avg_ms"`
	Calls     int64   `json:"calls"`
}

type DiagnoseOut struct {
	Service               string          `json:"service"`
	Status                string          `json:"status"`
	SymptomDetected       string          `json:"symptom_detected,omitempty"`
	Metrics               ServiceMetrics  `json:"metrics"`
	TopErrors             []ErrorDetail   `json:"top_errors"`
	SlowOperations        []SlowOperation `json:"slow_operations"`
	Dependencies          []Dependency    `json:"dependencies"`
	ChangePoints          []ChangePoint   `json:"change_points,omitempty"`
	CorrelatedLogPatterns []LogPattern    `json:"correlated_log_patterns,omitempty"`
	SuggestedTraces       []string        `json:"suggested_traces,omitempty"`
}

func (s *Server) diagnose(ctx context.Context, req *mcp.CallToolRequest, in DiagnoseIn) (*mcp.CallToolResult, DiagnoseOut, error) {
	if in.Service == "" {
		return nil, DiagnoseOut{}, fmt.Errorf("service is required")
	}

	tw, err := parseWindow(in.Window)
	if err != nil {
		return nil, DiagnoseOut{}, fmt.Errorf("invalid window: %w", err)
	}
	window := clampInt(tw.Minutes, minWindow, maxWindow, defWindow)
	result, err := s.svc.DiagnoseEnhanced(ctx, in.Service, window, in.Symptom, in.Namespace, in.TenantID)
	if err != nil {
		return nil, DiagnoseOut{}, fmt.Errorf("diagnose failed for %s: %w", in.Service, err)
	}

	metrics := ServiceMetrics{
		P50Ms:     result.P50Ms,
		P95Ms:     result.P95Ms,
		P99Ms:     result.P99Ms,
		ErrorRate: result.ErrorRate,
		Count:     result.SpanCount,
	}
	if result.Baseline != nil {
		p95Ratio := 0.0
		if result.Baseline.BaselineP95Ms > 0 {
			p95Ratio = result.P95Ms / result.Baseline.BaselineP95Ms
		}
		metrics.ComparisonToBaseline = &BaselineComparison{
			P95Ratio:       p95Ratio,
			BaselineP95Ms:  result.Baseline.BaselineP95Ms,
			BaselineWindow: result.Baseline.BaselineWindow,
		}
	}

	out := DiagnoseOut{
		Service:         result.Service,
		Status:          result.Status,
		SymptomDetected: result.SymptomDetected,
		Metrics:         metrics,
		TopErrors:       make([]ErrorDetail, 0, len(result.TopErrors)),
		SlowOperations:  make([]SlowOperation, 0, len(result.SlowOps)),
		Dependencies:    make([]Dependency, 0, len(result.Dependencies)),
	}

	for _, e := range result.TopErrors {
		out.TopErrors = append(out.TopErrors, ErrorDetail{
			Operation:     e.Operation,
			Message:       e.Message,
			ExceptionType: e.ExceptionType,
			Count:         e.Count,
			ExampleTrace:  e.TraceID,
		})
	}

	for _, op := range result.SlowOps {
		out.SlowOperations = append(out.SlowOperations, SlowOperation{
			Name:  op.Name,
			P95Ms: op.P95Ms,
			Count: op.Count,
		})
	}

	for _, d := range result.Dependencies {
		out.Dependencies = append(out.Dependencies, Dependency{
			Service:   d.Service,
			Status:    service.DeriveHealth(d.ErrorRate, d.AvgMs, d.CallCount),
			ErrorRate: d.ErrorRate,
			AvgMs:     d.AvgMs,
			Calls:     d.CallCount,
		})
	}

	// Populate change points.
	for _, cp := range result.ChangePoints {
		out.ChangePoints = append(out.ChangePoints, ChangePoint{
			Time:   cp.Time,
			Metric: cp.Metric,
			Before: cp.Before,
			After:  cp.After,
		})
	}

	// Populate correlated log patterns.
	for _, lp := range result.CorrelatedLogPatterns {
		out.CorrelatedLogPatterns = append(out.CorrelatedLogPatterns, LogPattern{
			Pattern:  lp.Pattern,
			Count:    lp.Count,
			Severity: lp.Severity,
		})
	}

	// Suggested traces for direct investigation.
	out.SuggestedTraces = result.SuggestedTraces

	return nil, out, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
