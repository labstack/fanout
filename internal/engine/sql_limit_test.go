package engine

import "testing"

func TestTelemetrySQLCapsRowsAtMaxRows(t *testing.T) {
	db := openTestDuck(t)
	d := &Duck{DB: db, writeDB: db}
	if rows := mustRunTelemetrySQL(t, t.Context(), d, "SELECT n FROM range(50) AS t(n)", 5); len(rows) != 5 {
		t.Errorf("rows = %d, want 5 (capped)", len(rows))
	}
}

func TestTelemetrySQLCapsWithoutChangingInnerLimitOrOrder(t *testing.T) {
	db := openTestDuck(t)
	d := &Duck{DB: db, writeDB: db}
	for _, q := range []string{
		"SELECT n FROM range(100) AS t(n) ORDER BY n DESC;",
		"WITH t AS (SELECT n FROM range(100) AS r(n) ORDER BY n DESC LIMIT 7) SELECT n FROM t ORDER BY n DESC",
	} {
		rows := mustRunTelemetrySQL(t, t.Context(), d, q, 3)
		if len(rows) != 3 || rows[0]["n"] != int64(99) || rows[2]["n"] != int64(97) {
			t.Fatalf("%s: %#v", q, rows)
		}
	}
	if rows := mustRunTelemetrySQL(t, t.Context(), d, "SELECT n FROM range(100) AS t(n) ORDER BY n DESC LIMIT 2", 3); len(rows) != 2 {
		t.Fatalf("inner cap: %#v", rows)
	}
}
