package db_test

import (
	"os"
	"strings"
	"testing"
)

func TestDashboardQueryHasNoUnboundedInsert(t *testing.T) {
	for _, file := range []string{"queries/dashboards.sql", "generated/dashboards.sql.go"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "-- name: InsertDashboard :") || strings.Contains(string(raw), "func (q *Queries) InsertDashboard(") {
			t.Errorf("obsolete unbounded insert in %s", file)
		}
	}
}
