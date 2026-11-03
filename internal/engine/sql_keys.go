package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
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
//
// The dedicated columns are part of the file layout, so the outermost SELECT
// excludes them from its stars: users see the attributes and resource maps.
// Stars inside expressions and COLUMNS(*) exclude them at every level,
// COLUMNS patterns that match them and whole-row references to relations
// that carry them are rejected.

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
	maps   map[string]keyMapSource // by lower-case column name
	open   bool                    // its other columns are unknown and may shadow these
	hidden map[string]bool         // dedicated columns it passes on
}

func (r *keyRelation) hide(names map[string]bool, excluded map[string]bool) {
	for name := range names {
		if !excluded[name] {
			if r.hidden == nil {
				r.hidden = map[string]bool{}
			}
			r.hidden[name] = true
		}
	}
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
	shadow    map[string]bool // lambda parameters, which bind before columns and relations
}

// shadowed reports whether a lambda parameter in s or an enclosing scope
// below the binding scope hides name.
func (s *keyScope) shadowed(name string) bool {
	return s.shadow[strings.ToLower(name)]
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
			if len(qualifier) == 1 && scope.shadowed(name) {
				return keyMapSource{}, false
			}
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
		if scope.shadowed(column) {
			return keyMapSource{}, false
		}
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
	err      error
	selects  map[uintptr]*keyScope // each SELECT node's FROM scope
}

// namedItem keeps a rewritten select item's original expression: DuckDB names
// an unaliased result column after its expression, and the rewrite must not
// rename results.
type namedItem struct {
	item, original map[string]any
}

// rewriteKeys rewrites the subscripts of one parsed statement in place.
func rewriteKeys(ctx context.Context, db snapshotSQL, statement map[string]any) error {
	r := &keyRewriter{selects: map[uintptr]*keyScope{}}
	r.queryAt(statement, nil, true)
	if r.err != nil {
		return r.err
	}
	return r.name(ctx, db)
}

func signalRelation(s *telemetry.Signal) *keyRelation {
	rel := &keyRelation{maps: map[string]keyMapSource{
		"attributes": {signal: s},
		"resource":   {signal: s},
	}, hidden: map[string]bool{}}
	for _, k := range s.Keys() {
		rel.hidden[k.Column] = true
	}
	return rel
}

// query analyzes a query node and returns the relation it produces.
func (r *keyRewriter) query(node map[string]any, outer *keyScope) *keyRelation {
	return r.queryAt(node, outer, false)
}

