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

func annotationOnly(n any) bool {
	switch v := n.(type) {
	case map[string]any:
		if v["class"] == "COLUMN_REF" {
			name := fieldText(v)
			return name == "service" || name == "namespace"
		}
		for _, child := range v {
			if !annotationOnly(child) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !annotationOnly(child) {
				return false
			}
		}
	}
	return true
}
func annotationNamespace(n any) bool {
	switch v := n.(type) {
	case map[string]any:
		if v["class"] == "COLUMN_REF" && fieldText(v) == "namespace" {
			return true
		}
		for _, child := range v {
			if annotationNamespace(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if annotationNamespace(child) {
				return true
			}
		}
	}
	return false
}

// exact=false means another signal field was omitted. AND can retain known
// necessary restrictions; OR and NOT containing unknown fields cannot.
func annotationPredicate(n any) (any, bool) {
	if annotationOnly(n) {
		return n, true
	}
	v, ok := n.(map[string]any)
	if !ok || v["class"] != "CONJUNCTION" || v["type"] != "CONJUNCTION_AND" {
		return nil, false
	}
	children, _ := v["children"].([]any)
	kept := []any{}
	exact := true
	for _, child := range children {
		projected, complete := annotationPredicate(child)
		exact = exact && complete
		if projected != nil {
			kept = append(kept, projected)
		}
	}
	if len(kept) == 0 {
		return nil, false
	}
	if len(kept) == 1 {
		return kept[0], exact
	}
	clone := map[string]any{}
	for key, value := range v {
		clone[key] = value
	}
	clone["children"] = kept
	return clone, exact
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
	projected, _ := annotationPredicate(tree["where_clause"])
	if projected == nil {
		return nil, nil
	}
	namespaceScoped := annotationNamespace(projected)
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
