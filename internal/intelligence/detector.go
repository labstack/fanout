package intelligence

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/query"
)

// isNoDataError returns true if the error indicates no parquet files exist yet
func isNoDataError(errMsg string) bool {
	return strings.Contains(errMsg, "No files found that match the pattern")
}

// Detector runs intelligence detection on observability data
type Detector struct {
	duck     *query.Duck
	config   DetectorConfig
	mu       sync.RWMutex
	snapshot *IntelligenceSnapshot
}

// NewDetector creates a new intelligence detector
func NewDetector(duck *query.Duck, config DetectorConfig) *Detector {
	return &Detector{
		duck:   duck,
		config: config,
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
	snapshot := d.GenerateSnapshot(ctx)

	d.mu.Lock()
	d.snapshot = &snapshot
	d.mu.Unlock()

	anomalyCount := len(snapshot.Anomalies)
	patternCount := len(snapshot.Patterns)

	if anomalyCount > 0 || patternCount > 0 {
		slog.Info("detection complete", "anomalies", anomalyCount, "patterns", patternCount, "health", snapshot.HealthScore)
	}
}

// LatestSnapshot returns the most recent intelligence snapshot, or nil if none.
func (d *Detector) LatestSnapshot() *IntelligenceSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.snapshot
}

// GenerateSnapshot generates a complete intelligence snapshot
func (d *Detector) GenerateSnapshot(ctx context.Context) IntelligenceSnapshot {
	now := time.Now()
	lookback := now.Add(-d.config.LookbackWindow)

	// Detect anomalies
	anomalies := d.detectAnomalies(ctx, lookback, now)

	// Detect log patterns
	patterns := d.detectLogPatterns(ctx, lookback, now)

	// Calculate health score
	healthScore := d.calculateHealthScore(anomalies, patterns)

	// Generate insights from anomalies
	insights := d.generateInsights(anomalies, patterns)

	return IntelligenceSnapshot{
		GeneratedAt: now,
		Timeframe:   fmt.Sprintf("last_%dm", int(d.config.LookbackWindow.Minutes())),
		Insights:    insights,
		Patterns:    patterns,
		Anomalies:   anomalies,
		Summary:     d.generateSummary(healthScore, anomalies, patterns),
		HealthScore: healthScore,
	}
}

// detectAnomalies detects statistical anomalies in metrics
func (d *Detector) detectAnomalies(ctx context.Context, start, end time.Time) []Anomaly {
	var anomalies []Anomaly

	// 1. Error rate anomalies by service
	errorAnomalies := d.detectErrorRateAnomalies(ctx, start, end)
	anomalies = append(anomalies, errorAnomalies...)

	// 2. Latency anomalies by service
	latencyAnomalies := d.detectLatencyAnomalies(ctx, start, end)
	anomalies = append(anomalies, latencyAnomalies...)

	// 3. Volume anomalies
	volumeAnomalies := d.detectVolumeAnomalies(ctx, start, end)
	anomalies = append(anomalies, volumeAnomalies...)

	return anomalies
}

// detectErrorRateAnomalies detects error rate spikes
func (d *Detector) detectErrorRateAnomalies(ctx context.Context, start, end time.Time) []Anomaly {
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	windowMins := int(d.config.LookbackWindow.Minutes())
	tenantID, namespace := d.duck.DefaultTenantID(), d.duck.DefaultNamespace()
	spansGlob := d.duck.SpansGlob(tenantID, namespace, windowMins*2) // 2x for baseline

	// Compare current error rate to baseline (previous period)
	sql := fmt.Sprintf(`
		WITH current_period AS (
			SELECT
				"name=service_name" as service_name,
				COUNT(*) FILTER (WHERE "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')) AS error_count,
				COUNT(*) AS total_count,
				(COUNT(*) FILTER (WHERE "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR'))::FLOAT / COUNT(*)::FLOAT) AS error_rate
			FROM read_parquet(%s, union_by_name=true)
			WHERE "name=start_unix_nano" >= %d AND "name=start_unix_nano" < %d
			GROUP BY "name=service_name"
		),
		baseline_period AS (
			SELECT
				"name=service_name" as service_name,
				COUNT(*) FILTER (WHERE "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')) AS error_count,
				COUNT(*) AS total_count,
				(COUNT(*) FILTER (WHERE "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR'))::FLOAT / COUNT(*)::FLOAT) AS error_rate,
				STDDEV(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_stddev
			FROM read_parquet(%s, union_by_name=true)
			WHERE "name=start_unix_nano" >= %d AND "name=start_unix_nano" < %d
			GROUP BY "name=service_name"
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
	`, spansGlob, startNano, endNano, spansGlob, startNano-endNano+startNano, startNano)

	resp := d.duck.ExecuteSQL(ctx, query.SQLRequest{Query: sql})
	if resp.Error != "" {
		if !isNoDataError(resp.Error) {
			slog.Error("error rate detection failed", "error", resp.Error)
		}
		return nil
	}

	var anomalies []Anomaly
	for _, row := range resp.Results {
		serviceName, _ := row["service_name"].(string)
		currentRate, _ := row["current_rate"].(float64)
		baselineRate, _ := row["baseline_rate"].(float64)
		zScore, _ := row["z_score"].(float64)

		if math.Abs(zScore) >= d.config.ErrorRateThreshold {
			anomalies = append(anomalies, Anomaly{
				Type:        AnomalyErrorRateChange,
				ServiceName: serviceName,
				Metric:      "error_rate",
				Current:     currentRate * 100, // Convert to percentage
				Baseline:    baselineRate * 100,
				ZScore:      zScore,
				DetectedAt:  time.Now(),
				Description: fmt.Sprintf("%s error rate: %.1f%% (baseline: %.1f%%, z-score: %.2f)",
					serviceName, currentRate*100, baselineRate*100, zScore),
			})
		}
	}

	return anomalies
}

