package service

import (
	"context"
	"fmt"
	"math"
)

// Timeline returns time-bucketed metrics with anomaly detection.
func (s *Service) Timeline(ctx context.Context, svc string, window, granularity int) (*TimelineResult, error) {
	if window == 0 {
		window = 60
	}
	if granularity == 0 {
		granularity = 5
	}

	out := &TimelineResult{
		Buckets:   []TimeBucket{},
		Anomalies: []Anomaly{},
	}

	svcFilter := ""
	if svc != "" {
		svcFilter = fmt.Sprintf(`AND "name=service_name" = '%s'`, escapeLike(svc))
	}

	q := fmt.Sprintf(`
SELECT
  time_bucket(INTERVAL '%d minutes', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) as bucket,
  COUNT(*) as cnt,
  SUM(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1 ELSE 0 END) as errors,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95
FROM read_parquet('%s/spans/year=*/month=*/day=*/hour=*/part-*.parquet')
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
GROUP BY bucket
ORDER BY bucket ASC;
`, granularity, s.cfg.LakeDir, window, svcFilter)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return out, nil
	}
	defer rows.Close()

	var buckets []TimeBucket
	var totalP95, totalErrRate float64

	for rows.Next() {
		var b TimeBucket
		var bucket any
		if err := rows.Scan(&bucket, &b.Requests, &b.Errors, &b.P95Ms); err != nil {
			continue
		}
		b.Time = fmt.Sprintf("%v", bucket)
		if b.Requests > 0 {
			b.ErrorRate = float64(b.Errors) / float64(b.Requests)
		}
		totalP95 += b.P95Ms
		totalErrRate += b.ErrorRate
		buckets = append(buckets, b)
	}

	if len(buckets) == 0 {
		return out, nil
	}

	// Calculate averages
	n := float64(len(buckets))
	avgP95 := totalP95 / n
	avgErrRate := totalErrRate / n

	// Calculate standard deviations for anomaly detection
	var p95Var, errVar float64
	for _, b := range buckets {
		p95Var += math.Pow(b.P95Ms-avgP95, 2)
		errVar += math.Pow(b.ErrorRate-avgErrRate, 2)
	}
	p95Std := math.Sqrt(p95Var / n)
	errStd := math.Sqrt(errVar / n)

	// Mark anomalies (> 2 std deviations)
	for i := range buckets {
		b := &buckets[i]

		// P95 anomaly
		if p95Std > 0 && math.Abs(b.P95Ms-avgP95) > 2*p95Std {
			b.IsAnomaly = true
			b.AnomalyType = "latency"
			out.Anomalies = append(out.Anomalies, Anomaly{
				Time:        b.Time,
				Type:        "latency_spike",
				Description: fmt.Sprintf("P95 latency %.0fms vs avg %.0fms", b.P95Ms, avgP95),
				Service:     svc,
				Value:       b.P95Ms,
				Expected:    avgP95,
			})
		}

		// Error rate anomaly
		if errStd > 0 && b.ErrorRate > avgErrRate+2*errStd && b.ErrorRate > 0.01 {
			b.IsAnomaly = true
			if b.AnomalyType == "" {
				b.AnomalyType = "errors"
			} else {
				b.AnomalyType = "latency+errors"
			}
			out.Anomalies = append(out.Anomalies, Anomaly{
				Time:        b.Time,
				Type:        "error_spike",
				Description: fmt.Sprintf("Error rate %.1f%% vs avg %.1f%%", b.ErrorRate*100, avgErrRate*100),
				Service:     svc,
				Value:       b.ErrorRate,
				Expected:    avgErrRate,
			})
		}

		// Traffic drop anomaly
		if i > 0 && buckets[i-1].Requests > 0 {
			prev := float64(buckets[i-1].Requests)
			curr := float64(b.Requests)
			if curr < prev*0.3 && prev > 10 {
				b.IsAnomaly = true
				if b.AnomalyType == "" {
					b.AnomalyType = "traffic_drop"
				}
				out.Anomalies = append(out.Anomalies, Anomaly{
					Time:        b.Time,
					Type:        "traffic_drop",
					Description: fmt.Sprintf("Traffic dropped from %d to %d requests", int64(prev), b.Requests),
					Service:     svc,
					Value:       curr,
					Expected:    prev,
				})
			}
		}
	}

	out.Buckets = buckets
	return out, nil
}

// escapeLike escapes SQL LIKE special characters.
func escapeLike(s string) string {
	// Simple escape for SQL injection prevention
	result := ""
	for _, c := range s {
		switch c {
		case '\'':
			result += "''"
		case '%':
			result += "\\%"
		case '_':
			result += "\\_"
		default:
			result += string(c)
		}
	}
	return result
}
