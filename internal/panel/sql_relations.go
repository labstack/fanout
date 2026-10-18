package panel

// SQLRelationSchema describes a panel-SQL-only relation. Its rows are not raw
// events, so it deliberately has neither a signal time column nor sample counts.
type SQLRelationSchema struct {
	Description string            `json:"description"`
	Columns     []SQLColumnSchema `json:"columns"`
}

type SQLColumnSchema struct {
	Field
	SQLType string `json:"sql_type"`
}

// SQLRelations is the shared catalog for discovery and generated documentation.
func SQLRelations() map[string]SQLRelationSchema {
	return map[string]SQLRelationSchema{
		"service_rollup": {
			Description: "Per-service analytical rollup by namespace and time bucket. Panel SQL only; filter bucket with $__window(bucket).",
			Columns: []SQLColumnSchema{
				{Field: str("namespace", "OpenTelemetry service namespace"), SQLType: "VARCHAR"},
				{Field: ts("bucket", "Rollup time bucket"), SQLType: "TIMESTAMP"},
				{Field: str("service", "Service name"), SQLType: "VARCHAR"},
				{Field: num("spans", "count", "Total spans"), SQLType: "BIGINT"},
				{Field: num("served_spans", "count", "Spans describing work performed by this service"), SQLType: "BIGINT"},
				{Field: num("p50_ms", "ms", "Median service latency"), SQLType: "DOUBLE"},
				{Field: num("p95_ms", "ms", "95th percentile service latency"), SQLType: "DOUBLE"},
				{Field: num("error_rate", "percent", "Error rate"), SQLType: "DOUBLE"},
				{Field: num("log_count", "count", "Log events"), SQLType: "BIGINT"},
				{Field: num("metric_count", "count", "Metric data points"), SQLType: "BIGINT"},
			},
		},
		"edge_rollup": {
			Description: "Dependency analytical rollup by namespace, time bucket, caller, callee and edge type. Panel SQL only; filter bucket with $__window(bucket).",
			Columns: []SQLColumnSchema{
				{Field: str("namespace", "OpenTelemetry service namespace"), SQLType: "VARCHAR"},
				{Field: ts("bucket", "Rollup time bucket"), SQLType: "TIMESTAMP"},
				{Field: str("caller", "Calling service"), SQLType: "VARCHAR"},
				{Field: str("callee", "Called service"), SQLType: "VARCHAR"},
				{Field: num("calls", "count", "Calls between services"), SQLType: "BIGINT"},
				{Field: num("avg_ms", "ms", "Average call duration"), SQLType: "DOUBLE"},
				{Field: num("error_rate", "percent", "Error rate"), SQLType: "DOUBLE"},
				{Field: str("edge_type", "Dependency edge type"), SQLType: "VARCHAR"},
			},
		},
		"resource_inventory": {
			Description: "Distinct full resource identities per signal, namespace and service observed inside the logical panel window [start,end). Panel SQL only; unavailable to structured query.from, variables and unscoped SQL. count(*) counts resource identities per signal, not events or instances. Resource extrema describe actual observations inside the selected window, not a historical registry.",
			Columns: []SQLColumnSchema{
				{Field: str("signal", "spans, logs or metrics; select the required signal"), SQLType: "VARCHAR"},
				{Field: str("namespace", "Original resource namespace, preserving NULL and empty values"), SQLType: "VARCHAR"},
				{Field: str("service", "Original service name, preserving NULL and empty values"), SQLType: "VARCHAR"},
				{Field: dict("resource", "Complete typed resource; use resource['key'], with dotted keys written literally"), SQLType: "VARIANT"},
				{Field: ts("first_seen", "Earliest actual event observation inside the selected panel window"), SQLType: "TIMESTAMPTZ_NS"},
				{Field: ts("last_seen", "Latest actual event observation inside the selected panel window"), SQLType: "TIMESTAMPTZ_NS"},
			},
		},
	}
}
