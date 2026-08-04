package query

import (
	"context"
	"testing"

	"github.com/labstack/fanout/internal/env"
)

func TestEnsureLimit_WrapsWithoutClobberingInnerLimits(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"plain", "SELECT 1", "SELECT * FROM (SELECT 1) AS _capped LIMIT 1000"},
		{"trailing semicolon", "SELECT 1;", "SELECT * FROM (SELECT 1) AS _capped LIMIT 1000"},
		{"trailing whitespace+semicolon", "SELECT 1 ;\n", "SELECT * FROM (SELECT 1) AS _capped LIMIT 1000"},
		{
			"inner limit preserved",
			"WITH t AS (SELECT * FROM spans LIMIT 5000) SELECT * FROM t",
			"SELECT * FROM (WITH t AS (SELECT * FROM spans LIMIT 5000) SELECT * FROM t) AS _capped LIMIT 1000",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ensureLimit(c.query, 1000); got != c.want {
				t.Errorf("ensureLimit(%q)\n got: %q\nwant: %q", c.query, got, c.want)
			}
		})
	}
}

// TestEnsureLimit_CapsAndPreservesOrder proves the wrap executes, enforces the row
// cap, and preserves an inner ORDER BY (DuckDB carries subquery ordering up).
func TestEnsureLimit_CapsAndPreservesOrder(t *testing.T) {
	db := openTestDuck(t)
	q := ensureLimit("SELECT n FROM range(100) AS t(n) ORDER BY n DESC", 3)
	rows, err := db.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("exec wrapped: %v", err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if len(got) != 3 || got[0] != 99 || got[1] != 98 || got[2] != 97 {
		t.Errorf("wrapped query = %v, want [99 98 97] (cap=3, order preserved)", got)
	}
}

// TestExecuteSQL_NonPositiveMaxRows guards the make([]RowMap, 0, MaxRows) path:
// a negative MaxRows must not panic and must fall back to the default.
func TestExecuteSQL_NonPositiveMaxRows(t *testing.T) {
	db := openTestDuck(t)
	d := &Duck{DB: db}
	for _, mr := range []int{-1, 0, -1000} {
		resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: "SELECT 1 AS x", MaxRows: mr})
		if resp.Error != "" {
			t.Errorf("MaxRows=%d returned error: %s", mr, resp.Error)
		}
		if resp.RowsReturned != 1 {
			t.Errorf("MaxRows=%d returned %d rows, want 1", mr, resp.RowsReturned)
		}
	}
}

func TestExecuteSQL_CapsRowsAtMaxRows(t *testing.T) {
	db := openTestDuck(t)
	d := &Duck{DB: db}
	resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: "SELECT n FROM range(50) AS t(n)", MaxRows: 5})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.RowsReturned != 5 {
		t.Errorf("RowsReturned = %d, want 5 (capped)", resp.RowsReturned)
	}
}

func TestRollupLagFromConfig(t *testing.T) {
	sec := int64(1_000_000_000)
	cases := []struct {
		flushSeconds int
		wantNanos    int64
	}{
		{15, 30 * sec}, // 2×15s = 30s
		{20, 40 * sec}, // 2×20s = 40s
		{5, 30 * sec},  // 2×5s = 10s, floored to 30s
		{0, 30 * sec},  // floored to 30s
	}
	for _, c := range cases {
		if got := rollupLagFromConfig(env.Config{FlushSeconds: c.flushSeconds}); got != c.wantNanos {
			t.Errorf("rollupLagFromConfig(FlushSeconds=%d) = %d, want %d", c.flushSeconds, got, c.wantNanos)
		}
	}
}
