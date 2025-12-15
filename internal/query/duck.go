package query

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/labstack/fanout/internal/config"
)

type Duck struct {
	DB  *sql.DB
	cfg config.Config
}

func NewDuck(ctx context.Context, cfg config.Config) (*Duck, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, err
	}
	d := &Duck{DB: db, cfg: cfg}
	// Create rollup table
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS svc_minute (
  ts_min TIMESTAMP,
  service_name TEXT,
  span_count BIGINT,
  p50_ms DOUBLE,
  p95_ms DOUBLE,
  error_rate DOUBLE
);`); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Duck) Close() error { return d.DB.Close() }

func (d *Duck) RunRollups(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(d.cfg.RollupEvery) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := d.rollupOnce(ctx); err != nil {
				log.Printf("[rollup] %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (d *Duck) rollupOnce(ctx context.Context) error {
	_, err := d.DB.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO svc_minute
SELECT
  date_trunc('minute', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) AS ts_min,
  "name=service_name" as service_name,
  COUNT(*) AS span_count,
  avg("name=duration_ms") AS p50_ms,
  quantile_cont("name=duration_ms", 0.95) AS p95_ms,
  avg(CASE WHEN "name=status_code" = 'STATUS_CODE_ERROR' OR "name=status_code" = 'ERROR' THEN 1 ELSE 0 END) AS error_rate
FROM read_parquet('%s/spans/year=*/month=*/day=*/hour=*/part-*.parquet')
WHERE ts_min > COALESCE((SELECT max(ts_min) FROM svc_minute), TIMESTAMP '1970-01-01')
GROUP BY ALL;
`, d.cfg.LakeDir))
	return err
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
WITH S AS (
  SELECT "name=service_name" as service_name,
         "name=duration_ms" as duration_ms,
         "name=status_code" as status_code,
         "name=start_unix_nano" as start_unix_nano
  FROM read_parquet('%s/spans/year=*/month=*/day=*/hour=*/part-*.parquet')
  WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
)
SELECT service_name,
       quantile_cont(duration_ms, 0.95) AS p95_ms,
       avg(CASE WHEN status_code = 'STATUS_CODE_ERROR' OR status_code = 'ERROR' THEN 1 ELSE 0 END) AS error_rate,
       count(*) AS spans
FROM S
GROUP BY service_name
ORDER BY p95_ms DESC
LIMIT 100;
`, d.cfg.LakeDir, windowMinutes)
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
	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       "name=body" as body,
       "name=service_name" as service_name,
       "name=severity" as severity
FROM read_parquet('%s/logs/year=*/month=*/day=*/hour=*/part-*.parquet')
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=body" ~ '%s'
ORDER BY ts DESC
LIMIT %d;
`, d.cfg.LakeDir, windowMinutes, escapeLike(pattern), limit)
	rows, err := d.DB.QueryContext(ctx, q)
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
	TSMin string `json:"ts_min"`
	Spans int64  `json:"spans"`
}

func (d *Duck) Throughput(ctx context.Context, windowMinutes int) ([]ThroughputRow, error) {
	q := fmt.Sprintf(`
SELECT strftime(ts_min, '%%Y-%%m-%%dT%%H:%%M:00Z') AS ts_min, SUM(span_count) AS spans
FROM svc_minute
WHERE ts_min >= now() - INTERVAL %d MINUTE
GROUP BY ts_min
ORDER BY ts_min ASC;
`, windowMinutes)
	rows, err := d.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThroughputRow{}
	for rows.Next() {
		var r ThroughputRow
		if err := rows.Scan(&r.TSMin, &r.Spans); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ServiceThroughputRow struct {
	ServiceName    string `json:"service_name"`
	SpansPerMinute int64  `json:"spans_per_minute"`
}

func (d *Duck) ServiceThroughput(ctx context.Context, windowMinutes int) ([]ServiceThroughputRow, error) {
	q := fmt.Sprintf(`
SELECT service_name, SUM(span_count)::BIGINT / %d AS spans_per_minute
FROM svc_minute
WHERE ts_min >= now() - INTERVAL %d MINUTE
GROUP BY service_name
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
		if err := rows.Scan(&r.ServiceName, &r.SpansPerMinute); err != nil {
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
	q := fmt.Sprintf(`
SELECT "name=body" AS route, COUNT(*) AS count
FROM read_parquet('%s/logs/year=*/month=*/day=*/hour=*/part-*.parquet')
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=severity" IN ('ERROR','ERR','WARN')
GROUP BY "name=body"
ORDER BY count DESC
LIMIT %d;
`, d.cfg.LakeDir, windowMinutes, limit)
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
	q := fmt.Sprintf(`
WITH spans_with_errors AS (
  SELECT "name=service_name" as service_name,
         "name=name" as name,
         "name=status_code" as status_code
  FROM read_parquet('%s/spans/year=*/month=*/day=*/hour=*/part-*.parquet')
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
`, d.cfg.LakeDir, windowMinutes, limit)
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

func escapeLike(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "''")
}
