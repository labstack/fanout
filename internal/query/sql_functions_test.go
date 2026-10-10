package query

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const builtinFunctionsSnapshot = "testdata/duckdb-functions.txt"

// A scalar macro whose definition runs a query can read the catalog, which
// holds absolute Parquet paths, so every such macro must be blocked.
func TestScalarMacrosThatRunQueriesAreBlocked(t *testing.T) {
	db := openTestDuck(t)
	rows, err := db.Query(`SELECT DISTINCT function_name FROM duckdb_functions()
WHERE function_type = 'macro' AND regexp_matches(lower(macro_definition), '\bselect\b') ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		found++
		if !blockedSQLFunctions[strings.ToLower(name)] {
			t.Errorf("macro %s runs a query but telemetry SQL can call it; add it to blockedSQLFunctions", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no query-running macros found; the catalog query no longer matches the engine")
	}
}

// An engine upgrade that adds, removes or retypes a built-in fails here until
// its functions are reviewed against blockedSQLFunctions and the table-function
// allowlist. Regenerate with FANOUT_UPDATE_SQL_FUNCTIONS=1 after that review.
func TestBuiltinFunctionsMatchTheReviewedSnapshot(t *testing.T) {
	db := openTestDuck(t)
	var version string
	if err := db.QueryRow(`SELECT version()`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT DISTINCT function_name || ' ' || function_type FROM duckdb_functions() ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	current := []string{"# engine " + version}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		current = append(current, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FANOUT_UPDATE_SQL_FUNCTIONS") == "1" {
		if err := os.MkdirAll(filepath.Dir(builtinFunctionsSnapshot), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(builtinFunctionsSnapshot, []byte(strings.Join(current, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(builtinFunctionsSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	reviewed := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	var added, removed []string
	for _, line := range current {
		if !slices.Contains(reviewed, line) {
			added = append(added, line)
		}
	}
	for _, line := range reviewed {
		if !slices.Contains(current, line) {
			removed = append(removed, line)
		}
	}
	if len(added) > 0 || len(removed) > 0 {
		t.Fatalf("built-in functions changed; review each new one before regenerating\nadded: %v\nremoved: %v", added, removed)
	}
}
