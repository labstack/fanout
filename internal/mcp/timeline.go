package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// timeline - Events with anomaly detection

type TimelineIn struct {
	Service     string `json:"service,omitempty" jsonschema:"Filter by service"`
	Window      int    `json:"window,omitempty" jsonschema:"Time window in minutes,default=60"`
	Granularity int    `json:"granularity,omitempty" jsonschema:"Bucket size in minutes,default=5"`
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
}

func (s *Server) timeline(ctx context.Context, req *mcp.CallToolRequest, in TimelineIn) (*mcp.CallToolResult, TimelineOut, error) {
	result, err := s.svc.Timeline(ctx, in.Service, in.Window, in.Granularity)
	if err != nil {
		return nil, TimelineOut{
			Service:   in.Service,
			Window:    in.Window,
			Buckets:   []TimelineBucket{},
			Anomalies: []Anomaly{},
		}, nil
	}

	out := TimelineOut{
		Service:   in.Service,
		Window:    in.Window,
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

	return nil, out, nil
}
