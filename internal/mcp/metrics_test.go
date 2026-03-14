package mcp

import "testing"

func TestMetricsIn_Defaults(t *testing.T) {
	in := MetricsIn{}
	if in.Action != "" {
		t.Errorf("Action should default to empty string (resolved to 'query' in handler), got %q", in.Action)
	}
	if in.Aggregation != "" {
		t.Errorf("Aggregation should be empty (resolved to 'avg' in service), got %q", in.Aggregation)
	}
}

func TestMetricsIn_ListAction(t *testing.T) {
	in := MetricsIn{
		Action:  "list",
		Window:  "1h",
		Service: "frontend",
	}
	if in.Action != "list" {
		t.Errorf("Action = %q, want 'list'", in.Action)
	}
	if in.Service != "frontend" {
		t.Errorf("Service = %q, want 'frontend'", in.Service)
	}
}

func TestMetricsIn_QueryAction(t *testing.T) {
	in := MetricsIn{
		Action:      "query",
		Name:        "http.server.duration",
		Aggregation: "avg",
		GroupBy:     []string{"service"},
		Granularity: "5m",
		Window:      "30m",
		Limit:       50,
	}
	if in.Action != "query" {
		t.Errorf("Action = %q, want 'query'", in.Action)
	}
	if in.Name != "http.server.duration" {
		t.Errorf("Name = %q", in.Name)
	}
	if len(in.GroupBy) != 1 || in.GroupBy[0] != "service" {
		t.Errorf("GroupBy = %v, want [service]", in.GroupBy)
	}
	if in.Granularity != "5m" {
		t.Errorf("Granularity = %q, want '5m'", in.Granularity)
	}
	if in.Limit != 50 {
		t.Errorf("Limit = %d, want 50", in.Limit)
	}
}

func TestMetricsIn_MultipleNames(t *testing.T) {
	in := MetricsIn{
		Action: "query",
		Names:  []string{"http.server.duration", "http.server.request.size"},
	}
	if len(in.Names) != 2 {
		t.Errorf("Names len = %d, want 2", len(in.Names))
	}
}

func TestMetricListEntryOut_Fields(t *testing.T) {
	entry := MetricListEntryOut{
		Name:        "http.server.duration",
		Type:        "histogram",
		Unit:        "ms",
		Services:    []string{"frontend", "checkout"},
		Description: "Duration of HTTP server requests",
	}
	if entry.Name != "http.server.duration" {
		t.Errorf("Name = %q", entry.Name)
	}
	if len(entry.Services) != 2 {
		t.Errorf("Services len = %d, want 2", len(entry.Services))
	}
}

func TestMetricSeriesOut_Fields(t *testing.T) {
	series := MetricSeriesOut{
		Labels:      map[string]string{"service": "frontend"},
		Metric:      "http.server.duration",
		Aggregation: "avg",
		Unit:        "ms",
		Datapoints: []MetricDatapointOut{
			{Time: "2026-03-14T16:30:00Z", Value: 14.2},
			{Time: "2026-03-14T16:35:00Z", Value: 15.8},
		},
	}
	if series.Metric != "http.server.duration" {
		t.Errorf("Metric = %q", series.Metric)
	}
	if series.Labels["service"] != "frontend" {
		t.Errorf("Labels[service] = %q, want 'frontend'", series.Labels["service"])
	}
	if len(series.Datapoints) != 2 {
		t.Errorf("Datapoints len = %d, want 2", len(series.Datapoints))
	}
	if series.Datapoints[0].Value != 14.2 {
		t.Errorf("Datapoints[0].Value = %f, want 14.2", series.Datapoints[0].Value)
	}
}

func TestMetricAnomalyOut_Fields(t *testing.T) {
	a := MetricAnomalyOut{
		Time:           "2026-03-14T16:40:00Z",
		Type:           "spike",
		Value:          18.1,
		Expected:       14.5,
		DeviationSigma: 2.3,
	}
	if a.Type != "spike" {
		t.Errorf("Type = %q, want 'spike'", a.Type)
	}
	if a.Value != 18.1 {
		t.Errorf("Value = %f, want 18.1", a.Value)
	}
	if a.DeviationSigma != 2.3 {
		t.Errorf("DeviationSigma = %f, want 2.3", a.DeviationSigma)
	}
}

func TestMetricsListOut_Empty(t *testing.T) {
	out := MetricsListOut{
		Metrics:    []MetricListEntryOut{},
		Suggestion: "No metrics found.",
	}
	if len(out.Metrics) != 0 {
		t.Errorf("Metrics len = %d, want 0", len(out.Metrics))
	}
}

func TestMetricsQueryOut_Empty(t *testing.T) {
	out := MetricsQueryOut{
		Series:     []MetricSeriesOut{},
		Anomalies:  []MetricAnomalyOut{},
		Suggestion: "No data found.",
	}
	if len(out.Series) != 0 {
		t.Errorf("Series len = %d, want 0", len(out.Series))
	}
	if len(out.Anomalies) != 0 {
		t.Errorf("Anomalies len = %d, want 0", len(out.Anomalies))
	}
}