// queryAt analyzes a query node; outermost marks the statement's result.
func (r *keyRewriter) queryAt(node map[string]any, outer *keyScope, outermost bool) *keyRelation {
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
		return r.selectNode(node, scope, outermost)
	case "SET_OPERATION_NODE":
		var parts []*keyRelation
		for _, key := range []string{"left", "right", "children"} {
			switch child := node[key].(type) {
			case map[string]any:
				parts = append(parts, r.queryAt(child, scope, outermost))
			case []any:
				for _, c := range child {
					if c, ok := c.(map[string]any); ok {
						parts = append(parts, r.queryAt(c, scope, outermost))
					}
				}
			}
		}
		r.expr(node["modifiers"], &keyScope{parent: scope})
		byName, _ := node["setop_type"].(string)
		out := unionRelation(parts, strings.HasSuffix(byName, "BY_NAME"))
		if out == nil {
			// Branches pass different dedicated columns at the same
			// positions, so none of them passes any on.
			r.hideBranches(node)
			out = unionRelation(parts, true)
			out.hidden = nil
		}
		return out
	case "RECURSIVE_CTE_NODE":
		// The anchor names the columns; the recursive step reads the CTE.
		left, _ := node["left"].(map[string]any)
		anchor := r.queryAt(left, scope, outermost)
		if right, ok := node["right"].(map[string]any); ok {
			r.queryAt(right, scope, outermost)
		}
		r.expr(node["modifiers"], &keyScope{parent: scope})
		return anchor
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
// DuckDB names the result after the first branch, or by name, where it
// carries every branch's dedicated columns. A positional union whose
// branches pass different dedicated columns returns nil.
func unionRelation(parts []*keyRelation, byName bool) *keyRelation {
	out := &keyRelation{maps: map[string]keyMapSource{}}
	if len(parts) == 0 {
		out.open = true
		return out
	}
	for _, p := range parts {
		if !byName && !maps.Equal(p.hidden, parts[0].hidden) {
			return nil
		}
		out.hide(p.hidden, nil)
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

// hideBranches excludes the dedicated columns from the stars of every
// branch of a set operation.
func (r *keyRewriter) hideBranches(node map[string]any) {
	switch node["type"] {
	case "SELECT_NODE":
		if scope := r.selects[reflect.ValueOf(node).Pointer()]; scope != nil {
			excludeHidden(node["select_list"], scope)
		}
	case "SET_OPERATION_NODE", "RECURSIVE_CTE_NODE":
		for _, key := range []string{"left", "right", "children"} {
			switch child := node[key].(type) {
			case map[string]any:
				r.hideBranches(child)
			case []any:
				for _, c := range child {
					if c, ok := c.(map[string]any); ok {
						r.hideBranches(c)
					}
				}
			}
		}
	}
}

func (r *keyRewriter) selectNode(node map[string]any, outer *keyScope, outermost bool) *keyRelation {
	scope := &keyScope{parent: outer, relations: map[string]*keyRelation{}}
	r.selects[reflect.ValueOf(node).Pointer()] = scope
	r.from(node["from_table"], scope)
	for key, child := range node {
		switch key {
		case "from_table", "cte_map":
		case "select_list":
			items, _ := child.([]any)
			for _, item := range items {
				item, _ := item.(map[string]any)
				alias, _ := item["alias"].(string)
				if item["class"] == "STAR" && item["columns"] != true {
					// A plain star passes the columns on; its modifiers are
					// expressions.
					r.expr(item["replace_list"], scope)
					r.expr(item["expr"], scope)
					continue
				}
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
	out := r.output(node["select_list"], scope)
	if outermost {
		excludeHidden(node["select_list"], scope)
	}
	return out
}

// excludeHidden adds the dedicated columns each plain star of a select list
// passes on to its exclude list.
func excludeHidden(value any, scope *keyScope) {
	items, _ := value.([]any)
	for _, item := range items {
		item, _ := item.(map[string]any)
		if item["class"] == "STAR" && item["columns"] != true && item["expr"] == nil {
			excludeStar(item, scope)
		}
	}
}

// starHidden lists the dedicated columns a star would expand to.
func starHidden(star map[string]any, scope *keyScope) []string {
	hidden := &keyRelation{}
	excluded := starExclusions(star)
	for _, rel := range starRelations(star, scope) {
		if rel != nil {
			hidden.hide(rel.hidden, excluded)
		}
	}
	names := make([]string, 0, len(hidden.hidden))
	for name := range hidden.hidden {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func excludeStar(star map[string]any, scope *keyScope) {
	names := starHidden(star, scope)
	if len(names) == 0 {
		return
	}
	list, _ := star["exclude_list"].([]any)
	for _, name := range names {
		list = append(list, name)
	}
	star["exclude_list"] = list
}

// expressionStar hides the dedicated columns from a star inside an
// expression, or from COLUMNS. A COLUMNS pattern cannot carry an exclude
// list, so it becomes the equivalent lambda over a star that excludes them:
// DuckDB matches a COLUMNS pattern as regexp_matches does, anywhere in the
// name and case-sensitively. An aliased pattern may rename by its groups, so
// it stays a pattern and is rejected if it matches a dedicated column. A
// lambda filters the star it is given, which excludes them itself.
func (r *keyRewriter) expressionStar(star map[string]any, scope *keyScope) {
	pattern, _ := star["expr"].(map[string]any)
	if pattern == nil {
		excludeStar(star, scope)
		return
	}
	names := starHidden(star, scope)
	if len(names) == 0 || r.err != nil {
		return
	}
	literal, _ := pattern["literal"].(map[string]any)
	text, _ := literal["text"].(string)
	if pattern["class"] == "CONSTANT" && literal["kind"] == "STRING" {
		if alias, _ := star["alias"].(string); alias == "" {
			star["expr"] = patternFilter(star, pattern)
			return
		}
		re, err := regexp.Compile(text)
		if err != nil {
			r.err = fmt.Errorf("COLUMNS pattern %q is not available to telemetry SQL", text)
			return
		}
		for _, name := range names {
			if re.MatchString(name) {
				r.err = fmt.Errorf("COLUMNS pattern %q matches internal column %q; use a pattern that leaves out names starting with _", text, name)
				return
			}
		}
		return
	}
	if !containsStar(pattern) {
		r.err = errors.New("this COLUMNS expression is not available to telemetry SQL; use *, a pattern or a lambda")
	}
}

// patternFilter writes COLUMNS('re') as COLUMNS(list_filter(*, lambda c:
// regexp_matches(c, 're'))); expr then excludes the dedicated columns from
// the inner star.
func patternFilter(star, pattern map[string]any) map[string]any {
	param := func() map[string]any {
		return map[string]any{"class": "COLUMN_REF", "type": "COLUMN_REF", "alias": "", "column_names": []any{"__fanout_column"}}
	}
	function := func(name string, args ...map[string]any) map[string]any {
		arguments := make([]any, len(args))
		for i, a := range args {
			arguments[i] = map[string]any{"expression": a, "name": ""}
		}
		return map[string]any{"class": "FUNCTION", "type": "FUNCTION", "alias": "", "function_name": name, "schema": "", "catalog": "",
			"arguments": arguments, "filter": nil, "order_bys": map[string]any{"type": "ORDER_MODIFIER", "orders": []any{}},
			"distinct": false, "is_operator": false, "export_state": false}
	}
	inner := map[string]any{"class": "STAR", "type": "STAR", "alias": "", "columns": false, "expr": nil, "relation_name": star["relation_name"],
		"exclude_list": []any{}, "qualified_exclude_list": []any{}, "replace_list": []any{}, "rename_list": []any{}}
	lambda := map[string]any{"class": "LAMBDA", "type": "LAMBDA", "alias": "", "syntax_type": "LAMBDA_KEYWORD", "lhs": param(),
		"expr": function("regexp_matches", param(), pattern)}
	return function("list_filter", inner, lambda)
}

func containsStar(value any) bool {
	switch v := value.(type) {
	case []any:
		return slices.ContainsFunc(v, containsStar)
	case map[string]any:
		if v["class"] == "STAR" {
			return true
		}
		for _, child := range v {
			if containsStar(child) {
				return true
			}
		}
	}
	return false
}

// rowReference rejects a bare reference to a FROM binding that carries
// dedicated columns: DuckDB reads it as the whole row, as a struct.
func (r *keyRewriter) rowReference(node map[string]any, scope *keyScope) {
	names := stringList(node["column_names"])
	if len(names) != 1 || r.err != nil || signalColumn(names[0]) {
		return
	}
	name := strings.ToLower(names[0])
	for s := scope; s != nil; s = s.parent {
		if s.shadowed(name) {
			return
		}
		if rel, ok := s.relations[name]; ok {
			if rel != nil && len(rel.hidden) > 0 {
				r.err = fmt.Errorf("row reference %q is not available to telemetry SQL; select its columns", names[0])
			}
			return
		}
	}
}

// signalColumn reports whether a name is a column of some signal relation,
// which DuckDB binds before a row reference.
func signalColumn(name string) bool {
	for _, s := range telemetry.Signals {
		if _, ok := s.Column(name); ok {
			return true
		}
		if _, ok := s.Field(name); ok {
			return true
		}
	}
	return false
}

func starExclusions(star map[string]any) map[string]bool {
	excluded := map[string]bool{}
	for _, key := range []string{"exclude_list", "qualified_exclude_list", "replace_list", "rename_list"} {
		collectNames(star[key], excluded)
	}
	return excluded
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
		// DuckDB binds a FROM subquery laterally: it sees the bindings to
		// its left before the enclosing query's. Renamed columns cannot pass
		// the dedicated columns on, so its stars leave them out, as a
		// statement's result does.
		subquery, _ := ref["subquery"].(map[string]any)
		node, _ := subquery["node"].(map[string]any)
		bind("", r.queryAt(node, scope, renamed))
	case "JOIN":
		r.from(ref["left"], scope)
		r.from(ref["right"], scope)
		r.expr(ref["condition"], scope)
	case "PIVOT":
		r.pivot(ref, scope)
		bind("", &keyRelation{open: true})
	default:
		for key, child := range ref {
			if key != "alias" {
				r.walk(child, scope)
			}
		}
		bind("", &keyRelation{open: true})
	}
}

// pivot keeps the dedicated columns out of a PIVOT or UNPIVOT, whose
// output carries its source's other columns: the source becomes a subquery
// that excludes them, so the pivot's own expressions read the maps.
func (r *keyRewriter) pivot(ref map[string]any, scope *keyScope) {
	source, _ := ref["source"].(map[string]any)
	inner := &keyScope{parent: scope, relations: map[string]*keyRelation{}}
	r.from(source, inner)
	star := map[string]any{"class": "STAR", "type": "STAR", "alias": "", "columns": false, "expr": nil, "relation_name": "",
		"exclude_list": []any{}, "qualified_exclude_list": []any{}, "replace_list": []any{}, "rename_list": []any{}}
	if names := starHidden(star, inner); len(names) > 0 && source != nil {
		excludeStar(star, inner)
		alias, _ := source["alias"].(string)
		if alias == "" && source["type"] == "BASE_TABLE" {
			alias, _ = source["table_name"].(string)
		}
		ref["source"] = map[string]any{"type": "SUBQUERY", "alias": alias, "sample": nil, "column_name_alias": []any{},
			"subquery": map[string]any{"named_param_map": []any{}, "node": map[string]any{
				"type": "SELECT_NODE", "modifiers": []any{}, "cte_map": map[string]any{"map": []any{}},
				"select_list": []any{star}, "from_table": source, "where_clause": nil, "group_expressions": []any{}, "group_sets": []any{},
				"aggregate_handling": "STANDARD_HANDLING", "having": nil, "sample": nil, "qualify": nil,
			}}}
	}
	opaque := &keyScope{parent: scope, relations: map[string]*keyRelation{}}
	opaque.bind("", &keyRelation{open: true})
	for key, child := range ref {
		if key != "alias" && key != "source" {
			r.walk(child, opaque)
		}
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
	case "service_rollup", "span_rollup", "edge_rollup":
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
			excluded := starExclusions(item)
			for _, rel := range starRelations(item, scope) {
				if rel == nil || rel.open {
					out.open = true
					continue
				}
				out.hide(rel.hidden, excluded)
				for name, m := range rel.maps {
					if excluded[name] {
						m = keyMapSource{}
					}
					out.add(name, m)
				}
			}
		default:
			// unnest and unpack of a struct produce columns named after its
			// fields, which may include a map name.
			if function, _ := item["function_name"].(string); item["class"] == "FUNCTION" &&
				(alias == "" || strings.EqualFold(function, "unnest") || strings.EqualFold(function, "unpack")) {
				out.open = true
				continue
			}
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

// selectScope is the nearest scope with FROM bindings, which an
// unqualified star expands.
func selectScope(scope *keyScope) *keyScope {
	for s := scope; s != nil; s = s.parent {
		if s.relations != nil {
			return s
		}
	}
	return scope
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
		case "STAR":
			r.expressionStar(v, selectScope(scope))
		case "COLUMN_REF":
			r.rowReference(v, scope)
		case "LAMBDA":
			// Parameters shadow columns and relations in the body.
			lambda := &keyScope{parent: scope, shadow: map[string]bool{}}
			var params func(any)
			params = func(value any) {
				switch p := value.(type) {
				case []any:
					for _, c := range p {
						params(c)
					}
				case map[string]any:
					if names := stringList(p["column_names"]); p["class"] == "COLUMN_REF" && len(names) == 1 {
						lambda.shadow[strings.ToLower(names[0])] = true
						return
					}
					for _, c := range p {
						params(c)
					}
				}
			}
			params(v["lhs"])
			r.expr(v["expr"], lambda)
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
