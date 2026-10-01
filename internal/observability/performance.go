package observability

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

var performancePointsQueryTemplate = `
SELECT
  time_bucket(INTERVAL '%s', bucket) AS point_time,
  CAST(SUM(spans) AS BIGINT),
  COALESCE(SUM(error_rate * spans) / NULLIF(SUM(spans), 0), 0),
  ` + windowP50SQL + `,
  ` + windowP95SQL + `,
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
WHERE start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (? = '' OR namespace = ?) AND (? = '' OR service = ?)
GROUP BY method, path
ORDER BY calls DESC, p95_ms DESC
LIMIT ?`

// Completed minutes and the uncached/partial-minute tail are disjoint. Their
// histogram counts merge without weighting precomputed percentiles.
var completedEndpointsQuery = `WITH rollup_source AS (
 SELECT method,path,calls,error_count,duration_count,duration_buckets FROM endpoint_minutes
 WHERE ($3='' OR namespace=$4) AND ($5='' OR service=$6)
), boundary_source AS (
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
 FROM endpoint_tail s
 WHERE s.start_time>=$1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time<$2::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND ($3='' OR s.namespace=$4) AND ($5='' OR coalesce(s.service,'')=$6)
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
  t.error_rate,
  t.duration_count,
  ` + endpointDurationColumns() + `
FROM endpoint_totals t
ORDER BY t.calls DESC, (t.duration_count - le_100) DESC, t.method, t.path
LIMIT $7`

var performanceHeatmapQueryTemplate = `
SELECT time_bucket(INTERVAL '%s', bucket) AS point_time, service, ` + windowP95SQL + `
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

var performanceAggregateQuery = `
SELECT
  CAST(COALESCE(SUM(spans), 0) AS DOUBLE),
  CAST(COALESCE(SUM(served_spans), 0) AS DOUBLE),
  COALESCE(SUM(error_rate * spans) / NULLIF(SUM(spans), 0), 0),
  ` + windowP50SQL + `,
  ` + windowP95SQL + `
