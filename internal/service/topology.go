package service

import (
	"context"
	"database/sql"
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
		if result, ok := v.(*TopologyResult); ok {
			return result, nil
		}
	}

	out := &TopologyResult{
		Nodes: []ServiceNode{},
		Edges: []ServiceEdge{},
	}

	q := fmt.Sprintf(`
SELECT
  service,
  SUM(spans)::BIGINT AS cnt,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) AS p95,
  AVG(CASE WHEN spans > 0 THEN error_rate END) AS error_rate,
  SUM(COALESCE(log_count, 0))::BIGINT AS log_cnt,
  SUM(COALESCE(metric_count, 0))::BIGINT AS metric_cnt
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY service
ORDER BY (SUM(spans) + SUM(COALESCE(log_count, 0)) + SUM(COALESCE(metric_count, 0))) DESC
LIMIT 50;
`, window)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		slog.Warn("query failed", "method", "Topology.nodes", "err", err)
		return out, nil
	}
	defer rows.Close()

	for rows.Next() {
		var n ServiceNode
		var p95null, errNull sql.NullFloat64
		if err := rows.Scan(&n.Name, &n.SpanCount, &p95null, &errNull, &n.LogCount, &n.MetricCount); err != nil {
			slog.Warn("scan failed", "method", "Topology.nodes", "err", err)
			continue
		}
		if p95null.Valid {
			n.P95Ms = p95null.Float64
		}
		if errNull.Valid {
			n.ErrorRate = errNull.Float64
		}
		n.Status = DeriveHealth(n.ErrorRate, n.P95Ms, n.SpanCount)
		out.Nodes = append(out.Nodes, n)
	}

	// Get service edges (caller -> callee)
	q = fmt.Sprintf(`
SELECT
  caller,
  callee,
  SUM(calls)::BIGINT AS call_count,
  AVG(avg_ms) AS avg_ms,
  AVG(error_rate) AS error_rate,
  COALESCE(edge_type, 'call') AS edge_type
FROM edge_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY caller, callee, edge_type
ORDER BY call_count DESC
LIMIT 100;
`, window)

	rows, err = s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		slog.Warn("query failed", "method", "Topology.edges", "err", err)
		return out, nil
	}
	defer rows.Close()

	for rows.Next() {
		var e ServiceEdge
		if err := rows.Scan(&e.From, &e.To, &e.CallCount, &e.AvgMs, &e.ErrorRate, &e.EdgeType); err != nil {
			slog.Warn("scan failed", "method", "Topology.edges", "err", err)
			continue
		}
		e.Status = DeriveHealth(e.ErrorRate, e.AvgMs)
		out.Edges = append(out.Edges, e)
	}

	query.SetCached(cacheKey, out)
	return out, nil
}
