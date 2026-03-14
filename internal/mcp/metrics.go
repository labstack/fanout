package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MetricsIn holds input parameters for the metrics tool.
type MetricsIn struct {
	Action      string            `json:"action,omitempty"      jsonschema:"Action: 'list' to discover metrics, 'query' to retrieve timeseries. Default: query"`
	Name        string            `json:"name,omitempty"        jsonschema:"Metric name for query action"`
	Names       []string          `json:"names,omitempty"       jsonschema:"Multiple metric names for overlay in query action"`
	Aggregation string            `json:"aggregation,omitempty" jsonschema:"Aggregation: avg|sum|min|max|count. Default: avg"`
	GroupBy     []string          `json:"group_by,omitempty"    jsonschema:"Group timeseries by dimension: service"`
	Granularity string            `json:"granularity,omitempty" jsonschema:"Bucket size: 1m|5m|15m|1h|auto. Default: auto"`
	Service     string            `json:"service,omitempty"     jsonschema:"Filter by service"`
	Attrs       map[string]string `json:"attrs,omitempty"       jsonschema:"Attribute filters as key-value pairs"`
	Window      string            `json:"window,omitempty"      jsonschema:"Time window: '15m','1h','7d', or ISO range 'start/end'. Default: 15m"`
	Namespace   string            `json:"namespace,omitempty"   jsonschema:"Filter by namespace"`
	Tenant      string            `json:"tenant,omitempty"      jsonschema:"Filter by tenant"`
	Limit       int               `json:"limit,omitempty"       jsonschema:"Max results. Default: 100"`
}

// MetricListEntryOut is a single metric in the list action result.
type MetricListEntryOut struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	Services    []string `json:"services"`
	Description string   `json:"description,omitempty"`
}

// MetricsListOut is the response envelope for the list action.
type MetricsListOut struct {
	Metrics    []MetricListEntryOut `json:"metrics"`
	Suggestion string               `json:"suggestion,omitempty"`
}

// MetricDatapointOut is a single timestamped value in a series.
type MetricDatapointOut struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

// MetricSeriesOut is one timeseries stream in the query action result.
type MetricSeriesOut struct {
	Labels      map[string]string    `json:"labels,omitempty"`
	Metric      string               `json:"metric"`
	Aggregation string               `json:"aggregation"`
	Unit        string               `json:"unit,omitempty"`
	Datapoints  []MetricDatapointOut `json:"datapoints"`
}

// MetricAnomalyOut describes a statistical anomaly in metric data.
type MetricAnomalyOut struct {
	Time           string  `json:"time"`
	Type           string  `json:"type"`
	Value          float64 `json:"value"`
	Expected       float64 `json:"expected"`
	DeviationSigma float64 `json:"deviation_sigma"`
}

// MetricsQueryOut is the response envelope for the query action.
type MetricsQueryOut struct {
	Series     []MetricSeriesOut  `json:"series"`
	Anomalies  []MetricAnomalyOut `json:"anomalies"`
	Suggestion string             `json:"suggestion,omitempty"`
}

func (s *Server) metrics(ctx context.Context, req *mcp.CallToolRequest, in MetricsIn) (*mcp.CallToolResult, any, error) {
	tw, err := parseWindow(in.Window)
	if err != nil {
		return nil, MetricsQueryOut{
			Series:     []MetricSeriesOut{},
			Anomalies:  []MetricAnomalyOut{},
			Suggestion: fmt.Sprintf("Invalid window: %s", err),
		}, nil
	}

	limit := clampInt(in.Limit, minLimit, maxLimit, 100)

	action := in.Action
	if action == "" {
		action = "query"
	}

	p := service.MetricParams{
		Action:      action,
		Name:        in.Name,
		Names:       in.Names,
		Aggregation: in.Aggregation,
		GroupBy:     in.GroupBy,
		Granularity: in.Granularity,
		Service:     in.Service,
		Attrs:       in.Attrs,
		Window:      tw.Minutes,
		Namespace:   in.Namespace,
		TenantID:    in.Tenant,
		Limit:       limit,
	}

	result, err := s.svc.MetricsTool(ctx, p)
	if err != nil {
		slog.Warn("metrics tool failed", "err", err)
		suggestion := fmt.Sprintf("Query failed: %s", err)
		if action == "list" {
			return nil, MetricsListOut{Metrics: []MetricListEntryOut{}, Suggestion: suggestion}, nil
		}
		return nil, MetricsQueryOut{Series: []MetricSeriesOut{}, Anomalies: []MetricAnomalyOut{}, Suggestion: suggestion}, nil
	}

	switch action {
	case "list":
		listResult, ok := result.(*service.MetricsListResult)
		if !ok {
			slog.Error("metrics list: unexpected result type", "type", fmt.Sprintf("%T", result))
			return nil, MetricsListOut{Metrics: []MetricListEntryOut{}, Suggestion: "Internal error: unexpected result type"}, nil
		}
		out := MetricsListOut{
			Metrics: make([]MetricListEntryOut, 0, len(listResult.Metrics)),
		}
		for _, m := range listResult.Metrics {
			svcs := m.Services
			if svcs == nil {
				svcs = []string{}
			}
			out.Metrics = append(out.Metrics, MetricListEntryOut{
				Name:        m.Name,
				Type:        m.Type,
				Unit:        m.Unit,
				Services:    svcs,
				Description: m.Description,
			})
		}
		if len(out.Metrics) == 0 {
			out.Suggestion = "No metrics found. Try widening the time window or adjusting filters."
		} else {
			out.Suggestion = fmt.Sprintf("Found %d metric(s). Use action='query' with name=<metric_name> to get timeseries.", len(out.Metrics))
		}
		return nil, out, nil

	default: // "query"
		queryResult, ok := result.(*service.MetricsQueryResult)
		if !ok {
			slog.Error("metrics query: unexpected result type", "type", fmt.Sprintf("%T", result))
			return nil, MetricsQueryOut{Series: []MetricSeriesOut{}, Anomalies: []MetricAnomalyOut{}, Suggestion: "Internal error: unexpected result type"}, nil
		}
		out := MetricsQueryOut{
			Series:    make([]MetricSeriesOut, 0, len(queryResult.Series)),
			Anomalies: make([]MetricAnomalyOut, 0, len(queryResult.Anomalies)),
		}
		for _, sr := range queryResult.Series {
			dps := make([]MetricDatapointOut, 0, len(sr.Datapoints))
			for _, dp := range sr.Datapoints {
				dps = append(dps, MetricDatapointOut{Time: dp.Time, Value: dp.Value})
			}
			out.Series = append(out.Series, MetricSeriesOut{
				Labels:      sr.Labels,
				Metric:      sr.Metric,
				Aggregation: sr.Aggregation,
				Unit:        sr.Unit,
				Datapoints:  dps,
			})
		}
		for _, a := range queryResult.Anomalies {
			out.Anomalies = append(out.Anomalies, MetricAnomalyOut{
				Time:           a.Time,
				Type:           a.Type,
				Value:          a.Value,
				Expected:       a.Expected,
				DeviationSigma: a.DeviationSigma,
			})
		}
		if len(out.Series) == 0 {
			out.Suggestion = "No data found. Try action='list' to discover available metrics, or widen the time window."
		} else if len(out.Anomalies) > 0 {
			out.Suggestion = fmt.Sprintf("%d anomaly(ies) detected in the time range.", len(out.Anomalies))
		}
		return nil, out, nil
	}
}
