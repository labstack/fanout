package observability

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/query"
)

const performancePointsQueryTemplate = `
SELECT
  time_bucket(INTERVAL '%s', bucket) AS point_time,
  CAST(SUM(spans) AS BIGINT),
  COALESCE(SUM(error_rate * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(SUM(p50_ms * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(MAX(p95_ms), 0),
  CAST(SUM(log_count) AS BIGINT),
  CAST(SUM(metric_count) AS BIGINT)
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?) AND (? = '' OR service = ?)
GROUP BY point_time
ORDER BY point_time ASC`

const rawEndpointsQuery = `
SELECT
  COALESCE(NULLIF(http_method, ''), 'CALL') AS method,
  COALESCE(NULLIF(http_route, ''), NULLIF(operation, ''), 'unknown') AS path,
  CAST(COUNT(*) AS BIGINT) AS calls,
  COALESCE(approx_quantile(duration_ms, 0.50), 0) AS p50_ms,
  COALESCE(approx_quantile(duration_ms, 0.95), 0) AS p95_ms,
  COALESCE(approx_quantile(duration_ms, 0.99), 0) AS p99_ms,
  COALESCE(AVG(CASE WHEN upper(status) IN ('ERROR', 'STATUS_CODE_ERROR') THEN 1.0 ELSE 0.0 END), 0) AS error_rate
FROM spans
WHERE start_time >= ? AND start_time < ? AND (? = '' OR namespace = ?) AND (? = '' OR service = ?)
GROUP BY method, path
ORDER BY calls DESC, p95_ms DESC
LIMIT ?`

const endpointRollupStatusQuery = `
SELECT
  COALESCE(MAX(CASE WHEN cache_key = '` + query.EndpointReadyStateKey + `' THEN last_ingested_unix_nano END), 0) != 0
    AND COALESCE(MAX(CASE WHEN cache_key = '` + query.EndpointDisabledStateKey + `' THEN last_ingested_unix_nano END), 0) = 0 AS ready,
  COALESCE(MAX(CASE WHEN cache_key = '` + query.EndpointRollupStateKey + `' THEN last_ingested_unix_nano END), 0)::BIGINT AS watermark
FROM rollup_state`

const endpointRollupMatureQuery = `
SELECT COUNT(*) >= 5
FROM (SELECT DISTINCT bucket FROM endpoint_rollup LIMIT 5)`

