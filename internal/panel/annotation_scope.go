package panel

import (
	"context"
	"fmt"
	"strings"
)

type AnnotationService struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
}
type AnnotationMatch struct {
	Services        []AnnotationService `json:"services"`
	NamespaceScoped bool                `json:"namespace_scoped,omitempty"`
	Limited         bool                `json:"limited,omitempty"`
}

func annotationOnly(n *SQLExpr) bool {
	return everySQLExpression(n, func(v *SQLExpr) bool {
		return v.Class != "COLUMN_REF" || fieldText(v) == "service" || fieldText(v) == "namespace"
	})
}
func annotationNamespace(n *SQLExpr) bool {
	return !everySQLExpression(n, func(v *SQLExpr) bool {
		return v.Class != "COLUMN_REF" || fieldText(v) != "namespace"
	})
}

// exact=false means another signal field was omitted. AND can retain known
// necessary restrictions; OR and NOT containing unknown fields cannot.
func annotationPredicate(n any, expr *SQLExpr) (any, *SQLExpr, bool) {
	if annotationOnly(expr) {
		return n, expr, true
	}
	v, ok := n.(map[string]any)
	if !ok || v["class"] != "CONJUNCTION" || v["type"] != "CONJUNCTION_AND" {
		return nil, nil, false
	}
	children, _ := v["children"].([]any)
	kept := []any{}
	typed := []*SQLExpr{}
	exact := true
	for i, child := range children {
		projected, projectedExpr, complete := annotationPredicate(child, expr.Children[i])
		exact = exact && complete
		if projected != nil {
			kept = append(kept, projected)
			typed = append(typed, projectedExpr)
		}
	}
	if len(kept) == 0 {
		return nil, nil, false
	}
	if len(kept) == 1 {
		return kept[0], typed[0], exact
	}
	clone := map[string]any{}
	for key, value := range v {
		clone[key] = value
	}
	clone["children"] = kept
	projectedExpr := *expr
	projectedExpr.Children = typed
	return clone, &projectedExpr, exact
}

type AnnotationFilter struct {
	Filter          Filter
	NamespaceScoped bool
}

func projectAnnotationFilter(ctx context.Context, parser Parser, sig *signal, vars map[string]Variable, f Filter) (*AnnotationFilter, error) {
	tree, err := parser.ParseSQL(ctx, "SELECT 1 FROM "+sig.name+" WHERE ("+f.Source+")")
	if err != nil {
		return nil, err
	}
	projected, expr, _ := annotationPredicate(tree["where_clause"], SQLExpression(tree["where_clause"]))
	if projected == nil {
		return nil, nil
	}
	namespaceScoped := annotationNamespace(expr)
	tree["where_clause"] = projected
	rendered, err := parser.RenderSQL(ctx, tree)
	if err != nil {
		return nil, err
	}
	prefix := "SELECT 1 FROM " + sig.name + " WHERE "
	if !strings.HasPrefix(rendered, prefix) {
		return nil, fmt.Errorf("annotation scope could not be rendered")
	}
	checked, err := checkFilter(ctx, parser, sig, vars, strings.TrimPrefix(rendered, prefix))
	if err != nil {
		return nil, err
	}
	return &AnnotationFilter{Filter: checked, NamespaceScoped: namespaceScoped}, nil
}
func (e *Executor) annotationScope(ctx context.Context, p *Panel, filters []AnnotationFilter, scope Scope) (*AnnotationMatch, error) {
	clauses := []string{}
	args := []any{}
	namespaceScoped := false
	for _, projected := range filters {
		f := projected.Filter
		if scope.dropped(f) {
			continue
		}
		empty := false
		for _, name := range f.Params {
			if v := scope.Vars[name]; !v.All && len(v.Values) == 0 {
				empty = true
			}
		}
		if empty {
			clauses = append(clauses, "FALSE")
			continue
		}
		predicate, _, bound, err := bindParams(f.Expr, scope.lookup(f))
		if err != nil {
			return nil, err
		}
		namespaceScoped = namespaceScoped || projected.NamespaceScoped
		clauses = append(clauses, "("+predicate+")")
		args = append(args, bound...)
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	out := &AnnotationMatch{Services: []AnnotationService{}, NamespaceScoped: namespaceScoped}
	rows, err := e.engine.QueryContext(ctx, `WITH keys AS (
 SELECT namespace,service FROM version_rollup UNION SELECT namespace,service FROM anomaly_log UNION SELECT namespace,service FROM service_rollup)
SELECT DISTINCT namespace,service FROM keys WHERE `+strings.Join(clauses, " AND ")+` ORDER BY namespace,service LIMIT 1001`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item AnnotationService
		if err := rows.Scan(&item.Namespace, &item.Service); err != nil {
			return nil, err
		}
		out.Services = append(out.Services, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Services) > 1000 {
		out.Services = out.Services[:1000]
		out.Limited = true
	}
	return out, nil
}
