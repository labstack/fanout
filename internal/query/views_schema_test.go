package query

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The telemetry views must survive a build that adds a column: older batches
// on disk lack it and must read as NULL rather than failing the glob.
//
// This is not a theoretical concern. CreateViews names every column explicitly
// and DuckDB binds a view eagerly, so a glob that rejects one old file fails at
// NewDuck and the process does not start at all -- it stays down until every
// pre-deploy batch has aged out. An earlier attempt to cut the views' bind cost
// by dropping union_by_name did exactly that, which is why this test exists
// rather than a comment saying to be careful.
func TestParquetViewsToleratePreDeploySchemas(t *testing.T) {
	dir := t.TempDir()
	batches := filepath.Join(dir, "batches")
	for _, name := range []string{"_schema.batch", "old.batch", "new.batch"} {
		if err := os.MkdirAll(filepath.Join(batches, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	write := func(batch, signal, sel string) {
		t.Helper()
		path := filepath.ToSlash(filepath.Join(batches, batch, signal+".parquet"))
		if _, err := db.Exec(fmt.Sprintf(`COPY (%s) TO '%s' (FORMAT PARQUET)`, sel, path)); err != nil {
			t.Fatalf("write %s/%s: %v", batch, signal, err)
		}
	}
	// The current build knows about added_later; the old batch predates it.
	for _, signal := range []string{"spans", "logs", "metrics"} {
		write("_schema.batch", signal, "SELECT 1 AS id, 'x' AS added_later WHERE false")
		write("old.batch", signal, "SELECT 1 AS id")
		write("new.batch", signal, "SELECT 2 AS id, 'present' AS added_later")
	}

	if err := CreateParquetViews(db, dir); err != nil {
		t.Fatalf("CreateParquetViews must not fail on a pre-deploy batch: %v", err)
	}

	for _, signal := range []string{"spans", "logs", "metrics"} {
		rows, err := db.Query(fmt.Sprintf(`SELECT id, added_later FROM telemetry.%s ORDER BY id`, signal))
		if err != nil {
			t.Fatalf("query telemetry.%s: %v", signal, err)
		}
		seen := map[int]bool{}
		for rows.Next() {
			var id int
			var added sql.NullString
			if err := rows.Scan(&id, &added); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			seen[id] = true
			if id == 1 && added.Valid {
				t.Errorf("%s: the pre-deploy batch reported %q for a column it does not have", signal, added.String)
			}
			if id == 2 && added.String != "present" {
				t.Errorf("%s: current batch read added_later=%q, want \"present\"", signal, added.String)
			}
		}
		rows.Close()
		if !seen[1] || !seen[2] {
			t.Errorf("%s: read %v, want rows from both the old and the new batch", signal, seen)
		}
	}
}
