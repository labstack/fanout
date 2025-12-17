package query

// GetSchema returns a description of the data schema for MCP/LLM context
func GetSchema() string {
	return `
## Fanout Data Schema

The data is stored in Parquet files partitioned by time: /lake/{signal}/year=YYYY/month=MM/day=DD/hour=HH/part-<ts>.parquet

### 1. Spans (Traces)
Table: read_parquet('lake/spans/**/*.parquet')

**IMPORTANT**: All data columns are named with a literal "name=" prefix (e.g. "name=trace_id") and must be double-quoted in SQL.

Columns:
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
- "name=tenant_id" (VARCHAR): Multi-tenant identifier (empty if not set)
- "name=ingested_unix_nano" (BIGINT): Ingestion timestamp in nanoseconds
- year (INTEGER): Partition column
- month (INTEGER): Partition column
- day (INTEGER): Partition column
- hour (INTEGER/VARCHAR): Partition column (DuckDB may infer VARCHAR due to zero-padding)

Time conversions and filtering:
- For timestamps: to_timestamp("name=start_unix_nano" / 1000000000.0)
- For display: strftime(to_timestamp("name=start_unix_nano" / 1000000000.0), '%Y-%m-%d %H:%M:%S')
- For ordering by time: ORDER BY "name=start_unix_nano" DESC (already in nanoseconds, directly sortable)
- Recent data (last N seconds): WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - <seconds>) * 1000000000
- Recent data (last N minutes): WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - <minutes>*60) * 1000000000

Common queries:
- Error spans: WHERE "name=status_code" = 'STATUS_CODE_ERROR'
- By service: WHERE "name=service_name" = 'checkout-service'
- By trace: WHERE "name=trace_id" = '...'
- Root spans only: WHERE "name=parent_span_id" IS NULL OR "name=parent_span_id" = ''

### 2. Logs
Table: read_parquet('lake/logs/**/*.parquet')
Columns:
- "name=time_unix_nano" (BIGINT): Log timestamp in nanoseconds since epoch
- "name=severity" (VARCHAR): Severity level (TRACE, DEBUG, INFO, WARN, ERROR, FATAL)
- "name=body" (VARCHAR): Log message body
- "name=service_name" (VARCHAR): Name of the service generating this log
- "name=trace_id" (VARCHAR): Associated trace ID (if available)
- "name=span_id" (VARCHAR): Associated span ID (if available)
- "name=resource_json" (BLOB): UTF-8 JSON bytes with resource attributes
- "name=attributes_json" (BLOB): UTF-8 JSON bytes with log attributes
- "name=tenant_id" (VARCHAR): Multi-tenant identifier (empty if not set)
- "name=ingested_unix_nano" (BIGINT): Ingestion timestamp in nanoseconds

Time conversions:
- For timestamps: to_timestamp("name=time_unix_nano" / 1000000000.0)
- For ordering: ORDER BY "name=time_unix_nano" DESC
- Recent logs (last N seconds): WHERE "name=time_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - <seconds>) * 1000000000

Common queries:
- Error logs: WHERE "name=severity" IN ('ERROR', 'FATAL')
- Search logs: WHERE "name=body" LIKE '%error%' or "name=body" ~ 'regex_pattern'
- By service: WHERE "name=service_name" = 'checkout'
- With trace: WHERE "name=trace_id" IS NOT NULL

### 3. Metrics
Table: read_parquet('lake/metrics/**/*.parquet')
Columns:
- "name=time_unix_nano" (BIGINT): Metric timestamp in nanoseconds since epoch
- "name=name" (VARCHAR): Metric name (e.g., "http.server.duration", "system.cpu.usage")
- "name=mtype" (VARCHAR): Metric type (gauge, sum, sum_delta, histogram)
- "name=service_name" (VARCHAR): Name of the service generating this metric
- "name=value" (DOUBLE): Metric value (for GAUGE and SUM types)
- "name=hist_bounds_json" (BLOB): UTF-8 JSON bytes with histogram bucket boundaries (for histogram)
- "name=hist_counts_json" (BLOB): UTF-8 JSON bytes with histogram bucket counts (for histogram)
- "name=hist_count" (BIGINT): Total histogram count
- "name=hist_sum" (DOUBLE): Total histogram sum
- "name=attributes_json" (BLOB): UTF-8 JSON bytes with metric attributes
- "name=resource_json" (BLOB): UTF-8 JSON bytes with resource attributes
- "name=tenant_id" (VARCHAR): Multi-tenant identifier (empty if not set)
- "name=ingested_unix_nano" (BIGINT): Ingestion timestamp in nanoseconds

Time conversions:
- For timestamps: to_timestamp("name=time_unix_nano" / 1000000000.0)
- Recent metrics: WHERE "name=time_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - <seconds>) * 1000000000

Common queries:
- By metric name: WHERE "name=name" = 'http.server.duration'
- By service: WHERE "name=service_name" = 'checkout'
- Gauge metrics: WHERE "name=mtype" = 'gauge'

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
1. Always filter by time to improve performance
2. Use read_parquet() for raw data access
3. Use service_rollup table for fast dashboard queries
4. JSON columns are BLOB; convert with from_utf8() before json_extract_*
5. Always include LIMIT clause (default max 1000 rows)
6. For time windows, use: NOW() - INTERVAL '<N> minutes' or EXTRACT(EPOCH FROM NOW()) * 1000000000 for nanoseconds
7. All data columns in Parquet files have "name=" prefix and must be quoted with double quotes
8. Partition columns (year, month, day, hour) do NOT have "name=" prefix

## DuckDB-Specific Functions
- read_parquet('path/**/*.parquet'): Read Parquet files with glob patterns
- from_utf8(blob_column): Convert BLOB → VARCHAR (for JSON columns)
- json_extract_string(json_text, '$.key'): Extract JSON values
- to_timestamp(seconds): Convert epoch seconds to timestamp
- EXTRACT(EPOCH FROM timestamp): Get epoch seconds from timestamp
- strftime(timestamp, format): Format timestamp as string
- REGEXP_MATCHES(column, 'pattern'): Regex matching
`
}
