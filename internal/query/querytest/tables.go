// Package querytest builds mutable fixtures; production reads immutable Parquet.
package querytest

import (
	"database/sql"
	"fmt"
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
  start_time TIMESTAMP_NS,
  end_time TIMESTAMP_NS,
  start_unix_nano BIGINT,
  end_unix_nano BIGINT,
  duration_ms DOUBLE,
  status VARCHAR,
  status_message VARCHAR,
  resource VARIANT,
  attributes VARIANT,
  events_json VARCHAR,
  links_json VARCHAR,
  trace_state VARCHAR,
  flags BIGINT,
  scope_name VARCHAR,
  scope_version VARCHAR,
  ingested_at TIMESTAMP_NS,
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
  exception_message VARCHAR,
  messaging_system VARCHAR GENERATED ALWAYS AS (TRY_CAST(attributes['messaging.system'] AS VARCHAR)),
  messaging_destination VARCHAR GENERATED ALWAYS AS (TRY_CAST(attributes['messaging.destination.name'] AS VARCHAR))
);`

const createLogsTable = `
CREATE TABLE IF NOT EXISTS telemetry.logs (
  namespace VARCHAR,
  log_time TIMESTAMP_NS,
  observed_time TIMESTAMP_NS,
  time_unix_nano BIGINT,
  observed_time_unix_nano BIGINT,
  severity VARCHAR,
  severity_number BIGINT,
  body VARCHAR,
  service VARCHAR,
  trace_id VARCHAR,
  span_id VARCHAR,
  flags BIGINT,
  resource VARIANT,
  attributes VARIANT,
  scope_name VARCHAR,
  scope_version VARCHAR,
  ingested_at TIMESTAMP_NS,
  ingested_unix_nano BIGINT,
  body_template VARCHAR
);`

const createMetricsTable = `
CREATE TABLE IF NOT EXISTS telemetry.metrics (
  namespace VARCHAR,
  metric_time TIMESTAMP_NS,
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
  attributes VARIANT,
  resource VARIANT,
  scope_name VARCHAR,
  scope_version VARCHAR,
  ingested_at TIMESTAMP_NS,
  ingested_unix_nano BIGINT
);`

func CreateTables(db *sql.DB, createCaches func(*sql.DB) error) error {
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS telemetry`); err != nil {
		return fmt.Errorf("create telemetry schema: %w", err)
	}
	for _, stmt := range []string{createSpansTable, createLogsTable, createMetricsTable} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return createCaches(db)
}
