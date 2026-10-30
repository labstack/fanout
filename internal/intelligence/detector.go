// Package intelligence derives anomalies and log patterns from telemetry.
//
// Public log-pattern SQL goes through internal/query's validator, which
// rejects "--" outright. A SQL comment in one of these statements therefore
// fails the query at runtime, on a background goroutine, as a logged error
// nobody is watching -- that is how latency detection stopped reporting for
// half an hour. Rationale goes in Go comments; TestNoSQLCommentsInQueryStrings
// enforces it.
package intelligence

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/query"
)

// Detector runs intelligence detection on observability data
type Detector struct {
	duck                *query.Duck
	config              DetectorConfig
	mu                  sync.RWMutex
	snapshot            *Snapshot
	evaluationMu        sync.Mutex
	evaluation          *anomalyEvaluation
	annotationMu        sync.Mutex
	unpublishedFindings []annotations.Anomaly
	annotationBatches   chan annotationBatch
}

type anomalyEvaluation struct {
	through  time.Time
	findings []Anomaly
}

type annotationBatch struct {
	findings []annotations.Anomaly
	at       time.Time
}

// NewDetector creates a new intelligence detector
func NewDetector(duck *query.Duck, config DetectorConfig) *Detector {
	return &Detector{
		duck:              duck,
		config:            config,
		annotationBatches: make(chan annotationBatch, 1),
	}
}

// Run starts the detection loop
func (d *Detector) Run(ctx context.Context) {
	if !d.config.Enabled {
		slog.Info("detector disabled")
		return
	}

	slog.Info("starting detector", "interval", d.config.CheckInterval, "lookback", d.config.LookbackWindow)

	ticker := time.NewTicker(d.config.CheckInterval)
	defer ticker.Stop()
	persistenceCtx, cancel := context.WithCancel(ctx)
	persistenceDone := make(chan struct{})
	go func() { defer close(persistenceDone); d.persistAnnotations(persistenceCtx, d.duck.RecordAnomalies) }()
	defer func() { cancel(); <-persistenceDone }()

	// Run once on startup
	d.safeRunCheck(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("detector stopping")
			return
		case <-ticker.C:
			d.safeRunCheck(ctx)
		}
	}
}

