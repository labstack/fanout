package observability

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const DependenciesSchema = "fanout.dependencies.result@1"

const dependencyDeadline = 20 * time.Second

type DependencyOptions struct {
	Service   string
	Direction string
	MaxDepth  int
	MaxNodes  int
}

type DependencyNode struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Hops      int    `json:"hops"`
}

type Dependencies struct {
	Service           string           `json:"service"`
	Direction         string           `json:"direction"`
	Nodes             []DependencyNode `json:"nodes"`
	MaxDepth          int              `json:"max_depth"`
	MaxNodes          int              `json:"max_nodes"`
	Truncated         bool             `json:"truncated"`
	DepthLimitReached bool             `json:"depth_limit_reached"`
	NodeLimitReached  bool             `json:"node_limit_reached"`
}

// Keyed recursion retains one minimum-hop entry per namespace/service rather
// than every path through a cyclic graph. Admission caps the accumulated map,
// not merely the returned page. The complete scoped rollup is the edge source.
const dependencyQuery = `
WITH RECURSIVE
settings AS (SELECT ?::VARCHAR AS root, ?::VARCHAR AS direction, ?::INTEGER AS depth, ?::INTEGER AS nodes),
edges AS MATERIALIZED (
 SELECT DISTINCT namespace,
   CASE WHEN settings.direction='upstream' THEN callee ELSE caller END AS source,
   CASE WHEN settings.direction='upstream' THEN caller ELSE callee END AS target
 FROM edge_rollup, settings
 WHERE bucket >= ? AND bucket < ? AND (?='' OR namespace=?) AND calls>0
),
root_namespaces AS (
 SELECT namespace FROM edges,settings WHERE source=settings.root OR target=settings.root
 UNION
 SELECT namespace FROM service_rollup,settings
 WHERE service=settings.root AND bucket >= ? AND bucket < ? AND (?='' OR namespace=?)
),
walk(namespace,service,hops) USING KEY(namespace,service) AS (
 SELECT namespace,settings.root,0 FROM root_namespaces,settings
 QUALIFY row_number() OVER (ORDER BY namespace) <= settings.nodes
 UNION
 SELECT namespace,target,hops FROM (
   SELECT e.namespace,e.target,MIN(w.hops)+1 AS hops,
     row_number() OVER (ORDER BY MIN(w.hops)+1,e.namespace,e.target) AS admission
   FROM walk w JOIN edges e ON e.namespace=w.namespace AND e.source=w.service
   LEFT JOIN recurring.walk seen ON seen.namespace=e.namespace AND seen.service=e.target
   CROSS JOIN settings
   WHERE seen.service IS NULL AND w.hops<settings.depth
   GROUP BY e.namespace,e.target
 ) candidates
 WHERE admission <= (SELECT nodes FROM settings) - (SELECT count(*) FROM recurring.walk)
),
omitted AS (
 SELECT w.hops FROM edges e JOIN walk w ON e.namespace=w.namespace AND e.source=w.service
 LEFT JOIN walk seen ON seen.namespace=e.namespace AND seen.service=e.target
 WHERE seen.service IS NULL
),
bounds AS (
 SELECT
   EXISTS(SELECT 1 FROM omitted) OR (SELECT count(*) FROM root_namespaces) > (SELECT count(*) FROM walk WHERE hops=0) AS truncated,
   EXISTS(SELECT 1 FROM omitted WHERE hops >= (SELECT depth FROM settings)) AS depth_limit,
   (SELECT count(*) FROM walk) >= (SELECT nodes FROM settings) AND
     (EXISTS(SELECT 1 FROM omitted WHERE hops < (SELECT depth FROM settings)) OR (SELECT count(*) FROM root_namespaces) > (SELECT count(*) FROM walk WHERE hops=0)) AS node_limit
)
SELECT namespace,service,hops,truncated,depth_limit,node_limit
FROM walk CROSS JOIN bounds ORDER BY hops,namespace,service`

func (s *Service) Dependencies(ctx context.Context, scope Scope, options DependencyOptions) (Result[Dependencies], error) {
	// Materializing the full scoped edge set also needs a bound, independent of
	// the accumulated node budget and the transport's caller deadline.
	ctx, cancel := context.WithTimeout(ctx, dependencyDeadline)
	defer cancel()
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[Dependencies]{}, err
	}
	options.Service = strings.TrimSpace(options.Service)
	if options.Direction == "" {
		options.Direction = "downstream"
	}
	if options.MaxDepth == 0 {
		options.MaxDepth = 8
	}
	if options.MaxNodes == 0 {
		options.MaxNodes = 100
	}
	if options.Service == "" || len(options.Service) > 512 || (options.Direction != "upstream" && options.Direction != "downstream") || options.MaxDepth < 1 || options.MaxDepth > 32 || options.MaxNodes < 1 || options.MaxNodes > 500 {
		return Result[Dependencies]{}, fmt.Errorf("%w: dependencies require a service, upstream/downstream direction, depth 1–32 and nodes 1–500", ErrInvalidScope)
	}
	rows, err := s.db.QueryContext(ctx, dependencyQuery, options.Service, options.Direction, options.MaxDepth, options.MaxNodes,
		scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Start, scope.End, scope.Namespace, scope.Namespace)
	if err != nil {
		return Result[Dependencies]{}, fmt.Errorf("query dependencies: %w", err)
	}
	defer rows.Close()
	data := Dependencies{Service: options.Service, Direction: options.Direction, MaxDepth: options.MaxDepth, MaxNodes: options.MaxNodes, Nodes: []DependencyNode{}}
	for rows.Next() {
		var node DependencyNode
		if err := rows.Scan(&node.Namespace, &node.Service, &node.Hops, &data.Truncated, &data.DepthLimitReached, &data.NodeLimitReached); err != nil {
			return Result[Dependencies]{}, fmt.Errorf("scan dependencies: %w", err)
		}
		data.Nodes = append(data.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		return Result[Dependencies]{}, err
	}
	provenance := s.provenanceFor(scope, "edge_rollup")
	provenance.Complete = !data.Truncated
	return Result[Dependencies]{Schema: DependenciesSchema, Summary: fmt.Sprintf("%d services in %s dependencies of %s", len(data.Nodes), data.Direction, data.Service), Data: data, Provenance: provenance}, nil
}
