package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/queryrows"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestResourceInventoryFrozenSnapshotPlans(t *testing.T) {
	root := os.Getenv("FANOUT_TEST_INVENTORY_SNAPSHOT")
	if root == "" {
		t.Skip("set FANOUT_TEST_INVENTORY_SNAPSHOT to an extracted telemetry repository")
	}
	evidence := os.Getenv("FANOUT_TEST_INVENTORY_EVIDENCE")
	if evidence == "" {
		t.Fatal("FANOUT_TEST_INVENTORY_EVIDENCE is required")
	}
	data, err := os.ReadFile(filepath.Join(evidence, "panel-sql.json"))
	if err != nil {
		t.Fatal(err)
	}
	var statements map[string]string
	if err := json.Unmarshal(data, &statements); err != nil {
		t.Fatal(err)
	}
	repo, err := telemetrystore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	cfg := config.Config{DataDir: filepath.Join(evidence, "runtime"), DuckDBMemory: "8GB", DuckDBThreads: 8, DuckDBMaxConns: 4}
	d, err := NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w := queryrows.Window{Start: time.Date(2026, 10, 16, 15, 47, 42, 261874001, time.UTC), End: time.Date(2026, 10, 16, 16, 47, 42, 261874001, time.UTC)}
	files := w
	files.Start = files.Start.Add(-10 * time.Minute)
	conn, err := d.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := beginRead(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	defer endRead(conn, tx)
	windowMacro := regexp.MustCompile(`\$__window\(([^)]+)\)`)
	for name, statement := range statements {
		statement = windowMacro.ReplaceAllStringFunc(statement, func(m string) string { return windowPredicate(windowMacro.FindStringSubmatch(m)[1], w) })
		statement = strings.NewReplacer("$__from", timeLiteral(w.Start), "$__to", timeLiteral(w.End)).Replace(statement)
		bound, err := d.bindSnapshot(queryrows.WithInventoryWindow(t.Context(), w), tx, statement, files)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidence, name+"-bound.sql"), []byte(bound+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.QueryContext(t.Context(), "EXPLAIN (FORMAT JSON) "+bound)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(value + "\n")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidence, name+"-plan.json"), []byte(plan.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
