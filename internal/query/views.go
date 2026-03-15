package query

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// viewSpans is the CREATE OR REPLACE VIEW for spans with clean column names.
// {lake} is replaced with the actual lake directory path at runtime.
const viewSpans = `
CREATE OR REPLACE VIEW spans AS
SELECT
  "name=trace_id" AS trace_id,
  "name=span_id" AS span_id,
  "name=parent_span_id" AS parent_span_id,
  "name=service_name" AS service,
  "name=name" AS operation,
  "name=kind" AS kind,
  to_timestamp("name=start_unix_nano" / 1e9) AS start_time,
  to_timestamp("name=end_unix_nano" / 1e9) AS end_time,
  "name=duration_ms" AS duration_ms,
  "name=status_code" AS status,
  "name=status_msg" AS status_message,
  decode("name=attributes_json") AS attributes_json,
  decode("name=resource_json") AS resource_json,
  decode("name=events_json") AS events_json,
  namespace, tenant
FROM read_parquet('{lake}/spans/**/*.parquet',
     hive_partitioning=true, union_by_name=true);`

// viewLogs is the CREATE OR REPLACE VIEW for logs with clean column names.
const viewLogs = `
CREATE OR REPLACE VIEW logs AS
SELECT
  to_timestamp("name=time_unix_nano" / 1e9) AS time,
  "name=severity" AS severity,
  "name=body" AS body,
  "name=service_name" AS service,
  "name=trace_id" AS trace_id,
  "name=span_id" AS span_id,
  decode("name=attributes_json") AS attributes_json,
  decode("name=resource_json") AS resource_json,
  namespace, tenant
FROM read_parquet('{lake}/logs/**/*.parquet',
     hive_partitioning=true, union_by_name=true);`

// viewMetrics is the CREATE OR REPLACE VIEW for metrics with clean column names.
const viewMetrics = `
CREATE OR REPLACE VIEW metrics AS
SELECT
  to_timestamp("name=time_unix_nano" / 1e9) AS time,
  "name=name" AS name,
  "name=mtype" AS type,
  "name=value" AS value,
  "name=unit" AS unit,
  "name=service_name" AS service,
  "name=description" AS description,
  decode("name=attributes_json") AS attributes_json,
  decode("name=resource_json") AS resource_json,
  namespace, tenant
FROM read_parquet('{lake}/metrics/**/*.parquet',
     hive_partitioning=true, union_by_name=true);`

// macroAttr creates the attr() convenience macro for extracting JSON keys.
const macroAttr = `
CREATE OR REPLACE MACRO attr(json_col, key) AS
  json_extract_string(json_col, '$.' || key);`

// placeholders maps each signal to the SQL that writes a zero-row sentinel
// Parquet file. The sentinel establishes the column schema so that DuckDB can
// validate the view definition even before real data arrives. We use
// union_by_name=true in the views, so additional columns from real data files
// are accepted transparently.
//
// Each sentinel is written to:
//
//	{lakeDir}/{signal}/_placeholder.parquet
var placeholders = map[string]string{
	"spans": `COPY (
SELECT
  NULL::VARCHAR AS "name=trace_id",
  NULL::VARCHAR AS "name=span_id",
  NULL::VARCHAR AS "name=parent_span_id",
  NULL::VARCHAR AS "name=service_name",
  NULL::VARCHAR AS "name=name",
  NULL::VARCHAR AS "name=kind",
  NULL::BIGINT  AS "name=start_unix_nano",
  NULL::BIGINT  AS "name=end_unix_nano",
  NULL::DOUBLE  AS "name=duration_ms",
  NULL::VARCHAR AS "name=status_code",
  NULL::VARCHAR AS "name=status_msg",
  NULL::BLOB    AS "name=attributes_json",
  NULL::BLOB    AS "name=resource_json",
  NULL::BLOB    AS "name=events_json",
  NULL::VARCHAR AS namespace,
  NULL::VARCHAR AS tenant
WHERE false
) TO '{path}' (FORMAT parquet);`,

	"logs": `COPY (
SELECT
  NULL::BIGINT  AS "name=time_unix_nano",
  NULL::VARCHAR AS "name=severity",
  NULL::VARCHAR AS "name=body",
  NULL::VARCHAR AS "name=service_name",
  NULL::VARCHAR AS "name=trace_id",
  NULL::VARCHAR AS "name=span_id",
  NULL::BLOB    AS "name=attributes_json",
  NULL::BLOB    AS "name=resource_json",
  NULL::VARCHAR AS namespace,
  NULL::VARCHAR AS tenant
WHERE false
) TO '{path}' (FORMAT parquet);`,

	"metrics": `COPY (
SELECT
  NULL::BIGINT  AS "name=time_unix_nano",
  NULL::VARCHAR AS "name=name",
  NULL::VARCHAR AS "name=mtype",
  NULL::DOUBLE  AS "name=value",
  NULL::VARCHAR AS "name=unit",
  NULL::VARCHAR AS "name=service_name",
  NULL::VARCHAR AS "name=description",
  NULL::BLOB    AS "name=attributes_json",
  NULL::BLOB    AS "name=resource_json",
  NULL::VARCHAR AS namespace,
  NULL::VARCHAR AS tenant
WHERE false
) TO '{path}' (FORMAT parquet);`,
}

