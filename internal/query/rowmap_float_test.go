package query

import (
	"context"
	"testing"
)

// DuckDB's FLOAT is single precision, so the driver hands back a float32 and
// every `row["x"].(float64)` on it fails -- yielding 0.0 from a value that was
// never zero. Callers write that assertion because every other numeric column
// they touch (DOUBLE, AVG, SUM) really is float64, so the one FLOAT column in a
// query fails silently while the rest of the row is fine.
//
// That is not hypothetical: internal/intelligence cast three columns to FLOAT
// and reported 0 for all of them for as long as the code existed -- volume
// anomalies showed "0 spans" for services that were serving thousands, and the
// error-rate detector's current and baseline rates were always 0.
//
// Fixing the casts alone fixes today's queries and leaves the trap armed for
// the next one, so the conversion is what changes here.
func TestRowMapWidensFloat32(t *testing.T) {
	db := openTestDuck(t)
	defer db.Close()
	d := &Duck{DB: db}

	resp := d.ExecuteSQL(context.Background(), SQLRequest{
		Query: "SELECT 327::FLOAT AS as_float, 0.25::FLOAT AS rate, 327::DOUBLE AS as_double",
	})
	if resp.Error != "" {
		t.Fatalf("ExecuteSQL: %s", resp.Error)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("got %d rows, want 1", len(resp.Results))
	}
	row := resp.Results[0]

	for name, want := range map[string]float64{"as_float": 327, "rate": 0.25, "as_double": 327} {
		got, ok := row[name].(float64)
		if !ok {
			t.Errorf("row[%q] is %T, not float64: every caller asserting float64 reads 0 from it", name, row[name])
			continue
		}
		if got != want {
			t.Errorf("row[%q] = %v, want %v", name, got, want)
		}
	}
}
