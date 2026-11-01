package engine

import (
	"database/sql"
	"fmt"
	"path/filepath"
)

const createServiceRollupTable = `
CREATE TABLE IF NOT EXISTS service_rollup (
  namespace TEXT,
  bucket TIMESTAMP,
  service TEXT,
  spans BIGINT,
  -- Spans that describe work this service performed, rather than a call it was
  -- waiting on. A bucket with none of them has no latency of its own to report,
  -- and readers prefer the buckets that do; see the rollup's span_agg.
  served_spans BIGINT DEFAULT 0,
  p50_ms DOUBLE,
  p95_ms DOUBLE,
  error_rate DOUBLE,
  log_count BIGINT DEFAULT 0,
  metric_count BIGINT DEFAULT 0,
  PRIMARY KEY (namespace, bucket, service)
);`

const createEdgeRollupTable = `
CREATE TABLE IF NOT EXISTS edge_rollup (
  namespace TEXT,
  bucket TIMESTAMP,
  caller TEXT,
  callee TEXT,
  calls BIGINT,
  avg_ms DOUBLE,
  error_rate DOUBLE,
  edge_type TEXT DEFAULT 'call',
  PRIMARY KEY (namespace, bucket, caller, callee, edge_type)
);`

const createRollupStateTable = `
CREATE TABLE IF NOT EXISTS rollup_state (
  cache_key TEXT PRIMARY KEY,
  last_ingested_unix_nano BIGINT,
  updated_at TIMESTAMP
);`

const viewSpans = `
CREATE OR REPLACE VIEW spans AS
SELECT
  namespace,
  trace_id,
  span_id,
  parent_span_id,
  service,
  operation,
  kind,
  start_time::TIMESTAMPTZ_NS AS start_time,
  end_time::TIMESTAMPTZ_NS AS end_time,
  start_unix_nano,
  end_unix_nano,
  duration_ms,
  status,
  status_message,
  resource,
  attributes,
  events_json,
  links_json,
  trace_state,
  flags,
  scope_name,
  scope_version,
  ingested_at::TIMESTAMPTZ_NS AS ingested_at,
  ingested_unix_nano,
  http_method,
  http_status_code,
  http_route,
  db_system,
  rpc_method,
  rpc_service,
  peer_service,
  service_version,
  deployment_env,
  exception_type,
  exception_message,
  messaging_system,
  messaging_destination
FROM telemetry.spans;`

const viewLogs = `
CREATE OR REPLACE VIEW logs AS
SELECT
  namespace,
  log_time::TIMESTAMPTZ_NS AS time,
  observed_time::TIMESTAMPTZ_NS AS observed_time,
  time_unix_nano,
  observed_time_unix_nano,
  severity,
  severity_number,
  body,
  service,
  trace_id,
  span_id,
  flags,
  resource,
  attributes,
  scope_name,
  scope_version,
  ingested_at::TIMESTAMPTZ_NS AS ingested_at,
  ingested_unix_nano,
  body_template
FROM telemetry.logs;`

const viewMetrics = `
CREATE OR REPLACE VIEW metrics AS
SELECT
  series_id,
  namespace,
  metric_time::TIMESTAMPTZ_NS AS time,
  time_unix_nano,
  name,
  description,
  unit,
  CASE WHEN metric_type='sum' AND monotonic THEN 'counter'
       WHEN metric_type='sum' THEN 'updown'
       WHEN metric_type='exponential_histogram' THEN 'histogram'
       ELSE metric_type END AS type,
  temporality,
  -- The int64 extremes are the engine's infinity sentinels and do not convert;
  -- TRY keeps one such client-supplied point from failing every query.
  CASE WHEN start_time_unix_nano=0 THEN NULL
       ELSE TRY(make_timestamp_ns(start_time_unix_nano)::TIMESTAMPTZ_NS) END AS start_time,
  service,
  value,
  hist_bounds_json,
  hist_counts_json,
  hist_count,
  hist_sum,
  exemplars_json,
  attributes,
  resource,
  scope_name,
  scope_version,
  ingested_at::TIMESTAMPTZ_NS AS ingested_at,
  ingested_unix_nano
FROM telemetry.metrics;`

const macroAttr = `
CREATE OR REPLACE MACRO attr(attributes, key) AS
  variant_extract(attributes, key);`

// A binding descriptor for panel validation, never the execution source.
const emptyResourceInventory = `SELECT NULL::VARCHAR AS signal,NULL::VARCHAR AS namespace,NULL::VARCHAR AS service,NULL::VARIANT AS resource,NULL::TIMESTAMPTZ_NS AS first_seen,NULL::TIMESTAMPTZ_NS AS last_seen WHERE false`

// CreateParquetViews exposes the fixed format-4 schema. Binding one file
// rather than unioning every footer bounds binder memory. Supplying a schema
// MAP suppresses DuckDB 2's VARIANT extraction pushdown, so read the physical
// schema directly; startup rejects mismatched batch schemas before this point.
func CreateParquetViews(db *sql.DB, parquetDir string) error {
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS telemetry`); err != nil {
		return err
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		pattern := filepath.ToSlash(filepath.Join(parquetDir, "batches", "*.batch", signal+".parquet"))
		projection := "*"
		if signal == "spans" {
			projection += " EXCLUDE (_trace_hash), TRY_CAST(attributes['messaging.system'] AS VARCHAR) AS messaging_system, TRY_CAST(attributes['messaging.destination.name'] AS VARCHAR) AS messaging_destination"
		}
		stmt := fmt.Sprintf(`CREATE OR REPLACE VIEW telemetry.%s AS SELECT %s FROM read_parquet(%s, union_by_name=false, hive_partitioning=false)`, signal, projection, sqlLiteral(pattern))
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create parquet view telemetry.%s: %w", signal, err)
		}
	}
	return nil
}

// CreateViews creates stable clean-name views plus the attr() macro.
func CreateViews(db *sql.DB) error {
	for _, stmt := range []string{macroAttr, viewSpans, viewLogs, viewMetrics, `CREATE OR REPLACE VIEW resource_inventory AS ` + emptyResourceInventory} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create view/macro: %w", err)
		}
	}
	return nil
}
