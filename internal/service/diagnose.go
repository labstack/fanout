package service

import (
	"context"
	"fmt"
	"log/slog"
)

// Diagnose returns detailed service diagnostics with root cause analysis.
func (s *Service) Diagnose(ctx context.Context, svc string, window int, namespace, tenantID string) (*DiagnoseResult, error) {
	if svc == "" {
		return nil, fmt.Errorf("service is required")
	}
	if window == 0 {
		window = 15
	}

	out := &DiagnoseResult{
		Service:      svc,
		TopErrors:    []ErrorInfo{},
		SlowOps:      []SlowOp{},
		Dependencies: []Dependency{},
	}

	// Always scope to single partition
	namespace, tenantID = s.defaults(namespace, tenantID)
	spansGlob := s.duck.SpansGlob(tenantID, namespace, window)
	q := fmt.Sprintf(`
SELECT
  COUNT(*) as cnt,
  COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p50,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95,
  COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p99,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=service_name" = ?;
`, spansGlob, window)

	row := s.duck.DB.QueryRowContext(ctx, q, svc)
	if err := row.Scan(&out.SpanCount, &out.P50Ms, &out.P95Ms, &out.P99Ms, &out.ErrorRate); err != nil {
		slog.Warn("query failed", "method", "Diagnose", "err", err)
		out.Status = "unknown"
		return out, nil
	}

	out.Status = DeriveHealth(out.ErrorRate, out.P95Ms)

	var suggestedTraces []string

	// Get top errors with example traces
	q = fmt.Sprintf(`
SELECT
  "name=status_msg" as msg,
  COUNT(*) as cnt,
  FIRST("name=trace_id") as trace_id
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=service_name" = ?
  AND "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')
  AND "name=status_msg" IS NOT NULL
  AND "name=status_msg" != ''
GROUP BY "name=status_msg"
ORDER BY cnt DESC
LIMIT 5;
`, spansGlob, window)

	rows, err := s.duck.DB.QueryContext(ctx, q, svc)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e ErrorInfo
			if err := rows.Scan(&e.Message, &e.Count, &e.TraceID); err != nil {
				slog.Warn("scan failed", "method", "Diagnose.errors", "err", err)
				continue
			}
			out.TopErrors = append(out.TopErrors, e)
			if e.TraceID != "" && len(suggestedTraces) < 3 {
				suggestedTraces = append(suggestedTraces, e.TraceID)
			}
		}
		if err := rows.Err(); err != nil {
			slog.Warn("iteration error", "method", "Diagnose.errors", "err", err)
		}
	}

	// Get slow operations
	q = fmt.Sprintf(`
SELECT
  "name=name" as op,
  PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms") as p95,
  COUNT(*) as cnt
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=service_name" = ?
GROUP BY "name=name"
HAVING p95 > 100
ORDER BY p95 DESC
LIMIT 5;
`, spansGlob, window)

	rows, err = s.duck.DB.QueryContext(ctx, q, svc)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var op SlowOp
			if err := rows.Scan(&op.Name, &op.P95Ms, &op.Count); err != nil {
				slog.Warn("scan failed", "method", "Diagnose.slowOps", "err", err)
				continue
			}
			out.SlowOps = append(out.SlowOps, op)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("iteration error", "method", "Diagnose.slowOps", "err", err)
		}
	}

	// Get downstream dependencies
	q = fmt.Sprintf(`
WITH downstream AS (
  SELECT
    child."name=service_name" as dep_service,
    child."name=duration_ms" as duration_ms,
    child."name=status_code" as status
  FROM read_parquet(%s, union_by_name=true) parent
  JOIN read_parquet(%s, union_by_name=true) child
    ON parent."name=span_id" = child."name=parent_span_id"
    AND parent."name=trace_id" = child."name=trace_id"
  WHERE epoch_ms(CAST(parent."name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
    AND parent."name=service_name" = ?
    AND child."name=service_name" != ?
)
SELECT
  dep_service,
  COUNT(*) as calls,
  PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms) as p95,
  AVG(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) as error_rate
FROM downstream
GROUP BY dep_service
ORDER BY calls DESC
LIMIT 10;
`, spansGlob, spansGlob, window)

	rows, err = s.duck.DB.QueryContext(ctx, q, svc, svc)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d Dependency
			if err := rows.Scan(&d.Service, &d.CallCount, &d.P95Ms, &d.ErrorRate); err != nil {
				slog.Warn("scan failed", "method", "Diagnose.deps", "err", err)
				continue
			}
			out.Dependencies = append(out.Dependencies, d)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("iteration error", "method", "Diagnose.deps", "err", err)
		}
	}

	return out, nil
}
