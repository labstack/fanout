package query

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
)

type Duck struct {
	DB  *sql.DB
	cfg config.Config
}

func NewDuck(ctx context.Context, cfg config.Config) (*Duck, error) {
	dbPath := filepath.Join(cfg.LakeDir, "fanout.duckdb")
	dsn := dbPath + "?threads=4&memory_limit=256MB"

	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		// Corruption recovery: remove DB + WAL, retry once
		slog.Warn("duckdb open failed, attempting recovery", "err", err)
		os.Remove(dbPath)
		os.Remove(dbPath + ".wal")
		db, err = sql.Open("duckdb", dsn)
		if err != nil {
			return nil, fmt.Errorf("duckdb open after recovery: %w", err)
		}
		slog.Info("duckdb recovered with fresh database")
	}
	d := &Duck{DB: db, cfg: cfg}
	// Create rollup tables
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS service_rollup (
  bucket TIMESTAMP,
  service TEXT,
  spans BIGINT,
  p50_ms DOUBLE,
  p95_ms DOUBLE,
  error_rate DOUBLE
);`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS edge_rollup (
  bucket TIMESTAMP,
  caller TEXT,
  callee TEXT,
  calls BIGINT,
  avg_ms DOUBLE,
  error_rate DOUBLE
);`); err != nil {
		return nil, err
	}

	// Add multi-signal columns (idempotent)
	for _, ddl := range []string{
		`ALTER TABLE service_rollup ADD COLUMN IF NOT EXISTS log_count BIGINT DEFAULT 0`,
		`ALTER TABLE service_rollup ADD COLUMN IF NOT EXISTS metric_count BIGINT DEFAULT 0`,
		`ALTER TABLE edge_rollup ADD COLUMN IF NOT EXISTS edge_type TEXT DEFAULT 'call'`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return nil, fmt.Errorf("migration %q: %w", ddl, err)
		}
	}

	return d, nil
}

func (d *Duck) Close() error { return d.DB.Close() }

// SpansGlob returns optimized glob pattern for spans within a single partition
func (d *Duck) SpansGlob(tenant, namespace string, windowMinutes int) string {
	return ParquetGlob(d.cfg.LakeDir, "spans", tenant, namespace, windowMinutes)
}

// LogsGlob returns optimized glob pattern for logs within a single partition
func (d *Duck) LogsGlob(tenant, namespace string, windowMinutes int) string {
	return ParquetGlob(d.cfg.LakeDir, "logs", tenant, namespace, windowMinutes)
}

// MetricsGlob returns optimized glob pattern for metrics within a single partition
func (d *Duck) MetricsGlob(tenant, namespace string, windowMinutes int) string {
	return ParquetGlob(d.cfg.LakeDir, "metrics", tenant, namespace, windowMinutes)
}

// DefaultTenantID returns the configured tenant ID
func (d *Duck) DefaultTenantID() string {
	return d.cfg.TenantID.String()
}

// DefaultNamespace returns empty string so queries search all namespaces.
func (d *Duck) DefaultNamespace() string {
	return ""
}

func (d *Duck) RunRollups(ctx context.Context) {
	// Run once immediately at startup so dashboards have data right away
	start := time.Now()
	rows, err := d.rollupOnce(ctx)
	if err != nil {
		slog.Error("startup rollup failed", "err", err)
	} else if rows > 0 {
		slog.Info("startup rollup complete", "rows", rows, "duration", time.Since(start))
		metrics.RecordRollup(rows, time.Since(start).Seconds())
	}

	ticker := time.NewTicker(time.Duration(d.cfg.RollupEvery) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			start := time.Now()
			rows, err := d.rollupOnce(ctx)
			if err != nil {
				slog.Error("rollup failed", "component", "rollup", "err", err)
				continue
			}
			metrics.RecordRollup(rows, time.Since(start).Seconds())
		case <-ctx.Done():
			return
		}
	}
}

