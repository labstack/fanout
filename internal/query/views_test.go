package query

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

func openTestDuck(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	if _, err := db.Exec(`ATTACH ':memory:' AS lake`); err != nil {
		t.Fatalf("attach lake catalog: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCreateViews_ViewsExist(t *testing.T) {
	db := openTestDuck(t)

	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	for _, view := range []string{"spans", "logs", "metrics"} {
		var count int
		err := db.QueryRowContext(context.Background(),
			`SELECT count(*) FROM duckdb_views() WHERE view_name = ?`, view).Scan(&count)
		if err != nil {
			t.Errorf("query duckdb_views for %q: %v", view, err)
			continue
		}
		if count != 1 {
			t.Errorf("view %q not found in duckdb_views() (count=%d)", view, count)
		}
	}
}

func TestCreateTables_CacheTablesIncludePartitionColumns(t *testing.T) {
	db := openTestDuck(t)

	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}

	rows, err := db.Query(`
SELECT table_name, column_name
FROM duckdb_columns()
WHERE table_name IN ('service_rollup', 'edge_rollup', 'rollup_state')
ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatalf("query duckdb_columns failed: %v", err)
	}
	defer rows.Close()

	seen := map[string]map[string]bool{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("scan duckdb_columns row: %v", err)
		}
		if seen[table] == nil {
			seen[table] = map[string]bool{}
		}
		seen[table][column] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate duckdb_columns: %v", err)
	}

	required := map[string][]string{
		"service_rollup": {"tenant", "namespace", "bucket", "service"},
		"edge_rollup":    {"tenant", "namespace", "bucket", "caller", "callee", "edge_type"},
		"rollup_state":   {"cache_key", "last_ingested_unix_nano", "updated_at"},
	}
	for table, columns := range required {
		for _, column := range columns {
			if !seen[table][column] {
				t.Errorf("%s missing column %q", table, column)
			}
		}
	}
}

func TestCreateViews_AttrMacroWorks(t *testing.T) {
	db := openTestDuck(t)

	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	var result string
	if err := db.QueryRowContext(context.Background(),
		`SELECT attr('{"key":"hello"}', 'key')`).Scan(&result); err != nil {
		t.Fatalf("attr() macro failed: %v", err)
	}
	if result != "hello" {
		t.Errorf("attr() = %q, want %q", result, "hello")
	}
}
