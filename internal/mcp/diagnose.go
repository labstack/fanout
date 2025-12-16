package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/render"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// diagnose - Service deep-dive with root cause analysis

type DiagnoseIn struct {
	Service string `json:"service" jsonschema:"Service name to diagnose"`
	Window  int    `json:"window,omitempty" jsonschema:"Time window in minutes,default=15"`
	Format  string `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
}

type ServiceMetrics struct {
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
	P99Ms     float64 `json:"p99_ms"`
	ErrorRate float64 `json:"error_rate"`
	Count     int64   `json:"request_count"`
}

type ErrorDetail struct {
	Message      string `json:"message"`
	Count        int64  `json:"count"`
	ExampleTrace string `json:"example_trace,omitempty"`
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
	P95Ms     float64 `json:"p95_ms"`
	Calls     int64   `json:"calls"`
}

type DiagnoseOut struct {
	Service        string          `json:"service"`
	Status         string          `json:"status"`
	Metrics        ServiceMetrics  `json:"metrics"`
	TopErrors      []ErrorDetail   `json:"top_errors"`
	SlowOperations []SlowOperation `json:"slow_operations"`
	Dependencies   []Dependency    `json:"dependencies"`
	Render         *render.Output  `json:"render,omitempty"`
}

func (s *Server) diagnose(ctx context.Context, req *mcp.CallToolRequest, in DiagnoseIn) (*mcp.CallToolResult, DiagnoseOut, error) {
	if in.Service == "" {
		return nil, DiagnoseOut{}, fmt.Errorf("service is required")
	}

	result, err := s.svc.Diagnose(ctx, in.Service, in.Window)
	if err != nil {
		return nil, DiagnoseOut{
			Service:        in.Service,
			Status:         "unknown",
			TopErrors:      []ErrorDetail{},
			SlowOperations: []SlowOperation{},
			Dependencies:   []Dependency{},
		}, nil
	}

	out := DiagnoseOut{
		Service: result.Service,
		Status:  result.Status,
		Metrics: ServiceMetrics{
			P50Ms:     result.P50Ms,
			P95Ms:     result.P95Ms,
			P99Ms:     result.P99Ms,
			ErrorRate: result.ErrorRate,
			Count:     result.SpanCount,
		},
		TopErrors:      make([]ErrorDetail, 0, len(result.TopErrors)),
		SlowOperations: make([]SlowOperation, 0, len(result.SlowOps)),
		Dependencies:   make([]Dependency, 0, len(result.Dependencies)),
	}

	for _, e := range result.TopErrors {
		out.TopErrors = append(out.TopErrors, ErrorDetail{
			Message:      e.Message,
			Count:        e.Count,
			ExampleTrace: e.TraceID,
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
			Status:    service.DeriveHealth(d.ErrorRate, d.P95Ms),
			ErrorRate: d.ErrorRate,
			P95Ms:     d.P95Ms,
			Calls:     d.CallCount,
		})
	}

	// Render output
	format := parseFormat(in.Format)
	if format != render.Data {
		rendered := renderDiagnose(&out)
		out.Render = &rendered
	}

	return nil, out, nil
}

func renderDiagnose(d *DiagnoseOut) render.Output {
	// Header with status badge
	header := &render.Panel{
		Title: d.Service,
		Content: []render.Renderer{
			&render.Badge{Label: d.Status, Status: d.Status},
		},
	}

	// Latency metrics with visual bars
	maxLatency := d.Metrics.P99Ms
	if maxLatency == 0 {
		maxLatency = 100
	}
	metrics := &render.Grid{
		Cols: 4,
		Items: []render.Renderer{
			&render.Metric{Label: "P50", Value: fmt.Sprintf("%.1f", d.Metrics.P50Ms), Unit: "ms"},
			&render.Metric{Label: "P95", Value: fmt.Sprintf("%.1f", d.Metrics.P95Ms), Unit: "ms"},
			&render.Metric{Label: "P99", Value: fmt.Sprintf("%.1f", d.Metrics.P99Ms), Unit: "ms"},
			&render.Metric{Label: "Error Rate", Value: fmt.Sprintf("%.2f", d.Metrics.ErrorRate*100), Unit: "%"},
		},
	}

	// Request count
	reqCount := &render.Metric{Label: "Requests", Value: fmt.Sprintf("%d", d.Metrics.Count)}

	// Top errors table
	var errorRows [][]string
	for _, e := range d.TopErrors {
		errorRows = append(errorRows, []string{
			truncate(e.Message, 50),
			fmt.Sprintf("%d", e.Count),
			truncate(e.ExampleTrace, 16),
		})
	}
	errorsTable := &render.Table{
		Title:    "Top Errors",
		Headers:  []string{"Message", "Count", "Trace"},
		Rows:     errorRows,
		MaxWidth: 50,
	}

	// Slow operations table
	var slowRows [][]string
	for _, op := range d.SlowOperations {
		slowRows = append(slowRows, []string{
			truncate(op.Name, 40),
			fmt.Sprintf("%.1fms", op.P95Ms),
			fmt.Sprintf("%d", op.Count),
		})
	}
	slowTable := &render.Table{
		Title:    "Slow Operations",
		Headers:  []string{"Operation", "P95", "Count"},
		Rows:     slowRows,
		MaxWidth: 40,
	}

	// Dependencies table
	var depRows [][]string
	for _, dep := range d.Dependencies {
		depRows = append(depRows, []string{
			dep.Service,
			dep.Status,
			fmt.Sprintf("%.1fms", dep.P95Ms),
			fmt.Sprintf("%.2f%%", dep.ErrorRate*100),
			fmt.Sprintf("%d", dep.Calls),
		})
	}
	depsTable := &render.Table{
		Title:   "Dependencies",
		Headers: []string{"Service", "Status", "P95", "Errors", "Calls"},
		Rows:    depRows,
	}

	// Compose
	composed := &render.Compose{
		Vertical: true,
		Items:    []render.Renderer{header, metrics, reqCount},
	}

	if len(errorRows) > 0 {
		composed.Items = append(composed.Items, errorsTable)
	}
	if len(slowRows) > 0 {
		composed.Items = append(composed.Items, slowTable)
	}
	if len(depRows) > 0 {
		composed.Items = append(composed.Items, depsTable)
	}

	return composed.Render(render.Both)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