// detectLatencyAnomalies detects latency degradation
func (d *Detector) detectLatencyAnomalies(ctx context.Context, start, end time.Time) []Anomaly {
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	windowMins := int(d.config.LookbackWindow.Minutes())
	tenantID, namespace := d.duck.DefaultTenantID(), d.duck.DefaultNamespace()
	spansGlob := d.duck.SpansGlob(tenantID, namespace, windowMins*2)

	sql := fmt.Sprintf(`
		WITH current_period AS (
			SELECT
				"name=service_name" as service_name,
				PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY ("name=end_unix_nano" - "name=start_unix_nano") / 1000000.0) AS p95_latency
			FROM read_parquet(%s, union_by_name=true)
			WHERE "name=start_unix_nano" >= %d AND "name=start_unix_nano" < %d
			AND "name=kind" = 'SPAN_KIND_SERVER'
			GROUP BY "name=service_name"
		),
		baseline_buckets AS (
			SELECT
				"name=service_name" as service_name,
				time_bucket(INTERVAL '5 minutes', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) AS bucket,
				PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY ("name=end_unix_nano" - "name=start_unix_nano") / 1000000.0) AS p95_latency
			FROM read_parquet(%s, union_by_name=true)
			WHERE "name=start_unix_nano" >= %d AND "name=start_unix_nano" < %d
			AND "name=kind" = 'SPAN_KIND_SERVER'
			GROUP BY "name=service_name", bucket
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
				WHEN b.p95_stddev > 0 THEN (c.p95_latency - b.avg_p95) / b.p95_stddev
				ELSE 0.0
			END AS z_score
		FROM current_period c
		LEFT JOIN baseline_period b ON c.service_name = b.service_name
	`, spansGlob, startNano, endNano, spansGlob, startNano-endNano+startNano, startNano)

	resp := d.duck.ExecuteSQL(ctx, query.SQLRequest{Query: sql})
	if resp.Error != "" {
		if !isNoDataError(resp.Error) {
			slog.Error("latency detection failed", "error", resp.Error)
		}
		return nil
	}

	var anomalies []Anomaly
	for _, row := range resp.Results {
		serviceName, _ := row["service_name"].(string)
		currentP95, _ := row["current_p95"].(float64)
		baselineP95, _ := row["baseline_p95"].(float64)
		zScore, _ := row["z_score"].(float64)

		if zScore >= d.config.LatencyThreshold {
			anomalies = append(anomalies, Anomaly{
				Type:        AnomalyLatencyDegradation,
				ServiceName: serviceName,
				Metric:      "p95_latency",
				Current:     currentP95,
				Baseline:    baselineP95,
				ZScore:      zScore,
				DetectedAt:  time.Now(),
				Description: fmt.Sprintf("%s P95 latency: %.1fms (baseline P95: %.1fms, z-score: %.2f)",
					serviceName, currentP95, baselineP95, zScore),
			})
		}
	}

	return anomalies
}