// safeRunCheck wraps runCheck with panic recovery to prevent the detector loop from dying
func (d *Detector) safeRunCheck(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("detector panic recovered", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	d.runCheck(ctx)
}

// runCheck performs a single detection cycle
func (d *Detector) runCheck(ctx context.Context) {
	snapshot, err := d.GenerateSnapshot(ctx)
	if err != nil {
		slog.Error("generate detector snapshot failed", "error", err)
		return
	}
	d.publishSnapshot(snapshot)

	anomalyCount := len(snapshot.Anomalies)
	patternCount := len(snapshot.Patterns)
	if anomalyCount > 0 || patternCount > 0 {
		slog.Info("detection complete", "anomalies", anomalyCount, "patterns", patternCount, "health", snapshot.HealthScore)
	}
}

func (d *Detector) publishSnapshot(snapshot Snapshot) {
	d.mu.Lock()
	d.snapshot = &snapshot
	d.mu.Unlock()
	batch := annotationBatch{detectorAnnotations(snapshot, d.duck.DefaultNamespace()), snapshot.GeneratedAt}
	// Only the producer modifies the pending slot. Persistence can be blocked
	// while a newer cycle replaces the single pending batch.
	d.annotationMu.Lock()
	defer d.annotationMu.Unlock()
	batch.findings = mergeAnnotationFindings(d.unpublishedFindings, batch.findings)
	d.unpublishedFindings = nil
	select {
	case d.annotationBatches <- batch:
		return
	default:
	}
	select {
	case displaced := <-d.annotationBatches:
		batch.findings = mergeAnnotationFindings(displaced.findings, batch.findings)
	default:
	}
	d.annotationBatches <- batch
}

// maxCarriedAnomalies bounds unpublished findings, pending batches, and failed
// writes; it matches the anomaly history the store retains.
const maxCarriedAnomalies = 10000

// Preserve each evaluated interval, deduplicate repeated evaluations, and keep
// the newest findings when unpublished, pending, or failed writes reach the history limit.
func mergeAnnotationFindings(older, newer []annotations.Anomaly) []annotations.Anomaly {
	capacity := min(maxCarriedAnomalies, len(older)+len(newer))
	merged := make([]annotations.Anomaly, 0, capacity)
	seen := make(map[annotations.Anomaly]bool, capacity)
	for _, findings := range [][]annotations.Anomaly{newer, older} {
		for i := len(findings) - 1; i >= 0 && len(merged) < maxCarriedAnomalies; i-- {
			finding := findings[i]
			if !seen[finding] {
				seen[finding] = true
				merged = append(merged, finding)
			}
		}
	}
	slices.Reverse(merged)
	return merged
}

func (d *Detector) persistAnnotations(ctx context.Context, record func(context.Context, []annotations.Anomaly, time.Time) error) {
	// Keep failed findings with their evaluated intervals, even when the
	// watermark stalls and no new batch arrives to trigger another write.
	var carried annotationBatch
	var retry *time.Timer
	var retryC <-chan time.Time
	delay := time.Second
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		var batch annotationBatch
		select {
		case <-ctx.Done():
			return
		case batch = <-d.annotationBatches:
			batch.findings = mergeAnnotationFindings(carried.findings, batch.findings)
		case <-retryC:
			batch = carried
		}
		if retry != nil {
			retry.Stop()
		}
		retryC = nil
		if err := record(ctx, batch.findings, batch.at); err != nil && ctx.Err() == nil {
			slog.Error("persist detector annotations", "error", err, "carried", len(batch.findings))
			carried = batch
			if retry == nil {
				retry = time.NewTimer(delay)
			} else {
				retry.Reset(delay)
			}
			retryC = retry.C
			delay = min(2*delay, 30*time.Second)
		} else {
			carried = annotationBatch{}
			delay = time.Second
		}
	}
}

// LatestSnapshot returns the most recent intelligence snapshot, or nil if none.
func (d *Detector) LatestSnapshot() *Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.snapshot
}

// GenerateSnapshot generates a complete intelligence snapshot
func (d *Detector) GenerateSnapshot(ctx context.Context) (Snapshot, error) {
	d.evaluationMu.Lock()
	defer d.evaluationMu.Unlock()
	completed, err := d.duck.CompletedServiceRollupMinute(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read detector service watermark: %w", err)
	}
	now := time.Now()
	lookback := now.Add(-d.config.LookbackWindow)

	// Only completed anomaly evaluations are memoized. Live patterns run on
	// every wall-time check, even before the first rollup or while it stalls.
	if d.evaluation == nil || !completed.Equal(d.evaluation.through) {
		anomalies, err := d.detectAnomalies(ctx, completed.Add(-d.config.LookbackWindow), completed)
		if err != nil {
			return Snapshot{}, err
		}
		d.evaluation = &anomalyEvaluation{through: completed, findings: anomalies}
	}
	anomalies := d.evaluation.findings

	// Detect log patterns
	patterns, err := d.detectLogPatterns(ctx, lookback, now)
	if err != nil {
		// A later watermark may replace this evaluation before patterns recover.
		// Keep its original intervals until a successful annotation publication.
		d.annotationMu.Lock()
		d.unpublishedFindings = mergeAnnotationFindings(d.unpublishedFindings, detectorAnnotations(Snapshot{Anomalies: anomalies}, d.duck.DefaultNamespace()))
		d.annotationMu.Unlock()
		return Snapshot{}, err
	}

	// Calculate health score
	healthScore := d.calculateHealthScore(anomalies, patterns)

	// Generate insights from anomalies
	insights := d.generateInsights(anomalies, patterns)

	return Snapshot{
		evaluatedThrough: completed,
		GeneratedAt:      now,
		Timeframe:        fmt.Sprintf("last_%dm", int(d.config.LookbackWindow.Minutes())),
		Insights:         insights,
		Patterns:         patterns,
		Anomalies:        anomalies,
		Summary:          d.generateSummary(healthScore, anomalies, patterns),
		HealthScore:      healthScore,
	}, nil
}

