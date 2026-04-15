package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
)

// --- Compare domain types ---

// CompareServicesParams holds typed parameters for the services comparison mode.
type CompareServicesParams struct {
	Services []string // 2-4 services to compare
	Window   int      // minutes
}

// CompareTimeParams holds typed parameters for the time comparison mode.
type CompareTimeParams struct {
	Service string
	Left    TimeRange
	Right   TimeRange
	Focus   []string // "latency", "errors", "throughput"
}

// CompareOperationsParams holds typed parameters for the operations comparison mode.
type CompareOperationsParams struct {
	Service        string
	LeftOperation  string
	RightOperation string
	Window         int      // minutes
	Focus          []string // "latency", "errors", "throughput"
}

// TimeRange is a start/end time pair used for compare queries.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// CompareServicesResult holds the output of a services comparison.
type CompareServicesResult struct {
	Services []ServiceMetrics
	Winner   string
	Summary  string
}

// CompareTimeResult holds the output of a time comparison.
type CompareTimeResult struct {
	Comparison map[string]MetricDiff
}

// CompareOperationsResult holds the output of an operations comparison.
type CompareOperationsResult struct {
	Comparison map[string]MetricDiff
}

// ServiceMetrics holds per-service aggregate metrics.
type ServiceMetrics struct {
	Service    string
	Requests   int64
	ErrorRate  float64
	P50Ms      float64
	P95Ms      float64
	AvgMs      float64
	ErrorCount int64
}

// MetricDiff describes how a metric changed between left and right.
type MetricDiff struct {
	LeftValue                float64
	RightValue               float64
	ChangePct                float64
	Direction                string // "regression", "improvement", "stable"
	StatisticallySignificant bool
}

// AggStats holds aggregated summary stats for a time window.
type AggStats struct {
	P95Ms      float64
	P50Ms      float64
	ErrorRate  float64
	Throughput float64 // spans per minute
}

// RollupBucket holds per-bucket stats from service_rollup.
type RollupBucket struct {
	P95Ms     float64
	P50Ms     float64
	ErrorRate float64
	Spans     int64
}

// --- Compare service methods ---

