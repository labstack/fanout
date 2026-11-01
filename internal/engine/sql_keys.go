package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/telemetry"
	"github.com/labstack/fanout/internal/telemetry/semconv"
)

// Batch format 5 copies the standard keys into typed dedicated columns
// (proposal 0005), converted at ingest to the key's type; the column is the
// key's read value. Telemetry SQL keeps writing attributes['k'] and
// resource['k'] with a literal key, and rewriteKeys replaces each such
// subscript with the dedicated column when the map belongs to a signal
// relation whose columns are visible where the subscript binds: the relation
// itself, or a subquery or CTE that passes it through *.
//
// Every other subscript reads the map unchanged, the value as sent: keys
// outside the list, the attributes of logs and metrics, subscripts whose
// relation the AST does not tell, and subscripts of a map that a subquery or
// CTE projects without its columns.

func keyMap(k semconv.Key) string {
	if k.Scope == semconv.Span {
		return "attributes"
	}
	return "resource"
}

// internalColumn reports whether a column name is part of the file layout
// rather than the language: users write the keys, never their columns.
func internalColumn(name string) (semconv.Key, bool) {
	name = strings.ToLower(name)
	for _, k := range semconv.Keys {
		if k.Column == name {
			return k, true
		}
	}
	return semconv.Key{}, name == "_trace_hash"
}

// keyMapSource is the origin of a relation's attributes or resource column.
type keyMapSource struct {
	// signal's map, with its dedicated columns visible beside it; nil: no
	// signal's map, a map projected without its columns, or unknown.
	signal *telemetry.Signal
}

// keyRelation is what a FROM binding exposes of the signal maps.
type keyRelation struct {
	maps map[string]keyMapSource // by lower-case column name
	open bool                    // its other columns are unknown and may shadow these
}

func (r *keyRelation) add(name string, m keyMapSource) {
	name = strings.ToLower(name)
	if previous, ok := r.maps[name]; ok && previous != m {
		m = keyMapSource{}
	}
	r.maps[name] = m
}

type keyScope struct {
	parent    *keyScope
	ctes      map[string]*keyRelation
	relations map[string]*keyRelation // FROM bindings by name; nil is ambiguous
	order     []*keyRelation
}

func (s *keyScope) bind(name string, r *keyRelation) {
	key := strings.ToLower(name)
	if _, ok := s.relations[key]; ok {
		r = nil
	}
	s.relations[key] = r
	s.order = append(s.order, r)
}

func (s *keyScope) cte(name string) (*keyRelation, bool) {
	for scope := s; scope != nil; scope = scope.parent {
		if r, ok := scope.ctes[strings.ToLower(name)]; ok {
			return r, true
		}
	}
	return nil, false
}

// lookup resolves a map column reference as DuckDB's binder would, or
// reports false when the AST does not tell.
func (s *keyScope) lookup(qualifier []string, column string) (keyMapSource, bool) {
	column = strings.ToLower(column)
	if len(qualifier) > 0 {
		if len(qualifier) > 2 || len(qualifier) == 2 && !strings.EqualFold(qualifier[0], "main") && !strings.EqualFold(qualifier[0], "telemetry") {
			return keyMapSource{}, false
		}
		name := strings.ToLower(qualifier[len(qualifier)-1])
		for scope := s; scope != nil; scope = scope.parent {
			if r, ok := scope.relations[name]; ok {
				if r == nil {
					return keyMapSource{}, false
				}
				m, ok := r.maps[column]
				return m, ok
			}
		}
		return keyMapSource{}, false
	}
	for scope := s; scope != nil; scope = scope.parent {
		var found keyMapSource
		count := 0
		for _, r := range scope.order {
			if r == nil || r.open {
				return keyMapSource{}, false
			}
			if m, ok := r.maps[column]; ok {
				found = m
				count++
			}
		}
		if count == 1 {
			return found, true
		}
		if count > 1 {
			return keyMapSource{}, false
		}
	}
	return keyMapSource{}, false
}

type keyRewriter struct {
	rewrites int
	named    []namedItem
}

// namedItem keeps a rewritten select item's original expression: DuckDB names
// an unaliased result column after its expression, and the rewrite must not
// rename results.
type namedItem struct {
	item, original map[string]any
}