// detectAnomalies evaluates exactly [start, end) from completed rollup minutes.
func (d *Detector) detectAnomalies(ctx context.Context, start, end time.Time) ([]Anomaly, error) {
	if end.IsZero() {
		return nil, nil
	}
	var anomalies []Anomaly

	// 1. Error rate anomalies by service
	errorAnomalies, err := d.detectErrorRateAnomalies(ctx, start, end)
	if err != nil {
		return nil, err
	}
	anomalies = append(anomalies, errorAnomalies...)

	// 2. Latency anomalies by service
	latencyAnomalies, err := d.detectLatencyAnomalies(ctx, start, end)
	if err != nil {
		return nil, err
	}
	anomalies = append(anomalies, latencyAnomalies...)

	// 3. Volume anomalies
	volumeAnomalies, err := d.detectVolumeAnomalies(ctx, start, end)
	if err != nil {
		return nil, err
	}
	anomalies = append(anomalies, volumeAnomalies...)

	return anomalies, nil
}

// minErrorRateStddev floors the error-rate z-score denominator at one
// percentage point. See errorRateAnomalySQL.
const minErrorRateStddev = 0.01

// minLatencyIncreaseRatio requires a 20% slowdown, keeping small proportional
// shifts in slow services quiet while allowing substantial regressions through.
const minLatencyIncreaseRatio = 0.20

// minLatencyIncreaseMS requires at least 5ms of extra p95 latency: even a large
// ratio on a sub-millisecond service is noise, but 20ms to 60ms still qualifies.
const minLatencyIncreaseMS = 5.0

// minLatencyStddev assumes at least 1ms of timing scatter, so a flat baseline
// can still detect regressions without amplifying fractional-millisecond jitter.
const minLatencyStddev = 1.0

// minVolumeChangeRatio requires a 25% traffic change in either direction,
// suppressing small count fluctuations while retaining 30% drops and doubling.
const minVolumeChangeRatio = 0.25

// minVolumeStddev assumes at least one span of counting uncertainty, allowing
// flat baselines to detect traffic stopping or surging without dividing by zero.
const minVolumeStddev = 1.0

// errorRateAnomalySQL compares the error rate in [startNano, endNano) against
// the window of equal length immediately before it.
//
// The z-score divides a difference of rates, so its denominator has to be the
// scatter of the RATE from bucket to bucket. It used to be
// STDDEV(CASE WHEN status = error THEN 1.0 ELSE 0.0 END) over raw spans, which
// is the scatter of individual span outcomes -- sqrt(p(1-p)), about 0.34 at a
// 14% error rate. Those are different quantities, and the mismatch punished
// exactly the services worth watching: the noisier the service, the larger the
// denominator, so at the 13.8% baseline the live demo ran, clearing the 2.0
// threshold needed the rate to jump 69 percentage points. A tripling to 41%
// scored 0.80.
//
// The denominator is floored because a healthy service's rate is flat at zero,
// giving it a bucket-to-bucket stddev of exactly zero -- so the service that
// just started failing was the one that could not alert (z=0.00 going from no
// errors to 42%). One percentage point is the least noise worth assuming: it
// keeps a single stray error in a few thousand spans below the threshold while
// letting a real break through.
func errorRateAnomalySQL(startNano, endNano int64, scope string) string {
	return fmt.Sprintf(`
		WITH current_period AS (
			SELECT
				service_name,
				AVG(bucket_rate) AS error_rate
			FROM (
				SELECT
					service as service_name,
					(SUM(error_rate * spans) / SUM(spans)) AS bucket_rate
				FROM service_rollup
				WHERE bucket >= make_timestamp_ns(%d) AND bucket < make_timestamp_ns(%d) AND spans > 0
				%s
				GROUP BY service, time_bucket(INTERVAL '5 minutes', bucket)
			) buckets
			GROUP BY service_name
		),
		baseline_period AS (
			SELECT
				service_name,
				AVG(bucket_rate) AS error_rate,
				GREATEST(COALESCE(STDDEV(bucket_rate), 0.0), %f) AS error_stddev
			FROM (
				SELECT
					service as service_name,
					(SUM(error_rate * spans) / SUM(spans)) AS bucket_rate
				FROM service_rollup
				WHERE bucket >= make_timestamp_ns(%d) AND bucket < make_timestamp_ns(%d) AND spans > 0
				%s
				GROUP BY service, time_bucket(INTERVAL '5 minutes', bucket)
			) buckets
			GROUP BY service_name
		)
		SELECT
			c.service_name,
			c.error_rate AS current_rate,
			COALESCE(b.error_rate, 0.0) AS baseline_rate,
			CASE
				WHEN b.error_stddev > 0 THEN (c.error_rate - b.error_rate) / b.error_stddev
				ELSE 0.0
			END AS z_score
		FROM current_period c
		LEFT JOIN baseline_period b ON c.service_name = b.service_name
		WHERE c.error_rate > 0
	`, startNano, endNano, scope, minErrorRateStddev, startNano-endNano+startNano, startNano, scope)
}

