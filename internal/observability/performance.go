package observability

import (
	"context"
	"fmt"
	"math"
	"strings"
)

const performancePointsQuery = `
SELECT
  time_bucket(INTERVAL '5 minutes', bucket) AS point_time,
  CAST(SUM(spans) AS BIGINT),
  COALESCE(SUM(error_rate * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(SUM(p50_ms * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(MAX(p95_ms), 0),
  CAST(SUM(log_count) AS BIGINT),
  CAST(SUM(metric_count) AS BIGINT)
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND namespace = ? AND (? = '' OR service = ?)
GROUP BY point_time
ORDER BY point_time ASC`

const endpointsQuery = `
SELECT
  COALESCE(NULLIF(http_method, ''), 'CALL') AS method,
  COALESCE(NULLIF(http_route, ''), NULLIF(operation, ''), 'unknown') AS path,
  CAST(COUNT(*) AS BIGINT) AS calls,
  COALESCE(approx_quantile(duration_ms, 0.50), 0) AS p50_ms,
  COALESCE(approx_quantile(duration_ms, 0.95), 0) AS p95_ms,
  COALESCE(approx_quantile(duration_ms, 0.99), 0) AS p99_ms,
  COALESCE(AVG(CASE WHEN upper(status) IN ('ERROR', 'STATUS_CODE_ERROR') THEN 1.0 ELSE 0.0 END), 0) AS error_rate
FROM spans
WHERE start_time >= ? AND start_time < ? AND namespace = ? AND (? = '' OR service = ?)
GROUP BY method, path
ORDER BY calls DESC, p95_ms DESC
LIMIT ?`

const performanceHeatmapQuery = `
SELECT time_bucket(INTERVAL '5 minutes', bucket) AS point_time, service, COALESCE(MAX(p95_ms), 0)
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND namespace = ?
  AND service IN (
    SELECT service FROM service_rollup
    WHERE bucket >= ? AND bucket < ? AND namespace = ?
    GROUP BY service ORDER BY SUM(spans) DESC LIMIT 12
  )
GROUP BY point_time, service
ORDER BY point_time ASC, service ASC`

const performanceAggregateQuery = `
SELECT
  CAST(COALESCE(SUM(spans), 0) AS DOUBLE),
  COALESCE(SUM(error_rate * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(SUM(p50_ms * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(MAX(p95_ms), 0)
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND namespace = ? AND (? = '' OR service = ?)`