// endpointRollupQuery uses the minute cache only through its current watermark.
// Raw spans cover both partial boundary minutes and any newer complete minutes,
// so the cache lag cannot appear as zero traffic and raw work stays bounded.
const endpointRollupQuery = `
WITH params AS (
  SELECT
    ?::TIMESTAMP AS start_time,
    ?::TIMESTAMP AS end_time,
    ?::TIMESTAMP AS rollup_end,
    ?::VARCHAR AS namespace,
    ?::VARCHAR AS service
),
bounds AS (
  SELECT
    *,
    CASE
      WHEN start_time = date_trunc('minute', start_time) THEN start_time
      ELSE date_trunc('minute', start_time) + INTERVAL 1 MINUTE
    END AS interior_start,
    LEAST(date_trunc('minute', end_time), date_trunc('minute', rollup_end)) AS interior_end
  FROM params
),
rollup_source AS (
  SELECT e.method, e.path, e.calls, e.error_count, e.duration_count, e.duration_buckets
  FROM endpoint_rollup e, bounds b
  WHERE e.bucket >= b.interior_start
    AND e.bucket < b.interior_end
    AND (b.namespace = '' OR e.namespace = b.namespace)
    AND (b.service = '' OR e.service = b.service)
),
boundary_source AS (
  SELECT
    COALESCE(NULLIF(s.http_method, ''), 'CALL') AS method,
    COALESCE(NULLIF(s.http_route, ''), NULLIF(s.operation, ''), 'unknown') AS path,
    COUNT(*) AS calls,
    COUNT(*) FILTER (WHERE upper(s.status) IN ('ERROR', 'STATUS_CODE_ERROR')) AS error_count,
    COUNT(s.duration_ms) AS duration_count,
    struct_pack(
      le_0_1 := COUNT(*) FILTER (WHERE s.duration_ms <= 0.1),
      le_0_5 := COUNT(*) FILTER (WHERE s.duration_ms <= 0.5),
      le_1 := COUNT(*) FILTER (WHERE s.duration_ms <= 1),
      le_2_5 := COUNT(*) FILTER (WHERE s.duration_ms <= 2.5),
      le_5 := COUNT(*) FILTER (WHERE s.duration_ms <= 5),
      le_10 := COUNT(*) FILTER (WHERE s.duration_ms <= 10),
      le_25 := COUNT(*) FILTER (WHERE s.duration_ms <= 25),
      le_50 := COUNT(*) FILTER (WHERE s.duration_ms <= 50),
      le_100 := COUNT(*) FILTER (WHERE s.duration_ms <= 100),
      le_250 := COUNT(*) FILTER (WHERE s.duration_ms <= 250),
      le_500 := COUNT(*) FILTER (WHERE s.duration_ms <= 500),
      le_750 := COUNT(*) FILTER (WHERE s.duration_ms <= 750),
      le_1000 := COUNT(*) FILTER (WHERE s.duration_ms <= 1000),
      le_2000 := COUNT(*) FILTER (WHERE s.duration_ms <= 2000),
      le_5000 := COUNT(*) FILTER (WHERE s.duration_ms <= 5000),
      le_30000 := COUNT(*) FILTER (WHERE s.duration_ms <= 30000),
      le_300000 := COUNT(*) FILTER (WHERE s.duration_ms <= 300000)
    ) AS duration_buckets
  FROM spans s, bounds b
  WHERE s.start_time >= b.start_time
    AND s.start_time < b.end_time
    AND (s.start_time < b.interior_start OR s.start_time >= b.interior_end)
    AND (b.namespace = '' OR s.namespace = b.namespace)
    AND (b.service = '' OR COALESCE(s.service, '') = b.service)
  GROUP BY method, path
),
sources AS (
  SELECT * FROM rollup_source
  UNION ALL
  SELECT * FROM boundary_source
),
endpoint_totals AS (
  SELECT
    method,
    path,
    CAST(SUM(calls) AS BIGINT) AS calls,
    COALESCE(SUM(error_count)::DOUBLE / NULLIF(SUM(calls), 0), 0) AS error_rate,
    SUM(duration_count) AS duration_count,
    SUM(duration_buckets.le_0_1) AS le_0_1,
    SUM(duration_buckets.le_0_5) AS le_0_5,
    SUM(duration_buckets.le_1) AS le_1,
    SUM(duration_buckets.le_2_5) AS le_2_5,
    SUM(duration_buckets.le_5) AS le_5,
    SUM(duration_buckets.le_10) AS le_10,
    SUM(duration_buckets.le_25) AS le_25,
    SUM(duration_buckets.le_50) AS le_50,
    SUM(duration_buckets.le_100) AS le_100,
    SUM(duration_buckets.le_250) AS le_250,
    SUM(duration_buckets.le_500) AS le_500,
    SUM(duration_buckets.le_750) AS le_750,
    SUM(duration_buckets.le_1000) AS le_1000,
    SUM(duration_buckets.le_2000) AS le_2000,
    SUM(duration_buckets.le_5000) AS le_5000,
    SUM(duration_buckets.le_30000) AS le_30000,
    SUM(duration_buckets.le_300000) AS le_300000
  FROM sources
  GROUP BY method, path
)
SELECT
  t.method,
  t.path,
  t.calls,
  CASE
    WHEN duration_count = 0 THEN 0
    WHEN le_0_1 >= duration_count * 0.50 THEN 0.1 WHEN le_0_5 >= duration_count * 0.50 THEN 0.5
    WHEN le_1 >= duration_count * 0.50 THEN 1 WHEN le_2_5 >= duration_count * 0.50 THEN 2.5
    WHEN le_5 >= duration_count * 0.50 THEN 5 WHEN le_10 >= duration_count * 0.50 THEN 10
    WHEN le_25 >= duration_count * 0.50 THEN 25 WHEN le_50 >= duration_count * 0.50 THEN 50
    WHEN le_100 >= duration_count * 0.50 THEN 100 WHEN le_250 >= duration_count * 0.50 THEN 250
    WHEN le_500 >= duration_count * 0.50 THEN 500 WHEN le_750 >= duration_count * 0.50 THEN 750
    WHEN le_1000 >= duration_count * 0.50 THEN 1000 WHEN le_2000 >= duration_count * 0.50 THEN 2000
    WHEN le_5000 >= duration_count * 0.50 THEN 5000 WHEN le_30000 >= duration_count * 0.50 THEN 30000
    ELSE 300000 END AS p50_ms,
  CASE
    WHEN duration_count = 0 THEN 0
    WHEN le_0_1 >= duration_count * 0.95 THEN 0.1 WHEN le_0_5 >= duration_count * 0.95 THEN 0.5
    WHEN le_1 >= duration_count * 0.95 THEN 1 WHEN le_2_5 >= duration_count * 0.95 THEN 2.5
    WHEN le_5 >= duration_count * 0.95 THEN 5 WHEN le_10 >= duration_count * 0.95 THEN 10
    WHEN le_25 >= duration_count * 0.95 THEN 25 WHEN le_50 >= duration_count * 0.95 THEN 50
    WHEN le_100 >= duration_count * 0.95 THEN 100 WHEN le_250 >= duration_count * 0.95 THEN 250
    WHEN le_500 >= duration_count * 0.95 THEN 500 WHEN le_750 >= duration_count * 0.95 THEN 750
    WHEN le_1000 >= duration_count * 0.95 THEN 1000 WHEN le_2000 >= duration_count * 0.95 THEN 2000
    WHEN le_5000 >= duration_count * 0.95 THEN 5000 WHEN le_30000 >= duration_count * 0.95 THEN 30000
    ELSE 300000 END AS p95_ms,
  CASE
    WHEN duration_count = 0 THEN 0
    WHEN le_0_1 >= duration_count * 0.99 THEN 0.1 WHEN le_0_5 >= duration_count * 0.99 THEN 0.5
    WHEN le_1 >= duration_count * 0.99 THEN 1 WHEN le_2_5 >= duration_count * 0.99 THEN 2.5
    WHEN le_5 >= duration_count * 0.99 THEN 5 WHEN le_10 >= duration_count * 0.99 THEN 10
    WHEN le_25 >= duration_count * 0.99 THEN 25 WHEN le_50 >= duration_count * 0.99 THEN 50
    WHEN le_100 >= duration_count * 0.99 THEN 100 WHEN le_250 >= duration_count * 0.99 THEN 250
    WHEN le_500 >= duration_count * 0.99 THEN 500 WHEN le_750 >= duration_count * 0.99 THEN 750
    WHEN le_1000 >= duration_count * 0.99 THEN 1000 WHEN le_2000 >= duration_count * 0.99 THEN 2000
    WHEN le_5000 >= duration_count * 0.99 THEN 5000 WHEN le_30000 >= duration_count * 0.99 THEN 30000
    ELSE 300000 END AS p99_ms,
  t.error_rate
FROM endpoint_totals t
ORDER BY t.calls DESC, p95_ms DESC
LIMIT ?`

