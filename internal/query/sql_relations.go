package query

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
		"span_rollup": {
			Description: "Per-minute spans by namespace, service, kind and status. Panel SQL only; filter bucket with $__window(bucket). Counts and duration aggregates merge across rows. Percentiles interpolate merged fixed-scale-3 OpenTelemetry exponential histograms and are approximate within one positive bucket (9.05%). Structured RED panels route here at minute resolution; fresh_to_ms reports the ingest horizon, usually 1–1.5 minutes behind ingestion. Window boundaries have minute precision.",
			Columns: []SQLColumnSchema{
				{Field: str("namespace", "OpenTelemetry service namespace"), SQLType: "VARCHAR"},
				{Field: ts("bucket", "UTC minute bucket"), SQLType: "TIMESTAMP_NS"},
				{Field: str("service", "Service name"), SQLType: "VARCHAR"},
				{Field: str("kind", "Span kind"), SQLType: "VARCHAR"},
				{Field: str("status", "Span status"), SQLType: "VARCHAR"},
				{Field: num("spans", "count", "Span count"), SQLType: "BIGINT"},
				{Field: num("duration_sum", "ms", "Sum of span durations"), SQLType: "DOUBLE"},
				{Field: num("duration_min", "ms", "Minimum span duration"), SQLType: "DOUBLE"},
				{Field: num("duration_max", "ms", "Maximum span duration"), SQLType: "DOUBLE"},
				{Field: dict("hist", "Duration bucket counts: ceil(log2(duration_ms)*8); -2147483648 represents zero or negative durations"), SQLType: "MAP(INTEGER, UBIGINT)"},
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

func str(name, description string) Field {
	return Field{Name: name, Type: TypeString, Description: description}
}
func num(name, unit, description string) Field {
	return Field{Name: name, Type: TypeNumber, Unit: unit, Description: description}
}
func ts(name, description string) Field {
	return Field{Name: name, Type: TypeTime, Description: description}
}
func dict(name, description string) Field {
	return Field{Name: name, Type: TypeMap, Description: description}
}
