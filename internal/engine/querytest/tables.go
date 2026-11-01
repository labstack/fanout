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
	telemetry.String: "VARCHAR", telemetry.Int64: "BIGINT", telemetry.Uint64: "UBIGINT", telemetry.Float64: "DOUBLE",
	telemetry.Bool: "BOOLEAN", telemetry.Time: "TIMESTAMP_NS", telemetry.Variant: "VARIANT",
}

// createTable mirrors a signal's raw relation; derived columns are generated.
func createTable(s *telemetry.Signal) string {
	var columns []string
	for _, c := range s.Columns {
		if c.Private {
			continue
		}
		column := "  " + c.Name + " " + types[c.Type]
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