// detectErrorRateAnomalies detects error rate spikes
func (d *Detector) detectErrorRateAnomalies(ctx context.Context, start, end time.Time) ([]Anomaly, error) {
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	namespace := d.duck.DefaultNamespace()
	scope := detectorScopeClause(namespace)

	// Compare current error rate to baseline (previous period)
	sql := errorRateAnomalySQL(startNano, endNano, scope)

	rows, err := d.readServiceAnomalies(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("error rate detection: %w", err)
	}

	var anomalies []Anomaly
	for _, row := range rows {
		serviceName := row.service
		currentRate := row.current
		baselineRate := row.baseline
		zScore := row.zScore

		if math.Abs(zScore) >= d.config.ErrorRateThreshold {
			anomalies = append(anomalies, Anomaly{
				Type:        AnomalyErrorRateChange,
				ServiceName: serviceName,
				Metric:      "error_rate",
				Current:     currentRate * 100, // Convert to percentage
				Baseline:    baselineRate * 100,
				ZScore:      zScore,
				DetectedAt:  time.Now(),
				WindowStart: start,
				WindowEnd:   end,
				Description: fmt.Sprintf("%s error rate: %.1f%% (baseline: %.1f%%, z-score: %.2f)",
					serviceName, currentRate*100, baselineRate*100, zScore),
			})
		}
	}

	return anomalies, nil
}

// detectLatencyAnomalies detects latency degradation
func (d *Detector) detectLatencyAnomalies(ctx context.Context, start, end time.Time) ([]Anomaly, error) {
	// service_rollup grades served spans (with its outbound-only fallback).
	// Weight minute p95 values by spans; do not recompute raw-span quantiles.
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	namespace := d.duck.DefaultNamespace()
	scope := detectorScopeClause(namespace)

	sql := latencyAnomalySQL(startNano, endNano, scope)

	rows, err := d.readServiceAnomalies(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("latency detection: %w", err)
	}

	var anomalies []Anomaly
	for _, row := range rows {
		serviceName := row.service
		currentP95 := row.current
		baselineP95 := row.baseline
		zScore := row.zScore

		increase := currentP95 - baselineP95
		if zScore >= d.config.LatencyThreshold &&
			increase >= minLatencyIncreaseMS && increase >= baselineP95*minLatencyIncreaseRatio {
			anomalies = append(anomalies, Anomaly{
				Type:        AnomalyLatencyDegradation,
				ServiceName: serviceName,
				Metric:      "p95_latency",
				Current:     currentP95,
				Baseline:    baselineP95,
				ZScore:      zScore,
				DetectedAt:  time.Now(),
				WindowStart: start,
				WindowEnd:   end,
				Description: fmt.Sprintf("%s P95 latency: %.1fms (baseline P95: %.1fms, z-score: %.2f)",
					serviceName, currentP95, baselineP95, zScore),
			})
		}
	}

	return anomalies, nil
}