// rewriteKeys rewrites the subscripts of one parsed statement in place.
func rewriteKeys(ctx context.Context, db snapshotSQL, statement map[string]any) error {
	r := &keyRewriter{}
	r.query(statement, nil)
	return r.name(ctx, db)
}

func signalRelation(s *telemetry.Signal) *keyRelation {
	return &keyRelation{maps: map[string]keyMapSource{
		"attributes": {signal: s},
		"resource":   {signal: s},
	}}
}

// query analyzes a query node and returns the relation it produces.
func (r *keyRewriter) query(node map[string]any, outer *keyScope) *keyRelation {
	scope := outer
	if cteMap, _ := node["cte_map"].(map[string]any); cteMap != nil {
		entries, _ := cteMap["map"].([]any)
		if len(entries) > 0 {
			scope = &keyScope{parent: outer, ctes: map[string]*keyRelation{}}
			for _, entry := range entries {
				entry, _ := entry.(map[string]any)
				name, _ := entry["key"].(string)
				definition, _ := entry["value"].(map[string]any)
				body, _ := definition["query_node"].(map[string]any)
				scope.ctes[strings.ToLower(name)] = r.query(body, scope)
			}
		}
	}
	switch node["type"] {
	case "SELECT_NODE":
		return r.selectNode(node, scope)
	case "SET_OPERATION_NODE":
		var parts []*keyRelation
		for _, key := range []string{"left", "right", "children"} {
			switch child := node[key].(type) {
			case map[string]any:
				parts = append(parts, r.query(child, scope))
			case []any:
				for _, c := range child {
					if c, ok := c.(map[string]any); ok {
						parts = append(parts, r.query(c, scope))
					}
				}
			}
		}
		r.expr(node["modifiers"], &keyScope{parent: scope})
		return unionRelation(parts)
	}
	for key, child := range node {
		if key == "cte_map" {
			continue
		}
		r.walk(child, scope)
	}
	return &keyRelation{open: true}
}

// walk analyzes the query nodes below an unfamiliar node.
func (r *keyRewriter) walk(value any, scope *keyScope) {
	switch v := value.(type) {
	case []any:
		for _, child := range v {
			r.walk(child, scope)
		}
	case map[string]any:
		if _, ok := v["select_list"]; ok {
			r.query(v, scope)
			return
		}
		if t, _ := v["type"].(string); strings.HasSuffix(t, "_NODE") {
			r.query(v, scope)
			return
		}
		r.expr(v, &keyScope{parent: scope})
	}
}

// unionRelation keeps the maps every branch of a set operation agrees on;
// DuckDB names the result after the first branch.
func unionRelation(parts []*keyRelation) *keyRelation {
	out := &keyRelation{maps: map[string]keyMapSource{}}
	if len(parts) == 0 {
		out.open = true
		return out
	}
	for name, m := range parts[0].maps {
		for _, p := range parts[1:] {
			if p.maps[name] != m {
				m = keyMapSource{}
			}
		}
		out.maps[name] = m
	}
	for _, p := range parts {
		out.open = out.open || p.open
	}
	return out
}

func (r *keyRewriter) selectNode(node map[string]any, outer *keyScope) *keyRelation {
	scope := &keyScope{parent: outer, relations: map[string]*keyRelation{}}
	r.from(node["from_table"], scope)
	for key, child := range node {
		switch key {
		case "from_table", "cte_map":
		case "select_list":
			items, _ := child.([]any)
			for _, item := range items {
				item, _ := item.(map[string]any)
				alias, _ := item["alias"].(string)
				if alias != "" || item["class"] == "STAR" {
					r.expr(item, scope)
					continue
				}
				original, before := clone(item), r.rewrites
				r.expr(item, scope)
				if r.rewrites != before {
					r.named = append(r.named, namedItem{item, original})
				}
			}
		default:
			r.expr(child, scope)
		}
	}
	return r.output(node["select_list"], scope)
}