func (d *Duck) rollupOnce(ctx context.Context) (int, error) {
	// Service rollup
	res, err := d.DB.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO service_rollup
SELECT
  date_trunc('minute', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) AS bucket,
  "name=service_name" as service,
  COUNT(*) AS spans,
  quantile_cont("name=duration_ms", 0.50) AS p50_ms,
  quantile_cont("name=duration_ms", 0.95) AS p95_ms,
  avg(CASE WHEN "name=status_code" = 'STATUS_CODE_ERROR' OR "name=status_code" = 'ERROR' THEN 1 ELSE 0 END) AS error_rate
FROM read_parquet(['%s/spans/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/*.parquet'], union_by_name=true, hive_partitioning=true)
WHERE bucket > COALESCE((SELECT max(bucket) FROM service_rollup), TIMESTAMP '1970-01-01')
GROUP BY ALL;
`, d.cfg.LakeDir))
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()

	// Log rollup — services that emit logs
	logRes, logErr := d.DB.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO service_rollup (bucket, service, spans, p50_ms, p95_ms, error_rate, log_count, metric_count)
SELECT
  date_trunc('minute', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT))) AS bucket,
  "name=service_name" AS service,
  0 AS spans, 0 AS p50_ms, 0 AS p95_ms, 0 AS error_rate,
  COUNT(*) AS log_count,
  0 AS metric_count
FROM read_parquet(['%s/logs/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/*.parquet'], union_by_name=true, hive_partitioning=true)
WHERE "name=service_name" IS NOT NULL AND "name=service_name" != ''
  AND date_trunc('minute', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)))
      > COALESCE((SELECT max(bucket) FROM service_rollup WHERE log_count > 0), TIMESTAMP '1970-01-01')
GROUP BY ALL;
`, d.cfg.LakeDir))
	if logErr != nil {
		slog.Error("log rollup failed", "err", logErr)
	} else {
		n, _ := logRes.RowsAffected()
		affected += n
	}

	// Metric rollup — services that emit metrics
	metricRes, metricErr := d.DB.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO service_rollup (bucket, service, spans, p50_ms, p95_ms, error_rate, log_count, metric_count)
SELECT
  date_trunc('minute', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT))) AS bucket,
  "name=service_name" AS service,
  0 AS spans, 0 AS p50_ms, 0 AS p95_ms, 0 AS error_rate,
  0 AS log_count,
  COUNT(DISTINCT "name=name") AS metric_count
FROM read_parquet(['%s/metrics/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/*.parquet'], union_by_name=true, hive_partitioning=true)
WHERE "name=service_name" IS NOT NULL AND "name=service_name" != ''
  AND date_trunc('minute', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)))
      > COALESCE((SELECT max(bucket) FROM service_rollup WHERE metric_count > 0), TIMESTAMP '1970-01-01')
GROUP BY ALL;
`, d.cfg.LakeDir))
	if metricErr != nil {
		slog.Error("metric rollup failed", "err", metricErr)
	} else {
		n, _ := metricRes.RowsAffected()
		affected += n
	}

	// Edge rollup (caller -> callee relationships)
	_, edgeErr := d.DB.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO edge_rollup
WITH calls AS (
  SELECT
    date_trunc('minute', epoch_ms(CAST(child."name=start_unix_nano"/1000000 AS BIGINT))) AS bucket,
    parent."name=service_name" as caller,
    child."name=service_name" as callee,
    child."name=duration_ms" as duration_ms,
    child."name=status_code" as status
  FROM read_parquet(['%s/spans/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/*.parquet'], union_by_name=true, hive_partitioning=true) child
  JOIN read_parquet(['%s/spans/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/*.parquet'], union_by_name=true, hive_partitioning=true) parent
    ON child."name=parent_span_id" = parent."name=span_id"
    AND child."name=trace_id" = parent."name=trace_id"
  WHERE bucket > COALESCE((SELECT max(bucket) FROM edge_rollup), TIMESTAMP '1970-01-01')
    AND parent."name=service_name" != child."name=service_name"
)
SELECT
  bucket,
  caller,
  callee,
  COUNT(*) as calls,
  AVG(duration_ms) as avg_ms,
  AVG(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) as error_rate
FROM calls
GROUP BY bucket, caller, callee;
`, d.cfg.LakeDir, d.cfg.LakeDir))
	if edgeErr != nil {
		slog.Error("edge rollup failed", "err", edgeErr)
	}

	// Messaging edge rollup (producer -> broker -> consumer via messaging spans)
	_, msgErr := d.DB.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO edge_rollup (bucket, caller, callee, calls, avg_ms, error_rate, edge_type)
