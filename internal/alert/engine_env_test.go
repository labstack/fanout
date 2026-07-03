package alert

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/labstack/fanout/internal/query"
)

// buildEnvs feeds CEL rules from a SQL aggregate over service_rollup. DuckDB
// widens SUM over integer columns to HUGEINT, which the driver scans as
// *big.Int — a type the env conversion must handle, or throughput/log_count
// silently read 0 while the DOUBLE fields (error_rate, p50, p95, deltas)
// stay populated. Seen live 2026-07-03: Throughput=0 with ThroughputDelta=7.65
// for a service pushing ~600 spans/min, making throughput rules unusable.
func TestBuildEnvs_IntegerSumsSurviveHugeint(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
CREATE TABLE service_rollup (
  namespace VARCHAR, bucket TIMESTAMP, service VARCHAR,
  spans BIGINT, error_rate DOUBLE, p50_ms DOUBLE, p95_ms DOUBLE,
  log_count BIGINT, metric_count BIGINT
)`); err != nil {
		t.Fatalf("create service_rollup: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO service_rollup VALUES
  ('', now(),                      'svc-a', 100, 0.0, 10.0, 20.0, 5, 0),
  ('', now() - INTERVAL 1 MINUTE,  'svc-a', 100, 0.0, 10.0, 20.0, 5, 0)`); err != nil {
		t.Fatalf("seed service_rollup: %v", err)
	}

	eng := NewEngine(nil, &query.Duck{DB: db}, nil, time.Minute, 7)
	envs := eng.buildEnvs(context.Background())
	env, ok := envs["svc-a"]
	if !ok {
		t.Fatalf("buildEnvs returned no env for svc-a: %#v", envs)
	}

	if env.Throughput != 200 {
		t.Errorf("Throughput = %v, want 200 (sum(spans) is HUGEINT — lost in conversion?)", env.Throughput)
	}
	if env.LogCount != 10 {
		t.Errorf("LogCount = %v, want 10 (sum(log_count) is HUGEINT — lost in conversion?)", env.LogCount)
	}
	if env.P95 != 20 {
		t.Errorf("P95 = %v, want 20", env.P95)
	}
}