// volumeAnomalySQL compares span volume in [startNano, endNano) against the
// window of equal length immediately before it.
//
// Both sides are averaged over 5-minute buckets, and that symmetry is the whole
// point. The current side used to be a single COUNT(*) over the entire window
// while the baseline averaged per-bucket counts, so on a 15-minute window the
// current figure was three times the baseline for every service no matter what
// the traffic did. Every service was permanently a critical volume anomaly:
// steady traffic scored z=75 in TestVolumeAnomalyComparesEqualWindows, and the
// live demo sat at health score 0 with 17 meaningless critical insights.
// Retain baseline-only services with a zero current count so traffic stopping
// is evaluated even though the rollup has no positive-span rows for that window.
func volumeAnomalySQL(startNano, endNano int64, scope string) string {
	return fmt.Sprintf(`
		WITH current_period AS (
			SELECT
				service_name,
				AVG(cnt) AS span_count
			FROM (
				SELECT
					service as service_name,
					SUM(spans) AS cnt
				FROM service_rollup
				WHERE bucket >= make_timestamp_ns(%d) AND bucket < make_timestamp_ns(%d) AND spans > 0
				%s
				GROUP BY service, time_bucket(INTERVAL '5 minutes', bucket)
			) subq
			GROUP BY service_name
		),
		baseline_period AS (
			SELECT
				service_name,
				AVG(cnt) AS avg_count,
				STDDEV(cnt) AS count_stddev
			FROM (
				SELECT
					service as service_name,
					SUM(spans) AS cnt
				FROM service_rollup
				WHERE bucket >= make_timestamp_ns(%d) AND bucket < make_timestamp_ns(%d) AND spans > 0
				%s
				GROUP BY service, time_bucket(INTERVAL '5 minutes', bucket)
			) subq
			GROUP BY service_name
		)
		SELECT
			COALESCE(c.service_name, b.service_name) AS service_name,
			COALESCE(c.span_count, 0.0)::DOUBLE AS current_count,
			COALESCE(b.avg_count, 0.0) AS baseline_count,
			CASE
				WHEN b.avg_count > 0 THEN (COALESCE(c.span_count, 0.0) - b.avg_count) / GREATEST(COALESCE(b.count_stddev, 0.0), %f)
				ELSE 0.0
			END AS z_score
		FROM current_period c
		FULL OUTER JOIN baseline_period b ON c.service_name = b.service_name
	`, startNano, endNano, scope, startNano-endNano+startNano, startNano, scope, minVolumeStddev)
}

// detectVolumeAnomalies detects unusual traffic volume changes
func (d *Detector) detectVolumeAnomalies(ctx context.Context, start, end time.Time) ([]Anomaly, error) {
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	namespace := d.duck.DefaultNamespace()
	scope := detectorScopeClause(namespace)

	sql := volumeAnomalySQL(startNano, endNano, scope)

	rows, err := d.readServiceAnomalies(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("volume detection: %w", err)
	}

	var anomalies []Anomaly
	for _, row := range rows {
		serviceName := row.service
		currentCount := row.current
		baselineCount := row.baseline
		zScore := row.zScore

		if math.Abs(zScore) >= d.config.VolumeThreshold && baselineCount > 0 &&
			math.Abs(currentCount-baselineCount) >= baselineCount*minVolumeChangeRatio {
			anomalies = append(anomalies, Anomaly{
				Type:        AnomalyVolumeChange,
				ServiceName: serviceName,
				Metric:      "span_count",
				Current:     currentCount,
				Baseline:    baselineCount,
				ZScore:      zScore,
				DetectedAt:  time.Now(),
				WindowStart: start,
				WindowEnd:   end,
				Description: fmt.Sprintf("%s volume: %.0f spans (baseline: %.0f, z-score: %.2f)",
					serviceName, currentCount, baselineCount, zScore),
			})
		}
	}

	return anomalies, nil
}