// from binds a FROM clause's relations into scope.
func (r *keyRewriter) from(value any, scope *keyScope) {
	ref, _ := value.(map[string]any)
	if ref == nil {
		return
	}
	alias, _ := ref["alias"].(string)
	renamed := false
	if names, _ := ref["column_name_alias"].([]any); len(names) > 0 {
		renamed = true
	}
	bind := func(name string, rel *keyRelation) {
		if renamed {
			rel = &keyRelation{open: true}
		}
		if alias != "" {
			name = alias
		}
		scope.bind(name, rel)
	}
	switch ref["type"] {
	case "EMPTY":
	case "BASE_TABLE":
		name, _ := ref["table_name"].(string)
		schema, _ := ref["schema_name"].(string)
		catalog, _ := ref["catalog_name"].(string)
		bind(name, r.table(name, schema, catalog, scope.parent))
	case "SUBQUERY":
		subquery, _ := ref["subquery"].(map[string]any)
		node, _ := subquery["node"].(map[string]any)
		bind("", r.query(node, scope.parent))
	case "JOIN":
		r.from(ref["left"], scope)
		r.from(ref["right"], scope)
		r.expr(ref["condition"], scope)
	default:
		for key, child := range ref {
			if key != "alias" {
				r.walk(child, scope.parent)
			}
		}
		bind("", &keyRelation{open: true})
	}
}

// table resolves a base table: a CTE in scope, a telemetry signal, or a
// table without signal maps.
func (r *keyRewriter) table(name, schema, catalog string, scope *keyScope) *keyRelation {
	if catalog == "" && schema == "" {
		if rel, ok := scope.cte(name); ok {
			return rel
		}
	}
	if catalog != "" || schema != "" && !strings.EqualFold(schema, "main") && !strings.EqualFold(schema, "telemetry") {
		return &keyRelation{open: true}
	}
	if s, ok := telemetry.LookupSignal(strings.ToLower(name)); ok {
		return signalRelation(s)
	}
	switch strings.ToLower(name) {
	case "resource_inventory":
		return &keyRelation{maps: map[string]keyMapSource{"resource": {}}}
	case "service_rollup", "edge_rollup":
		return &keyRelation{maps: map[string]keyMapSource{}}
	}
	return &keyRelation{open: true}
}

// output is the relation a SELECT produces: the maps its stars pass through
// with their columns, and maps it projects by name without them.
func (r *keyRewriter) output(value any, scope *keyScope) *keyRelation {
	out := &keyRelation{maps: map[string]keyMapSource{}}
	items, _ := value.([]any)
	for _, item := range items {
		item, _ := item.(map[string]any)
		alias, _ := item["alias"].(string)
		switch item["class"] {
		case "STAR":
			if item["columns"] == true || item["expr"] != nil {
				out.open = true
				continue
			}
			excluded := map[string]bool{}
			for _, key := range []string{"exclude_list", "qualified_exclude_list", "replace_list", "rename_list"} {
				collectNames(item[key], excluded)
			}
			for _, rel := range starRelations(item, scope) {
				if rel == nil || rel.open {
					out.open = true
					continue
				}
				for name, m := range rel.maps {
					if excluded[name] {
						m = keyMapSource{}
					}
					out.add(name, m)
				}
			}
		default:
			name := alias
			if names := stringList(item["column_names"]); name == "" && item["class"] == "COLUMN_REF" && len(names) > 0 {
				name = names[len(names)-1]
			}
			// A map projected by name passes on without its columns.
			if mapColumn(name) {
				out.add(name, keyMapSource{})
			}
		}
	}
	return out
}

func mapColumn(name string) bool {
	return strings.EqualFold(name, "attributes") || strings.EqualFold(name, "resource")
}

// starRelations lists the bindings a star expands.
func starRelations(star map[string]any, scope *keyScope) []*keyRelation {
	if name, _ := star["relation_name"].(string); name != "" {
		rel, ok := scope.relations[strings.ToLower(name)]
		if !ok {
			return []*keyRelation{{open: true}}
		}
		return []*keyRelation{rel}
	}
	return scope.order
}

// collectNames gathers every string below a star's modifier lists; a name
// that appears there no longer passes through unchanged.
func collectNames(value any, names map[string]bool) {
	switch v := value.(type) {
	case string:
		names[strings.ToLower(v)] = true
	case []any:
		for _, child := range v {
			collectNames(child, names)
		}
	case map[string]any:
		for _, child := range v {
			collectNames(child, names)
		}
	}
}

