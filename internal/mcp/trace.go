package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// trace - Request journey with auto root cause analysis

type TraceIn struct {
	TraceID        string `json:"trace_id" jsonschema:"Trace ID to analyze"`
	IncludeLogs    *bool  `json:"include_logs,omitempty" jsonschema:"Include correlated logs,default=true"`
	Window         string `json:"window,omitempty" jsonschema:"Time window: duration (15m, 1h, 24h, 7d) or ISO range,default=24h"`
	CompareTo      string `json:"compare_to,omitempty" jsonschema:"Another trace ID for side-by-side latency comparison"`
	IncludeMetrics *bool  `json:"include_metrics,omitempty" jsonschema:"Include service_rollup metric snapshots around trace time,default=false"`
}

type TraceSpan struct {
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Service      string         `json:"service"`
	Operation    string         `json:"operation"`
	Kind         string         `json:"kind,omitempty"`
	StartTime    string         `json:"start_time"`
	DurationMs   float64        `json:"duration_ms"`
	Status       string         `json:"status"`
	StatusMsg    string         `json:"status_msg,omitempty"`
	SelfTimeMs   float64        `json:"self_time_ms"`
	IsCritical   bool           `json:"is_critical,omitempty"`
	Events       []SpanEventOut `json:"events,omitempty"`
	Links        []SpanLinkOut  `json:"links,omitempty"`
	TraceState   string         `json:"trace_state,omitempty"`
	ScopeName    string         `json:"scope_name,omitempty"`
	ScopeVersion string         `json:"scope_version,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

type SpanEventOut struct {
	Time       int64             `json:"time_unix_nano"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type SpanLinkOut struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	TraceState string            `json:"trace_state,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type CorrelatedLog struct {
	Timestamp      string `json:"ts"`
	ObservedTime   string `json:"observed_ts,omitempty"`
	Service        string `json:"service"`
	Severity       string `json:"severity"`
	SeverityNumber int32  `json:"severity_number,omitempty"`
	Body           string `json:"body"`
	SpanID         string `json:"span_id,omitempty"`
	ScopeName      string `json:"scope_name,omitempty"`
	ScopeVersion   string `json:"scope_version,omitempty"`
}

type RootCause struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	SpanID      string `json:"span_id,omitempty"`
	Service     string `json:"service,omitempty"`
}

type TraceComparison struct {
	OtherTraceID    string          `json:"other_trace_id"`
	OtherDurationMs float64         `json:"other_duration_ms"`
	DurationDeltaMs float64         `json:"duration_delta_ms"`
	SpanDiffs       []TraceSpanDiff `json:"span_diffs"`
}

type TraceSpanDiff struct {
	Operation string  `json:"operation"`
	Service   string  `json:"service"`
	ThisMs    float64 `json:"this_ms"`
	OtherMs   float64 `json:"other_ms"`
	DeltaMs   float64 `json:"delta_ms"`
}

type TraceMetricContext struct {
	Service     string              `json:"service"`
	AtTraceTime TraceMetricSnapshot `json:"at_trace_time"`
}

type TraceMetricSnapshot struct {
	P50Ms       float64 `json:"p50_ms"`
	P95Ms       float64 `json:"p95_ms"`
	ErrorRate   float64 `json:"error_rate"`
	SpansPerMin float64 `json:"spans_per_min"`
}

type TraceOut struct {
	TraceID       string               `json:"trace_id"`
	TotalDuration float64              `json:"total_duration_ms"`
	SpanCount     int                  `json:"span_count"`
	Services      []string             `json:"services"`
	HasError      bool                 `json:"has_error"`
	Spans         []TraceSpan          `json:"spans"`
	Logs          []CorrelatedLog      `json:"logs"`
	RootCause     *RootCause           `json:"root_cause,omitempty"`
	CriticalPath  []string             `json:"critical_path"`
	Comparison    *TraceComparison     `json:"comparison,omitempty"`
	MetricContext []TraceMetricContext `json:"metric_context,omitempty"`
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

	// Parse window; default to 24h for trace lookups
	windowStr := in.Window
	if windowStr == "" {
		windowStr = "24h"
	}
	tw, err := parseWindow(windowStr)
	if err != nil {
		return nil, TraceOut{}, fmt.Errorf("invalid window: %w", err)
	}
	window := clampInt(tw.Minutes, minWindow, maxWindow, 1440)

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
		ts := TraceSpan{
			SpanID:       sp.SpanID,
			ParentSpanID: sp.ParentID,
			Service:      sp.Service,
			Operation:    sp.Name,
			Kind:         sp.Kind,
			StartTime:    sp.StartTime,
			DurationMs:   sp.Duration,
			Status:       sp.Status,
			StatusMsg:    sp.StatusMsg,
			SelfTimeMs:   sp.SelfTime,
			IsCritical:   sp.IsCritical,
			TraceState:   sp.TraceState,
			ScopeName:    sp.ScopeName,
			ScopeVersion: sp.ScopeVersion,
			Attributes:   sp.Attributes,
		}
		for _, ev := range sp.Events {
			ts.Events = append(ts.Events, SpanEventOut{
				Time:       ev.Time,
				Name:       ev.Name,
				Attributes: ev.Attributes,
			})
		}
		for _, ln := range sp.Links {
			ts.Links = append(ts.Links, SpanLinkOut{
				TraceID:    ln.TraceID,
				SpanID:     ln.SpanID,
				TraceState: ln.TraceState,
				Attributes: ln.Attributes,
			})
		}
		out.Spans = append(out.Spans, ts)
	}

	for _, lg := range result.Logs {
		out.Logs = append(out.Logs, CorrelatedLog{
			Timestamp:      lg.Time,
			ObservedTime:   lg.ObservedTime,
			Service:        lg.Service,
			Severity:       lg.Severity,
			SeverityNumber: lg.SeverityNumber,
			Body:           lg.Body,
			SpanID:         lg.SpanID,
			ScopeName:      lg.ScopeName,
			ScopeVersion:   lg.ScopeVersion,
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

	// Optional: side-by-side trace comparison
	if in.CompareTo != "" {
		cmp := s.svc.CompareTrace(ctx, result, in.CompareTo, window)
		if cmp != nil {
			outCmp := &TraceComparison{
				OtherTraceID:    cmp.OtherTraceID,
				OtherDurationMs: cmp.OtherDurationMs,
				DurationDeltaMs: cmp.DurationDeltaMs,
			}
			for _, d := range cmp.SpanDiffs {
				outCmp.SpanDiffs = append(outCmp.SpanDiffs, TraceSpanDiff{
					Operation: d.Operation,
					Service:   d.Service,
					ThisMs:    d.ThisMs,
					OtherMs:   d.OtherMs,
					DeltaMs:   d.DeltaMs,
				})
			}
			out.Comparison = outCmp
		}
	}

	// Optional: metric context from service_rollup
	includeMetrics := false
	if in.IncludeMetrics != nil {
		includeMetrics = *in.IncludeMetrics
	}
	if includeMetrics {
		mcs := s.svc.FetchMetricContext(ctx, result)
		for _, mc := range mcs {
			out.MetricContext = append(out.MetricContext, TraceMetricContext{
				Service: mc.Service,
				AtTraceTime: TraceMetricSnapshot{
					P50Ms:       mc.AtTraceTime.P50Ms,
					P95Ms:       mc.AtTraceTime.P95Ms,
					ErrorRate:   mc.AtTraceTime.ErrorRate,
					SpansPerMin: mc.AtTraceTime.SpansPerMin,
				},
			})
		}
	}

	return nil, out, nil
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
