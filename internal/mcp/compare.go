package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// compare - Side-by-side service comparison with multiple modes

type CompareIn struct {
	Mode     string            `json:"mode,omitempty" jsonschema:"Comparison mode: services, time, operations,default=services"`
	Services []string          `json:"services,omitempty" jsonschema:"Services to compare (2-4) for services mode"`
	Service  string            `json:"service,omitempty" jsonschema:"Service for time/operations mode"`
	Left     map[string]string `json:"left,omitempty" jsonschema:"Left side config: window (ISO range) for time mode, operation for operations mode"`
	Right    map[string]string `json:"right,omitempty" jsonschema:"Right side config: window (ISO range) for time mode, operation for operations mode"`
	Focus    []string          `json:"focus,omitempty" jsonschema:"Metrics to compare,default=[latency,errors,throughput]"`
	Window   string            `json:"window,omitempty" jsonschema:"Time window for services mode,default=1h"`
}

type CompareMetrics struct {
	Service    string  `json:"service"`
	Requests   int64   `json:"requests"`
	ErrorRate  float64 `json:"error_rate"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	AvgMs      float64 `json:"avg_ms"`
	ErrorCount int64   `json:"error_count"`
}

// MetricDiff describes how a metric changed between left and right.
type MetricDiff struct {
	LeftValue                float64 `json:"left_value"`
	RightValue               float64 `json:"right_value"`
	ChangePct                float64 `json:"change_pct"`
	Direction                string  `json:"direction"` // "regression", "improvement", "stable"
	StatisticallySignificant bool    `json:"statistically_significant"`
}

type CompareOut struct {
	// Services mode (existing)
	Services []CompareMetrics `json:"services,omitempty"`
	Winner   string           `json:"winner,omitempty"`
	Summary  string           `json:"summary,omitempty"`

	// All modes
	Mode       string                `json:"mode"`
	LeftLabel  string                `json:"left_label,omitempty"`
	RightLabel string                `json:"right_label,omitempty"`
	Comparison map[string]MetricDiff `json:"comparison,omitempty"`
	Verdict    string                `json:"verdict,omitempty"`
}

func (s *Server) compare(ctx context.Context, req *mcp.CallToolRequest, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	mode := in.Mode
	if mode == "" {
		mode = "services"
	}

	switch mode {
	case "services":
		return s.compareServices(ctx, in)
	case "time":
		return s.compareTime(ctx, in)
	case "operations":
		return s.compareOperations(ctx, in)
	default:
		return nil, CompareOut{}, fmt.Errorf("invalid mode %q: must be services, time, or operations", mode)
	}
}

// compareServices handles the original services-mode comparison.
func (s *Server) compareServices(ctx context.Context, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	if len(in.Services) < 2 {
		return nil, CompareOut{}, fmt.Errorf("need at least 2 services to compare")
	}
	if len(in.Services) > 4 {
		return nil, CompareOut{}, fmt.Errorf("max 4 services to compare")
	}

	// Parse window string; default to 1h
	windowStr := in.Window
	if windowStr == "" {
		windowStr = "1h"
	}
	tw, err := parseWindow(windowStr)
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid window: %w", err)
	}
	window := clampInt(tw.Minutes, minWindow, maxWindow, 60)

	// Build parameterized IN clause for services
	placeholders := make([]string, len(in.Services))
	args := make([]any, len(in.Services))
	for i, svc := range in.Services {
		placeholders[i] = "?"
		args[i] = svc
	}

	// Query metrics for all services at once
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
		return nil, CompareOut{}, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Parse results
	var metrics []CompareMetrics
	for rows.Next() {
		var m CompareMetrics
		var logCount, metricCount int64
		if err := rows.Scan(&m.Service, &m.Requests, &m.ErrorRate, &m.P50Ms, &m.P95Ms, &logCount, &metricCount); err != nil {
			slog.Warn("scan failed", "method", "compareServices", "err", err)
			continue
		}
		// Count all signals for determining if service has data
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
		return nil, CompareOut{}, fmt.Errorf("compare iteration: %w", err)
	}

	// Add empty entries for services with no data
	found := make(map[string]bool)
	for _, m := range metrics {
		found[m.Service] = true
	}
	for _, svc := range in.Services {
		if !found[svc] {
			metrics = append(metrics, CompareMetrics{Service: svc})
		}
	}

	// Determine winner (lowest P95 with acceptable error rate)
	winner := ""
	bestScore := float64(-1)
	for _, m := range metrics {
		if m.Requests == 0 || (m.P50Ms == 0 && m.P95Ms == 0) {
			continue // skip services with no span-based latency data
		}
		// Score: lower is better (P95 * (1 + error_rate*10))
		score := m.P95Ms * (1 + m.ErrorRate*10)
		if bestScore < 0 || score < bestScore {
			bestScore = score
			winner = m.Service
		}
	}

	// Build summary
	summary := fmt.Sprintf("Compared %d services over %d minutes. ", len(metrics), window)
	if winner != "" {
		summary += fmt.Sprintf("%s has best performance.", winner)
	}

	out := CompareOut{
		Mode:     "services",
		Services: metrics,
		Winner:   winner,
		Summary:  summary,
	}

	return nil, out, nil
}

// compareTime compares the same service across two time windows.
func (s *Server) compareTime(ctx context.Context, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	if in.Service == "" {
		return nil, CompareOut{}, fmt.Errorf("service is required for time mode")
	}
	if in.Left == nil || in.Left["window"] == "" {
		return nil, CompareOut{}, fmt.Errorf("left.window is required for time mode (ISO range: start/end)")
	}
	if in.Right == nil || in.Right["window"] == "" {
		return nil, CompareOut{}, fmt.Errorf("right.window is required for time mode (ISO range: start/end)")
	}

	leftTW, err := parseWindow(in.Left["window"])
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid left.window: %w", err)
	}
	rightTW, err := parseWindow(in.Right["window"])
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid right.window: %w", err)
	}

	leftLabel := fmt.Sprintf("Before (%s)", formatWindowLabel(leftTW))
	rightLabel := fmt.Sprintf("After (%s)", formatWindowLabel(rightTW))

	focus := resolveFocus(in.Focus)

	// Query per-bucket stats for each window for statistical significance
	leftBuckets, err := queryRollupBuckets(ctx, s, in.Service, leftTW)
	if err != nil {
		slog.Warn("left window query failed", "method", "compareTime", "err", err)
	}
	rightBuckets, err := queryRollupBuckets(ctx, s, in.Service, rightTW)
	if err != nil {
		slog.Warn("right window query failed", "method", "compareTime", "err", err)
	}

	// Aggregate summary stats for each window
	leftAgg := aggregateBuckets(leftBuckets)
	rightAgg := aggregateBuckets(rightBuckets)

	comparison := buildComparison(leftAgg, rightAgg, leftBuckets, rightBuckets, focus)
	verdict := buildVerdict(comparison)

	out := CompareOut{
		Mode:       "time",
		LeftLabel:  leftLabel,
		RightLabel: rightLabel,
		Comparison: comparison,
		Verdict:    verdict,
	}

	return nil, out, nil
}

// compareOperations compares two operations within the same service.
func (s *Server) compareOperations(ctx context.Context, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	if in.Service == "" {
		return nil, CompareOut{}, fmt.Errorf("service is required for operations mode")
	}
	if in.Left == nil || in.Left["operation"] == "" {
		return nil, CompareOut{}, fmt.Errorf("left.operation is required for operations mode")
	}
	if in.Right == nil || in.Right["operation"] == "" {
		return nil, CompareOut{}, fmt.Errorf("right.operation is required for operations mode")
	}

	leftOp := in.Left["operation"]
	rightOp := in.Right["operation"]

	// Default window for operations comparison
	windowStr := in.Window
	if windowStr == "" {
		windowStr = "1h"
	}
	tw, err := parseWindow(windowStr)
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("invalid window: %w", err)
	}

	focus := resolveFocus(in.Focus)

	leftStats, err := queryOperationStats(ctx, s, in.Service, leftOp, tw)
	if err != nil {
		slog.Warn("left operation query failed", "method", "compareOperations", "err", err)
	}
	rightStats, err := queryOperationStats(ctx, s, in.Service, rightOp, tw)
	if err != nil {
		slog.Warn("right operation query failed", "method", "compareOperations", "err", err)
	}

	// For operations mode, we don't have bucket-level data for significance testing
	// Use empty slices to skip statistical significance check
	comparison := buildComparison(leftStats, rightStats, nil, nil, focus)
	verdict := buildVerdict(comparison)

	out := CompareOut{
		Mode:       "operations",
		LeftLabel:  leftOp,
		RightLabel: rightOp,
		Comparison: comparison,
		Verdict:    verdict,
	}

	return nil, out, nil
}

// rollupBucket holds per-bucket stats from service_rollup.
type rollupBucket struct {
	P95Ms     float64
	P50Ms     float64
	ErrorRate float64
	Spans     int64
}

// aggStats holds aggregated summary stats.
type aggStats struct {
	P95Ms      float64
	P50Ms      float64
	ErrorRate  float64
	Throughput float64 // spans per minute
}

// queryRollupBuckets fetches per-bucket rows from service_rollup for a service in a window.
func queryRollupBuckets(ctx context.Context, s *Server, service string, tw TimeWindow) ([]rollupBucket, error) {
	q := `
		SELECT
			COALESCE(p95_ms, 0),
			COALESCE(p50_ms, 0),
			COALESCE(error_rate, 0),
			COALESCE(spans, 0)
		FROM service_rollup
		WHERE service = ? AND bucket >= ? AND bucket < ?
		ORDER BY bucket ASC
	`
	rows, err := s.duck.DB.QueryContext(ctx, q, service, tw.Start, tw.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []rollupBucket
	for rows.Next() {
		var b rollupBucket
		if err := rows.Scan(&b.P95Ms, &b.P50Ms, &b.ErrorRate, &b.Spans); err != nil {
			slog.Warn("scan failed", "method", "queryRollupBuckets", "err", err)
			continue
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

// aggregateBuckets computes summary stats across all buckets.
func aggregateBuckets(buckets []rollupBucket) aggStats {
	if len(buckets) == 0 {
		return aggStats{}
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
		return aggStats{Throughput: 0}
	}
	minutes := float64(len(buckets)) // each bucket is ~1 minute
	if minutes == 0 {
		minutes = 1
	}
	return aggStats{
		P95Ms:      sumP95 / float64(count),
		P50Ms:      sumP50 / float64(count),
		ErrorRate:  sumErrorRate / float64(count),
		Throughput: float64(totalSpans) / minutes,
	}
}

// queryOperationStats queries span Parquet for operation-level stats.
func queryOperationStats(ctx context.Context, s *Server, service, operation string, tw TimeWindow) (aggStats, error) {
	q := `
		SELECT
			COALESCE(quantile_cont(duration_ms, 0.95), 0) AS p95_ms,
			COALESCE(quantile_cont(duration_ms, 0.50), 0) AS p50_ms,
			COALESCE(AVG(CASE WHEN status = 'ERROR' THEN 1.0 ELSE 0.0 END), 0) AS error_rate,
			COUNT(*) AS total_spans
		FROM spans
		WHERE service = ?
		  AND operation = ?
		  AND start_time >= ?
		  AND start_time < ?
	`
	rows, err := s.duck.DB.QueryContext(ctx, q, service, operation, tw.Start, tw.End)
	if err != nil {
		return aggStats{}, err
	}
	defer rows.Close()

	if rows.Next() {
		var a aggStats
		var totalSpans int64
		if err := rows.Scan(&a.P95Ms, &a.P50Ms, &a.ErrorRate, &totalSpans); err != nil {
			return aggStats{}, err
		}
		minutes := tw.End.Sub(tw.Start).Minutes()
		if minutes == 0 {
			minutes = 1
		}
		a.Throughput = float64(totalSpans) / minutes
		return a, rows.Err()
	}
	return aggStats{}, rows.Err()
}

// buildComparison builds the comparison map for the given focus metrics.
func buildComparison(left, right aggStats, leftBuckets, rightBuckets []rollupBucket, focus []string) map[string]MetricDiff {
	out := make(map[string]MetricDiff)

	for _, f := range focus {
		switch f {
		case "latency":
			// Use P95 as the primary latency metric
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
			out["latency"] = makeMetricDiff(left.P95Ms, right.P95Ms, leftSeries, rightSeries, true /* higher is worse */)

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
			out["errors"] = makeMetricDiff(left.ErrorRate*100, right.ErrorRate*100, leftSeries, rightSeries, true /* higher is worse */)

		case "throughput":
			// For throughput, higher is better
			out["throughput"] = makeMetricDiff(left.Throughput, right.Throughput, nil, nil, false /* higher is better */)
		}
	}

	return out
}

// makeMetricDiff computes a MetricDiff from left/right values and optional bucket series.
// higherIsBad controls direction: if true, an increase is a regression.
func makeMetricDiff(leftVal, rightVal float64, leftSeries, rightSeries []float64, higherIsBad bool) MetricDiff {
	changePct := 0.0
	if leftVal != 0 {
		changePct = ((rightVal - leftVal) / leftVal) * 100
	}

	direction := "stable"
	threshold := 5.0 // 5% change to be considered non-stable
	if math.Abs(changePct) > threshold {
		if (changePct > 0) == higherIsBad {
			direction = "regression"
		} else {
			direction = "improvement"
		}
	}

	sig := isSignificant(leftSeries, rightSeries)

	return MetricDiff{
		LeftValue:                leftVal,
		RightValue:               rightVal,
		ChangePct:                math.Round(changePct*10) / 10,
		Direction:                direction,
		StatisticallySignificant: sig,
	}
}

// buildVerdict produces a human-readable verdict from the comparison results.
func buildVerdict(comparison map[string]MetricDiff) string {
	var regressions, improvements []string
	for metric, diff := range comparison {
		if diff.Direction == "regression" && diff.StatisticallySignificant {
			regressions = append(regressions, fmt.Sprintf("%s (%+.0f%%)", metric, diff.ChangePct))
		} else if diff.Direction == "regression" {
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

// resolveFocus returns the focus list, defaulting to all three metrics.
func resolveFocus(focus []string) []string {
	if len(focus) == 0 {
		return []string{"latency", "errors", "throughput"}
	}
	return focus
}

// formatWindowLabel formats a TimeWindow as a short human-readable label.
func formatWindowLabel(tw TimeWindow) string {
	start := tw.Start.Format("15:04")
	end := tw.End.Format("15:04")
	return fmt.Sprintf("%s–%s", start, end)
}

// isSignificant returns true if the difference between the two sample means is
// greater than 2x the stddev of the smaller sample, and both samples have >= 5 buckets.
func isSignificant(left, right []float64) bool {
	if len(left) < 5 || len(right) < 5 {
		return false
	}

	// Use the smaller sample for stddev calculation
	smaller, larger := left, right
	if len(right) < len(left) {
		smaller, larger = right, left
	}

	smallerMean, smallerStddev := meanStdDev(smaller)
	largerMean, _ := meanStdDev(larger)

	if smallerStddev == 0 {
		// If stddev is 0, any difference is "significant"
		return largerMean != smallerMean
	}

	return math.Abs(largerMean-smallerMean) > 2*smallerStddev
}

// meanStdDev returns the mean and population standard deviation of a slice.
func meanStdDev(vals []float64) (mean, stddev float64) {
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

// Helper functions for row parsing
func getString(row map[string]interface{}, key string) string {
	if v, ok := row[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt64(row map[string]interface{}, key string) int64 {
	if v, ok := row[key]; ok {
		switch n := v.(type) {
		case int64:
			return n
		case float64:
			return int64(n)
		case int:
			return int64(n)
		}
	}
	return 0
}

func getFloat64(row map[string]interface{}, key string) float64 {
	if v, ok := row[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int64:
			return float64(n)
		case int:
			return float64(n)
		}
	}
	return 0
}
