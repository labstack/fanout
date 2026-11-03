package engine

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/telemetry"
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

// spanHistogramZero is outside the index range of positive finite durations.
const spanHistogramZero = -2147483648

const createSpanRollupTable = `
CREATE TABLE IF NOT EXISTS span_rollup (
  namespace TEXT,
  bucket TIMESTAMP_NS,
  service TEXT,
  kind TEXT,
  status TEXT,
  spans BIGINT,
  duration_sum DOUBLE,
  duration_min DOUBLE,
  duration_max DOUBLE,
  hist MAP(INTEGER, UBIGINT),
  PRIMARY KEY (namespace, bucket, service, kind, status)
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
  p50_ms DOUBLE DEFAULT 0,
  p95_ms DOUBLE DEFAULT 0,
  PRIMARY KEY (namespace, bucket, caller, callee, edge_type)
);`

const createRollupStateTable = `
CREATE TABLE IF NOT EXISTS rollup_state (
  cache_key TEXT PRIMARY KEY,
  last_ingested_unix_nano BIGINT,
  updated_at TIMESTAMP
);`

const macroAttr = `
CREATE OR REPLACE MACRO attr(attributes, key) AS
  variant_extract(attributes, key);`

// A binding descriptor for panel validation, never the execution source.
const emptyResourceInventory = `SELECT NULL::VARCHAR AS signal,NULL::VARCHAR AS namespace,NULL::VARCHAR AS service,NULL::VARIANT AS resource,NULL::TIMESTAMPTZ_NS AS first_seen,NULL::TIMESTAMPTZ_NS AS last_seen WHERE false`

// CreateParquetViews exposes the fixed format-5 schema. Binding one file
// rather than unioning every footer bounds binder memory. Supplying a schema
// MAP suppresses DuckDB 2's VARIANT extraction pushdown, so read the physical
// schema directly; startup rejects mismatched batch schemas before this point.
// The bound file is the schema batch, listed first: a retired batch may be
// deleted between DuckDB's glob expansion and its bind. Reads of these views
// are bound to the captured catalog whenever it is not empty.
func CreateParquetViews(db *sql.DB, parquetDir string) error {
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS telemetry`); err != nil {
		return err
	}
	for _, signal := range telemetry.Signals {
		stmt := fmt.Sprintf(`CREATE OR REPLACE VIEW telemetry.%s AS SELECT %s FROM read_parquet(%s, union_by_name=false, hive_partitioning=false)`, signal.Name, rawProjection(signal), parquetGlob(parquetDir, signal.Name))
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create parquet view telemetry.%s: %w", signal.Name, err)
		}
	}
	return nil
}

// CreateViews creates stable clean-name views plus the attr() macro.
func CreateViews(db *sql.DB) error {
	statements := []string{macroAttr}
	for _, signal := range telemetry.Signals {
		statements = append(statements, createView(signal.Name))
	}
	for _, stmt := range append(statements, `CREATE OR REPLACE VIEW resource_inventory AS `+emptyResourceInventory) {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create view/macro: %w", err)
		}
	}
	return nil
}

// rawProjection selects a signal's raw relation from its files: every stored
// column except private ones, then the derived columns.
func rawProjection(s *telemetry.Signal) string {
	var private, derived []string
	for _, c := range s.Columns {
		switch {
		case c.Private:
			private = append(private, c.Name)
		case !c.Stored():
			derived = append(derived, c.Expr+" AS "+c.Name)
		}
	}
	projection := "*"
	if len(private) > 0 {
		projection += " EXCLUDE (" + strings.Join(private, ", ") + ")"
	}
	for _, column := range derived {
		projection += ", " + column
	}
	return projection
}

func createView(signal string) string {
	return "CREATE OR REPLACE VIEW " + signal + " AS\n" + cleanSelect(signal, "telemetry."+signal) + ";"
}

// cleanSelect projects a signal's clean view from a raw relation.
func cleanSelect(signal, source string) string {
	s, _ := telemetry.LookupSignal(signal)
	var columns []string
	for _, c := range s.Columns {
		f := c.View
		if f.Name == "" {
			continue
		}
		expr := f.Expr
		if expr == "" {
			expr = c.Name
		}
		if expr != f.Name {
			expr += " AS " + f.Name
		}
		columns = append(columns, "  "+expr)
	}
	return "SELECT\n" + strings.Join(columns, ",\n") + "\nFROM " + source
}