// latencyAnomalySQL compares span-weighted minute p95 values. Baseline
// scatter remains the standard deviation of five-minute bucket values.
func latencyAnomalySQL(startNano, endNano int64, scope string) string {
	return fmt.Sprintf(`
		WITH current_period AS (
			SELECT
				service as service_name,
				SUM(p95_ms * spans) / SUM(spans) AS p95_latency
			FROM service_rollup
			WHERE bucket >= make_timestamp_ns(%d) AND bucket < make_timestamp_ns(%d) AND spans > 0
			  %s
			GROUP BY service
		),
		baseline_buckets AS (
			SELECT
				service as service_name,
				time_bucket(INTERVAL '5 minutes', bucket) AS baseline_bucket,
				SUM(p95_ms * spans) / SUM(spans) AS p95_latency
			FROM service_rollup
			WHERE bucket >= make_timestamp_ns(%d) AND bucket < make_timestamp_ns(%d) AND spans > 0
			  %s
			GROUP BY service, baseline_bucket
		),
		baseline_period AS (
			SELECT
				service_name,
				AVG(p95_latency) AS avg_p95,
				STDDEV(p95_latency) AS p95_stddev
			FROM baseline_buckets
			GROUP BY service_name
		)
		SELECT
			c.service_name,
			c.p95_latency AS current_p95,
			COALESCE(b.avg_p95, 0.0) AS baseline_p95,
			CASE
				WHEN b.avg_p95 IS NOT NULL THEN (c.p95_latency - b.avg_p95) / GREATEST(COALESCE(b.p95_stddev, 0.0), %f)
				ELSE 0.0
			END AS z_score
		FROM current_period c
		LEFT JOIN baseline_period b ON c.service_name = b.service_name
	`, startNano, endNano, scope, startNano-endNano+startNano, startNano, scope, minLatencyStddev)

}

type serviceAnomalyRow struct {
	service                   string
	current, baseline, zScore float64
}