const performanceHeatmapQueryTemplate = `
SELECT time_bucket(INTERVAL '%s', bucket) AS point_time, service, COALESCE(MAX(p95_ms), 0)
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?)
  AND service IN (
    SELECT service FROM service_rollup
    WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?)
    GROUP BY service ORDER BY SUM(spans) DESC LIMIT 12
  )
GROUP BY point_time, service
ORDER BY point_time ASC, service ASC`

func performancePointsSQL(window time.Duration) string {
	return fmt.Sprintf(performancePointsQueryTemplate, timelineBucketWidth(window))
}

func performanceHeatmapSQL(window time.Duration) string {
	return fmt.Sprintf(performanceHeatmapQueryTemplate, timelineBucketWidth(window))
}

const performanceAggregateQuery = `
SELECT
  CAST(COALESCE(SUM(spans), 0) AS DOUBLE),
  COALESCE(SUM(error_rate * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(SUM(p50_ms * spans) / NULLIF(SUM(spans), 0), 0),
  COALESCE(MAX(p95_ms), 0)
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?) AND (? = '' OR service = ?)`

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
	window := scope.End.Sub(scope.Start)

	data := Performance{Service: service, Points: []PerformancePoint{}, Endpoints: []Endpoint{}, Heatmap: []HeatmapPoint{}, Comparison: []ComparisonMetric{}}
	rows, err := s.db.QueryContext(ctx, performancePointsSQL(window), scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service)
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

	endpoints, endpointSource, err := s.queryEndpoints(ctx, scope, service, limit)
	if err != nil {
		return Result[Performance]{}, err
	}
	data.Endpoints = endpoints

	rows, err = s.db.QueryContext(ctx, performanceHeatmapSQL(window), scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Start, scope.End, scope.Namespace, scope.Namespace)
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
		Provenance: s.provenanceFor(scope, "service_rollup + "+endpointSource),
	}, nil
}