FROM service_rollup
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?) AND (? = '' OR service = ?)`

// PerformanceOptions selects what a performance read costs.
//
// Heatmap is the cross-service comparison grid: 12 services by every bucket in
// the window. The browser's performance page draws it. An MCP caller asked
// about one service, so for that path it is a second DuckDB query and a payload
// -- about 85% of a 113 KB response on the live demo -- covering 12 services it
// did not ask about.
type PerformanceOptions struct {
	Service string
	Limit   int
	Heatmap bool
}

func (s *Service) Performance(ctx context.Context, scope Scope, opts PerformanceOptions) (Result[Performance], error) {
	service, limit := opts.Service, opts.Limit
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[Performance]{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Result[Performance]{}, err
	}
	service = strings.TrimSpace(service)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End, Namespace: scope.Namespace, Service: service})
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

	if opts.Heatmap {
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
	}

	midpoint := scope.Start.Add(scope.End.Sub(scope.Start) / 2)
	before, err := s.performanceAggregate(ctx, Scope{Namespace: scope.Namespace, Start: scope.Start, End: midpoint}, service)
	if err != nil {
		return Result[Performance]{}, err
	}
	after, err := s.performanceAggregate(ctx, Scope{Namespace: scope.Namespace, Start: midpoint, End: scope.End}, service)
	if err != nil {
		return Result[Performance]{}, err
	}
	data.Totals = totalsOf(before, after)
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
	reader, histogram := s.db.(queryrows.BatchReader)
	histogram = histogram && reader.CompletedBatchReads()
	statement := rawEndpointsQuery
	source := "spans"
	if histogram {
		statement = completedEndpointsQuery
		source = "completed batch histograms + uncached spans and partial minutes"
		ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End, Namespace: scope.Namespace, Service: service, Kind: queryrows.EndpointRead})
	}
	args := []any{scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service, limit}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query endpoints: %w", err)
	}
	defer rows.Close()

	endpoints := make([]Endpoint, 0)
	for rows.Next() {
		var endpoint Endpoint
		if histogram {
			var total float64
			cumulative := make([]float64, len(endpointDurationBuckets))
			targets := []any{&endpoint.Method, &endpoint.Path, &endpoint.Calls, &endpoint.ErrorRate, &total}
			for i := range cumulative {
				targets = append(targets, &cumulative[i])
			}
			if err := rows.Scan(targets...); err != nil {
				return nil, "", fmt.Errorf("scan endpoint: %w", err)
			}
			endpoint.P50MS = histogramQuantile(cumulative, total, 0.50)
			endpoint.P95MS = histogramQuantile(cumulative, total, 0.95)
			endpoint.P99MS = histogramQuantile(cumulative, total, 0.99)
		} else if err := rows.Scan(&endpoint.Method, &endpoint.Path, &endpoint.Calls, &endpoint.P50MS, &endpoint.P95MS, &endpoint.P99MS, &endpoint.ErrorRate); err != nil {
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

// endpointDurationBuckets pairs each histogram boundary with the column that
// counts it, and both the query's column list and the interpolation read from
// here.
//
// The two were written out separately at first, which couples them by position
// alone: reordering one list, or adding a boundary to the rollup without adding
// it here, would go on compiling and quietly report one bucket's count against
// another's boundary. Percentiles would shift and nothing would look broken.
// The boundaries themselves must stay in step with the histogram the rollup
// writes (endpointBatchSelect, in internal/query). Nothing here can check
// that at compile time; TestEndpointRollupQueryMergesBucketsAndExactBoundaries
// is what catches a drift, because it fills a real rollup row by column name
// and reads the percentiles back out.
var endpointDurationBuckets = []struct {
	Column string
	Bound  float64
}{
	{"le_0_1", 0.1}, {"le_0_5", 0.5}, {"le_1", 1}, {"le_2_5", 2.5},
	{"le_5", 5}, {"le_10", 10}, {"le_25", 25}, {"le_50", 50},
	{"le_100", 100}, {"le_250", 250}, {"le_500", 500}, {"le_750", 750},
	{"le_1000", 1000}, {"le_2000", 2000}, {"le_5000", 5000},
	{"le_30000", 30000}, {"le_300000", 300000},
}

// endpointDurationColumns is the SELECT list for those counts, in the order
// histogramQuantile expects to read them.
func endpointDurationColumns() string {
	names := make([]string, len(endpointDurationBuckets))
	for i, bucket := range endpointDurationBuckets {
		names[i] = bucket.Column
	}
	return strings.Join(names, ", ")
}

// histogramQuantile reads a quantile out of cumulative bucket counts,
// interpolating inside the bucket it lands in.
//
// Reporting the bucket's upper bound instead — which is what the rollup query
// used to do — makes a table that reads as broken: every endpoint whose P95 and
// P99 fall in one bucket shows the same number twice, and a page of "25.0ms /
// 25.0ms" invites the reader to distrust the whole view. Interpolation also
// stops a busy endpoint reporting 50ms when almost all of its calls sit just
// above 25ms.
//
// Values beyond the last boundary are reported at that boundary: the rollup
// stops counting there, so it is the most the data can support.
func histogramQuantile(cumulative []float64, total, quantile float64) float64 {
	if total <= 0 || len(cumulative) == 0 {
		return 0
	}
	rank := quantile * total
	previousBound, previousCount := 0.0, 0.0
	for i, count := range cumulative {
		if i >= len(endpointDurationBuckets) {
			break
		}
		bound := endpointDurationBuckets[i].Bound
		if count >= rank {
			width, inBucket := bound-previousBound, count-previousCount
			if width <= 0 || inBucket <= 0 {
				return bound
			}
			return previousBound + width*((rank-previousCount)/inBucket)
		}
		previousBound, previousCount = bound, count
	}
	return endpointDurationBuckets[len(endpointDurationBuckets)-1].Bound
}

type performanceAggregate struct {
	Spans       float64
	ServedSpans float64
	ErrorRate   float64
	P50MS       float64
	P95MS       float64
}

func (s *Service) performanceAggregate(ctx context.Context, scope Scope, service string) (performanceAggregate, error) {
	rows, err := s.db.QueryContext(ctx, performanceAggregateQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service)
	if err != nil {
		return performanceAggregate{}, fmt.Errorf("query performance comparison: %w", err)
	}
	defer rows.Close()
	var value performanceAggregate
	if rows.Next() {
		if err := rows.Scan(&value.Spans, &value.ServedSpans, &value.ErrorRate, &value.P50MS, &value.P95MS); err != nil {
			return performanceAggregate{}, fmt.Errorf("scan performance comparison: %w", err)
		}
	}
	return value, rows.Err()
}

// totalsOf folds the two comparison halves back into one window rather than
// running a third aggregate query: the halves tile the window exactly, so
// summing spans, weighting the averages by span count and taking the larger
// P95 reproduces what performanceAggregateQuery would return over the whole
// scope. Keep these rules in step with that query.
//
// Latency comes only from halves in which the service served something,
// matching what the whole-window query does across buckets. Taking the larger
// P95 of both halves regardless would let a half spent holding a subscription
// open decide the figure, and the card would then disagree with the overview
// beside it — which is the disagreement these totals exist to end. Counts and
// error rate still cover every span: an operation the service waited on is
// still an operation it performed, and a failed outbound call is still its
// problem.
func totalsOf(before, after performanceAggregate) PerformanceTotals {
	spans := before.Spans + after.Spans
	totals := PerformanceTotals{Spans: int64(spans)}
	if spans > 0 {
		totals.ErrorRate = (before.ErrorRate*before.Spans + after.ErrorRate*after.Spans) / spans
	}

	latency := []performanceAggregate{}
	for _, half := range []performanceAggregate{before, after} {
		if half.ServedSpans > 0 {
			latency = append(latency, half)
		}
	}
	if len(latency) == 0 {
		latency = []performanceAggregate{before, after}
	}
	var weight float64
	for _, half := range latency {
		totals.P95MS = math.Max(totals.P95MS, half.P95MS)
		// Weighted by served spans, matching windowP50SQL: the half's p50
		// describes the requests it served, not the calls it also made.
		span := half.ServedSpans
		if span == 0 {
			span = half.Spans
		}
		totals.P50MS += half.P50MS * span
		weight += span
	}
	if weight > 0 {
		totals.P50MS /= weight
	} else {
		totals.P50MS = 0
	}
	return totals
}

// latencyNoiseMS is the smallest latency move worth calling a change. A p50
// that goes from 3.9ms to 4.0ms is not a regression, but as a percentage it is
// 2.2% — enough to have been painted red beside an arrow, which spends the
// colour that should mean "look here" on sampling noise.
const latencyNoiseMS = 1.0

func comparisonMetric(label, unit string, before, after float64, lowerIsBetter bool) ComparisonMetric {
	change := 0.0
	if before != 0 {
		change = (after - before) / math.Abs(before) * 100
	} else if after != 0 {
		change = 100
	}
	direction := DirectionStable
	if unit == "ms" && math.Abs(after-before) < latencyNoiseMS {
		// The percentage goes too. Passing it through printed "↑ 100.0%" inside
		// a grey "stable" badge whenever the earlier half had no traffic — the
		// same number-against-colour contradiction this floor exists to remove.
		return ComparisonMetric{Label: label, Unit: unit, Before: before, After: after, Direction: direction}
	}
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