// Use the same internal read path as the product's service-rollup panels.
// These fixed statements never pass through the public SQL allowlist.
func (d *Detector) readServiceAnomalies(ctx context.Context, statement string) ([]serviceAnomalyRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := d.duck.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []serviceAnomalyRow
	for rows.Next() {
		var row serviceAnomalyRow
		if err := rows.Scan(&row.service, &row.current, &row.baseline, &row.zScore); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// detectLogPatterns groups repetitive log messages
func (d *Detector) detectLogPatterns(ctx context.Context, start, end time.Time) ([]Pattern, error) {
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	namespace := d.duck.DefaultNamespace()
	scope := detectorScopeClause(namespace)

	sql := fmt.Sprintf(`
		SELECT
			body_template AS template,
			severity,
			service as service_name,
			COUNT(*) AS occurrence_count,
			epoch_ns(MIN(time)::TIMESTAMP_NS) AS first_seen_nano,
			epoch_ns(MAX(time)::TIMESTAMP_NS) AS last_seen_nano
		FROM logs
		WHERE time >= make_timestamp_ns(%d)::TIMESTAMPTZ_NS AND time < make_timestamp_ns(%d)::TIMESTAMPTZ_NS
		AND severity IN ('WARN', 'ERROR', 'FATAL')
		%s
		GROUP BY body_template, severity, service
		HAVING COUNT(*) >= 3
		ORDER BY occurrence_count DESC
		LIMIT 50
	`, startNano, endNano, scope)

	resp := d.duck.ExecuteSQL(ctx, query.SQLRequest{Query: sql})
	if resp.Error != "" {
		return nil, fmt.Errorf("pattern detection: %s", resp.Error)
	}

	var patterns []Pattern
	for _, row := range resp.Results {
		template, _ := row["template"].(string)
		severity, _ := row["severity"].(string)
		serviceName, _ := row["service_name"].(string)
		count := toInt(row["occurrence_count"])
		firstSeenNano := toInt64(row["first_seen_nano"])
		lastSeenNano := toInt64(row["last_seen_nano"])

		patterns = append(patterns, Pattern{
			Template:    template,
			Count:       count,
			FirstSeen:   time.Unix(0, firstSeenNano),
			LastSeen:    time.Unix(0, lastSeenNano),
			Severity:    severity,
			ServiceName: serviceName,
		})
	}

	return patterns, nil
}

// calculateHealthScore calculates overall system health (0-100, lower is worse)
func (d *Detector) calculateHealthScore(anomalies []Anomaly, patterns []Pattern) float64 {
	score := 100.0

	// Deduct points for anomalies based on severity (z-score)
	for _, a := range anomalies {
		penalty := 0.0
		if math.Abs(a.ZScore) >= 3.0 {
			penalty = 15.0 // Critical anomaly
		} else if math.Abs(a.ZScore) >= 2.5 {
			penalty = 10.0 // Serious anomaly
		} else if math.Abs(a.ZScore) >= 2.0 {
			penalty = 5.0 // Moderate anomaly
		}
		score -= penalty
	}

	// Deduct points for repetitive error/warn patterns
	for _, p := range patterns {
		if p.Severity == "ERROR" && p.Count >= 10 {
			score -= 5.0
		} else if p.Severity == "WARN" && p.Count >= 20 {
			score -= 2.0
		}
	}

	// Floor at 0
	if score < 0 {
		score = 0
	}

	return score
}

// generateInsights converts anomalies to user-facing insights
func (d *Detector) generateInsights(anomalies []Anomaly, patterns []Pattern) []Insight {
	var insights []Insight

	for _, a := range anomalies {
		severity := "info"
		if math.Abs(a.ZScore) >= 3.0 {
			severity = "critical"
		} else if math.Abs(a.ZScore) >= 2.5 {
			severity = "warning"
		}

		insightType := "anomaly"
		if a.Type == AnomalyErrorRateChange || a.Type == AnomalyErrorSpike {
			insightType = "anomaly"
		} else if a.Type == AnomalyLatencyDegradation {
			insightType = "trend"
		}

		insights = append(insights, Insight{
			Type:     insightType,
			Severity: severity,
			Message:  a.Description,
		})
	}

	// Add insights for severe pattern repetition
	for _, p := range patterns {
		if p.Severity == "ERROR" && p.Count >= 20 {
			insights = append(insights, Insight{
				Type:     "trend",
				Severity: "warning",
				Message:  fmt.Sprintf("%s: %s repeated %d times", p.ServiceName, p.Template, p.Count),
			})
		}
	}

	return insights
}

// generateSummary creates a human-readable summary
func (d *Detector) generateSummary(healthScore float64, anomalies []Anomaly, patterns []Pattern) string {
	if healthScore >= 90 {
		return fmt.Sprintf("System healthy (%.0f/100). %d patterns detected.", healthScore, len(patterns))
	} else if healthScore >= 70 {
		return fmt.Sprintf("Minor issues detected (%.0f/100). %d anomalies, %d patterns.",
			healthScore, len(anomalies), len(patterns))
	} else if healthScore >= 50 {
		return fmt.Sprintf("System degraded (%.0f/100). %d anomalies require attention.",
			healthScore, len(anomalies))
	} else {
		return fmt.Sprintf("Critical system issues (%.0f/100). Immediate attention required.", healthScore)
	}
}

func detectorScopeClause(namespace string) string {
	if namespace == "" {
		return ""
	}
	return fmt.Sprintf("AND namespace = %s", detectorSQLLiteral(namespace))
}

func detectorSQLLiteral(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// toInt64 converts interface{} to int64, handling both int64 and float64
func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	default:
		return 0
	}
}

// toInt converts interface{} to int, handling both int64 and float64
func toInt(v interface{}) int {
	return int(toInt64(v))
}

func detectorAnnotations(snapshot Snapshot, namespace string) []annotations.Anomaly {
	findings := make([]annotations.Anomaly, 0, len(snapshot.Anomalies))
	for _, a := range snapshot.Anomalies {
		severity := "warn"
		if math.Abs(a.ZScore) >= 4 {
			severity = "bad"
		}
		findings = append(findings, annotations.Anomaly{Namespace: namespace, Service: a.ServiceName, Kind: string(a.Type), From: a.WindowStart, To: a.WindowEnd, Title: a.Description, Severity: severity})
	}
	return findings
}