func (s *Service) Performance(ctx context.Context, scope Scope, service string, limit int) (Result[Performance], error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[Performance]{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Result[Performance]{}, err
	}
	service = strings.TrimSpace(service)

	data := Performance{Service: service, Points: []PerformancePoint{}, Endpoints: []Endpoint{}, Heatmap: []HeatmapPoint{}, Comparison: []ComparisonMetric{}}
	rows, err := s.db.QueryContext(ctx, performancePointsQuery, scope.Start, scope.End, scope.Namespace, service, service)
	if err != nil {
		return Result[Performance]{}, fmt.Errorf("query performance points: %w", err)
	}
	for rows.Next() {
		var point PerformancePoint
		if err := rows.Scan(&point.Time, &point.Spans, &point.ErrorRate, &point.P50MS, &point.P95MS, &point.LogCount, &point.MetricCount); err != nil {
			rows.Close()
			return Result[Performance]{}, fmt.Errorf("scan performance point: %w", err)
		}
		data.Points = append(data.Points, point)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Result[Performance]{}, fmt.Errorf("iterate performance points: %w", err)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, endpointsQuery, scope.Start, scope.End, scope.Namespace, service, service, limit)
	if err != nil {
		return Result[Performance]{}, fmt.Errorf("query endpoints: %w", err)
	}
	for rows.Next() {
		var endpoint Endpoint
		if err := rows.Scan(&endpoint.Method, &endpoint.Path, &endpoint.Calls, &endpoint.P50MS, &endpoint.P95MS, &endpoint.P99MS, &endpoint.ErrorRate); err != nil {
			rows.Close()
			return Result[Performance]{}, fmt.Errorf("scan endpoint: %w", err)
		}
		endpoint.Health = classify(endpoint.ErrorRate, endpoint.P95MS)
		data.Endpoints = append(data.Endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Result[Performance]{}, fmt.Errorf("iterate endpoints: %w", err)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, performanceHeatmapQuery, scope.Start, scope.End, scope.Namespace, scope.Start, scope.End, scope.Namespace)
	if err != nil {
		return Result[Performance]{}, fmt.Errorf("query latency heatmap: %w", err)
	}
	for rows.Next() {
		var point HeatmapPoint
		if err := rows.Scan(&point.Time, &point.Service, &point.P95MS); err != nil {
			rows.Close()
			return Result[Performance]{}, fmt.Errorf("scan latency heatmap: %w", err)
		}
		data.Heatmap = append(data.Heatmap, point)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Result[Performance]{}, fmt.Errorf("iterate latency heatmap: %w", err)
	}
	rows.Close()

	midpoint := scope.Start.Add(scope.End.Sub(scope.Start) / 2)
	before, err := s.performanceAggregate(ctx, Scope{Namespace: scope.Namespace, Start: scope.Start, End: midpoint}, service)
	if err != nil {
		return Result[Performance]{}, err
	}
	after, err := s.performanceAggregate(ctx, Scope{Namespace: scope.Namespace, Start: midpoint, End: scope.End}, service)
	if err != nil {
		return Result[Performance]{}, err
	}
	data.Comparison = []ComparisonMetric{
		comparisonMetric("Throughput", "spans", before.Spans, after.Spans, false),
		comparisonMetric("Error rate", "%", before.ErrorRate*100, after.ErrorRate*100, true),
		comparisonMetric("P50 latency", "ms", before.P50MS, after.P50MS, true),
		comparisonMetric("P95 latency", "ms", before.P95MS, after.P95MS, true),
	}

	target := "all services"
	if service != "" {
		target = service
	}
	return Result[Performance]{
		Schema:     PerformanceSchema,
		Summary:    fmt.Sprintf("%d activity points and %d endpoints for %s", len(data.Points), len(data.Endpoints), target),
		Data:       data,
		Provenance: s.provenanceFor(scope, "service_rollup + spans"),
	}, nil
}

type performanceAggregate struct {
	Spans     float64
	ErrorRate float64
	P50MS     float64
	P95MS     float64
}

func (s *Service) performanceAggregate(ctx context.Context, scope Scope, service string) (performanceAggregate, error) {
	rows, err := s.db.QueryContext(ctx, performanceAggregateQuery, scope.Start, scope.End, scope.Namespace, service, service)
	if err != nil {
		return performanceAggregate{}, fmt.Errorf("query performance comparison: %w", err)
	}
	defer rows.Close()
	var value performanceAggregate
	if rows.Next() {
		if err := rows.Scan(&value.Spans, &value.ErrorRate, &value.P50MS, &value.P95MS); err != nil {
			return performanceAggregate{}, fmt.Errorf("scan performance comparison: %w", err)
		}
	}
	return value, rows.Err()
}

func comparisonMetric(label, unit string, before, after float64, lowerIsBetter bool) ComparisonMetric {
	change := 0.0
	if before != 0 {
		change = (after - before) / math.Abs(before) * 100
	} else if after != 0 {
		change = 100
	}
	direction := DirectionStable
	if math.Abs(change) >= 1 {
		improved := change > 0
		if lowerIsBetter {
			improved = change < 0
		}
		if improved {
			direction = DirectionImprovement
		} else {
			direction = DirectionRegression
		}
	}
	return ComparisonMetric{Label: label, Unit: unit, Before: before, After: after, ChangePct: change, Direction: direction, Significant: math.Abs(change) >= 10}
}