// detectVolumeAnomalies detects unusual traffic volume changes
func (d *Detector) detectVolumeAnomalies(ctx context.Context, start, end time.Time) []Anomaly {
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	windowMins := int(d.config.LookbackWindow.Minutes())
	tenantID, namespace := d.duck.DefaultTenantID(), d.duck.DefaultNamespace()
	spansGlob := d.duck.SpansGlob(tenantID, namespace, windowMins*2)

	sql := fmt.Sprintf(`
		WITH current_period AS (
			SELECT
				"name=service_name" as service_name,
				COUNT(*) AS span_count
			FROM read_parquet(%s, union_by_name=true)
			WHERE "name=start_unix_nano" >= %d AND "name=start_unix_nano" < %d
			GROUP BY "name=service_name"
		),
		baseline_period AS (
			SELECT
				service_name,
				AVG(cnt) AS avg_count,
				STDDEV(cnt) AS count_stddev
			FROM (
				SELECT
					"name=service_name" as service_name,
					COUNT(*) AS cnt
				FROM read_parquet(%s, union_by_name=true)
				WHERE "name=start_unix_nano" >= %d AND "name=start_unix_nano" < %d
				GROUP BY "name=service_name", time_bucket(INTERVAL '5 minutes', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)))
			) subq
			GROUP BY service_name
		)
		SELECT
			c.service_name,
			c.span_count::FLOAT AS current_count,
			COALESCE(b.avg_count, 0.0) AS baseline_count,
			CASE
				WHEN b.count_stddev > 0 THEN (c.span_count - b.avg_count) / b.count_stddev
				ELSE 0.0
			END AS z_score
		FROM current_period c
		LEFT JOIN baseline_period b ON c.service_name = b.service_name
	`, spansGlob, startNano, endNano, spansGlob, startNano-endNano+startNano, startNano)

	resp := d.duck.ExecuteSQL(ctx, query.SQLRequest{Query: sql})
	if resp.Error != "" {
		if !isNoDataError(resp.Error) {
			slog.Error("volume detection failed", "error", resp.Error)
		}
		return nil
	}

	var anomalies []Anomaly
	for _, row := range resp.Results {
		serviceName, _ := row["service_name"].(string)
		currentCount, _ := row["current_count"].(float64)
		baselineCount, _ := row["baseline_count"].(float64)
		zScore, _ := row["z_score"].(float64)

		if math.Abs(zScore) >= d.config.VolumeThreshold {
			anomalies = append(anomalies, Anomaly{
				Type:        AnomalyVolumeChange,
				ServiceName: serviceName,
				Metric:      "span_count",
				Current:     currentCount,
				Baseline:    baselineCount,
				ZScore:      zScore,
				DetectedAt:  time.Now(),
				Description: fmt.Sprintf("%s volume: %.0f spans (baseline: %.0f, z-score: %.2f)",
					serviceName, currentCount, baselineCount, zScore),
			})
		}
	}

	return anomalies
}

// detectLogPatterns groups repetitive log messages
func (d *Detector) detectLogPatterns(ctx context.Context, start, end time.Time) []Pattern {
	startNano := start.UnixNano()
	endNano := end.UnixNano()
	windowMins := int(d.config.LookbackWindow.Minutes())
	tenantID, namespace := d.duck.DefaultTenantID(), d.duck.DefaultNamespace()
	logsGlob := d.duck.LogsGlob(tenantID, namespace, windowMins)

	// Group logs by normalized template (falls back to first 100 chars for old data without body_template)
	sql := fmt.Sprintf(`
		SELECT
			COALESCE("name=body_template", SUBSTRING("name=body", 1, 100)) AS template,
			"name=severity" as severity,
			"name=service_name" as service_name,
			COUNT(*) AS occurrence_count,
			MIN("name=time_unix_nano") AS first_seen_nano,
			MAX("name=time_unix_nano") AS last_seen_nano
		FROM read_parquet(%s, union_by_name=true)
		WHERE "name=time_unix_nano" >= %d AND "name=time_unix_nano" < %d
		AND "name=severity" IN ('WARN', 'ERROR', 'FATAL')
		GROUP BY COALESCE("name=body_template", SUBSTRING("name=body", 1, 100)), "name=severity", "name=service_name"
		HAVING COUNT(*) >= 3
		ORDER BY occurrence_count DESC
		LIMIT 50
	`, logsGlob, startNano, endNano)

	resp := d.duck.ExecuteSQL(ctx, query.SQLRequest{Query: sql})
	if resp.Error != "" {
		if !isNoDataError(resp.Error) {
			slog.Error("pattern detection failed", "error", resp.Error)
		}
		return nil
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

	return patterns
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