WITH producers AS (
  SELECT
    date_trunc('minute', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) AS bucket,
    "name=service_name" AS service,
    json_extract_string(from_utf8("name=attributes_json"), '$.messaging.destination.name') AS destination,
    json_extract_string(from_utf8("name=attributes_json"), '$.messaging.system') AS msg_system
  FROM read_parquet(['%s/spans/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/*.parquet'], union_by_name=true, hive_partitioning=true)
  WHERE "name=kind" = 'SPAN_KIND_PRODUCER'
    AND json_extract_string(from_utf8("name=attributes_json"), '$.messaging.destination.name') IS NOT NULL
),
consumers AS (
  SELECT
    date_trunc('minute', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) AS bucket,
    "name=service_name" AS service,
    json_extract_string(from_utf8("name=attributes_json"), '$.messaging.destination.name') AS destination,
    json_extract_string(from_utf8("name=attributes_json"), '$.messaging.system') AS msg_system
  FROM read_parquet(['%s/spans/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/*.parquet'], union_by_name=true, hive_partitioning=true)
  WHERE "name=kind" = 'SPAN_KIND_CONSUMER'
    AND json_extract_string(from_utf8("name=attributes_json"), '$.messaging.destination.name') IS NOT NULL
)
SELECT
  p.bucket,
  p.service AS caller,
  c.service AS callee,
  COUNT(*) AS calls,
  0 AS avg_ms,
  0 AS error_rate,
  'messaging' AS edge_type
FROM producers p
JOIN consumers c ON p.destination = c.destination AND p.msg_system = c.msg_system AND p.bucket = c.bucket
WHERE p.service != c.service
  AND p.bucket > COALESCE((SELECT max(bucket) FROM edge_rollup WHERE edge_type = 'messaging'), TIMESTAMP '1970-01-01')
GROUP BY p.bucket, p.service, c.service;
`, d.cfg.LakeDir, d.cfg.LakeDir))
	if msgErr != nil {
		slog.Error("messaging edge rollup failed", "err", msgErr)
	}

	// Prune old rollup data
	if d.cfg.RetentionDays > 0 {
		for _, tbl := range []string{"service_rollup", "edge_rollup"} {
			_, err := d.DB.ExecContext(ctx, fmt.Sprintf(
				`DELETE FROM %s WHERE bucket < now() - INTERVAL %d DAY`, tbl, d.cfg.RetentionDays))
			if err != nil {
				slog.Warn("rollup retention failed", "table", tbl, "err", err)
			}
		}
	}

	return int(affected), nil
}

// ---- Queries for API ----

type LatencyRow struct {
	ServiceName string  `json:"service_name"`
	P95Ms       float64 `json:"p95_ms"`
	ErrorRate   float64 `json:"error_rate"`
	Spans       int64   `json:"spans"`
}

func (d *Duck) LatencyOverview(ctx context.Context, windowMinutes int) ([]LatencyRow, error) {
	q := fmt.Sprintf(`
