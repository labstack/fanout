package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"

	"github.com/labstack/fanout/internal/query"
)

// Status returns system health overview.
func (s *Service) Status(ctx context.Context, window int, namespace, tenantID string) (*StatusResult, error) {
	if window == 0 {
		window = 15
	}

	// Always scope to single partition
	namespace, tenantID = s.defaults(namespace, tenantID)

	// Check cache
	cacheKey := fmt.Sprintf("status:%d:%s:%s", window, namespace, tenantID)
	if v, ok := query.GetCached(cacheKey); ok {
		if result, ok := v.(*StatusResult); ok {
			return result, nil
		}
	}

	q := fmt.Sprintf(`
SELECT
  service,
  SUM(spans)::BIGINT AS span_cnt,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) AS p95_ms,
  AVG(CASE WHEN spans > 0 THEN error_rate END) AS error_rate,
  SUM(COALESCE(log_count, 0))::BIGINT AS log_cnt,
  SUM(COALESCE(metric_count, 0))::BIGINT AS metric_cnt
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY service
ORDER BY (SUM(spans) + SUM(COALESCE(log_count, 0)) + SUM(COALESCE(metric_count, 0))) DESC
LIMIT 100;
`, window)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		slog.Warn("query failed", "method", "Status", "err", err)
		return &StatusResult{
			Healthy:   true,
			Summary:   "No telemetry data yet",
			Services:  ServiceSummary{},
			TopIssues: []TopIssue{},
		}, nil
	}
	defer rows.Close()

	var totalCount int64
	var totalSpans int64
	var totalP95, totalErrorRate float64
	var services []struct {
		name        string
		spans       int64
		p95         float64
		errorRate   float64
		logCount    int64
		metricCount int64
		status      string
	}

	for rows.Next() {
		var svc struct {
			name        string
			spans       int64
			p95         float64
			errorRate   float64
			logCount    int64
			metricCount int64
			status      string
		}
		var p95null, errNull sql.NullFloat64
		if err := rows.Scan(&svc.name, &svc.spans, &p95null, &errNull, &svc.logCount, &svc.metricCount); err != nil {
			slog.Warn("scan failed", "method", "Status", "err", err)
			continue
		}
		if p95null.Valid {
			svc.p95 = p95null.Float64
		}
		if errNull.Valid {
			svc.errorRate = errNull.Float64
		}
		svc.status = DeriveHealth(svc.errorRate, svc.p95, svc.spans)
		count := svc.spans + svc.logCount + svc.metricCount
		totalCount += count
		totalSpans += svc.spans
		totalP95 += svc.p95 * float64(svc.spans)
		totalErrorRate += svc.errorRate * float64(svc.spans)
		services = append(services, svc)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "method", "Status", "err", err)
	}

	out := &StatusResult{
		TopIssues: []TopIssue{},
	}

	if totalSpans > 0 {
		out.P95Ms = totalP95 / float64(totalSpans)
		out.ErrorRate = totalErrorRate / float64(totalSpans)
	}
	out.ThroughputPerMin = totalCount / int64(window)

	for _, svc := range services {
		out.Services.Total++
		switch svc.status {
		case "healthy", "active":
			out.Services.Healthy++
		case "degraded":
			out.Services.Degraded++
		case "unhealthy":
			out.Services.Unhealthy++
		}

		// Only add to TopIssues if there's a specific issue to report
		if len(out.TopIssues) < 5 {
			var issue TopIssue
			if svc.errorRate > 0.05 {
				issue = TopIssue{
					Service: svc.name,
					Issue:   "Errors",
					Value:   svc.errorRate,
					Detail:  fmt.Sprintf("%.1f%% errors", svc.errorRate*100),
				}
			} else if svc.p95 > 1000 {
				issue = TopIssue{
					Service: svc.name,
					Issue:   "Latency",
					Value:   svc.p95,
					Detail:  fmt.Sprintf("p95 %.0fms", svc.p95),
				}
			}
			if issue.Issue != "" {
				out.TopIssues = append(out.TopIssues, issue)
			}
		}
	}

	out.Healthy = out.Services.Unhealthy == 0 && out.Services.Degraded == 0

	if out.Healthy {
		out.Summary = fmt.Sprintf("%d services healthy, %.0f signals/min", out.Services.Total, float64(out.ThroughputPerMin))
	} else {
		out.Summary = fmt.Sprintf("%d degraded, %d unhealthy of %d services",
			out.Services.Degraded, out.Services.Unhealthy, out.Services.Total)
	}

	query.SetCached(cacheKey, out)
	return out, nil
}

