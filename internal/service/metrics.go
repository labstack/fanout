package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
)

// validMetricAggregations is the allowlist for aggregation values.
var validMetricAggregations = map[string]bool{
	"avg":   true,
	"sum":   true,
	"min":   true,
	"max":   true,
	"count": true,
}

// aggSQL maps aggregation names to SQL expressions over the value column.
var aggSQL = map[string]string{
	"avg":   "AVG(value)",
	"sum":   "SUM(value)",
	"min":   "MIN(value)",
	"max":   "MAX(value)",
	"count": "COUNT(value)",
}

// autoGranularity picks a sensible bucket size for a given window in minutes.
func autoGranularity(windowMinutes int) string {
	switch {
	case windowMinutes <= 60:
		return "1m"
	case windowMinutes <= 360:
		return "5m"
	case windowMinutes <= 1440:
		return "15m"
	default:
		return "1h"
	}
}

// granularityMinutes converts a granularity string to an integer number of minutes.
func granularityMinutes(g string) int {
	switch g {
	case "1m":
		return 1
	case "5m":
		return 5
	case "15m":
		return 15
	case "1h":
		return 60
	default:
		return 5
	}
}

// MetricsList returns distinct metric names and metadata seen in the window.
func (s *Service) MetricsList(ctx context.Context, p MetricListParams) (*MetricsListResult, error) {
	if p.Window == 0 {
		p.Window = 15
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)
	out := &MetricsListResult{Metrics: []MetricListEntry{}}

	var clauses []string
	var args []any

	clauses = append(clauses, fmt.Sprintf("time >= now() - INTERVAL '%d' MINUTE", p.Window))

	if p.Service != "" {
		clauses = append(clauses, "service = ?")
		args = append(args, p.Service)
	}
	if p.Namespace != "" {
		clauses = append(clauses, "namespace = ?")
		args = append(args, p.Namespace)
	}
	if p.TenantID != "" {
		clauses = append(clauses, "tenant = ?")
		args = append(args, p.TenantID)
	}
	for k, v := range p.Attrs {
		clauses = append(clauses, "json_extract_string(attributes_json, ?) = ?")
		args = append(args, "$."+k, v)
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	q := fmt.Sprintf(`
SELECT
  name,
  first(type) AS type,
  first(unit) AS unit,
  first(description) AS description,
  list(DISTINCT service) AS services
FROM metrics
%s
GROUP BY name
ORDER BY name
LIMIT %d`, where, p.Limit)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("metrics list query failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var entry MetricListEntry
		var servicesJSON []byte
		if err := rows.Scan(&entry.Name, &entry.Type, &entry.Unit, &entry.Description, &servicesJSON); err != nil {
			slog.Warn("scan failed", "method", "MetricsList", "err", err)
			continue
		}
		if len(servicesJSON) > 0 {
			if err := json.Unmarshal(servicesJSON, &entry.Services); err != nil {
				slog.Debug("MetricsList: failed to parse services JSON", "metric", entry.Name, "err", err)
			}
		}
		if entry.Services == nil {
			entry.Services = []string{}
		}
		out.Metrics = append(out.Metrics, entry)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "method", "MetricsList", "err", err)
	}

	return out, nil
}

