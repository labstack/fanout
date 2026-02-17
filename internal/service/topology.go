package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/labstack/fanout/internal/query"
)

// Topology returns the service dependency map with health indicators.
func (s *Service) Topology(ctx context.Context, window int, namespace, tenantID string) (*TopologyResult, error) {
	if window == 0 {
		window = 60
	}

	// Always scope to single partition
	namespace, tenantID = s.defaults(namespace, tenantID)

	// Check cache
	cacheKey := fmt.Sprintf("topology:%d:%s:%s", window, namespace, tenantID)
	if v, ok := query.GetCached(cacheKey); ok {
		return v.(*TopologyResult), nil
	}

	out := &TopologyResult{
		Nodes: []ServiceNode{},
		Edges: []ServiceEdge{},
	}

	// Use rollups for long time ranges (>60 min), raw parquet for short
	var q string
	if window > 60 {
		// Fast path: use pre-aggregated rollup table
		q = fmt.Sprintf(`
SELECT
  service,
  SUM(spans)::BIGINT as cnt,
  AVG(p95_ms) as p95,
  AVG(error_rate) as error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY service
ORDER BY cnt DESC
LIMIT 50;
`, window)
	} else {
		// Detailed path: scan raw parquet for accurate percentiles
		spansGlob := s.duck.SpansGlob(tenantID, namespace, window)
		q = fmt.Sprintf(`
SELECT
  "name=service_name" as service,
  COUNT(*) as cnt,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=service_name"
ORDER BY cnt DESC
LIMIT 50;
`, spansGlob, window)
	}

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		slog.Warn("query failed", "method", "Topology.nodes", "err", err)
		return out, nil
	}
	defer rows.Close()

	for rows.Next() {
		var n ServiceNode
		if err := rows.Scan(&n.Name, &n.SpanCount, &n.P95Ms, &n.ErrorRate); err != nil {
			slog.Warn("scan failed", "method", "Topology.nodes", "err", err)
			continue
		}
		n.Status = DeriveHealth(n.ErrorRate, n.P95Ms)
		out.Nodes = append(out.Nodes, n)
	}

	// Get service edges (caller -> callee)
	if window > 60 {
		// Fast path: use edge rollup table
		q = fmt.Sprintf(`
SELECT
  caller,
  callee,
  SUM(calls)::BIGINT as call_count,
  AVG(avg_ms) as avg_ms,
  AVG(error_rate) as error_rate
FROM edge_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY caller, callee
ORDER BY call_count DESC
LIMIT 100;
`, window)
	} else {
		// Detailed path: scan raw parquet
		spansGlob := s.duck.SpansGlob(tenantID, namespace, window)
		q = fmt.Sprintf(`
WITH calls AS (
  SELECT
    parent."name=service_name" as caller,
    child."name=service_name" as callee,
    child."name=duration_ms" as duration_ms,
    child."name=status_code" as status
  FROM read_parquet(%s, union_by_name=true) child
  JOIN read_parquet(%s, union_by_name=true) parent
    ON child."name=parent_span_id" = parent."name=span_id"
    AND child."name=trace_id" = parent."name=trace_id"
  WHERE epoch_ms(CAST(child."name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
    AND parent."name=service_name" != child."name=service_name"
)
SELECT
  caller,
  callee,
  COUNT(*) as call_count,
  AVG(duration_ms) as avg_ms,
  AVG(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) as error_rate
FROM calls
GROUP BY caller, callee
ORDER BY call_count DESC
LIMIT 100;
`, spansGlob, spansGlob, window)
	}

	rows, err = s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		slog.Warn("query failed", "method", "Topology.edges", "err", err)
		return out, nil
	}
	defer rows.Close()

	for rows.Next() {
		var e ServiceEdge
		if err := rows.Scan(&e.From, &e.To, &e.CallCount, &e.AvgMs, &e.ErrorRate); err != nil {
			slog.Warn("scan failed", "method", "Topology.edges", "err", err)
			continue
		}
		e.Status = DeriveHealth(e.ErrorRate, e.AvgMs)
		out.Edges = append(out.Edges, e)
	}

	query.SetCached(cacheKey, out)
	return out, nil
}
