package query

// GetSchema returns a description of the data schema for MCP/LLM context
func GetSchema() string {
	return `
## Fanout Data Schema

The data is stored in Parquet files partitioned by tenant, namespace, and time:
/lake/{signal}/tenant={tenant}/namespace={namespace}/year=YYYY/month=MM/day=DD/hour=HH/part-<ts>.parquet

**IMPORTANT**: Use hive_partitioning=true to access partition columns (tenant, namespace, year, month, day, hour).
Use union_by_name=true to handle schema evolution (old files may not have new columns).

### 1. Spans (Traces)
Table: read_parquet('lake/spans/tenant=*/namespace=*/**/*.parquet', hive_partitioning=true, union_by_name=true)

**IMPORTANT**: Data columns are named with a literal "name=" prefix (e.g. "name=trace_id") and must be double-quoted.
Partition columns (tenant, namespace, year, month, day, hour) have NO prefix.

Data columns:
- "name=trace_id" (VARCHAR): Unique identifier for the entire trace
- "name=span_id" (VARCHAR): Unique identifier for this span
- "name=parent_span_id" (VARCHAR): ID of parent span (empty string for root spans)
- "name=service_name" (VARCHAR): Name of the service generating this span
- "name=name" (VARCHAR): Operation name (e.g., "GET /api/users", "db.query")
- "name=kind" (VARCHAR): Span kind (SPAN_KIND_CLIENT, SPAN_KIND_SERVER, SPAN_KIND_INTERNAL, etc.)
- "name=start_unix_nano" (BIGINT): Start timestamp in nanoseconds since epoch
- "name=end_unix_nano" (BIGINT): End timestamp in nanoseconds since epoch
- "name=duration_ms" (DOUBLE): Duration in milliseconds
- "name=status_code" (VARCHAR): STATUS_CODE_OK, STATUS_CODE_ERROR, or STATUS_CODE_UNSET
- "name=status_msg" (VARCHAR): Error message if status is ERROR
- "name=resource_json" (BLOB): UTF-8 JSON bytes with resource attributes
- "name=attributes_json" (BLOB): UTF-8 JSON bytes with span attributes (http.method, http.status_code, etc.)
- "name=events_json" (BLOB): UTF-8 JSON array of span events [{time_unix_nano, name, attributes}, ...]
- "name=links_json" (BLOB): UTF-8 JSON array of span links [{trace_id, span_id, trace_state, attributes}, ...]
- "name=trace_state" (VARCHAR): W3C trace state string
- "name=flags" (INT32): Span flags (sampled, etc.)
- "name=scope_name" (VARCHAR): Instrumentation scope/library name
- "name=scope_version" (VARCHAR): Instrumentation scope/library version
- "name=ingested_unix_nano" (BIGINT): Ingestion timestamp in nanoseconds

Partition columns (extracted from path, no "name=" prefix):
- tenant (VARCHAR): Tenant identifier (UUIDv7)
- namespace (VARCHAR): Namespace
- year (INTEGER): Year partition
- month (INTEGER): Month partition
- day (INTEGER): Day partition
- hour (INTEGER): Hour partition

Time conversions and filtering:
- For timestamps: to_timestamp("name=start_unix_nano" / 1000000000.0)
- For display: strftime(to_timestamp("name=start_unix_nano" / 1000000000.0), '%Y-%m-%d %H:%M:%S')
- For ordering by time: ORDER BY "name=start_unix_nano" DESC
- Recent data (last N minutes): WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - <minutes>*60) * 1000000000

Common queries:
- Error spans: WHERE "name=status_code" = 'STATUS_CODE_ERROR'
- By service: WHERE "name=service_name" = 'checkout-service'
- By namespace: WHERE namespace = 'opentelemetry-demo'
- By tenant: WHERE tenant = 'default'
- By trace: WHERE "name=trace_id" = '...'
- Root spans only: WHERE "name=parent_span_id" IS NULL OR "name=parent_span_id" = ''

### 2. Logs
Table: read_parquet('lake/logs/tenant=*/namespace=*/**/*.parquet', hive_partitioning=true, union_by_name=true)

Data columns:
- "name=time_unix_nano" (BIGINT): Log timestamp in nanoseconds since epoch
- "name=observed_time_unix_nano" (BIGINT): When log was observed/collected (vs generated)
- "name=severity" (VARCHAR): Severity level text (TRACE, DEBUG, INFO, WARN, ERROR, FATAL)
- "name=severity_number" (INT32): Numeric severity (1-24, higher = more severe)
- "name=body" (VARCHAR): Log message body
- "name=service_name" (VARCHAR): Name of the service generating this log
- "name=trace_id" (VARCHAR): Associated trace ID (if available)
- "name=span_id" (VARCHAR): Associated span ID (if available)
- "name=flags" (INT32): Log record flags
- "name=resource_json" (BLOB): UTF-8 JSON bytes with resource attributes
- "name=attributes_json" (BLOB): UTF-8 JSON bytes with log attributes
- "name=scope_name" (VARCHAR): Instrumentation scope/library name
- "name=scope_version" (VARCHAR): Instrumentation scope/library version
- "name=ingested_unix_nano" (BIGINT): Ingestion timestamp in nanoseconds

Partition columns: tenant, namespace, year, month, day, hour

Common queries:
- Error logs: WHERE "name=severity" IN ('ERROR', 'FATAL') or "name=severity_number" >= 17
- Search logs: WHERE "name=body" LIKE '%error%' or "name=body" ~ 'regex_pattern'
- By service: WHERE "name=service_name" = 'checkout'
- By namespace: WHERE namespace = 'opentelemetry-demo'
- By instrumentation: WHERE "name=scope_name" = 'my-logger'

### 3. Metrics
Table: read_parquet('lake/metrics/tenant=*/namespace=*/**/*.parquet', hive_partitioning=true, union_by_name=true)

Data columns:
- "name=time_unix_nano" (BIGINT): Metric timestamp in nanoseconds since epoch
- "name=name" (VARCHAR): Metric name (e.g., "http.server.duration", "system.cpu.usage")
- "name=description" (VARCHAR): Metric description
- "name=unit" (VARCHAR): Metric unit (e.g., "ms", "By", "1")
- "name=mtype" (VARCHAR): Metric type (gauge, sum, sum_delta, histogram, exp_histogram, summary)
- "name=service_name" (VARCHAR): Name of the service generating this metric
- "name=value" (DOUBLE): Metric value (for GAUGE and SUM types)
- "name=hist_bounds_json" (BLOB): UTF-8 JSON bytes with histogram bucket boundaries (or quantiles for summary)
- "name=hist_counts_json" (BLOB): UTF-8 JSON bytes with histogram bucket counts (or quantile values for summary)
- "name=hist_count" (BIGINT): Total histogram/summary count
- "name=hist_sum" (DOUBLE): Total histogram/summary sum
- "name=exemplars_json" (BLOB): UTF-8 JSON array of exemplars [{time_unix_nano, trace_id, span_id, value, attributes}, ...]
- "name=attributes_json" (BLOB): UTF-8 JSON bytes with metric attributes
- "name=resource_json" (BLOB): UTF-8 JSON bytes with resource attributes
- "name=scope_name" (VARCHAR): Instrumentation scope/library name
- "name=scope_version" (VARCHAR): Instrumentation scope/library version
- "name=ingested_unix_nano" (BIGINT): Ingestion timestamp in nanoseconds

Partition columns: tenant, namespace, year, month, day, hour

Common queries:
- By metric name: WHERE "name=name" = 'http.server.duration'
- By type: WHERE "name=mtype" = 'histogram'
- By unit: WHERE "name=unit" = 'ms'
- Get exemplar traces: SELECT json_extract_string(from_utf8("name=exemplars_json"), '$[0].trace_id') as trace_id

### 4. Rollup Table (service_rollup)
Pre-aggregated data for fast queries:
Table: service_rollup
Columns:
- bucket (TIMESTAMP): 1-minute time bucket
- service (VARCHAR): Service name
- spans (BIGINT): Number of spans in this bucket
- error_rate (DOUBLE): Error rate (0.0 to 1.0)
- p50_ms (DOUBLE): P50 (median) duration
- p95_ms (DOUBLE): 95th percentile duration

Time range:
- Recent data: WHERE bucket >= NOW() - INTERVAL '<minutes> minutes'

## Query Guidelines
1. Always use hive_partitioning=true with read_parquet for partition column access
2. Use union_by_name=true to handle schema evolution (old files may not have new columns)
3. Filter by time to improve performance
4. Use service_rollup table for fast dashboard queries
5. JSON columns are BLOB; convert with from_utf8() before json_extract_*
6. Always include LIMIT clause (default max 1000 rows)
7. Data columns have "name=" prefix and must be double-quoted
8. Partition columns (tenant, namespace, year, month, day, hour) have NO prefix
9. Filter by namespace: WHERE namespace = 'my-namespace'
10. Filter by tenant: WHERE tenant = 'my-tenant'

## DuckDB-Specific Functions
- read_parquet('path/**/*.parquet', hive_partitioning=true, union_by_name=true): Read Parquet with partition columns and schema evolution
- from_utf8(blob_column): Convert BLOB → VARCHAR (for JSON columns)
- json_extract_string(json_text, '$.key'): Extract JSON values
- to_timestamp(seconds): Convert epoch seconds to timestamp
- EXTRACT(EPOCH FROM timestamp): Get epoch seconds from timestamp
- strftime(timestamp, format): Format timestamp as string
- REGEXP_MATCHES(column, 'pattern'): Regex matching
`
}
