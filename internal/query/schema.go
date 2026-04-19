package query

import "strings"

// GetSchema returns a description of the data schema for MCP/LLM context.
func GetSchema(dataDir string) string {
	return strings.ReplaceAll(schemaTemplate, "{DATA_DIR}", dataDir)
}

const schemaTemplate = `
## Fanout Data Schema

Fanout stores telemetry in DuckLake tables attached under the lake catalog.
The local metadata catalog, query cache, and product state live under {DATA_DIR}.

Primary query surfaces:
- spans view: clean span columns for most queries
- logs view: clean log columns for most queries
- metrics view: clean metric columns for most queries
- service_rollup table: partition-aware cached service health buckets
- edge_rollup table: partition-aware cached service dependency edges

### 1. Spans
Base table: lake.spans
Preferred query surface: spans

Important columns:
- namespace (VARCHAR): partitioning/filtering dimension
- trace_id, span_id, parent_span_id (VARCHAR)
- service, operation, kind (VARCHAR)
- start_time, end_time, ingested_at (TIMESTAMP)
- start_unix_nano, end_unix_nano, ingested_unix_nano (BIGINT)
- duration_ms (DOUBLE)
- status, status_message (VARCHAR)
- attributes_json, resource_json, events_json, links_json (VARCHAR JSON text)
- trace_state, scope_name, scope_version (VARCHAR)
- http_method, http_status_code, http_route (VARCHAR)
- db_system, rpc_method, rpc_service, peer_service (VARCHAR)
- service_version, deployment_env (VARCHAR)
- exception_type, exception_message (VARCHAR)

Common queries:
- Recent spans: SELECT * FROM spans WHERE start_time > now() - INTERVAL 15 MINUTE
- Error spans: ... WHERE status IN ('STATUS_CODE_ERROR', 'ERROR')
- By trace: ... WHERE trace_id = '...'
- Root spans only: ... WHERE parent_span_id IS NULL OR parent_span_id = ''

### 2. Logs
Base table: lake.logs
Preferred query surface: logs

Important columns:
- namespace (VARCHAR)
- time, observed_time, ingested_at (TIMESTAMP)
- time_unix_nano, observed_time_unix_nano, ingested_unix_nano (BIGINT)
- severity, severity_number (VARCHAR/BIGINT)
- body, body_template (VARCHAR)
- service, trace_id, span_id (VARCHAR)
- attributes_json, resource_json (VARCHAR JSON text)
- scope_name, scope_version (VARCHAR)

Common queries:
- Error logs: SELECT * FROM logs WHERE severity IN ('ERROR', 'FATAL')
- Search logs: ... WHERE body ILIKE '%timeout%'
- Trace-correlated logs: ... WHERE trace_id = '...'

### 3. Metrics
Base table: lake.metrics
Preferred query surface: metrics

Important columns:
- namespace (VARCHAR)
- time, ingested_at (TIMESTAMP)
- time_unix_nano, ingested_unix_nano (BIGINT)
- name, type, unit, description (VARCHAR)
- service (VARCHAR)
- value, hist_sum (DOUBLE)
- hist_count (BIGINT)
- hist_bounds_json, hist_counts_json, exemplars_json (VARCHAR JSON text)
- attributes_json, resource_json (VARCHAR JSON text)
- scope_name, scope_version (VARCHAR)

Common queries:
- By metric name: SELECT * FROM metrics WHERE name = 'http.server.duration'
- Histogram exemplars: SELECT json_extract_string(exemplars_json, '$[0].trace_id') FROM metrics ...

### 4. Rollups
service_rollup columns:
- namespace (VARCHAR)
- bucket (TIMESTAMP)
- service (VARCHAR)
- spans, log_count, metric_count (BIGINT)
- p50_ms, p95_ms, error_rate (DOUBLE)

edge_rollup columns:
- namespace (VARCHAR)
- bucket (TIMESTAMP)
- caller, callee, edge_type (VARCHAR)
- calls (BIGINT)
- avg_ms, error_rate (DOUBLE)

## Query Guidelines
1. Prefer spans, logs, and metrics over raw lake.* tables.
2. Always add a recent time filter for large queries.
3. Filter by namespace when relevant.
4. JSON columns are already text; use json_extract_string(attributes_json, '$.key') directly.
5. Use service_rollup and edge_rollup as rebuildable cache tables for dashboards before scanning raw telemetry.
6. Always include a LIMIT unless aggregation makes it unnecessary.

## Useful DuckDB Functions
- json_extract_string(json_text, '$.key')
- time_bucket(INTERVAL '5 minutes', time)
- strftime(timestamp, format)
- approx_quantile(value, 0.95)
`