// expr rewrites the subscripts below an expression.
func (r *keyRewriter) expr(value any, scope *keyScope) {
	switch v := value.(type) {
	case []any:
		for _, child := range v {
			r.expr(child, scope)
		}
	case map[string]any:
		switch v["class"] {
		case "OPERATOR":
			if v["type"] == "ARRAY_EXTRACT" && r.subscript(v, scope) {
				r.rewrites++
				return
			}
		case "SUBQUERY":
			subquery, _ := v["subquery"].(map[string]any)
			if node, _ := subquery["node"].(map[string]any); node != nil {
				r.query(node, scope)
			}
			r.expr(v["child"], scope)
			return
		}
		for _, child := range v {
			r.expr(child, scope)
		}
	}
}

// subscript rewrites one map subscript in place.
func (r *keyRewriter) subscript(node map[string]any, scope *keyScope) bool {
	children, _ := node["children"].([]any)
	if len(children) != 2 {
		return false
	}
	column, _ := children[0].(map[string]any)
	key, _ := children[1].(map[string]any)
	if column["class"] != "COLUMN_REF" || key["class"] != "CONSTANT" {
		return false
	}
	literal, _ := key["literal"].(map[string]any)
	name, _ := literal["text"].(string)
	names := stringList(column["column_names"])
	if literal["kind"] != "STRING" || len(names) == 0 || !mapColumn(names[len(names)-1]) {
		return false
	}
	m, ok := scope.lookup(names[:len(names)-1], names[len(names)-1])
	if !ok || m.signal == nil {
		return false
	}
	k, ok := m.signal.Dedicated(strings.ToLower(names[len(names)-1]), name)
	if !ok {
		return false
	}
	alias, _ := node["alias"].(string)
	path := make([]any, 0, len(names))
	for _, n := range names[:len(names)-1] {
		path = append(path, n)
	}
	replacement := map[string]any{"class": "COLUMN_REF", "type": "COLUMN_REF", "alias": alias, "column_names": append(path, k.Column)}
	for field := range node {
		delete(node, field)
	}
	for field, value := range replacement {
		node[field] = value
	}
	return true
}

// name aliases each rewritten select item with the name DuckDB gives its
// original expression, rendered by DuckDB itself.
func (r *keyRewriter) name(ctx context.Context, db snapshotSQL) error {
	if len(r.named) == 0 {
		return nil
	}
	calls := make([]string, len(r.named))
	args := make([]any, len(r.named))
	for i, n := range r.named {
		statement := map[string]any{"error": false, "statements": []any{map[string]any{"node": map[string]any{
			"type": "SELECT_NODE", "modifiers": []any{}, "cte_map": map[string]any{"map": []any{}},
			"select_list": []any{n.original}, "from_table": map[string]any{"type": "EMPTY", "alias": "", "sample": nil},
			"where_clause": nil, "group_expressions": []any{}, "group_sets": []any{}, "aggregate_handling": "STANDARD_HANDLING",
			"having": nil, "sample": nil, "qualify": nil,
		}, "named_param_map": []any{}}}}
		encoded, err := json.Marshal(statement)
		if err != nil {
			return err
		}
		calls[i] = "json_deserialize_sql(?::JSON)::VARCHAR"
		args[i] = string(encoded)
	}
	rendered := make([]string, len(r.named))
	targets := make([]any, len(r.named))
	for i := range rendered {
		targets[i] = &rendered[i]
	}
	if err := db.QueryRowContext(ctx, "SELECT "+strings.Join(calls, ","), args...).Scan(targets...); err != nil {
		return fmt.Errorf("name rewritten columns: %w", err)
	}
	for i, n := range r.named {
		name, ok := strings.CutPrefix(rendered[i], "SELECT ")
		if !ok {
			return fmt.Errorf("name rewritten columns: unexpected rendering %q", rendered[i])
		}
		n.item["alias"] = name
	}
	return nil
}

func stringList(value any) []string {
	list, _ := value.([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

func clone(node map[string]any) map[string]any {
	encoded, _ := json.Marshal(node)
	var out map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	_ = decoder.Decode(&out)
	return out
}