func (s *Service) queryEndpoints(ctx context.Context, scope Scope, service string, limit int) ([]Endpoint, string, error) {
	ready, watermark, err := s.endpointCacheState(ctx)
	if err != nil {
		// The endpoint cache is an optimization. A transient local-state probe
		// failure must not take down the raw-span query path.
		slog.Warn("endpoint rollup state unavailable; using raw spans", "err", err)
		ready = false
	}

	query := rawEndpointsQuery
	args := []any{scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service, limit}
	source := "spans"
	if ready {
		query = endpointRollupQuery
		args = []any{scope.Start, scope.End, watermark, scope.Namespace, service, limit}
		source = "endpoint_rollup + raw spans (histogram upper-bound percentiles)"
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query endpoints: %w", err)
	}
	defer rows.Close()

	endpoints := make([]Endpoint, 0)
	for rows.Next() {
		var endpoint Endpoint
		if err := rows.Scan(&endpoint.Method, &endpoint.Path, &endpoint.Calls, &endpoint.P50MS, &endpoint.P95MS, &endpoint.P99MS, &endpoint.ErrorRate); err != nil {
			return nil, "", fmt.Errorf("scan endpoint: %w", err)
		}
		endpoint.Health = classify(endpoint.ErrorRate, endpoint.P95MS)
		endpoints = append(endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate endpoints: %w", err)
	}
	return endpoints, source, nil
}

func (s *Service) endpointCacheState(ctx context.Context) (bool, time.Time, error) {
	rows, err := s.db.QueryContext(ctx, endpointRollupStatusQuery)
	if err != nil {
		return false, time.Time{}, err
	}
	var ready bool
	var watermarkNanos int64
	if rows.Next() {
		if err := rows.Scan(&ready, &watermarkNanos); err != nil {
			rows.Close()
			return false, time.Time{}, err
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, time.Time{}, err
	}
	rows.Close()
	if !ready || watermarkNanos <= 0 {
		return false, time.Time{}, nil
	}
	watermark := time.Unix(0, watermarkNanos).UTC()
	if s.endpointMature.Load() {
		return true, watermark, nil
	}

	// On a brand-new/hot dataset, fewer than five cached minutes cannot offset
	// the wider histogram aggregation. Stay on the simpler raw query until the
	// cache is large enough to replace meaningful work, then remember that fact.
	rows, err = s.db.QueryContext(ctx, endpointRollupMatureQuery)
	if err != nil {
		return false, time.Time{}, err
	}
	defer rows.Close()
	var mature bool
	if rows.Next() {
		if err := rows.Scan(&mature); err != nil {
			return false, time.Time{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return false, time.Time{}, err
	}
	if mature {
		s.endpointMature.Store(true)
	}
	return mature, watermark, nil
}

type performanceAggregate struct {
	Spans     float64
	ErrorRate float64
	P50MS     float64
	P95MS     float64
}

func (s *Service) performanceAggregate(ctx context.Context, scope Scope, service string) (performanceAggregate, error) {
	rows, err := s.db.QueryContext(ctx, performanceAggregateQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service)
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
