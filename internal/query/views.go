package query

import (
	"database/sql"
	"fmt"
	"path/filepath"
)

const createSpansTable = `
CREATE TABLE IF NOT EXISTS telemetry.spans (
  namespace VARCHAR,
  trace_id VARCHAR,
  span_id VARCHAR,
  parent_span_id VARCHAR,
  service VARCHAR,
  operation VARCHAR,
  kind VARCHAR,
  start_time TIMESTAMP,
  end_time TIMESTAMP,
  start_unix_nano BIGINT,
  end_unix_nano BIGINT,
  duration_ms DOUBLE,
  status VARCHAR,
  status_message VARCHAR,
  resource_json VARCHAR,
  attributes_json VARCHAR,
  events_json VARCHAR,
  links_json VARCHAR,
  trace_state VARCHAR,
  flags BIGINT,
  scope_name VARCHAR,
  scope_version VARCHAR,
  ingested_at TIMESTAMP,
  ingested_unix_nano BIGINT,
  http_method VARCHAR,
  http_status_code VARCHAR,
  http_route VARCHAR,
  db_system VARCHAR,
  rpc_method VARCHAR,
  rpc_service VARCHAR,
  peer_service VARCHAR,
  service_version VARCHAR,
  deployment_env VARCHAR,
  exception_type VARCHAR,
  exception_message VARCHAR
);`

const createLogsTable = `
CREATE TABLE IF NOT EXISTS telemetry.logs (
  namespace VARCHAR,
  log_time TIMESTAMP,
  observed_time TIMESTAMP,
  time_unix_nano BIGINT,
  observed_time_unix_nano BIGINT,
  severity VARCHAR,
  severity_number BIGINT,
  body VARCHAR,
  service VARCHAR,
  trace_id VARCHAR,
  span_id VARCHAR,
  flags BIGINT,
  resource_json VARCHAR,
  attributes_json VARCHAR,
  scope_name VARCHAR,
  scope_version VARCHAR,
  ingested_at TIMESTAMP,
  ingested_unix_nano BIGINT,
  body_template VARCHAR
);`

const createMetricsTable = `
CREATE TABLE IF NOT EXISTS telemetry.metrics (
  namespace VARCHAR,
  metric_time TIMESTAMP,
  time_unix_nano BIGINT,
  name VARCHAR,
  description VARCHAR,
  unit VARCHAR,
  metric_type VARCHAR,
  service VARCHAR,
  value DOUBLE,
  hist_bounds_json VARCHAR,
  hist_counts_json VARCHAR,
  hist_count BIGINT,
  hist_sum DOUBLE,
  exemplars_json VARCHAR,
  attributes_json VARCHAR,
  resource_json VARCHAR,
  scope_name VARCHAR,
  scope_version VARCHAR,
  ingested_at TIMESTAMP,
  ingested_unix_nano BIGINT
);`

const createServiceRollupTable = `
CREATE TABLE service_rollup (
  namespace TEXT,
  bucket TIMESTAMP,
  service TEXT,
  spans BIGINT,
  p50_ms DOUBLE,
  p95_ms DOUBLE,
  error_rate DOUBLE,
  log_count BIGINT DEFAULT 0,
  metric_count BIGINT DEFAULT 0,
  PRIMARY KEY (namespace, bucket, service)
);`

