package service

import (
	"context"
	"fmt"
)

// Diagnose returns detailed service diagnostics with root cause analysis.
func (s *Service) Diagnose(ctx context.Context, svc string, window int) (*DiagnoseResult, error) {
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

	// Get overall metrics
	spansGlob := s.duck.SpansGlob(window)
	q := fmt.Sprintf(`
SELECT
  COUNT(*) as cnt,
  COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p50,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95,
  COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p99,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=service_name" = '%s';
`, spansGlob, window, escapeLike(svc))

	row := s.duck.DB.QueryRowContext(ctx, q)
	if err := row.Scan(&out.SpanCount, &out.P50Ms, &out.P95Ms, &out.P99Ms, &out.ErrorRate); err != nil {
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
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=service_name" = '%s'
  AND "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')
  AND "name=status_msg" IS NOT NULL
  AND "name=status_msg" != ''
GROUP BY "name=status_msg"
ORDER BY cnt DESC
LIMIT 5;
`, spansGlob, window, escapeLike(svc))

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e ErrorInfo
			rows.Scan(&e.Message, &e.Count, &e.TraceID)
			out.TopErrors = append(out.TopErrors, e)
			if e.TraceID != "" && len(suggestedTraces) < 3 {
				suggestedTraces = append(suggestedTraces, e.TraceID)
			}
		}
	}

	// Get slow operations
	q = fmt.Sprintf(`
SELECT
  "name=name" as op,
  PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms") as p95,
  COUNT(*) as cnt
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=service_name" = '%s'
GROUP BY "name=name"
HAVING p95 > 100
ORDER BY p95 DESC
LIMIT 5;
`, spansGlob, window, escapeLike(svc))

	rows, err = s.duck.DB.QueryContext(ctx, q)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var op SlowOp
			rows.Scan(&op.Name, &op.P95Ms, &op.Count)
			out.SlowOps = append(out.SlowOps, op)
		}
	}

	// Get downstream dependencies
	q = fmt.Sprintf(`
WITH downstream AS (
  SELECT
    child."name=service_name" as dep_service,
    child."name=duration_ms" as duration_ms,
    child."name=status_code" as status
  FROM read_parquet(%s) parent
  JOIN read_parquet(%s) child
    ON parent."name=span_id" = child."name=parent_span_id"
    AND parent."name=trace_id" = child."name=trace_id"
  WHERE epoch_ms(CAST(parent."name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
    AND parent."name=service_name" = '%s'
    AND child."name=service_name" != '%s'
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
`, spansGlob, spansGlob, window, escapeLike(svc), escapeLike(svc))

	rows, err = s.duck.DB.QueryContext(ctx, q)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d Dependency
			rows.Scan(&d.Service, &d.CallCount, &d.P95Ms, &d.ErrorRate)
			out.Dependencies = append(out.Dependencies, d)
		}
	}

	return out, nil
}
