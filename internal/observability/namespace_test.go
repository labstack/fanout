package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestOverviewEmptyNamespaceIncludesAllNamespaces(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
CREATE TABLE service_rollup (
  bucket TIMESTAMP,
  namespace VARCHAR,
  service VARCHAR,
  spans BIGINT,
  error_rate DOUBLE,
  p50_ms DOUBLE,
  p95_ms DOUBLE,
  log_count BIGINT,
  metric_count BIGINT
)`); err != nil {
		t.Fatalf("create service_rollup: %v", err)
	}

	stamp := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO service_rollup VALUES
    (?, 'prod', 'checkout', 2, 0, 10, 20, 1, 1),
    (?, 'staging', 'catalog', 3, 0, 10, 20, 1, 1)`, stamp, stamp); err != nil {
		t.Fatalf("insert service rollups: %v", err)
	}

	svc := New(SQLDB(db), newTestRepository(t))
	result, err := svc.Overview(context.Background(), Scope{Start: stamp.Add(-time.Minute), End: stamp.Add(time.Minute)}, 100)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if result.Data.ServiceCount != 2 || result.Data.TotalSpans != 5 {
		t.Fatalf("overview = %#v, want both namespaces", result.Data)
	}
}
