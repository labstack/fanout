package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"time"
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

	out.Status = DeriveHealth(out.ErrorRate, out.P95Ms, out.SpanCount)

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

	// Get downstream dependencies from edge rollup
	q = fmt.Sprintf(`
SELECT
  callee as dep_service,
  SUM(calls)::BIGINT as calls,
  AVG(avg_ms) as avg_ms,
  AVG(error_rate) as error_rate
FROM edge_rollup
WHERE caller = ?
  AND bucket >= now() - INTERVAL %d MINUTE
GROUP BY callee
ORDER BY calls DESC
LIMIT 10;
`, window)

	rows, err = s.duck.DB.QueryContext(ctx, q, svc)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d Dependency
			if err := rows.Scan(&d.Service, &d.CallCount, &d.AvgMs, &d.ErrorRate); err != nil {
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

// DiagnoseEnhanced runs the standard Diagnose and enriches the result with
// baseline comparison, change-point detection, and correlated log patterns.
// symptom is one of: "latency", "errors", "throughput_drop", "auto".
func (s *Service) DiagnoseEnhanced(ctx context.Context, svc string, window int, symptom, namespace, tenantID string) (*DiagnoseResult, error) {
	out, err := s.Diagnose(ctx, svc, window, namespace, tenantID)
	if err != nil {
		return out, err
	}

	if symptom == "" {
		symptom = "auto"
	}

	// Detect symptom from metrics when auto.
	detected := symptom
	if symptom == "auto" {
		detected = detectSymptom(out.ErrorRate, out.P95Ms, out.SpanCount)
	}
	out.SymptomDetected = detected

	// Baseline comparison: same time-of-day over past 7 days.
	baseline, err := s.queryBaseline(ctx, svc, window)
	if err != nil {
		slog.Warn("baseline query failed", "method", "DiagnoseEnhanced", "err", err)
	} else {
		out.Baseline = baseline
	}

	// Change point detection over rollup buckets in the window.
	changePoints, cpTime := s.detectChangePoints(ctx, svc, window, detected)
	out.ChangePoints = changePoints

	// Correlated log patterns around the change point (or window start).
	if logPatterns, lerr := s.correlatedLogs(ctx, svc, cpTime, window, namespace, tenantID); lerr != nil {
		slog.Warn("log correlation failed", "method", "DiagnoseEnhanced", "err", lerr)
	} else {
		out.CorrelatedLogPatterns = logPatterns
	}

	return out, nil
}

// detectSymptom infers the dominant symptom from current metrics.
func detectSymptom(errorRate, p95Ms float64, spanCount int64) string {
	if errorRate > 0.10 {
		return "errors"
	}
	if p95Ms > 5000 {
		return "latency"
	}
	if spanCount == 0 {
		return "throughput_drop"
	}
	return "latency"
}

// queryBaseline computes the historical same-time-of-day P95 average over the past 7 days.
// Returns nil when there is insufficient data (< 3 distinct days).
func (s *Service) queryBaseline(ctx context.Context, svc string, window int) (*BaselineComparison, error) {
	// Determine the current hour range covered by the window.
	now := time.Now()
	startHour := now.Add(-time.Duration(window) * time.Minute).Hour()
	endHour := now.Hour()

	var q string
	var row *sql.Row
	if startHour > endHour {
		// Window crosses midnight: match hours >= startHour OR <= endHour
		q = `
SELECT
  AVG(CASE WHEN spans > 0 THEN p95_ms END) as baseline_p95,
  COUNT(DISTINCT DATE_TRUNC('day', bucket)) as day_count
FROM service_rollup
WHERE service = ?
  AND bucket >= NOW() - INTERVAL 7 DAY
  AND (EXTRACT(HOUR FROM bucket) >= ? OR EXTRACT(HOUR FROM bucket) <= ?)
  AND spans > 0;`
		row = s.duck.DB.QueryRowContext(ctx, q, svc, startHour, endHour)
	} else {
		q = `
SELECT
  AVG(CASE WHEN spans > 0 THEN p95_ms END) as baseline_p95,
  COUNT(DISTINCT DATE_TRUNC('day', bucket)) as day_count
FROM service_rollup
WHERE service = ?
  AND bucket >= NOW() - INTERVAL 7 DAY
  AND EXTRACT(HOUR FROM bucket) BETWEEN ? AND ?
  AND spans > 0;`
		row = s.duck.DB.QueryRowContext(ctx, q, svc, startHour, endHour)
	}

	var baselineP95 *float64
	var dayCount int64
	if err := row.Scan(&baselineP95, &dayCount); err != nil {
		return nil, fmt.Errorf("baseline scan: %w", err)
	}

	if dayCount < 3 || baselineP95 == nil || *baselineP95 == 0 {
		return nil, nil //nolint:nilnil // intentional: no baseline available
	}

	return &BaselineComparison{
		BaselineWindow: "7d",
		BaselineP95Ms:  *baselineP95,
	}, nil
}

// rollupBucket holds a single service_rollup row for change-point scanning.
type rollupBucket struct {
	bucket    time.Time
	p95Ms     float64
	errorRate float64
	spans     int64
}

// detectChangePoints scans service_rollup buckets for >2σ jumps in the symptom metric.
// Returns change points and the time of the first detected change (or window start if none).
func (s *Service) detectChangePoints(ctx context.Context, svc string, window int, symptom string) ([]ChangePoint, time.Time) {
	q := fmt.Sprintf(`
SELECT bucket, COALESCE(p95_ms, 0), COALESCE(error_rate, 0), COALESCE(spans, 0)
FROM service_rollup
WHERE service = ?
  AND bucket >= NOW() - INTERVAL %d MINUTE
ORDER BY bucket ASC;`, window)

	rows, err := s.duck.DB.QueryContext(ctx, q, svc)
	windowStart := time.Now().Add(-time.Duration(window) * time.Minute)
	if err != nil {
		slog.Warn("change point query failed", "err", err)
		return nil, windowStart
	}
	defer rows.Close()

	var buckets []rollupBucket
	for rows.Next() {
		var b rollupBucket
		if err := rows.Scan(&b.bucket, &b.p95Ms, &b.errorRate, &b.spans); err != nil {
			slog.Warn("change point scan failed", "err", err)
			continue
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("change point iteration error", "err", err)
	}

	if len(buckets) < 4 {
		return nil, windowStart
	}

	// Extract the target series.
	series := make([]float64, len(buckets))
	for i, b := range buckets {
		switch symptom {
		case "errors":
			series[i] = b.errorRate
		case "throughput_drop":
			series[i] = float64(b.spans)
		default: // latency / auto
			series[i] = b.p95Ms
		}
	}

	// Determine metric label.
	metricLabel := "p95_ms"
	if symptom == "errors" {
		metricLabel = "error_rate"
	} else if symptom == "throughput_drop" {
		metricLabel = "spans"
	}

	// Compute mean and stddev for the whole window.
	mean, stddev := MeanStdDev(series)
	threshold := mean + 2*stddev
	if threshold == mean {
		// Flat signal — nothing to detect.
		return nil, windowStart
	}

	var changePoints []ChangePoint
	firstCPTime := windowStart

	for i := 1; i < len(series); i++ {
		delta := math.Abs(series[i] - series[i-1])
		if delta > 2*stddev && series[i] > threshold {
			cp := ChangePoint{
				Time:   buckets[i].bucket.UTC().Format(time.RFC3339),
				Metric: metricLabel,
				Before: series[i-1],
				After:  series[i],
			}
			changePoints = append(changePoints, cp)
			if len(changePoints) == 1 {
				firstCPTime = buckets[i].bucket
			}
		}
	}

	if len(changePoints) == 0 {
		return nil, windowStart
	}
	return changePoints, firstCPTime
}

// correlatedLogs queries log patterns near the given change-point time.
func (s *Service) correlatedLogs(ctx context.Context, svc string, around time.Time, window int, namespace, tenantID string) ([]LogPattern, error) {
	namespace, tenantID = s.defaults(namespace, tenantID)

	// Search ±5 minutes around the change point, but stay within the window.
	windowStart := time.Now().Add(-time.Duration(window) * time.Minute)
	from := around.Add(-5 * time.Minute)
	if from.Before(windowStart) {
		from = windowStart
	}
	to := around.Add(5 * time.Minute)
	if to.After(time.Now()) {
		to = time.Now()
	}

	// Use the logs view for a clean query. Fall back to parquet glob if view fails.
	q := `
SELECT LEFT(body, 50) as pattern, severity, COUNT(*) as cnt
FROM logs
WHERE service = ?
  AND time BETWEEN ? AND ?
GROUP BY LEFT(body, 50), severity
ORDER BY cnt DESC
LIMIT 5;`

	rows, err := s.duck.DB.QueryContext(ctx, q, svc, from, to)
	if err != nil {
		// Fall back to raw parquet glob.
		logsGlob := s.duck.LogsGlob(tenantID, namespace, window)
		fromNano := from.UnixNano()
		toNano := to.UnixNano()
		q2 := fmt.Sprintf(`
SELECT LEFT("name=body", 50) as pattern, "name=severity" as severity, COUNT(*) as cnt
FROM read_parquet(%s, union_by_name=true)
WHERE "name=service_name" = ?
  AND "name=time_unix_nano" BETWEEN ? AND ?
GROUP BY LEFT("name=body", 50), "name=severity"
ORDER BY cnt DESC
LIMIT 5;`, logsGlob)
		rows, err = s.duck.DB.QueryContext(ctx, q2, svc, fromNano, toNano)
		if err != nil {
			return nil, fmt.Errorf("correlated logs query: %w", err)
		}
	}
	defer rows.Close()

	var patterns []LogPattern
	for rows.Next() {
		var lp LogPattern
		if err := rows.Scan(&lp.Pattern, &lp.Severity, &lp.Count); err != nil {
			slog.Warn("log pattern scan failed", "err", err)
			continue
		}
		patterns = append(patterns, lp)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("log pattern iteration error", "err", err)
	}
	return patterns, nil
}

// meanStddev is a backward-compatible alias for MeanStdDev.
// Deprecated: use MeanStdDev directly.
func meanStddev(vals []float64) (mean, stddev float64) {
	return MeanStdDev(vals)
}
