package dashboard

import "github.com/labstack/fanout/internal/panel"

// DefaultSpec is every owner's first dashboard: the shape of the whole system,
// filterable to one service.
func DefaultSpec() panel.Dashboard {
	server := "kind = 'SPAN_KIND_SERVER'"
	service := "service = $service"
	where := []string{server, service}
	return panel.Dashboard{
		Version:     panel.SpecVersion,
		Name:        "System overview",
		Description: "Traffic, errors and latency across services, with the slowest endpoints.",
		Time:        panel.Time{Range: "1h", Refresh: "30s"},
		Variables:   []panel.Variable{{Name: "service", Kind: "query", From: "spans", Field: "service", IncludeAll: true, Default: panel.AllValue}},
		Panels: []panel.Panel{
			{ID: "requests", Title: "Requests per second", Viz: "stat", Better: "higher", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"rate()"}}},
			{ID: "errors", Better: "lower", Title: "Error rate", Viz: "stat", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"error_rate()"}}, Thresholds: []panel.Threshold{{Value: 1, Status: "warn"}, {Value: 5, Status: "bad"}}},
			{ID: "latency_p95", Better: "lower", Title: "p95 latency", Viz: "stat", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"p95(duration_ms)"}}, Thresholds: []panel.Threshold{{Value: 750, Status: "warn"}, {Value: 2000, Status: "bad"}}},
			{ID: "error_logs", Better: "lower", Title: "Error logs", Viz: "stat", Query: &panel.Query{From: "logs", Where: []string{"upper(severity) IN ('ERROR', 'FATAL')", service}, Measures: []string{"count()"}}, Thresholds: []panel.Threshold{{Value: 1, Status: "warn"}}},
			{ID: "traffic", Title: "Requests by service", Viz: "timeseries", Options: &panel.Options{Style: "stacked"}, Query: &panel.Query{From: "spans", Where: where, Measures: []string{"rate()"}, By: []string{"service"}, Bucket: "auto"}},
			{ID: "latency", Better: "lower", Title: "p95 latency by service", Viz: "timeseries", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Bucket: "auto"}, Thresholds: []panel.Threshold{{Value: 2000, Status: "bad"}}},
			{ID: "error_services", Title: "Error rate by service", Viz: "bar", Width: 4, Click: &panel.Click{SetVariable: "service"}, Query: &panel.Query{From: "spans", Where: []string{server}, Measures: []string{"error_rate()"}, By: []string{"service"}, Limit: 10}},
			{ID: "endpoints", Title: "Slowest endpoints", Viz: "table", Width: 8, Query: &panel.Query{From: "spans", Where: []string{server, service, "http_route <> ''"}, Measures: []string{"p95(duration_ms)", "error_rate()", "rate()"}, By: []string{"service", "http_route"}, Sort: "p95", Limit: 15}},
		},
	}
}