// DeriveHealth determines service health from error rate and p95 latency.
// If spans is provided and is 0 (with zero error rate and latency), returns "active"
// for services discovered only via logs/metrics.
func DeriveHealth(errorRate, p95 float64, spans ...int64) string {
	spanCount := int64(0)
	if len(spans) > 0 {
		spanCount = spans[0]
	}
	if spanCount == 0 && errorRate == 0 && p95 == 0 {
		return "active"
	}
	if errorRate > 0.1 || p95 > 5000 {
		return "unhealthy"
	}
	if errorRate > 0.01 || p95 > 1000 {
		return "degraded"
	}
	return "healthy"
}

// HealthScore computes a composite health score for a service (0.0–1.0).
// Components: error rate (40%), p95 latency (30%), throughput presence (30%).
func HealthScore(errorRate, p95 float64, spans int64) float64 {
	var errScore float64
	switch {
	case errorRate < 0.01:
		errScore = 1.0
	case errorRate < 0.05:
		errScore = 0.7
	case errorRate < 0.10:
		errScore = 0.3
	default:
		errScore = 0.0
	}

	var latScore float64
	switch {
	case p95 < 500:
		latScore = 1.0
	case p95 < 2000:
		latScore = 0.7
	case p95 < 5000:
		latScore = 0.3
	default:
		latScore = 0.0
	}

	var tputScore float64
	if spans > 0 {
		tputScore = 1.0
	}

	return errScore*0.4 + latScore*0.3 + tputScore*0.3
}

// DeriveHealthFromScore maps a health score to a status string.
func DeriveHealthFromScore(score float64) string {
	if score >= 0.9 {
		return "healthy"
	}
	if score >= 0.7 {
		return "degraded"
	}
	return "unhealthy"
}

// overviewRow is an internal type for scored service data during overview computation.
type overviewRow struct {
	name      string
	spans     int64
	p50       float64
	p95       float64
	errorRate float64
	score     float64
	status    string
}

