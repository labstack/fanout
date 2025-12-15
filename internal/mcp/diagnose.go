package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// diagnose - Service deep-dive with root cause analysis

type DiagnoseIn struct {
	Service string `json:"service" jsonschema:"Service name to diagnose"`
	Window  int    `json:"window,omitempty" jsonschema:"Time window in minutes,default=15"`
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

	return nil, out, nil
}