// CompareServices compares metrics across 2-4 services within a time window.
func (s *Service) CompareServices(ctx context.Context, params CompareServicesParams) (*CompareServicesResult, error) {
	if len(params.Services) < 2 {
		return nil, fmt.Errorf("need at least 2 services to compare")
	}
	if len(params.Services) > 4 {
		return nil, fmt.Errorf("max 4 services to compare")
	}

	window := params.Window
	if window <= 0 {
		window = 60
	}

	// Build parameterized IN clause for services
	placeholders := make([]string, len(params.Services))
	args := make([]any, len(params.Services))
	for i, svc := range params.Services {
		placeholders[i] = "?"
		args[i] = svc
	}

	q := fmt.Sprintf(`
		SELECT
			service,
			COALESCE(SUM(spans), 0) AS requests,
			COALESCE(AVG(CASE WHEN spans > 0 THEN error_rate END), 0) AS error_rate,
			COALESCE(AVG(CASE WHEN spans > 0 THEN p50_ms END), 0) AS p50_ms,
			COALESCE(AVG(CASE WHEN spans > 0 THEN p95_ms END), 0) AS p95_ms,
			COALESCE(SUM(log_count), 0) AS log_count,
			COALESCE(SUM(metric_count), 0) AS metric_count
		FROM service_rollup
		WHERE service IN (%s) AND bucket >= NOW() - INTERVAL '%d minutes'
		GROUP BY service
		ORDER BY (COALESCE(SUM(spans), 0) + COALESCE(SUM(log_count), 0) + COALESCE(SUM(metric_count), 0)) DESC
	`, strings.Join(placeholders, ","), window)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var metrics []ServiceMetrics
	for rows.Next() {
		var m ServiceMetrics
		var logCount, metricCount int64
		if err := rows.Scan(&m.Service, &m.Requests, &m.ErrorRate, &m.P50Ms, &m.P95Ms, &logCount, &metricCount); err != nil {
			slog.Warn("scan failed", "method", "CompareServices", "err", err)
			continue
		}
		if m.Requests == 0 && (logCount > 0 || metricCount > 0) {
			m.Requests = logCount + metricCount
		}
		m.ErrorCount = int64(float64(m.Requests) * m.ErrorRate)
		if m.Requests > 0 {
			m.AvgMs = (m.P50Ms + m.P95Ms) / 2
		}
		metrics = append(metrics, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("compare iteration: %w", err)
	}

	// Add empty entries for services with no data
	found := make(map[string]bool)
	for _, m := range metrics {
		found[m.Service] = true
	}
	for _, svc := range params.Services {
		if !found[svc] {
			metrics = append(metrics, ServiceMetrics{Service: svc})
		}
	}

	// Determine winner (lowest P95 with acceptable error rate)
	winner := ""
	bestScore := float64(-1)
	for _, m := range metrics {
		if m.Requests == 0 || (m.P50Ms == 0 && m.P95Ms == 0) {
			continue
		}
		score := m.P95Ms * (1 + m.ErrorRate*10)
		if bestScore < 0 || score < bestScore {
			bestScore = score
			winner = m.Service
		}
	}

	summary := fmt.Sprintf("Compared %d services over %d minutes. ", len(metrics), window)
	if winner != "" {
		summary += fmt.Sprintf("%s has best performance.", winner)
	}

	return &CompareServicesResult{
		Services: metrics,
		Winner:   winner,
		Summary:  summary,
	}, nil
}

// CompareTime compares the same service across two time windows.
func (s *Service) CompareTime(ctx context.Context, params CompareTimeParams) (*CompareTimeResult, error) {
	if params.Service == "" {
		return nil, fmt.Errorf("service is required for time comparison")
	}

	focus := ResolveFocus(params.Focus)

	leftBuckets, leftErr := s.QueryRollupBuckets(ctx, params.Service, params.Left.Start, params.Left.End)
	if leftErr != nil {
		return nil, fmt.Errorf("left window query failed: %w", leftErr)
	}
	rightBuckets, rightErr := s.QueryRollupBuckets(ctx, params.Service, params.Right.Start, params.Right.End)
	if rightErr != nil {
		return nil, fmt.Errorf("right window query failed: %w", rightErr)
	}

	leftAgg := AggregateBuckets(leftBuckets)
	rightAgg := AggregateBuckets(rightBuckets)

	comparison := BuildComparison(leftAgg, rightAgg, leftBuckets, rightBuckets, focus)

	return &CompareTimeResult{
		Comparison: comparison,
	}, nil
}

// CompareOperations compares two operations within the same service.
func (s *Service) CompareOperations(ctx context.Context, params CompareOperationsParams) (*CompareOperationsResult, error) {
	if params.Service == "" {
		return nil, fmt.Errorf("service is required for operations comparison")
	}

	window := params.Window
	if window <= 0 {
		window = 60
	}

	focus := ResolveFocus(params.Focus)

	end := time.Now()
	start := end.Add(-time.Duration(window) * time.Minute)

	leftStats, leftErr := s.queryOperationStats(ctx, params.Service, params.LeftOperation, start, end)
	if leftErr != nil {
		return nil, fmt.Errorf("left operation query failed: %w", leftErr)
	}
	rightStats, rightErr := s.queryOperationStats(ctx, params.Service, params.RightOperation, start, end)
	if rightErr != nil {
		return nil, fmt.Errorf("right operation query failed: %w", rightErr)
	}

	// No bucket-level data for operations, so skip statistical significance.
	comparison := BuildComparison(leftStats, rightStats, nil, nil, focus)

	return &CompareOperationsResult{
		Comparison: comparison,
	}, nil
}

// --- Query helpers ---

// QueryRollupBuckets fetches per-bucket rows from service_rollup for a service in a time range.
func (s *Service) QueryRollupBuckets(ctx context.Context, service string, start, end time.Time) ([]RollupBucket, error) {
	q := `
		SELECT
			COALESCE(AVG(CASE WHEN spans > 0 THEN p95_ms END), 0),
			COALESCE(AVG(CASE WHEN spans > 0 THEN p50_ms END), 0),
			COALESCE(AVG(CASE WHEN spans > 0 THEN error_rate END), 0),
			COALESCE(SUM(spans), 0)
		FROM service_rollup
		WHERE service = ? AND bucket >= ? AND bucket < ?
		GROUP BY bucket
		ORDER BY bucket ASC
	`
	rows, err := s.duck.DB.QueryContext(ctx, q, service, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []RollupBucket
	for rows.Next() {
		var b RollupBucket
		if err := rows.Scan(&b.P95Ms, &b.P50Ms, &b.ErrorRate, &b.Spans); err != nil {
			slog.Warn("scan failed", "method", "QueryRollupBuckets", "err", err)
			continue
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

// queryOperationStats queries the spans view for operation-level aggregate stats.
func (s *Service) queryOperationStats(ctx context.Context, service, operation string, start, end time.Time) (AggStats, error) {
	q := `
		SELECT
			COALESCE(quantile_cont(duration_ms, 0.95), 0) AS p95_ms,
			COALESCE(quantile_cont(duration_ms, 0.50), 0) AS p50_ms,
			COALESCE(AVG(CASE WHEN status IN ('STATUS_CODE_ERROR','ERROR') THEN 1.0 ELSE 0.0 END), 0) AS error_rate,
			COUNT(*) AS total_spans
		FROM spans
		WHERE service = ?
		  AND operation = ?
		  AND start_time >= ?
		  AND start_time < ?
	`
	rows, err := s.duck.DB.QueryContext(ctx, q, service, operation, start, end)
	if err != nil {
		return AggStats{}, err
	}
	defer rows.Close()

	if rows.Next() {
		var a AggStats
		var totalSpans int64
		if err := rows.Scan(&a.P95Ms, &a.P50Ms, &a.ErrorRate, &totalSpans); err != nil {
			return AggStats{}, err
		}
		minutes := end.Sub(start).Minutes()
		if minutes == 0 {
			minutes = 1
		}
		a.Throughput = float64(totalSpans) / minutes
		return a, rows.Err()
	}
	return AggStats{}, rows.Err()
}

// --- Pure computation functions ---

// AggregateBuckets computes summary stats across all rollup buckets.
func AggregateBuckets(buckets []RollupBucket) AggStats {
	if len(buckets) == 0 {
		return AggStats{}
	}
	var sumP95, sumP50, sumErrorRate float64
	var totalSpans int64
	var count int
	for _, b := range buckets {
		if b.Spans > 0 {
			sumP95 += b.P95Ms
			sumP50 += b.P50Ms
			sumErrorRate += b.ErrorRate
			count++
		}
		totalSpans += b.Spans
	}
	if count == 0 {
		return AggStats{Throughput: 0}
	}
	minutes := float64(len(buckets))
	if minutes == 0 {
		minutes = 1
	}
	return AggStats{
		P95Ms:      sumP95 / float64(count),
		P50Ms:      sumP50 / float64(count),
		ErrorRate:  sumErrorRate / float64(count),
		Throughput: float64(totalSpans) / minutes,
	}
}

// BuildComparison builds the comparison map for the given focus metrics.
func BuildComparison(left, right AggStats, leftBuckets, rightBuckets []RollupBucket, focus []string) map[string]MetricDiff {
	out := make(map[string]MetricDiff)

	for _, f := range focus {
		switch f {
		case "latency":
			var leftSeries, rightSeries []float64
			for _, b := range leftBuckets {
				if b.Spans > 0 {
					leftSeries = append(leftSeries, b.P95Ms)
				}
			}
			for _, b := range rightBuckets {
				if b.Spans > 0 {
					rightSeries = append(rightSeries, b.P95Ms)
				}
			}
			out["latency"] = MakeMetricDiff(left.P95Ms, right.P95Ms, leftSeries, rightSeries, true)

		case "errors":
			var leftSeries, rightSeries []float64
			for _, b := range leftBuckets {
				if b.Spans > 0 {
					leftSeries = append(leftSeries, b.ErrorRate*100)
				}
			}
			for _, b := range rightBuckets {
				if b.Spans > 0 {
					rightSeries = append(rightSeries, b.ErrorRate*100)
				}
			}
			out["errors"] = MakeMetricDiff(left.ErrorRate*100, right.ErrorRate*100, leftSeries, rightSeries, true)

		case "throughput":
			out["throughput"] = MakeMetricDiff(left.Throughput, right.Throughput, nil, nil, false)
		}
	}

	return out
}

// MakeMetricDiff computes a MetricDiff from left/right values and optional bucket series.
// higherIsBad controls direction: if true, an increase is a regression.
func MakeMetricDiff(leftVal, rightVal float64, leftSeries, rightSeries []float64, higherIsBad bool) MetricDiff {
	changePct := 0.0
	if leftVal != 0 {
		changePct = ((rightVal - leftVal) / leftVal) * 100
	}

	direction := "stable"
	threshold := 5.0
	if math.Abs(changePct) > threshold {
		if (changePct > 0) == higherIsBad {
			direction = "regression"
		} else {
			direction = "improvement"
		}
	}

	sig := IsSignificant(leftSeries, rightSeries)

	return MetricDiff{
		LeftValue:                leftVal,
		RightValue:               rightVal,
		ChangePct:                math.Round(changePct*10) / 10,
		Direction:                direction,
		StatisticallySignificant: sig,
	}
}

// BuildVerdict produces a human-readable verdict from the comparison results.
func BuildVerdict(comparison map[string]MetricDiff) string {
	var regressions, improvements []string
	for metric, diff := range comparison {
		if diff.Direction == "regression" {
			regressions = append(regressions, fmt.Sprintf("%s (%+.0f%%)", metric, diff.ChangePct))
		} else if diff.Direction == "improvement" {
			improvements = append(improvements, fmt.Sprintf("%s (%+.0f%%)", metric, diff.ChangePct))
		}
	}

	if len(regressions) == 0 && len(improvements) == 0 {
		return "No significant differences detected."
	}

	verdict := ""
	if len(regressions) > 0 {
		verdict += "Regression in " + strings.Join(regressions, ", ") + ". "
	}
	if len(improvements) > 0 {
		verdict += "Improvement in " + strings.Join(improvements, ", ") + "."
	}
	return strings.TrimSpace(verdict)
}

// ResolveFocus returns the focus list, defaulting to all three metrics.
func ResolveFocus(focus []string) []string {
	if len(focus) == 0 {
		return []string{"latency", "errors", "throughput"}
	}
	return focus
}

// IsSignificant returns true if the difference between the two sample means is
// greater than 2x the stddev of the smaller sample, and both samples have >= 5 buckets.
func IsSignificant(left, right []float64) bool {
	if len(left) < 5 || len(right) < 5 {
		return false
	}

	smaller, larger := left, right
	if len(right) < len(left) {
		smaller, larger = right, left
	}

	smallerMean, smallerStddev := MeanStdDev(smaller)
	largerMean, _ := MeanStdDev(larger)

	if smallerStddev == 0 {
		return largerMean != smallerMean
	}

	return math.Abs(largerMean-smallerMean) > 2*smallerStddev
}

// MeanStdDev returns the mean and population standard deviation of a slice.
func MeanStdDev(vals []float64) (mean, stddev float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	mean = sum / float64(len(vals))

	varSum := 0.0
	for _, v := range vals {
		diff := v - mean
		varSum += diff * diff
	}
	stddev = math.Sqrt(varSum / float64(len(vals)))
	return mean, stddev
}
