package service

import (
	"context"
	"fmt"
	"log/slog"
)

// Status returns system health overview.
func (s *Service) Status(ctx context.Context, window int, namespace, tenantID string) (*StatusResult, error) {
	if window == 0 {
		window = 15
	}

	// Always scope to single partition
	namespace, tenantID = s.defaults(namespace, tenantID)

	q := fmt.Sprintf(`
SELECT
  "name=service_name" as service,
  COUNT(*) as cnt,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95_ms,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=service_name"
ORDER BY cnt DESC
LIMIT 100;
`, s.duck.SpansGlob(tenantID, namespace, window), window)

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
	var totalP95, totalErrorRate float64
	var services []struct {
		name      string
		count     int64
		p95       float64
		errorRate float64
		status    string
	}

	for rows.Next() {
		var svc struct {
			name      string
			count     int64
			p95       float64
			errorRate float64
			status    string
		}
		if err := rows.Scan(&svc.name, &svc.count, &svc.p95, &svc.errorRate); err != nil {
			slog.Warn("scan failed", "method", "Status", "err", err)
			continue
		}
		svc.status = DeriveHealth(svc.errorRate, svc.p95)
		totalCount += svc.count
		totalP95 += svc.p95 * float64(svc.count)
		totalErrorRate += svc.errorRate * float64(svc.count)
		services = append(services, svc)
	}

	out := &StatusResult{
		TopIssues: []TopIssue{},
	}

	if len(services) > 0 {
		out.P95Ms = totalP95 / float64(totalCount)
		out.ErrorRate = totalErrorRate / float64(totalCount)
	}
	out.ThroughputPerMin = totalCount / int64(window)

	for _, svc := range services {
		out.Services.Total++
		switch svc.status {
		case "healthy":
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
		out.Summary = fmt.Sprintf("%d services healthy, %.0f req/min", out.Services.Total, float64(out.ThroughputPerMin))
	} else {
		out.Summary = fmt.Sprintf("%d degraded, %d unhealthy of %d services",
			out.Services.Degraded, out.Services.Unhealthy, out.Services.Total)
	}

	return out, nil
}

// DeriveHealth determines service health from error rate and p95 latency.
func DeriveHealth(errorRate, p95 float64) string {
	if errorRate > 0.1 || p95 > 5000 {
		return "unhealthy"
	}
	if errorRate > 0.01 || p95 > 1000 {
		return "degraded"
	}
	return "healthy"
}