// Overview returns a structured health overview using the new OverviewResult type.
func (s *Service) Overview(ctx context.Context, window int, include []string, sortBy, namespace, tenantID string, limit int) (*OverviewResult, error) {
	if window == 0 {
		window = 15
	}
	if limit <= 0 {
		limit = 100
	}

	namespace, tenantID = s.defaults(namespace, tenantID)

	cacheKey := fmt.Sprintf("overview:%d:%s:%s:%s", window, sortBy, namespace, tenantID)
	if v, ok := query.GetCached(cacheKey); ok {
		if result, ok := v.(*OverviewResult); ok {
			return result, nil
		}
	}

	q := fmt.Sprintf(`
SELECT
  service,
  SUM(spans)::BIGINT AS span_cnt,
  AVG(CASE WHEN spans > 0 THEN p50_ms END) AS p50_ms,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) AS p95_ms,
  AVG(CASE WHEN spans > 0 THEN error_rate END) AS error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY service
ORDER BY SUM(spans) DESC
LIMIT %d;
`, window, limit)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		slog.Warn("query failed", "method", "Overview", "err", err)
		return &OverviewResult{
			Health: OverviewHealth{
				ByStatus: map[string]int{"healthy": 0, "degraded": 0, "unhealthy": 0},
			},
			Services: []OverviewService{},
			Issues:   []OverviewIssue{},
		}, nil
	}
	defer rows.Close()

	var svcs []overviewRow
	for rows.Next() {
		var r overviewRow
		var p50null, p95null, errNull sql.NullFloat64
		if err := rows.Scan(&r.name, &r.spans, &p50null, &p95null, &errNull); err != nil {
			slog.Warn("scan failed", "method", "Overview", "err", err)
			continue
		}
		if p50null.Valid {
			r.p50 = p50null.Float64
		}
		if p95null.Valid {
			r.p95 = p95null.Float64
		}
		if errNull.Valid {
			r.errorRate = errNull.Float64
		}
		r.score = HealthScore(r.errorRate, r.p95, r.spans)
		r.status = DeriveHealthFromScore(r.score)
		svcs = append(svcs, r)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "method", "Overview", "err", err)
	}

	// Compute global aggregates
	var totalScore float64
	var totalSpans int64
	var totalWeightedP95, totalWeightedErr float64
	byStatus := map[string]int{"healthy": 0, "degraded": 0, "unhealthy": 0}

	for _, r := range svcs {
		totalScore += r.score
		totalSpans += r.spans
		totalWeightedP95 += r.p95 * float64(r.spans)
		totalWeightedErr += r.errorRate * float64(r.spans)
		byStatus[r.status]++
	}

	globalScore := 0.0
	if len(svcs) > 0 {
		globalScore = totalScore / float64(len(svcs))
	}
	globalP95 := 0.0
	globalErrRate := 0.0
	if totalSpans > 0 {
		globalP95 = totalWeightedP95 / float64(totalSpans)
		globalErrRate = totalWeightedErr / float64(totalSpans)
	}

	// Sort services
	sortOverviewRows(svcs, sortBy)

	out := &OverviewResult{
		Services: []OverviewService{},
		Issues:   []OverviewIssue{},
	}

	// Determine which sections to include
	includeAll := len(include) == 0
	wantHealth := includeAll || containsStr(include, "health")
	wantServices := includeAll || containsStr(include, "services")
	wantIssues := includeAll || containsStr(include, "issues")

	if wantHealth {
		out.Health = OverviewHealth{
			Score:            globalScore,
			TotalServices:    len(svcs),
			ByStatus:         byStatus,
			ThroughputPerMin: float64(totalSpans) / float64(window),
			GlobalErrorRate:  globalErrRate,
			GlobalP95Ms:      globalP95,
		}
	}

	if wantServices {
		for _, r := range svcs {
			out.Services = append(out.Services, OverviewService{
				Service:   r.name,
				Status:    r.status,
				Requests:  r.spans,
				ErrorRate: r.errorRate,
				P50Ms:     r.p50,
				P95Ms:     r.p95,
			})
		}
	}

	if wantIssues {
		for _, r := range svcs {
			if len(out.Issues) >= 10 {
				break
			}
			if r.errorRate > 0.05 {
				out.Issues = append(out.Issues, OverviewIssue{
					Service:   r.name,
					Issue:     "high_error_rate",
					Value:     r.errorRate,
					Threshold: 0.05,
				})
			} else if r.p95 > 500 {
				out.Issues = append(out.Issues, OverviewIssue{
					Service:   r.name,
					Issue:     "p95_latency",
					Value:     r.p95,
					Threshold: 500,
				})
			}
		}
	}

	query.SetCached(cacheKey, out)
	return out, nil
}

// sortOverviewRows sorts service rows by the given field (descending, worst first).
func sortOverviewRows(svcs []overviewRow, by string) {
	sort.SliceStable(svcs, func(i, j int) bool {
		a, b := svcs[i], svcs[j]
		switch by {
		case "error_rate":
			return a.errorRate > b.errorRate
		case "latency":
			return a.p95 > b.p95
		case "throughput":
			return a.spans > b.spans
		default: // "severity" or ""
			return a.score < b.score // lowest score (worst) first
		}
	})
}

// containsStr checks if a string slice contains a value.
func containsStr(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
