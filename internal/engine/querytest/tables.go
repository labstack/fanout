// Package querytest builds mutable fixtures; production reads immutable Parquet.
package querytest

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/telemetry"
)

// types are the engine types the raw relation reads from each physical type.
var types = map[telemetry.Type]string{
	telemetry.String: "VARCHAR", telemetry.Int32: "INTEGER", telemetry.Int64: "BIGINT", telemetry.Uint32: "UINTEGER",
	telemetry.Uint64: "UBIGINT", telemetry.Float64: "DOUBLE", telemetry.Bool: "BOOLEAN", telemetry.Time: "TIMESTAMP_NS",
	telemetry.Variant: "VARIANT",
}

// createTable mirrors a signal's raw relation; derived columns are generated.
func createTable(s *telemetry.Signal) string {
	var columns []string
	for _, c := range s.Columns {
		if c.Private {
			continue
		}
		column := "  " + c.Name + " " + types[c.Type]
		if c.List {
			column += "[]"
		}
		if !c.Stored() {
			column += " GENERATED ALWAYS AS (" + c.Expr + ")"
		}
		columns = append(columns, column)
	}
	return "CREATE TABLE IF NOT EXISTS telemetry." + s.Name + " (\n" + strings.Join(columns, ",\n") + "\n);"
}

func CreateTables(db *sql.DB, createCaches func(*sql.DB) error) error {
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS telemetry`); err != nil {
		return fmt.Errorf("create telemetry schema: %w", err)
	}
	for _, signal := range telemetry.Signals {
		if _, err := db.Exec(createTable(signal)); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return createCaches(db)
}

// RefreshSpanRollup publishes summaries for read-side fixtures that do not run
// the background engine loop. Engine write-path tests use the real rollup pass.
func RefreshSpanRollup(db *sql.DB) error {
	if _, err := db.Exec(`DELETE FROM span_rollup`); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO span_rollup SELECT namespace,date_trunc('minute',start_time::TIMESTAMP_NS),service,coalesce(kind,''),coalesce(status,''),count(*),sum(duration_ms),min(duration_ms),max(duration_ms),histogram(CASE WHEN duration_ms>0 THEN coalesce(try_cast(ceil(log2(duration_ms)*8) AS INTEGER),2147483647) ELSE -2147483648 END) FROM spans WHERE service IS NOT NULL GROUP BY ALL`)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM log_rollup; INSERT INTO log_rollup SELECT namespace,date_trunc('minute',time::TIMESTAMP_NS),service,coalesce(severity,''),count(*) FROM logs WHERE service IS NOT NULL GROUP BY ALL`); err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM endpoint_rollup; INSERT INTO endpoint_rollup SELECT namespace,date_trunc('minute',start_time::TIMESTAMP_NS),service,coalesce(kind,''),coalesce(status,''),_a_http_route,false,count(*),sum(duration_ms),min(duration_ms),max(duration_ms),histogram(CASE WHEN duration_ms>0 THEN coalesce(try_cast(ceil(log2(duration_ms)*8) AS INTEGER),2147483647) ELSE -2147483648 END) FROM spans WHERE service IS NOT NULL AND _a_http_route<>'' GROUP BY ALL`)
	return err
}
