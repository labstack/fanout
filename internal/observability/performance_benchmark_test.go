package observability

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

func BenchmarkEndpointQueries24Hours(b *testing.B) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`SET TimeZone='UTC'; SET threads=4`); err != nil {
		b.Fatal(err)
	}
	start := time.Date(2026, 7, 20, 0, 0, 30, 0, time.UTC)
	if _, err := db.Exec(`CREATE TABLE spans AS
SELECT
  'prod'::VARCHAR AS namespace,
  'svc-' || lpad((i % 50)::VARCHAR, 2, '0') AS service,
  ?::TIMESTAMP + (i % 86400) * INTERVAL 1 SECOND AS start_time,
  ((i % 300000) + 1)::DOUBLE / 100.0 AS duration_ms,
  CASE WHEN i % 20 = 0 THEN 'STATUS_CODE_ERROR' ELSE 'STATUS_CODE_OK' END::VARCHAR AS status,
  CASE WHEN i % 2 = 0 THEN 'GET' ELSE 'POST' END::VARCHAR AS http_method,
  '/route-' || (i % 20)::VARCHAR AS http_route,
  'operation-' || (i % 20)::VARCHAR AS operation
FROM range(5000000) t(i)`, start); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE endpoint_rollup (
  namespace TEXT, bucket TIMESTAMP, service TEXT, method TEXT, path TEXT,
  calls BIGINT, error_count BIGINT, duration_count BIGINT,
  duration_buckets STRUCT(
    le_0_1 UBIGINT, le_0_5 UBIGINT, le_1 UBIGINT, le_2_5 UBIGINT,
    le_5 UBIGINT, le_10 UBIGINT, le_25 UBIGINT, le_50 UBIGINT,
    le_100 UBIGINT, le_250 UBIGINT, le_500 UBIGINT, le_750 UBIGINT,
    le_1000 UBIGINT, le_2000 UBIGINT, le_5000 UBIGINT,
    le_30000 UBIGINT, le_300000 UBIGINT
  )
)`); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO endpoint_rollup
SELECT
  namespace,
  date_trunc('minute', start_time),
  COALESCE(service, ''),
  COALESCE(NULLIF(http_method, ''), 'CALL'),
  COALESCE(NULLIF(http_route, ''), NULLIF(operation, ''), 'unknown'),
  COUNT(*),
  COUNT(*) FILTER (WHERE upper(status) IN ('ERROR', 'STATUS_CODE_ERROR')),
  COUNT(duration_ms),
  struct_pack(
    le_0_1 := COUNT(*) FILTER (WHERE duration_ms <= 0.1),
    le_0_5 := COUNT(*) FILTER (WHERE duration_ms <= 0.5),
    le_1 := COUNT(*) FILTER (WHERE duration_ms <= 1),
    le_2_5 := COUNT(*) FILTER (WHERE duration_ms <= 2.5),
    le_5 := COUNT(*) FILTER (WHERE duration_ms <= 5),
    le_10 := COUNT(*) FILTER (WHERE duration_ms <= 10),
    le_25 := COUNT(*) FILTER (WHERE duration_ms <= 25),
    le_50 := COUNT(*) FILTER (WHERE duration_ms <= 50),
    le_100 := COUNT(*) FILTER (WHERE duration_ms <= 100),
    le_250 := COUNT(*) FILTER (WHERE duration_ms <= 250),
    le_500 := COUNT(*) FILTER (WHERE duration_ms <= 500),
    le_750 := COUNT(*) FILTER (WHERE duration_ms <= 750),
    le_1000 := COUNT(*) FILTER (WHERE duration_ms <= 1000),
    le_2000 := COUNT(*) FILTER (WHERE duration_ms <= 2000),
    le_5000 := COUNT(*) FILTER (WHERE duration_ms <= 5000),
    le_30000 := COUNT(*) FILTER (WHERE duration_ms <= 30000),
    le_300000 := COUNT(*) FILTER (WHERE duration_ms <= 300000)
  )
FROM spans
GROUP BY
  namespace,
  date_trunc('minute', start_time),
  service,
  COALESCE(NULLIF(http_method, ''), 'CALL'),
  COALESCE(NULLIF(http_route, ''), NULLIF(operation, ''), 'unknown')`); err != nil {
		b.Fatal(err)
	}
	end := start.Add(24*time.Hour - time.Second)

	bench := func(b *testing.B, query string, args ...any) {
		b.Helper()
		for i := 0; i < b.N; i++ {
			rows, err := db.Query(query, args...)
			if err != nil {
				b.Fatal(err)
			}
			count := 0
			for rows.Next() {
				var method, path string
				var calls int64
				var p50, p95, p99, errorRate float64
				if err := rows.Scan(&method, &path, &calls, &p50, &p95, &p99, &errorRate); err != nil {
					rows.Close()
					b.Fatal(err)
				}
				count++
			}
			if err := rows.Close(); err != nil {
				b.Fatal(err)
			}
			if count == 0 {
				b.Fatal("query returned no endpoints")
			}
		}
	}
	b.Run("raw-spans", func(b *testing.B) {
		b.ReportMetric(5000000, "dataset_spans")
		bench(b, rawEndpointsQuery, start, end, "prod", "", "", 100)
	})
	b.Run("endpoint-rollup", func(b *testing.B) {
		b.ReportMetric(5000000, "dataset_spans")
		bench(b, endpointRollupQuery, start, end, "prod", "", 100)
	})
}
