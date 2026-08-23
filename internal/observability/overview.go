package observability

import (
	"context"
	"fmt"
)

const overviewQuery = `
SELECT
  service,
  CAST(SUM(spans) AS BIGINT) AS spans,
  COALESCE(SUM(error_rate * spans) / NULLIF(SUM(spans), 0), 0) AS error_rate,
  COALESCE(SUM(p50_ms * spans) / NULLIF(SUM(spans), 0), 0) AS p50_ms,
  COALESCE(MAX(p95_ms), 0) AS p95_ms,
  CAST(SUM(log_count) AS BIGINT) AS log_count,
  CAST(SUM(metric_count) AS BIGINT) AS metric_count
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?)
GROUP BY service
ORDER BY error_rate DESC, p95_ms DESC, spans DESC, service ASC
LIMIT ?`

func (s *Service) Overview(ctx context.Context, scope Scope, limit int) (Result[Overview], error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[Overview]{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Result[Overview]{}, err
	}

	services, err := s.serviceHealth(ctx, scope, limit)
	if err != nil {
		return Result[Overview]{}, err
	}

	data := Overview{Services: services, ServiceCount: len(services)}
	var weightedErrors float64
	for _, service := range services {
		data.TotalSpans += service.Spans
		weightedErrors += service.ErrorRate * float64(service.Spans)
		switch service.Health {
		case HealthUnhealthy:
			data.Counts.Unhealthy++
		case HealthDegraded:
			data.Counts.Degraded++
		default:
			data.Counts.Healthy++
		}
	}
	if data.TotalSpans > 0 {
		data.ErrorRate = weightedErrors / float64(data.TotalSpans)
	}
	data.Health = overallHealth(data.Counts)

	return Result[Overview]{
		Schema:     OverviewSchema,
		Summary:    fmt.Sprintf("%d services: %d unhealthy, %d degraded, %d healthy", data.ServiceCount, data.Counts.Unhealthy, data.Counts.Degraded, data.Counts.Healthy),
		Data:       data,
		Provenance: s.provenance(scope),
	}, nil
}

func (s *Service) serviceHealth(ctx context.Context, scope Scope, limit int) ([]ServiceHealth, error) {
	rows, err := s.db.QueryContext(ctx, overviewQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, limit)
	if err != nil {
		return nil, fmt.Errorf("query service health: %w", err)
	}
	defer rows.Close()

	services := make([]ServiceHealth, 0)
	for rows.Next() {
		var service ServiceHealth
		if err := rows.Scan(
			&service.Service,
			&service.Spans,
			&service.ErrorRate,
			&service.P50MS,
			&service.P95MS,
			&service.LogCount,
			&service.MetricCount,
		); err != nil {
			return nil, fmt.Errorf("scan service health: %w", err)
		}
		service.Health = classify(service.ErrorRate, service.P95MS)
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service health: %w", err)
	}
	return services, nil
}
