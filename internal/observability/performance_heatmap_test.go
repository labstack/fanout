package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// The heatmap is a cross-service comparison grid: the twelve busiest services
// by every bucket in the window. The browser's performance page draws it.
//
// An MCP caller asks about one service and got that grid too -- on the live
// demo about 85% of a 113 KB response, describing twelve services it did not
// ask about, plus the second DuckDB query that built it. So it is opt-in, and
// the query has to be skipped rather than the field blanked afterwards.
func TestPerformanceOmitsTheHeatmapUnlessAsked(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE service_rollup (
  bucket TIMESTAMP, namespace VARCHAR, service VARCHAR,
  spans BIGINT, served_spans BIGINT, error_rate DOUBLE,
  p50_ms DOUBLE, p95_ms DOUBLE, log_count BIGINT, metric_count BIGINT
)`); err != nil {
		t.Fatal(err)
	}
	// queryEndpoints reads both the rollup and raw spans at the window edges.
	if _, err := db.Exec(`
CREATE TABLE endpoint_rollup (
  bucket TIMESTAMP, namespace VARCHAR, service VARCHAR, method VARCHAR, path VARCHAR,
  calls BIGINT, error_rate DOUBLE, p50_ms DOUBLE, p95_ms DOUBLE, p99_ms DOUBLE
);
CREATE TABLE spans (
  namespace VARCHAR, service VARCHAR, operation VARCHAR, kind VARCHAR, status VARCHAR,
  http_method VARCHAR, http_route VARCHAR,
  duration_ms DOUBLE, start_time TIMESTAMP, attributes_json VARCHAR
)`); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 9, 21, 20, 0, 0, 0, time.UTC)
	for bucket := range 6 {
		at := start.Add(time.Duration(bucket) * time.Minute)
		for _, service := range []string{"checkout", "cart", "frontend", "payment"} {
			if _, err := db.Exec(`INSERT INTO service_rollup VALUES (?, 'prod', ?, 10, 10, 0, 5, 20, 1, 1)`,
				at, service); err != nil {
				t.Fatal(err)
			}
		}
	}

	svc := New(SQLDB(db), newTestRepository(t).Parquet, 30)
	scope := Scope{Namespace: "prod", Start: start, End: start.Add(10 * time.Minute)}
	ctx := context.Background()

	withGrid, err := svc.Performance(ctx, scope, PerformanceOptions{Service: "checkout", Limit: 50, Heatmap: true})
	if err != nil {
		t.Fatalf("Performance with heatmap: %v", err)
	}
	if len(withGrid.Data.Heatmap) == 0 {
		t.Fatal("the fixture produced no heatmap, so this test compares nothing")
	}

	withoutGrid, err := svc.Performance(ctx, scope, PerformanceOptions{Service: "checkout", Limit: 50})
	if err != nil {
		t.Fatalf("Performance without heatmap: %v", err)
	}
	if got := len(withoutGrid.Data.Heatmap); got != 0 {
		t.Errorf("heatmap carries %d points when it was not requested", got)
	}
	// Everything the caller did ask for is untouched.
	if len(withoutGrid.Data.Points) != len(withGrid.Data.Points) {
		t.Errorf("points = %d without the grid, %d with it", len(withoutGrid.Data.Points), len(withGrid.Data.Points))
	}
	if withoutGrid.Data.Totals != withGrid.Data.Totals {
		t.Errorf("totals differ without the grid:\n %+v\n %+v", withoutGrid.Data.Totals, withGrid.Data.Totals)
	}
}
