package observability

import (
	"context"
	"fmt"
)

const topologyEdgesQuery = `
SELECT
  caller,
  callee,
  edge_type,
  CAST(SUM(calls) AS BIGINT) AS calls,
  COALESCE(SUM(avg_ms * calls) / NULLIF(SUM(calls), 0), 0) AS average_ms,
  COALESCE(SUM(error_rate * calls) / NULLIF(SUM(calls), 0), 0) AS error_rate
FROM edge_rollup
WHERE bucket >= ? AND bucket < ? AND namespace = ?
GROUP BY caller, callee, edge_type
ORDER BY calls DESC, caller ASC, callee ASC
LIMIT ?`

func (s *Service) Topology(ctx context.Context, scope Scope, limit int) (Result[Topology], error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[Topology]{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Result[Topology]{}, err
	}

	nodes, err := s.serviceHealth(ctx, scope, limit)
	if err != nil {
		return Result[Topology]{}, err
	}
	rows, err := s.db.QueryContext(ctx, topologyEdgesQuery, scope.Start, scope.End, scope.Namespace, limit)
	if err != nil {
		return Result[Topology]{}, fmt.Errorf("query topology edges: %w", err)
	}
	defer rows.Close()

	edges := make([]Edge, 0)
	for rows.Next() {
		var edge Edge
		if err := rows.Scan(&edge.Caller, &edge.Callee, &edge.Type, &edge.Calls, &edge.AverageMS, &edge.ErrorRate); err != nil {
			return Result[Topology]{}, fmt.Errorf("scan topology edge: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return Result[Topology]{}, fmt.Errorf("iterate topology edges: %w", err)
	}

	data := Topology{Nodes: nodes, Edges: edges}
	return Result[Topology]{
		Schema:     TopologySchema,
		Summary:    fmt.Sprintf("%d services connected by %d dependency edges", len(nodes), len(edges)),
		Data:       data,
		Provenance: s.provenance(scope),
	}, nil
}