const createEdgeRollupTable = `
CREATE TABLE edge_rollup (
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

const createEndpointRollupTable = `
CREATE TABLE endpoint_rollup (
  namespace TEXT,
  bucket TIMESTAMP,
  service TEXT,
  method TEXT,
  path TEXT,
  calls BIGINT,
  error_count BIGINT,
  duration_count BIGINT,
  duration_buckets STRUCT(
    le_0_1 UBIGINT,
    le_0_5 UBIGINT,
    le_1 UBIGINT,
    le_2_5 UBIGINT,
    le_5 UBIGINT,
    le_10 UBIGINT,
    le_25 UBIGINT,
    le_50 UBIGINT,
    le_100 UBIGINT,
    le_250 UBIGINT,
    le_500 UBIGINT,
    le_750 UBIGINT,
    le_1000 UBIGINT,
    le_2000 UBIGINT,
    le_5000 UBIGINT,
    le_30000 UBIGINT,
    le_300000 UBIGINT
  ),
  PRIMARY KEY (namespace, bucket, service, method, path)
);`

const createRollupStateTable = `
CREATE TABLE rollup_state (
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
  start_time,
  end_time,
  start_unix_nano,
  end_unix_nano,
  duration_ms,
  status,
  status_message,
  resource_json,
  attributes_json,
  events_json,
  links_json,
  trace_state,
  flags,
  scope_name,
  scope_version,
  ingested_at,
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
  exception_message
FROM telemetry.spans;`

const viewLogs = `
CREATE OR REPLACE VIEW logs AS
SELECT
  namespace,
  log_time AS time,
  observed_time,
  time_unix_nano,
  observed_time_unix_nano,
  severity,
  severity_number,
  body,
  service,
  trace_id,
  span_id,
  flags,
  resource_json,
  attributes_json,
  scope_name,
  scope_version,
  ingested_at,
  ingested_unix_nano,
  body_template
FROM telemetry.logs;`

const viewMetrics = `
CREATE OR REPLACE VIEW metrics AS
SELECT
  namespace,
  metric_time AS time,
  time_unix_nano,
  name,
  description,
  unit,
  metric_type AS type,
  service,
  value,
  hist_bounds_json,
  hist_counts_json,
  hist_count,
  hist_sum,
  exemplars_json,
  attributes_json,
  resource_json,
  scope_name,
  scope_version,
  ingested_at,
  ingested_unix_nano
FROM telemetry.metrics;`

const macroAttr = `
CREATE OR REPLACE MACRO attr(json_col, key) AS
  json_extract_string(json_col, '$."' || key || '"');`

// CreateTables creates mutable telemetry tables for query-kernel tests and
// benchmarks. Production startup never calls this function: telemetry is
// exposed exclusively through CreateParquetViews.
func CreateTables(db *sql.DB) error {
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS telemetry`); err != nil {
		return fmt.Errorf("create telemetry schema: %w", err)
	}
	for _, stmt := range []string{createSpansTable, createLogsTable, createMetricsTable} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return CreateCacheTables(db)
}

// CreateCacheTables creates only DuckDB's rebuildable query accelerators. The
// production telemetry rows themselves live in immutable Parquet batches.
func CreateCacheTables(db *sql.DB) error {
	if err := ensureCacheTable(db, "service_rollup", createServiceRollupTable,
		"namespace", "bucket", "service", "spans", "p50_ms", "p95_ms", "error_rate", "log_count", "metric_count"); err != nil {
		return err
	}
	if err := ensureCacheTable(db, "edge_rollup", createEdgeRollupTable,
		"namespace", "bucket", "caller", "callee", "calls", "avg_ms", "error_rate", "edge_type"); err != nil {
		return err
	}
	if err := ensureCacheTable(db, "endpoint_rollup", createEndpointRollupTable,
		"namespace", "bucket", "service", "method", "path", "calls", "error_count", "duration_count", "duration_buckets"); err != nil {
		return err
	}
	if err := ensureCacheTable(db, "rollup_state", createRollupStateTable,
		"cache_key", "last_ingested_unix_nano", "updated_at"); err != nil {
		return err
	}
	return nil
}

// CreateParquetViews exposes the repository's open Parquet files under the
// canonical telemetry schema used by Fanout's SQL kernel.
func CreateParquetViews(db *sql.DB, parquetDir string) error {
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS telemetry`); err != nil {
		return err
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		pattern := filepath.ToSlash(filepath.Join(parquetDir, "batches", "*.batch", signal+".parquet"))
		projection := "*"
		if signal == "spans" {
			projection = "* EXCLUDE (_trace_hash)"
		}
		stmt := fmt.Sprintf(`CREATE OR REPLACE VIEW telemetry.%s AS SELECT %s FROM read_parquet(%s, union_by_name=true)`, signal, projection, sqlLiteral(pattern))
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create parquet view telemetry.%s: %w", signal, err)
		}
	}
	return nil
}

// CreateViews creates stable clean-name views plus the attr() macro.
func CreateViews(db *sql.DB) error {
	for _, stmt := range []string{macroAttr, viewSpans, viewLogs, viewMetrics} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create view/macro: %w", err)
		}
	}
	return nil
}

func ensureCacheTable(db *sql.DB, table, createStmt string, requiredColumns ...string) error {
	columns, err := cacheTableColumns(db, table)
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
	}
	if len(columns) == 0 {
		if _, err := db.Exec(createStmt); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
		return nil
	}
	for _, column := range requiredColumns {
		if _, ok := columns[column]; !ok {
			if _, err := db.Exec("DROP TABLE " + table); err != nil {
				return fmt.Errorf("drop stale %s: %w", table, err)
			}
			if _, err := db.Exec(createStmt); err != nil {
				return fmt.Errorf("recreate %s: %w", table, err)
			}
			return nil
		}
	}
	return nil
}

func cacheTableColumns(db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.Query(`SELECT column_name FROM duckdb_columns() WHERE table_name = ?`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	return columns, rows.Err()
}