// MigrateOldPartitions moves parquet files that sit directly under day=
// directories into hour=00/ subdirectories. Older compaction code wrote
// compacted.parquet at the day level, but DuckDB requires consistent Hive
// partition depth across all files.
func MigrateOldPartitions(lakeDir string) {
	for _, signal := range []string{"spans", "logs", "metrics"} {
		pattern := filepath.Join(lakeDir, signal, "tenant=*", "namespace=*",
			"year=*", "month=*", "day=*", "*.parquet")
		matches, _ := filepath.Glob(pattern)
		for _, f := range matches {
			dayDir := filepath.Dir(f)
			hourDir := filepath.Join(dayDir, "hour=00")
			if err := os.MkdirAll(hourDir, 0o755); err != nil {
				continue
			}
			dest := filepath.Join(hourDir, filepath.Base(f))
			if err := os.Rename(f, dest); err == nil {
				slog.Info("migrated old partition file", "from", f, "to", dest)
			}
		}
	}
}

// CreateViews creates (or replaces) the clean-name DuckDB views over Parquet
// files and the attr() macro. lakeDir is substituted for the {lake} placeholder
// in all view definitions.
//
// Views are created with CREATE OR REPLACE so this is safe to call multiple
// times (e.g., after a recovery restart). DuckDB's read_parquet raises an IO
// error at view-creation time when the glob pattern matches no files, so we
// write a zero-row sentinel Parquet file into each signal directory. This
// ensures the views are always registered in the catalog and return empty result
// sets until real data arrives.
func CreateViews(db *sql.DB, lakeDir string) error {
	// Validate lakeDir before substituting into SQL strings.
	if strings.ContainsAny(lakeDir, "'\"\\;") {
		return fmt.Errorf("lake dir contains unsafe characters: %q", lakeDir)
	}

	// Write sentinel Parquet files so read_parquet globs are non-empty.
	// Skip if real data already exists — the placeholder lacks hive partition
	// columns and conflicts with hive-partitioned real files.
	for signal, copySQLTemplate := range placeholders {
		dir := filepath.Join(lakeDir, signal)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		dest := filepath.Join(dir, "_placeholder.parquet")
		// If real data exists (any subdirectory with parquet files),
		// remove stale placeholder and skip creation.
		if hasRealData(dir) {
			os.Remove(dest) // clean up stale placeholder if present
			continue
		}
		// Skip if placeholder already exists — avoid repeated COPY on every restart.
		if _, statErr := os.Stat(dest); statErr == nil {
			continue
		}
		copySQL := strings.ReplaceAll(copySQLTemplate, "{path}", dest)
		if _, err := db.Exec(copySQL); err != nil {
			return fmt.Errorf("write placeholder for %s: %w", signal, err)
		}
	}

	stmts := []struct {
		name string
		sql  string
	}{
		{"spans view", viewSpans},
		{"logs view", viewLogs},
		{"metrics view", viewMetrics},
		{"attr() macro", macroAttr},
	}

	for _, s := range stmts {
		q := strings.ReplaceAll(s.sql, "{lake}", lakeDir)
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("create %s: %w", s.name, err)
		}
	}
	return nil
}

// hasRealData checks if a signal directory contains any parquet files beyond
// the root-level placeholder (i.e., in hive-partitioned subdirectories).
func hasRealData(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			// Any subdirectory means hive-partitioned data exists
			return true
		}
	}
	return false
}
