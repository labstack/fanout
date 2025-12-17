package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// trace - Request journey with auto root cause analysis

type TraceIn struct {
	TraceID     string `json:"trace_id" jsonschema:"Trace ID to analyze"`
	IncludeLogs *bool  `json:"include_logs,omitempty" jsonschema:"Include correlated logs,default=true"`
	Window      int    `json:"window,omitempty" jsonschema:"Time window in minutes to search,default=1440 (24h)"`
	Format      string `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
}

type TraceSpan struct {
	SpanID       string  `json:"span_id"`
	ParentSpanID string  `json:"parent_span_id,omitempty"`
	Service      string  `json:"service"`
	Operation    string  `json:"operation"`
	StartTime    string  `json:"start_time"`
	DurationMs   float64 `json:"duration_ms"`
	Status       string  `json:"status"`
	StatusMsg    string  `json:"status_msg,omitempty"`
	SelfTimeMs   float64 `json:"self_time_ms"`
	IsCritical   bool    `json:"is_critical,omitempty"`
}

type CorrelatedLog struct {
	Timestamp string `json:"ts"`
	Service   string `json:"service"`
	Severity  string `json:"severity"`
	Body      string `json:"body"`
	SpanID    string `json:"span_id,omitempty"`
}

type RootCause struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	SpanID      string `json:"span_id,omitempty"`
	Service     string `json:"service,omitempty"`
}

type TraceOut struct {
	TraceID       string          `json:"trace_id"`
	TotalDuration float64         `json:"total_duration_ms"`
	SpanCount     int             `json:"span_count"`
	Services      []string        `json:"services"`
	HasError      bool            `json:"has_error"`
	Spans         []TraceSpan     `json:"spans"`
	Logs          []CorrelatedLog `json:"logs"`
	RootCause     *RootCause      `json:"root_cause,omitempty"`
	CriticalPath  []string        `json:"critical_path"`
	Render        *render.Output  `json:"render,omitempty"`
}

func (s *Server) trace(ctx context.Context, req *mcp.CallToolRequest, in TraceIn) (*mcp.CallToolResult, TraceOut, error) {
	if in.TraceID == "" {
		return nil, TraceOut{}, fmt.Errorf("trace_id is required")
	}

	// Default to true if not specified
	includeLogs := true
	if in.IncludeLogs != nil {
		includeLogs = *in.IncludeLogs
	}

	// Default to 24h (1440 min) for trace lookups
	window := clampInt(in.Window, minWindow, maxWindow, 1440)

	result, err := s.svc.Trace(ctx, in.TraceID, includeLogs, window)
	if err != nil {
		return nil, TraceOut{}, err
	}

	out := TraceOut{
		TraceID:       result.TraceID,
		TotalDuration: result.Duration,
		SpanCount:     result.SpanCount,
		Services:      result.Services,
		HasError:      result.HasError,
		Spans:         make([]TraceSpan, 0, len(result.Spans)),
		Logs:          make([]CorrelatedLog, 0, len(result.Logs)),
		CriticalPath:  result.CriticalPath,
	}

	if result.Services == nil {
		out.Services = []string{}
	}
	if result.CriticalPath == nil {
		out.CriticalPath = []string{}
	}

	for _, sp := range result.Spans {
		out.Spans = append(out.Spans, TraceSpan{
			SpanID:       sp.SpanID,
			ParentSpanID: sp.ParentID,
			Service:      sp.Service,
			Operation:    sp.Name,
			StartTime:    sp.StartTime,
			DurationMs:   sp.Duration,
			Status:       sp.Status,
			StatusMsg:    sp.StatusMsg,
			SelfTimeMs:   sp.SelfTime,
			IsCritical:   sp.IsCritical,
		})
	}

	for _, lg := range result.Logs {
		out.Logs = append(out.Logs, CorrelatedLog{
			Timestamp: lg.Time,
			Service:   lg.Service,
			Severity:  lg.Severity,
			Body:      lg.Body,
			SpanID:    lg.SpanID,
		})
	}

	if result.RootCause != nil {
		out.RootCause = &RootCause{
			Type:        result.RootCause.Reason,
			Description: result.RootCause.Description,
			SpanID:      result.RootCause.SpanID,
			Service:     result.RootCause.Service,
		}
	}

	// Render output
	format := parseFormat(in.Format)
	if format != render.Data {
		rendered := renderTrace(&out)
		out.Render = &rendered
	}

	return nil, out, nil
}

func renderTrace(t *TraceOut) render.Output {
	var items []render.Renderer

	// Header with key metrics
	status := "healthy"
	if t.HasError {
		status = "unhealthy"
	}
	header := &render.Grid{
		Cols: 4,
		Items: []render.Renderer{
			&render.Badge{Label: status, Status: status},
			&render.Metric{Label: "Duration", Value: fmt.Sprintf("%.1f", t.TotalDuration), Unit: "ms"},
			&render.Metric{Label: "Spans", Value: fmt.Sprintf("%d", t.SpanCount)},
			&render.Metric{Label: "Services", Value: fmt.Sprintf("%d", len(t.Services))},
		},
	}
	items = append(items, header)

	// Root cause alert if present
	if t.RootCause != nil {
		rootCause := &render.Panel{
			Title: "Root Cause",
			Content: []render.Renderer{
				&render.Badge{Label: t.RootCause.Type, Status: "unhealthy"},
				&render.Text{Content: t.RootCause.Description},
				&render.Text{Content: fmt.Sprintf("Service: %s", t.RootCause.Service), Style: "dim"},
			},
		}
		items = append(items, rootCause)
	}

	// Build span tree
	tree := buildTraceTree(t.Spans)
	items = append(items, &render.Panel{
		Title:   "Trace Tree",
		Content: []render.Renderer{tree},
	})

	// Critical path
	if len(t.CriticalPath) > 0 {
		pathText := ""
		for i, p := range t.CriticalPath {
			if i > 0 {
				pathText += " → "
			}
			pathText += p
		}
		items = append(items, &render.Text{Content: "Critical Path: " + pathText, Style: "bold"})
	}

	// Logs table if present
	if len(t.Logs) > 0 {
		var rows [][]string
		for _, lg := range t.Logs {
			rows = append(rows, []string{
				lg.Timestamp,
				lg.Service,
				lg.Severity,
				truncate(lg.Body, 50),
			})
		}
		logsTable := &render.Table{
			Title:    "Correlated Logs",
			Headers:  []string{"Time", "Service", "Severity", "Body"},
			Rows:     rows,
			MaxWidth: 50,
		}
		items = append(items, logsTable)
	}

	composed := &render.Compose{Vertical: true, Items: items}
	return composed.Render(render.Both)
}

func buildTraceTree(spans []TraceSpan) *render.Tree {
	if len(spans) == 0 {
		return &render.Tree{Root: &render.Node{Label: "No spans"}}
	}

	nodeMap := make(map[string]*render.Node)
	var root *render.Node

	// Create nodes
	for _, sp := range spans {
		statusIcon := "○"
		if sp.Status == "error" || sp.Status == "ERROR" {
			statusIcon = "✗"
		} else if sp.IsCritical {
			statusIcon = "●"
		}

		node := &render.Node{
			Label: fmt.Sprintf("%s %s/%s", statusIcon, sp.Service, sp.Operation),
			Value: fmt.Sprintf("%.1fms", sp.DurationMs),
			Meta: map[string]string{
				"status":    sp.Status,
				"self_time": fmt.Sprintf("%.1fms", sp.SelfTimeMs),
			},
		}
		nodeMap[sp.SpanID] = node

		if sp.ParentSpanID == "" {
			root = node
		}
	}

	// Link children to parents
	for _, sp := range spans {
		if sp.ParentSpanID != "" {
			if parent, ok := nodeMap[sp.ParentSpanID]; ok {
				parent.Children = append(parent.Children, nodeMap[sp.SpanID])
			}
		}
	}

	// Fallback if no root found
	if root == nil && len(spans) > 0 {
		root = nodeMap[spans[0].SpanID]
	}

	return &render.Tree{Root: root}
}
