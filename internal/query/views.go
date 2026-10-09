package query

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

const createServiceRollupTable = `
CREATE TABLE service_rollup (
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
  namespace,
  metric_time::TIMESTAMPTZ_NS AS time,
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

// CreateCacheTables creates only DuckDB's rebuildable query accelerators. The
// production telemetry rows themselves live in immutable Parquet batches.
func CreateCacheTables(db *sql.DB) error {
	if err := ensureCacheTable(db, "service_rollup", createServiceRollupTable,
		"namespace", "bucket", "service", "spans", "served_spans", "p50_ms", "p95_ms", "error_rate", "log_count", "metric_count"); err != nil {
		return err
	}
	if err := ensureCacheTable(db, "edge_rollup", createEdgeRollupTable,
		"namespace", "bucket", "caller", "callee", "calls", "avg_ms", "error_rate", "edge_type"); err != nil {
		return err
	}
	if err := ensureCacheTable(db, "rollup_state", createRollupStateTable,
		"cache_key", "last_ingested_unix_nano", "updated_at"); err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS endpoint_rollup`); err != nil {
		return err
	}
	if err := forgetRollupProgress(db, "endpoint_rollup"); err != nil {
		return err
	}
	if err := createAnnotationTables(db); err != nil {
		return err
	}
	return createBatchCaches(db)
}

// CreateParquetViews exposes the fixed format-3 schema. Binding one file
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
			return forgetRollupProgress(db, table)
		}
	}
	return nil
}

// forgetRollupProgress discards the watermarks belonging to a cache table that
// has just been emptied.
//
// A rollup only rebuilds what its watermark says is missing. Recreating the
// table without clearing that watermark leaves it pointing past every bucket
// that was just dropped, so the history never comes back and every query over
// it reports an empty window — a silent, permanent data loss on upgrade rather
// than a rebuild. Progress keys are named for their table, which is what lets
// this find them.
func forgetRollupProgress(db *sql.DB, table string) error {
	if table == "rollup_state" {
		return nil
	}
	if _, err := db.Exec("DELETE FROM rollup_state WHERE cache_key LIKE ? || '%'", table); err != nil {
		// A fresh database has no rollup_state yet; it is created alongside
		// these tables and has nothing to forget.
		if strings.Contains(err.Error(), "rollup_state") {
			return nil
		}
		return fmt.Errorf("clear %s rollup progress: %w", table, err)
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
