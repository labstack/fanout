package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// trace - Request journey with auto root cause analysis

type TraceIn struct {
	TraceID     string `json:"trace_id" jsonschema:"Trace ID to analyze"`
	IncludeLogs bool   `json:"include_logs,omitempty" jsonschema:"Include correlated logs,default=true"`
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
}

func (s *Server) trace(ctx context.Context, req *mcp.CallToolRequest, in TraceIn) (*mcp.CallToolResult, TraceOut, error) {
	if in.TraceID == "" {
		return nil, TraceOut{}, fmt.Errorf("trace_id is required")
	}

	result, err := s.svc.Trace(ctx, in.TraceID, true) // always include logs
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

	return nil, out, nil
}
