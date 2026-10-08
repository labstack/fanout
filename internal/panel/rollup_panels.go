package panel

import (
	"context"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/observability"
)

type RollupReader interface {
	Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error)
	Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error)
	Performance(context.Context, observability.Scope, observability.PerformanceOptions) (observability.Result[observability.Performance], error)
}

func (e *Executor) SetRollupReader(reader RollupReader) { e.rollups = reader }
func rollupFilterNode(n any) map[string]any {
	v, _ := n.(map[string]any)
	return v
}
func rollupFilterValue(ctx context.Context, parser Parser, f Filter, scope Scope) (string, string, error) {
	tree, err := parser.ParseSQL(ctx, "SELECT 1 FROM spans WHERE ("+f.Source+")")
	if err != nil {
		return "", "", err
	}
	// Parse the author's source, not the canonical filter with its injected
	// parameter casts. Explicit casts cannot be represented by a string scope.
	node := rollupFilterNode(tree["where_clause"])
	if node == nil || node["class"] != "COMPARISON" || node["type"] != "COMPARE_EQUAL" {
		return "", "", fmt.Errorf("rollup panels support equality filters on namespace or service")
	}
	left, right := rollupFilterNode(node["left"]), rollupFilterNode(node["right"])
	if left["class"] != "COLUMN_REF" {
		left, right = right, left
	}
	field := fieldText(left)
	if left["class"] != "COLUMN_REF" || (field != "service" && field != "namespace") {
		return "", "", fmt.Errorf("rollup panels support equality filters on namespace or service")
	}
	switch right["class"] {
	case "CONSTANT":
		value := constantText(right)
		if value == "" {
			return "", "", fmt.Errorf("scope equality requires a nonempty string")
		}
		if value != strings.TrimSpace(value) {
			return "", "", fmt.Errorf("rollup equality cannot preserve surrounding whitespace")
		}
		return field, value, nil
	case "PARAMETER":
		name, _ := right["identifier"].(string)
		v, ok := scope.Vars[name]
		if !ok || v.All || len(v.Values) != 1 || v.Values[0] == "" {
			return "", "", fmt.Errorf("rollup equality needs one selected value")
		}
		if v.Values[0] != strings.TrimSpace(v.Values[0]) {
			return "", "", fmt.Errorf("rollup equality cannot preserve surrounding whitespace")
		}
		return field, v.Values[0], nil
	default:
		return "", "", fmt.Errorf("rollup equality needs a literal or variable")
	}
}
func (e *Executor) runRollupPanel(ctx context.Context, p *Panel, filters []Filter, scope Scope) (*Frame, string, error) {
	if e.rollups == nil {
		return nil, "", fmt.Errorf("rollup reader unavailable")
	}
	request := observability.Scope{Start: scope.Start, End: scope.End}
	seen := map[string]string{}
	for _, f := range filters {
		if scope.dropped(f) {
			continue
		}
		field, value, err := rollupFilterValue(ctx, e.engine, f, scope)
		if err != nil {
			return nil, "", err
		}
		if prior, ok := seen[field]; ok && prior != value {
			columns := healthColumns()
			if p.Viz == "service_map" {
				columns = mapColumns()
			}
			return newFrame(columns), "observability: conflicting scope filters", nil
		}
		seen[field] = value
		if field == "namespace" {
			request.Namespace = value
		} else {
			request.Service = value
		}
	}
	limit := 400
	if p.Query.Limit > 0 {
		limit = min(400, p.Query.Limit)
	}
	if p.Viz == "health" {
		result, err := e.rollups.Overview(ctx, request, limit)
		if err != nil {
			return nil, "", err
		}
		f := newFrame(healthColumns())
		f.Truncated = len(result.Data.Services) == limit
		performance, err := e.rollups.Performance(ctx, request, observability.PerformanceOptions{Service: request.Service, Limit: 1})
		if err != nil {
			return nil, "", err
		}
		f.Health = &HealthFrame{Health: string(result.Data.Health), Counts: result.Data.Counts, TotalSpans: result.Data.TotalSpans, ErrorRate: result.Data.ErrorRate * 100, ServiceCount: result.Data.ServiceCount, ErrorTrend: []float64{}}
		for _, point := range performance.Data.Points {
			f.Health.ErrorTrend = append(f.Health.ErrorTrend, point.ErrorRate*100)
		}
		for _, s := range result.Data.Services {
			appendPanelRow(f, s.Service, string(s.Health), float64(s.Spans), s.ErrorRate*100, s.P50MS, s.P95MS, float64(s.LogCount), float64(s.MetricCount))
		}
		return f, "observability.Service.Overview", nil
	}
	result, err := e.rollups.Topology(ctx, request, limit)
	if err != nil {
		return nil, "", err
	}
	f := newFrame(mapColumns())
	f.Truncated = len(result.Data.Nodes) == limit || len(result.Data.Edges) == limit
	for _, n := range result.Data.Nodes {
		appendPanelRow(f, "node", n.Service, "", "", "", nil, nil, n.ErrorRate*100, string(n.Health), n.P95MS, float64(n.Spans))
	}
	for _, edge := range result.Data.Edges {
		appendPanelRow(f, "edge", "", edge.Caller, edge.Callee, edge.Type, float64(edge.Calls), edge.AverageMS, edge.ErrorRate*100, "", nil, nil)
	}
	return f, "observability.Service.Topology", nil
}
func mapColumns() []Column {
	return []Column{{Name: "kind", Type: "string", Role: "dimension"}, {Name: "service", Type: "string", Role: "dimension"}, {Name: "caller", Type: "string", Role: "dimension"}, {Name: "callee", Type: "string", Role: "dimension"}, {Name: "edge_type", Type: "string", Role: "dimension"}, {Name: "calls", Type: "number", Role: "measure", Unit: "count"}, {Name: "average_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "error_rate", Type: "number", Role: "measure", Unit: "percent"}, {Name: "health", Type: "string", Role: "dimension"}, {Name: "p95_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "spans", Type: "number", Role: "measure", Unit: "count"}}
}
func healthColumns() []Column {
	return []Column{{Name: "service", Type: "string", Role: "dimension"}, {Name: "health", Type: "string", Role: "dimension"}, {Name: "spans", Type: "number", Role: "measure", Unit: "count"}, {Name: "error_rate", Type: "number", Role: "measure", Unit: "percent"}, {Name: "p50_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "p95_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "log_count", Type: "number", Role: "measure", Unit: "count"}, {Name: "metric_count", Type: "number", Role: "measure", Unit: "count"}}
}
func appendPanelRow(f *Frame, values ...any) {
	for i, v := range values {
		f.Values[i] = append(f.Values[i], v)
	}
	f.Rows++
}
func validateRollupPanel(p *Panel, path string, problems *Problems) {
	if p.Viz != "health" && p.Viz != "service_map" {
		return
	}
	if p.Query == nil || p.Query.From != "spans" {
		problems.add(path+".query", "health and service_map require a structured spans scope")
		return
	}
	if len(p.Query.Measures) > 0 || len(p.Query.By) > 0 || p.Query.Bucket != "" {
		problems.add(path+".query", "rollup panels use scope filters and a limit, without measures, dimensions or buckets")
	}
	if p.Query.Sort != "" || p.Options != nil && strings.TrimSpace(p.Options.Style) != "" {
		problems.add(path+".options", "rollup panels have fixed health and graph encodings")
	}
}
