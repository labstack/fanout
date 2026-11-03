package observability

import (
	"context"
	"fmt"
	"github.com/labstack/fanout/internal/queryrows"
	"sort"
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
WHERE edge_type<>'peer' AND bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?) AND (? = '' OR caller = ? OR callee = ?)
GROUP BY caller, callee, edge_type
ORDER BY calls DESC, caller ASC, callee ASC
LIMIT ?`

// Peer latency percentiles are call-weighted minute summaries, matching the
// service health rollup's approximation across buckets.
const topologyPeersQuery = `WITH edges AS (
 SELECT caller,callee,sum(calls)::BIGINT AS calls,sum(avg_ms*calls)/sum(calls) AS average_ms,
 sum(error_rate*calls)/sum(calls) AS error_rate,sum(p50_ms*calls)/sum(calls) AS p50,sum(p95_ms*calls)/sum(calls) AS p95
 FROM edge_rollup WHERE edge_type='peer' AND bucket>=? AND bucket<?
 AND (?='' OR namespace=?) AND (?='' OR caller=? OR callee=?)
 AND NOT EXISTS (SELECT 1 FROM service_rollup s WHERE s.namespace=edge_rollup.namespace AND s.service=edge_rollup.callee AND s.bucket>=? AND s.bucket<?)
 GROUP BY caller,callee),
 peers AS (SELECT callee,sum(calls)::BIGINT AS spans,sum(error_rate*calls)/sum(calls) AS error_rate,
 sum(p50*calls)/sum(calls) AS p50,sum(p95*calls)/sum(calls) AS p95 FROM edges GROUP BY callee)
 SELECT e.caller,e.callee,e.calls,e.average_ms,e.error_rate,p.spans,p.error_rate,p.p50,p.p95
 FROM edges e JOIN peers p USING(callee) ORDER BY p.error_rate DESC,p.p95 DESC,e.calls DESC,e.caller,e.callee LIMIT ?`

func (s *Service) Topology(ctx context.Context, scope Scope, limit int) (Topology, error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Topology{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Topology{}, err
	}

	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End, Namespace: scope.Namespace})
	nodes, err := s.serviceHealth(ctx, scope, limit)
	if err != nil {
		return Topology{}, err
	}
	rows, err := s.db.QueryContext(ctx, topologyEdgesQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Service, scope.Service, scope.Service, limit)
	if err != nil {
		return Topology{}, fmt.Errorf("query topology edges: %w", err)
	}
	defer rows.Close()

	edges := make([]Edge, 0)
	for rows.Next() {
		var edge Edge
		if err := rows.Scan(&edge.Caller, &edge.Callee, &edge.Type, &edge.Calls, &edge.AverageMS, &edge.ErrorRate); err != nil {
			return Topology{}, fmt.Errorf("scan topology edge: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return Topology{}, fmt.Errorf("iterate topology edges: %w", err)
	}

	// Close the first stream before acquiring another read-pool slot.
	rows.Close()
	external, err := s.db.QueryContext(ctx, topologyPeersQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Service, scope.Service, scope.Service, scope.Start, scope.End, limit)
	if err != nil {
		return Topology{}, fmt.Errorf("query external peers: %w", err)
	}
	defer external.Close()
	known := map[string]bool{}
	for _, n := range nodes {
		known[n.Service] = true
	}
	for external.Next() {
		var edge Edge
		var peer ServiceHealth
		edge.Type = "call"
		if err := external.Scan(&edge.Caller, &edge.Callee, &edge.Calls, &edge.AverageMS, &edge.ErrorRate, &peer.Spans, &peer.ErrorRate, &peer.P50MS, &peer.P95MS); err != nil {
			return Topology{}, fmt.Errorf("scan external peer: %w", err)
		}
		peer.Service = edge.Callee
		peer.Health = classify(peer.ErrorRate, peer.P95MS)
		if !known[peer.Service] {
			nodes = append(nodes, peer)
			known[peer.Service] = true
		}
		edges = append(edges, edge)
	}
	if err := external.Err(); err != nil {
		return Topology{}, err
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.ErrorRate != b.ErrorRate {
			return a.ErrorRate > b.ErrorRate
		}
		if a.P95MS != b.P95MS {
			return a.P95MS > b.P95MS
		}
		if a.Spans != b.Spans {
			return a.Spans > b.Spans
		}
		return a.Service < b.Service
	})
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.ErrorRate != b.ErrorRate {
			return a.ErrorRate > b.ErrorRate
		}
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		if a.Caller != b.Caller {
			return a.Caller < b.Caller
		}
		return a.Callee < b.Callee
	})
	nodes = nodes[:min(len(nodes), limit)]
	edges = edges[:min(len(edges), limit)]

	return Topology{Nodes: nodes, Edges: edges}, nil
}
