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
  SUM(spans)::BIGINT as cnt,
  AVG(p95_ms) as p95,
  AVG(error_rate) as error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
GROUP BY service
ORDER BY cnt DESC
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
		if err := rows.Scan(&n.Name, &n.SpanCount, &n.P95Ms, &n.ErrorRate); err != nil {
			slog.Warn("scan failed", "method", "Topology.nodes", "err", err)
			continue
		}
		n.Status = DeriveHealth(n.ErrorRate, n.P95Ms)
		out.Nodes = append(out.Nodes, n)
	}

	// Get service edges (caller -> callee)
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