// MetricsQuery returns time-bucketed metric series with anomaly detection.
func (s *Service) MetricsQuery(ctx context.Context, p MetricQueryParams) (*MetricsQueryResult, error) {
	if p.Window == 0 {
		p.Window = 15
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	if p.Aggregation == "" {
		p.Aggregation = "avg"
	}
	if p.Granularity == "" {
		p.Granularity = "auto"
	}
	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)
	out := &MetricsQueryResult{
		Series:    []MetricSeries{},
		Anomalies: []MetricAnomaly{},
	}

	// Validate aggregation
	if !validMetricAggregations[p.Aggregation] {
		return nil, fmt.Errorf("invalid aggregation %q: use avg, sum, min, max, or count", p.Aggregation)
	}

	// Collect names
	names := p.Names
	if p.Name != "" {
		names = append([]string{p.Name}, names...)
	}
	// Deduplicate (new slice to avoid mutating caller's p.Names)
	seen := map[string]bool{}
	dedupNames := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			dedupNames = append(dedupNames, n)
		}
	}
	names = dedupNames

	if len(names) == 0 {
		return nil, fmt.Errorf("query action requires at least one of 'name' or 'names'")
	}

	// Resolve granularity
	gran := p.Granularity
	if gran == "auto" {
		gran = autoGranularity(p.Window)
	}
	granMins := granularityMinutes(gran)

	// Determine group-by columns
	groupByService := false
	for _, g := range p.GroupBy {
		if g == "service" {
			groupByService = true
		}
	}

	agg := aggSQL[p.Aggregation]

	// Query each requested metric name, collect all datapoints keyed by series labels
	type seriesKey struct {
		metric  string
		service string
	}
	type seriesData struct {
		unit       string
		datapoints []MetricDatapoint
	}
	seriesMap := map[seriesKey]*seriesData{}
	var seriesOrder []seriesKey

	for _, metricName := range names {
		var clauses []string
		var args []any

		clauses = append(clauses, fmt.Sprintf("time >= now() - INTERVAL '%d' MINUTE", p.Window))
		clauses = append(clauses, "name = ?")
		args = append(args, metricName)

		if p.Service != "" {
			clauses = append(clauses, "service = ?")
			args = append(args, p.Service)
		}
		if p.Namespace != "" {
			clauses = append(clauses, "namespace = ?")
			args = append(args, p.Namespace)
		}
		if p.TenantID != "" {
			clauses = append(clauses, "tenant = ?")
			args = append(args, p.TenantID)
		}
		for k, v := range p.Attrs {
			clauses = append(clauses, "json_extract_string(attributes_json, ?) = ?")
			args = append(args, "$."+k, v)
		}

		where := ""
		if len(clauses) > 0 {
			where = "WHERE " + strings.Join(clauses, " AND ")
		}

		var selectCols, groupCols string
		if groupByService {
			selectCols = fmt.Sprintf(`time_bucket(INTERVAL '%d minutes', time) AS bucket,
  service,
  %s AS value,
  first(unit) AS unit`, granMins, agg)
			groupCols = "bucket, service"
		} else {
			selectCols = fmt.Sprintf(`time_bucket(INTERVAL '%d minutes', time) AS bucket,
  '' AS service,
  %s AS value,
  first(unit) AS unit`, granMins, agg)
			groupCols = "bucket"
		}

		q := fmt.Sprintf(`
SELECT %s
FROM metrics
%s
GROUP BY %s
ORDER BY bucket ASC
LIMIT %d`, selectCols, where, groupCols, p.Limit)

		rows, err := s.duck.DB.QueryContext(ctx, q, args...)
		if err != nil {
			slog.Warn("query failed", "method", "MetricsQuery", "metric", metricName, "err", err)
			continue
		}

		for rows.Next() {
			var bucket any
			var svcName string
			var value float64
			var unit string
			if err := rows.Scan(&bucket, &svcName, &value, &unit); err != nil {
				slog.Warn("scan failed", "method", "MetricsQuery", "err", err)
				continue
			}
			key := seriesKey{metric: metricName, service: svcName}
			dp := MetricDatapoint{
				Time:  fmt.Sprintf("%v", bucket),
				Value: value,
			}
			if sd, ok := seriesMap[key]; ok {
				sd.datapoints = append(sd.datapoints, dp)
			} else {
				seriesMap[key] = &seriesData{unit: unit, datapoints: []MetricDatapoint{dp}}
				seriesOrder = append(seriesOrder, key)
			}
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "method", "MetricsQuery", "metric", metricName, "err", err)
		}
		rows.Close()
	}

	// Build series output and detect anomalies
	for _, key := range seriesOrder {
		sd := seriesMap[key]

		labels := map[string]string{}
		if key.service != "" {
			labels["service"] = key.service
		}

		series := MetricSeries{
			Labels:      labels,
			Metric:      key.metric,
			Aggregation: p.Aggregation,
			Unit:        sd.unit,
			Datapoints:  sd.datapoints,
		}
		out.Series = append(out.Series, series)

		// Anomaly detection: flag points > 2σ from mean
		anomalies := detectMetricAnomalies(key.metric, sd.datapoints)
		out.Anomalies = append(out.Anomalies, anomalies...)
	}

	return out, nil
}

// detectMetricAnomalies runs statistical anomaly detection on a slice of datapoints.
func detectMetricAnomalies(metricName string, dps []MetricDatapoint) []MetricAnomaly {
	if len(dps) < 3 {
		return nil
	}

	// Compute mean
	var sum float64
	for _, dp := range dps {
		sum += dp.Value
	}
	mean := sum / float64(len(dps))

	// Compute stddev
	var variance float64
	for _, dp := range dps {
		diff := dp.Value - mean
		variance += diff * diff
	}
	stddev := math.Sqrt(variance / float64(len(dps)))

	if stddev == 0 {
		return nil
	}

	var anomalies []MetricAnomaly
	for _, dp := range dps {
		deviation := math.Abs(dp.Value-mean) / stddev
		if deviation > 2.0 {
			anomalyType := "spike"
			if dp.Value < mean {
				anomalyType = "drop"
			}
			anomalies = append(anomalies, MetricAnomaly{
				Time:           dp.Time,
				Type:           anomalyType,
				Value:          dp.Value,
				Expected:       mean,
				DeviationSigma: math.Round(deviation*100) / 100,
			})
		}
	}
	return anomalies
}
