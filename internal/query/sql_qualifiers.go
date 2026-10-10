package query

import "strings"

type relationQualifierBinding struct {
	schema            string
	unaliasedRelation bool
}

type relationQualifierScope struct {
	parent *relationQualifierScope
	names  map[string]relationQualifierBinding
}

func (s *relationQualifierScope) resolves(schema, name string) bool {
	for current := s; current != nil; current = current.parent {
		if binding, ok := current.names[strings.ToLower(name)]; ok {
			return binding.unaliasedRelation && (binding.schema == schema || binding.schema == "" && schema == "main")
		}
	}
	return false
}

func (s *relationQualifierScope) add(name string, binding relationQualifierBinding) {
	key := strings.ToLower(name)
	if _, exists := s.names[key]; exists {
		binding = relationQualifierBinding{} // Preserve native rejection of ambiguous names.
	}
	s.names[key] = binding
}

// Collect only the current FROM bindings, before replacing any physical table.
// Aliased TableRefs shadow outer names without exposing their inner bindings.
func (s *relationQualifierScope) collect(value any, relations map[string]string) {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			s.collect(child, relations)
		}
	case map[string]any:
		kind, _ := node["type"].(string)
		if kind == "SELECT_NODE" {
			return
		}
		alias, _ := node["alias"].(string)
		_, tableRef := node["sample"]
		if tableRef && alias != "" {
			s.add(alias, relationQualifierBinding{})
			return
		}
		if kind == "BASE_TABLE" {
			name, _ := node["table_name"].(string)
			schema, _ := node["schema_name"].(string)
			catalog, _ := node["catalog_name"].(string)
			key := strings.ToLower(name)
			if schema != "" && schema != "main" {
				key = schema + "." + key
			}
			_, allowed := relations[key]
			allowed = allowed && catalog == "" && (schema == "" || schema == "main" || schema == "telemetry")
			s.add(name, relationQualifierBinding{schema: schema, unaliasedRelation: allowed})
			return
		}
		if kind == "SUBQUERY" {
			return
		}
		for _, child := range node {
			s.collect(child, relations)
		}
	}
}

// NormalizeLogQualifiers binds approved schema-qualified log columns before
// the panel replaces logs with its redacted projection. Snapshot compilation
// uses the same lexical resolver for every relation it replaces.
func NormalizeLogQualifiers(node map[string]any) {
	normalizeRelationQualifiers(node, map[string]string{"logs": "", "telemetry.logs": ""})
}

func normalizeRelationQualifiers(node map[string]any, relations map[string]string) {
	walkRelationQualifiers(node, nil, relations)
}

func walkRelationQualifiers(value any, scope *relationQualifierScope, relations map[string]string) {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			walkRelationQualifiers(child, scope, relations)
		}
	case map[string]any:
		kind, _ := node["type"].(string)
		if kind == "SELECT_NODE" {
			scope = &relationQualifierScope{parent: scope, names: map[string]relationQualifierBinding{}}
			scope.collect(node["from_table"], relations)
		} else if _, hasFrom := node["from_table"]; hasFrom {
			return // An unfamiliar query scope must not capture an outer binding.
		}
		if node["class"] == "COLUMN_REF" {
			names, _ := node["column_names"].([]any)
			if len(names) == 3 {
				schema, schemaOK := names[0].(string)
				table, tableOK := names[1].(string)
				if schemaOK && tableOK && (schema == "main" || schema == "telemetry") && scope.resolves(schema, table) {
					node["column_names"] = names[1:]
				}
			}
			return
		}
		if ctes, ok := node["cte_map"].(map[string]any); ok {
			entries, _ := ctes["map"].([]any)
			for _, entry := range entries {
				definition, _ := entry.(map[string]any)
				walkRelationQualifiers(definition["value"], nil, relations)
			}
		}
		for key, child := range node {
			if key != "cte_map" {
				walkRelationQualifiers(child, scope, relations)
			}
		}
	}
}
