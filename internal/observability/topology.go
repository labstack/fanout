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
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?) AND (? = '' OR caller = ? OR callee = ?)
GROUP BY caller, callee, edge_type
ORDER BY calls DESC, caller ASC, callee ASC
LIMIT ?`

// Uninstrumented peers are visible through client spans; they do not require
// another rollup or an integration with the remote service.
// Exclude known services before looking for instrumented children. Materialize
// the remaining candidates so child lookups build only their keys, rather than
// every raw span in the window.
const topologyExternalQuery = `WITH raw_candidates AS (
 SELECT s.namespace,s.trace_id,s.span_id,s.service AS caller,s.peer_service AS callee,s.duration_ms,
 CASE WHEN s.status = 'STATUS_CODE_ERROR' THEN 1.0 ELSE 0.0 END AS error
 FROM spans s WHERE s.start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND (?='' OR s.namespace=?) AND (?='' OR s.service=? OR s.peer_service=?)
 AND s.kind='SPAN_KIND_CLIENT' AND coalesce(s.peer_service,'')<>'' AND s.peer_service<>s.service),
 observed AS MATERIALIZED (
 SELECT DISTINCT namespace,service FROM spans
 WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 candidates AS MATERIALIZED (SELECT c.* FROM raw_candidates c
 WHERE NOT EXISTS (SELECT 1 FROM observed o WHERE o.namespace=c.namespace AND o.service=c.callee)),
 children AS MATERIALIZED (
 SELECT DISTINCT child.namespace,child.trace_id,child.parent_span_id,child.service
 FROM spans child SEMI JOIN candidates c ON child.namespace=c.namespace AND child.trace_id=c.trace_id AND child.parent_span_id=c.span_id
 WHERE child.start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND child.start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 clients AS MATERIALIZED (
 SELECT c.caller,c.callee,c.duration_ms,c.error FROM candidates c
 WHERE NOT EXISTS (SELECT 1 FROM children child WHERE child.namespace=c.namespace AND child.trace_id=c.trace_id AND child.parent_span_id=c.span_id AND child.service<>c.caller)),
 peers AS (SELECT callee,count(*) AS spans,avg(error) AS error_rate,quantile_cont(duration_ms,.5) AS p50,quantile_cont(duration_ms,.95) AS p95 FROM clients GROUP BY callee),
 edges AS (SELECT caller,callee,count(*) AS calls,avg(duration_ms) AS average_ms,avg(error) AS error_rate FROM clients GROUP BY caller,callee)
 SELECT e.caller,e.callee,e.calls,e.average_ms,e.error_rate,p.spans,p.error_rate,p.p50,p.p95 FROM edges e JOIN peers p USING(callee)
 ORDER BY p.error_rate DESC,p.p95 DESC,e.calls DESC,e.caller,e.callee LIMIT ?`

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
	external, err := s.db.QueryContext(ctx, topologyExternalQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Service, scope.Service, scope.Service, scope.Start, scope.End, scope.Start, scope.End, limit)
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
