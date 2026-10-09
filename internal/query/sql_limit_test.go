package query

import (
	"context"
	"testing"
)

// TestExecuteSQL_NonPositiveMaxRows guards the make([]RowMap, 0, MaxRows) path:
// a negative MaxRows must not panic and must fall back to the default.
func TestExecuteSQL_NonPositiveMaxRows(t *testing.T) {
	db := openTestDuck(t)
	d := &Duck{DB: db, writeDB: db}
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
	d := &Duck{DB: db, writeDB: db}
	resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: "SELECT n FROM range(50) AS t(n)", MaxRows: 5})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.RowsReturned != 5 {
		t.Errorf("RowsReturned = %d, want 5 (capped)", resp.RowsReturned)
	}
}

func TestExecuteSQLCapsWithoutChangingInnerLimitOrOrder(t *testing.T) {
	db := openTestDuck(t)
	d := &Duck{DB: db, writeDB: db}
	for _, q := range []string{
		"SELECT n FROM range(100) AS t(n) ORDER BY n DESC;",
		"WITH t AS (SELECT n FROM range(100) AS r(n) ORDER BY n DESC LIMIT 7) SELECT n FROM t ORDER BY n DESC",
	} {
		resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: q, MaxRows: 3})
		if resp.Error != "" || resp.RowsReturned != 3 || resp.Results[0]["n"] != int64(99) || resp.Results[2]["n"] != int64(97) {
			t.Fatalf("%s: %#v", q, resp)
		}
	}
	resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: "SELECT n FROM range(100) AS t(n) ORDER BY n DESC LIMIT 2", MaxRows: 3})
	if resp.Error != "" || resp.RowsReturned != 2 {
		t.Fatalf("inner cap: %#v", resp)
	}
}
