package dashboard

import "github.com/labstack/fanout/internal/panel"

// DefaultSpec is every owner's first dashboard: the shape of the whole system,
// filterable to one service.
func DefaultSpec() panel.Dashboard {
	server := "kind = 'SPAN_KIND_SERVER'"
	service := "service = $service"
	where := []string{server, service}
	deploys := true
	return panel.Dashboard{
		Version:     panel.SpecVersion,
		Name:        "System overview",
		Description: "Traffic, errors and latency, reachability, host and container capacity, and service versions.",
		Time:        panel.Time{Range: "1h", Refresh: "30s"},
		Annotations: &panel.Annotations{Deploys: &deploys},
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
			{ID: "check_states", Title: "Is it reachable?", Description: receiverDescription("httpcheck", "Endpoint and host.name; silence is bad after two intervals. Labels warn if all checks run on service hosts."), Viz: "state_timeline", Width: 12, SQL: reachabilitySQL, Better: "lower", Thresholds: []panel.Threshold{{Value: 1, Status: "bad"}}},
			{ID: "check_latency", Title: "Check latency", Description: receiverDescription("httpcheck", "Latest check duration by endpoint and reporting host.name."), Viz: "timeseries", Width: 12, SQL: checkLatencySQL, Unit: "ms"},
			{ID: "service_health", Title: "Is anything failing or slow?", Description: receiverDescription("otlp", "Service health and error trend with deploy markers."), Viz: "health", Width: 12, Query: &panel.Query{From: "spans", Where: []string{service}}},
			{ID: "service_map", Title: "Services and external dependencies", Description: receiverDescription("otlp", "Failing or slow services and external dependencies observed in client spans."), Viz: "service_map", Width: 12, Height: "l", Query: &panel.Query{From: "spans", Where: []string{service}}},
			capacityPanel("hosts", "Is anything running out? Hosts", "hostmetrics", "CPU busy, memory used and the fullest filesystem; worst first. Not reporting after two intervals is bad.", hostCapacitySQL()),
			capacityPanel("containers", "Containers", "docker_stats", "CPU: 100% is one core. Status uses limits; memory only if no CPU limit. Silence after two intervals is bad.", containerCapacitySQL()),
			{ID: "versions", Title: "What changed?", Description: receiverDescription("otlp", "Service versions, first seen in the selected range, runtime and SDK."), Viz: "table", Width: 12, SQL: `WITH observations AS (` + versionObservationsSQL + `) SELECT namespace,service,version,min(seen) AS first_seen,runtime_name,runtime_version,sdk_language,sdk_version FROM observations WHERE version IS NOT NULL AND version<>'' GROUP BY namespace,service,version,runtime_name,runtime_version,sdk_language,sdk_version ORDER BY first_seen DESC,namespace,service,version`},
		},
	}
}

func receiverDescription(receiver, description string) string {
	return description + " Supplied by the Collector `" + receiver + "` receiver. [Collector setup](https://fanout.run/docs/get-started/send-telemetry#watch-hosts-containers-and-endpoints)."
}

func capacityPanel(id, title, receiver, description, sql string) panel.Panel {
	columns := []panel.ColumnFormat{{Field: "status", Format: "status"}, {Field: "cpu_percent", Format: "unit", Unit: "percent"}, {Field: "memory_percent", Format: "unit", Unit: "percent"}}
	if id == "hosts" {
		columns = append(columns, panel.ColumnFormat{Field: "filesystem_percent", Format: "unit", Unit: "percent"})
	}
	return panel.Panel{ID: id, Title: title, Description: receiverDescription(receiver, description), Viz: "table", Width: 12, SQL: sql, Better: "lower", Thresholds: []panel.Threshold{{Value: 80, Status: "warn"}, {Value: 90, Status: "bad"}}, Options: &panel.Options{Columns: columns}}
}
