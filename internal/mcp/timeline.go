package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// timeline - Events with anomaly detection

type TimelineIn struct {
	Service     string `json:"service,omitempty" jsonschema:"Filter by service"`
	Window      int    `json:"window,omitempty" jsonschema:"Time window in minutes,default=60"`
	Granularity int    `json:"granularity,omitempty" jsonschema:"Bucket size in minutes,default=5"`
	Format      string `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
}

type TimelineBucket struct {
	Time         string  `json:"time"`
	RequestCount int64   `json:"request_count"`
	ErrorCount   int64   `json:"error_count"`
	P95Ms        float64 `json:"p95_ms"`
	ErrorRate    float64 `json:"error_rate"`
	IsAnomaly    bool    `json:"is_anomaly,omitempty"`
	AnomalyType  string  `json:"anomaly_type,omitempty"`
}

type Anomaly struct {
	Time        string  `json:"time"`
	Type        string  `json:"type"`
	Description string  `json:"description"`
	Value       float64 `json:"value"`
	Expected    float64 `json:"expected"`
}

type TimelineOut struct {
	Service   string           `json:"service,omitempty"`
	Window    int              `json:"window_minutes"`
	Buckets   []TimelineBucket `json:"buckets"`
	Anomalies []Anomaly        `json:"anomalies"`
	Render    *render.Output   `json:"render,omitempty"`
}

func (s *Server) timeline(ctx context.Context, req *mcp.CallToolRequest, in TimelineIn) (*mcp.CallToolResult, TimelineOut, error) {
	window := clampInt(in.Window, minWindow, maxWindow, 60)            // default 60 for timeline
	granularity := clampInt(in.Granularity, minGranularity, maxGranularity, defGranularity)

	result, err := s.svc.Timeline(ctx, in.Service, window, granularity)
	if err != nil {
		return nil, TimelineOut{
			Service:   in.Service,
			Window:    window,
			Buckets:   []TimelineBucket{},
			Anomalies: []Anomaly{},
		}, nil
	}

	out := TimelineOut{
		Service:   in.Service,
		Window:    window,
		Buckets:   make([]TimelineBucket, 0, len(result.Buckets)),
		Anomalies: make([]Anomaly, 0, len(result.Anomalies)),
	}

	for _, b := range result.Buckets {
		out.Buckets = append(out.Buckets, TimelineBucket{
			Time:         b.Time,
			RequestCount: b.Requests,
			ErrorCount:   b.Errors,
			P95Ms:        b.P95Ms,
			ErrorRate:    b.ErrorRate,
			IsAnomaly:    b.IsAnomaly,
			AnomalyType:  b.AnomalyType,
		})
	}

	for _, a := range result.Anomalies {
		out.Anomalies = append(out.Anomalies, Anomaly{
			Time:        a.Time,
			Type:        a.Type,
			Description: a.Description,
			Value:       a.Value,
			Expected:    a.Expected,
		})
	}

	// Render output
	format := parseFormat(in.Format)
	if format != render.Data {
		rendered := renderTimeline(&out)
		out.Render = &rendered
	}

	return nil, out, nil
}

func renderTimeline(t *TimelineOut) render.Output {
	var items []render.Renderer

	// Header
	title := "System Timeline"
	if t.Service != "" {
		title = t.Service + " Timeline"
	}
	header := &render.Text{Content: title, Style: "bold"}
	items = append(items, header)

	// Extract values for sparklines
	var latencies, requests, errors []float64
	for _, b := range t.Buckets {
		latencies = append(latencies, b.P95Ms)
		requests = append(requests, float64(b.RequestCount))
		errors = append(errors, b.ErrorRate*100)
	}

	// Sparklines for key metrics
	if len(latencies) > 0 {
		items = append(items, &render.Sparkline{Label: "P95 Latency (ms)", Values: latencies})
		items = append(items, &render.Sparkline{Label: "Requests", Values: requests})
		items = append(items, &render.Sparkline{Label: "Error Rate (%)", Values: errors})
	}

	// Anomalies
	if len(t.Anomalies) > 0 {
		var anomalyItems []render.Renderer
		for _, a := range t.Anomalies {
			icon := "⚠"
			status := "warning"
			if strings.Contains(strings.ToLower(a.Type), "error") || strings.Contains(strings.ToLower(a.Type), "spike") {
				icon = "⚡"
				status = "unhealthy"
			}
			anomalyItems = append(anomalyItems, &render.Grid{
				Cols: 3,
				Items: []render.Renderer{
					&render.Badge{Label: icon + " " + a.Type, Status: status},
					&render.Text{Content: a.Description},
					&render.Text{Content: fmt.Sprintf("%.1f (expected %.1f)", a.Value, a.Expected), Style: "dim"},
				},
			})
		}
		items = append(items, &render.Panel{
			Title:   fmt.Sprintf("Anomalies (%d)", len(t.Anomalies)),
			Content: anomalyItems,
		})
	} else {
		items = append(items, &render.Badge{Label: "No anomalies detected", Status: "healthy"})
	}

	// Time range
	if len(t.Buckets) > 0 {
		timeRange := fmt.Sprintf("Time range: %s - %s", t.Buckets[0].Time, t.Buckets[len(t.Buckets)-1].Time)
		items = append(items, &render.Text{Content: timeRange, Style: "dim"})
	}

	composed := &render.Compose{Vertical: true, Items: items}
	return composed.Render(render.Both)
}