SELECT
  service as service_name,
  AVG(p95_ms) as p95_ms,
  AVG(error_rate) as error_rate,
  SUM(spans)::BIGINT as spans
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY service
ORDER BY p95_ms DESC
LIMIT 100;
`, windowMinutes)
	rows, err := d.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LatencyRow{}
	for rows.Next() {
		var r LatencyRow
		if err := rows.Scan(&r.ServiceName, &r.P95Ms, &r.ErrorRate, &r.Spans); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type LogsSample struct {
	TS   string `json:"ts"`
	Body string `json:"body"`
	Svc  string `json:"service_name"`
	Sev  string `json:"severity"`
}

func (d *Duck) LogsSamples(ctx context.Context, windowMinutes, limit int, pattern string) ([]LogsSample, error) {
	tenantID, namespace := d.DefaultTenantID(), d.DefaultNamespace()
	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       "name=body" as body,
       "name=service_name" as service_name,
       "name=severity" as severity
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=body" ~ ?
ORDER BY ts DESC
LIMIT %d;
`, d.LogsGlob(tenantID, namespace, windowMinutes), windowMinutes, limit)
	rows, err := d.DB.QueryContext(ctx, q, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogsSample{}
	for rows.Next() {
		var r LogsSample
		if err := rows.Scan(&r.TS, &r.Body, &r.Svc, &r.Sev); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ThroughputRow struct {
	Bucket string `json:"bucket"`
	Spans  int64  `json:"spans"`
}

func (d *Duck) Throughput(ctx context.Context, windowMinutes int) ([]ThroughputRow, error) {
	q := fmt.Sprintf(`
SELECT strftime(bucket, '%%Y-%%m-%%dT%%H:%%M:00Z') AS bucket,
  (SUM(spans) + SUM(COALESCE(log_count, 0)) + SUM(COALESCE(metric_count, 0))) AS spans
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY bucket
ORDER BY bucket ASC;
`, windowMinutes)
	rows, err := d.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThroughputRow{}
	for rows.Next() {
		var r ThroughputRow
		if err := rows.Scan(&r.Bucket, &r.Spans); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ServiceThroughputRow struct {
	Service        string `json:"service"`
	SpansPerMinute int64  `json:"spans_per_minute"`
}

func (d *Duck) ServiceThroughput(ctx context.Context, windowMinutes int) ([]ServiceThroughputRow, error) {
	q := fmt.Sprintf(`
SELECT service, (SUM(spans) + SUM(COALESCE(log_count, 0)) + SUM(COALESCE(metric_count, 0)))::BIGINT / %d AS spans_per_minute
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY service
ORDER BY spans_per_minute DESC
LIMIT 20;
`, windowMinutes, windowMinutes)
	rows, err := d.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceThroughputRow{}
	for rows.Next() {
		var r ServiceThroughputRow
		if err := rows.Scan(&r.Service, &r.SpansPerMinute); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ErrorRoute struct {
	Route string `json:"route"`
	Count int64  `json:"count"`
}

func (d *Duck) ErrorRoutes(ctx context.Context, windowMinutes, limit int) ([]ErrorRoute, error) {
	tenantID, namespace := d.DefaultTenantID(), d.DefaultNamespace()
	q := fmt.Sprintf(`
SELECT "name=body" AS route, COUNT(*) AS count
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=severity" IN ('ERROR','ERR','WARN')
GROUP BY "name=body"
ORDER BY count DESC
LIMIT %d;
`, d.LogsGlob(tenantID, namespace, windowMinutes), windowMinutes, limit)
	rows, err := d.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ErrorRoute{}
	for rows.Next() {
		var r ErrorRoute
		if err := rows.Scan(&r.Route, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ErrorRouteRow struct {
	ServiceName string  `json:"service_name"`
	Name        string  `json:"name"`
	Errors      int64   `json:"errors"`
	ErrorRate   float64 `json:"error_rate"`
}

func (d *Duck) ErrorRouteDetails(ctx context.Context, windowMinutes, limit int) ([]ErrorRouteRow, error) {
	tenantID, namespace := d.DefaultTenantID(), d.DefaultNamespace()
	q := fmt.Sprintf(`
WITH spans_with_errors AS (
  SELECT "name=service_name" as service_name,
         "name=name" as name,
         "name=status_code" as status_code
  FROM read_parquet(%s, union_by_name=true)
  WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
)
SELECT service_name,
       name,
       SUM(CASE WHEN status_code = 'STATUS_CODE_ERROR' OR status_code = 'ERROR' THEN 1 ELSE 0 END) AS errors,
       AVG(CASE WHEN status_code = 'STATUS_CODE_ERROR' OR status_code = 'ERROR' THEN 1.0 ELSE 0.0 END) AS error_rate
FROM spans_with_errors
GROUP BY service_name, name
HAVING errors > 0
ORDER BY errors DESC
LIMIT %d;
`, d.SpansGlob(tenantID, namespace, windowMinutes), windowMinutes, limit)
	rows, err := d.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ErrorRouteRow{}
	for rows.Next() {
		var r ErrorRouteRow
		if err := rows.Scan(&r.ServiceName, &r.Name, &r.Errors, &r.ErrorRate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
