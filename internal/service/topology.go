package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"

	"github.com/labstack/fanout/internal/query"
)

// Topology returns the service dependency map with health indicators.
// This is the legacy signature kept for backward compatibility with the AI orchestrator.
func (s *Service) Topology(ctx context.Context, window int, namespace string) (*TopologyResult, error) {
	return s.TopologyWithParams(ctx, TopologyParams{
		Window:    window,
		Namespace: namespace,
		EdgeType:  "all",
	})
}

// TopologyWithParams returns the service dependency map with full parameter support.
func (s *Service) TopologyWithParams(ctx context.Context, p TopologyParams) (*TopologyResult, error) {
	if p.Window == 0 {
		p.Window = 60
	}
	if p.EdgeType == "" {
		p.EdgeType = "all"
	}

	// Check cache
	cacheKey := fmt.Sprintf("topology:%d:%s:%s:%d:%s:%v", p.Window, p.Namespace, p.EdgeType, p.Depth, p.Service, p.IncludeInactive)
	if v, ok := query.GetCached(cacheKey); ok {
		if result, ok := v.(*TopologyResult); ok {
			return result, nil
		}
	}

	out := &TopologyResult{
		Nodes:         []ServiceNode{},
		Edges:         []ServiceEdge{},
		CriticalPaths: [][]string{},
	}

	q := fmt.Sprintf(`
SELECT
  service,
  SUM(spans)::BIGINT AS cnt,
  AVG(CASE WHEN spans > 0 THEN p50_ms END) AS p50,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) AS p95,
  AVG(CASE WHEN spans > 0 THEN error_rate END) AS error_rate,
  SUM(COALESCE(log_count, 0))::BIGINT AS log_cnt,
  SUM(COALESCE(metric_count, 0))::BIGINT AS metric_cnt
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
GROUP BY service
ORDER BY (SUM(spans) + SUM(COALESCE(log_count, 0)) + SUM(COALESCE(metric_count, 0))) DESC
LIMIT 50;
`, p.Window)

	rows, err := s.duck.DB.QueryContext(ctx, q, p.Namespace, p.Namespace)
	if err != nil {
		return nil, fmt.Errorf("topology nodes query failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var n ServiceNode
		var p50null, p95null, errNull sql.NullFloat64
		if err := rows.Scan(&n.Name, &n.SpanCount, &p50null, &p95null, &errNull, &n.LogCount, &n.MetricCount); err != nil {
			slog.Warn("scan failed", "method", "Topology.nodes", "err", err)
			continue
		}
		if p50null.Valid {
			n.P50Ms = p50null.Float64
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
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "method", "Topology.nodes", "err", err)
	}

	// Get service edges (caller -> callee)
	edgeTypeFilter := ""
	if p.EdgeType != "all" && p.EdgeType != "" {
		// Only allow safe, known edge type values to prevent SQL injection
		if p.EdgeType == "call" || p.EdgeType == "messaging" {
			edgeTypeFilter = fmt.Sprintf("AND COALESCE(edge_type, 'call') = '%s'", p.EdgeType)
		}
	}

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
  AND (? = '' OR namespace = ?)
%s
GROUP BY caller, callee, edge_type
ORDER BY call_count DESC
LIMIT 100;
`, p.Window, edgeTypeFilter)

	rows, err = s.duck.DB.QueryContext(ctx, q, p.Namespace, p.Namespace)
	if err != nil {
		return nil, fmt.Errorf("topology edges query failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e ServiceEdge
		if err := rows.Scan(&e.From, &e.To, &e.CallCount, &e.AvgMs, &e.ErrorRate, &e.EdgeType); err != nil {
			slog.Warn("scan failed", "method", "Topology.edges", "err", err)
			continue
		}
		e.Status = DeriveHealth(e.ErrorRate, e.AvgMs, e.CallCount)
		out.Edges = append(out.Edges, e)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "method", "Topology.edges", "err", err)
	}

	// Apply depth filter (BFS from focus service)
	if p.Service != "" && p.Depth > 0 {
		out.Nodes, out.Edges = applyDepthFilter(out.Nodes, out.Edges, p.Service, p.Depth)
	}

	// Compute upstream/downstream counts and blast_radius per node
	computeNodeMetrics(out.Nodes, out.Edges)

	// Compute critical paths
	out.CriticalPaths = computeCriticalPaths(out.Edges, 3)

	query.SetCached(cacheKey, out)
	return out, nil
}

// applyDepthFilter returns nodes and edges reachable within depth hops from the focus service using BFS.
func applyDepthFilter(nodes []ServiceNode, edges []ServiceEdge, focus string, depth int) ([]ServiceNode, []ServiceEdge) {
	// Build adjacency (bidirectional for BFS reach)
	adj := make(map[string][]string)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}

	// BFS
	visited := make(map[string]bool)
	queue := []string{focus}
	visited[focus] = true
	for hop := 0; hop < depth && len(queue) > 0; hop++ {
		next := []string{}
		for _, svc := range queue {
			for _, neighbor := range adj[svc] {
				if !visited[neighbor] {
					visited[neighbor] = true
					next = append(next, neighbor)
				}
			}
		}
		queue = next
	}

	// Filter nodes (new slices to avoid mutating caller's backing array)
	filteredNodes := make([]ServiceNode, 0, len(nodes))
	for _, n := range nodes {
		if visited[n.Name] {
			filteredNodes = append(filteredNodes, n)
		}
	}

	// Filter edges to only those between visited nodes
	filteredEdges := make([]ServiceEdge, 0, len(edges))
	for _, e := range edges {
		if visited[e.From] && visited[e.To] {
			filteredEdges = append(filteredEdges, e)
		}
	}

	return filteredNodes, filteredEdges
}

// computeNodeMetrics fills UpstreamCount, DownstreamCount, and BlastRadius for each node.
// blast_radius = sum(calls on edges where this node is source or target) / sum(all edge calls)
func computeNodeMetrics(nodes []ServiceNode, edges []ServiceEdge) {
	var totalCalls int64
	callsByNode := make(map[string]int64, len(nodes))

	for _, e := range edges {
		totalCalls += e.CallCount
		callsByNode[e.From] += e.CallCount
		callsByNode[e.To] += e.CallCount
	}

	upstreamCount := make(map[string]int, len(nodes))
	downstreamCount := make(map[string]int, len(nodes))
	for _, e := range edges {
		downstreamCount[e.From]++
		upstreamCount[e.To]++
	}

	for i := range nodes {
		nodes[i].UpstreamCount = upstreamCount[nodes[i].Name]
		nodes[i].DownstreamCount = downstreamCount[nodes[i].Name]
		if totalCalls > 0 {
			nodes[i].BlastRadius = float64(callsByNode[nodes[i].Name]) / float64(totalCalls)
		}
	}
}

// computeCriticalPaths returns the top N paths through the DAG by total weight (calls * avg_ms).
// Uses DFS from root nodes (nodes with no incoming edges). Max path length: 10 hops.
func computeCriticalPaths(edges []ServiceEdge, topN int) [][]string {
	if len(edges) == 0 {
		return [][]string{}
	}

	// Build outbound adjacency and weight map; track callee set for root detection
	type edgeKey struct{ from, to string }
	adj := make(map[string][]string)
	weights := make(map[edgeKey]float64)
	calleeSet := make(map[string]bool)

	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		weights[edgeKey{e.From, e.To}] = float64(e.CallCount) * e.AvgMs
		calleeSet[e.To] = true
	}

	// Root nodes: appear as source but never as callee
	callerSet := make(map[string]bool)
	for _, e := range edges {
		callerSet[e.From] = true
	}
	var roots []string
	for from := range callerSet {
		if !calleeSet[from] {
			roots = append(roots, from)
		}
	}
	sort.Strings(roots) // deterministic order

	type pathEntry struct {
		path   []string
		weight float64
	}

	const maxPaths = 10000
	var allPaths []pathEntry

	var dfs func(node string, path []string, visited map[string]bool, weight float64)
	dfs = func(node string, path []string, visited map[string]bool, weight float64) {
		if len(allPaths) >= maxPaths {
			return
		}
		path = append(path, node)
		if len(path) > 10 {
			// Max path length exceeded; record what we have
			cp := make([]string, len(path))
			copy(cp, path)
			allPaths = append(allPaths, pathEntry{cp, weight})
			return
		}
		neighbors := adj[node]
		if len(neighbors) == 0 {
			// Leaf node
			cp := make([]string, len(path))
			copy(cp, path)
			allPaths = append(allPaths, pathEntry{cp, weight})
			return
		}
		for _, next := range neighbors {
			if visited[next] {
				// Cycle: record path so far
				cp := make([]string, len(path))
				copy(cp, path)
				allPaths = append(allPaths, pathEntry{cp, weight})
				continue
			}
			w := weights[edgeKey{node, next}]
			visited[next] = true
			dfs(next, path, visited, weight+w)
			delete(visited, next)
		}
	}

	for _, root := range roots {
		visited := map[string]bool{root: true}
		dfs(root, []string{}, visited, 0)
	}

	// Sort by weight descending, take top N
	sort.Slice(allPaths, func(i, j int) bool {
		return allPaths[i].weight > allPaths[j].weight
	})

	result := make([][]string, 0, topN)
	for i := 0; i < len(allPaths) && i < topN; i++ {
		result = append(result, allPaths[i].path)
	}
	return result
}
