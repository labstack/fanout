package panel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/query"
)

var (
	windowMacro = regexp.MustCompile(`\$__window\(\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)
	bucketMacro = regexp.MustCompile(`\$__bucket\(\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)
	macroToken  = regexp.MustCompile(`\$__([A-Za-z_]+)`)
)

// expandMacros rewrites $__window(col) into the binding the AGENTS.md
// timestamp rule requires and $__bucket(col) into a time_bucket call with the
// panel's interval. Only identifier arguments are accepted, so a macro can
// never carry SQL. Every SQL panel must filter time with $__window: the read
// window prunes files, and a panel that ignored it would silently see only
// the files that overlap the window.
func expandMacros(sql string, interval time.Duration) (string, error) {
	if !windowMacro.MatchString(sql) {
		return "", errors.New("SQL panels must filter time with $__window(time_column)")
	}
	out := windowMacro.ReplaceAllString(sql, `$1 >= $$__from::TIMESTAMP_NS::TIMESTAMPTZ_NS AND $1 < $$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS`)
	seconds := int64(interval / time.Second)
	if seconds <= 0 {
		seconds = 60
	}
	out = bucketMacro.ReplaceAllString(out, fmt.Sprintf(`time_bucket(INTERVAL '%d seconds', $1::TIMESTAMP_NS)`, seconds))
	for _, m := range macroToken.FindAllStringSubmatch(stripQuoted(out), -1) {
		if m[1] != "from" && m[1] != "to" {
			return "", fmt.Errorf("unknown macro $__%s; use $__window(column) or $__bucket(column)", m[1])
		}
	}
	return out, nil
}

// canonicalSQL parses macro-expanded panel SQL, renders it back with DuckDB,
// and requires the rendering to carry the read window. Callers bind and run
// the returned text, never the author's: a $__window inside a comment or
// string, a literal ?, or an unusual lexical form cannot survive the
// rendering. The order is expandMacros, canonicalSQL, bindParams,
// PrepareTelemetrySQL; the executor must follow it.
func canonicalSQL(ctx context.Context, parser Parser, expanded string) (string, error) {
	node, err := parser.ParseSQL(ctx, expanded)
	if err != nil {
		return "", err
	}
	query.NormalizeLogQualifiers(node)
	if err := redactSQLLogTables(ctx, parser, node, nil); err != nil {
		return "", err
	}
	rendered, err := parser.RenderSQL(ctx, node)
	if err != nil {
		return "", err
	}
	code := stripQuoted(rendered)
	if !strings.Contains(code, "$__from") || !strings.Contains(code, "$__to") {
		return "", errors.New("SQL panels must filter time with $__window(time_column)")
	}
	return rendered, nil
}

// Replace physical logs references with a redacted relation under the same
// alias. Work on DuckDB's AST, never string substitutions: literals and CTE
// names keep their meaning, and PrepareTelemetrySQL still validates every
// underlying relation and function after rendering. Disallowed schemas and
// catalogs stay intact so the boundary rejects them.
func redactSQLLogTables(ctx context.Context, parser Parser, value any, ctes map[string]bool) error {
	switch node := value.(type) {
	case map[string]any:
		if cteMap, ok := node["cte_map"].(map[string]any); ok {
			scope := copyCTEScope(ctes)
			entries, _ := cteMap["map"].([]any)
			for _, entry := range entries {
				entry, _ := entry.(map[string]any)
				name, _ := entry["key"].(string)
				switch strings.ToLower(name) {
				case "logs", "spans", "metrics", "service_rollup", "edge_rollup":
					return errors.New("CTE names must not shadow telemetry relations")
				}
				definition, _ := entry["value"].(map[string]any)
				if err := redactSQLLogTables(ctx, parser, definition, scope); err != nil {
					return err
				}
				scope[strings.ToLower(name)] = true
			}
			ctes = scope
		}
		kind, _ := node["type"].(string)
		if kind == "BASE_TABLE" {
			name, _ := node["table_name"].(string)
			schema, _ := node["schema_name"].(string)
			catalog, _ := node["catalog_name"].(string)
			cte := catalog == "" && ((schema == "" && ctes[strings.ToLower(name)]) || (schema == "recurring" && ctes["recurring:"+strings.ToLower(name)]))
			if strings.EqualFold(name, "logs") && !cte && catalog == "" && (schema == "" || schema == "main" || schema == "telemetry") {
				projection, err := parser.ParseSQL(ctx, "SELECT * FROM ("+redactedLogSource()+") AS logs")
				if err != nil {
					return err
				}
				replacement, ok := projection["from_table"].(map[string]any)
				if !ok {
					return errors.New("missing redacted logs projection")
				}
				if alias, _ := node["alias"].(string); alias != "" {
					replacement["alias"] = alias
				}
				// Preserve sampling and any column aliases on the original TableRef.
				for _, key := range []string{"sample", "column_name_alias"} {
					if v, ok := node[key]; ok {
						replacement[key] = v
					}
				}
				for key := range node {
					delete(node, key)
				}
				for key, child := range replacement {
					node[key] = child
				}
				return nil // Do not recursively redact the trusted inner logs reference.
			}
		}
		for key, child := range node {
			if key == "cte_map" {
				continue
			}
			scope := ctes
			if kind == "RECURSIVE_CTE_NODE" && key == "right" {
				scope = copyCTEScope(ctes)
				name, _ := node["cte_name"].(string)
				scope[strings.ToLower(name)] = true
				scope["recurring:"+strings.ToLower(name)] = true
			}
			if err := redactSQLLogTables(ctx, parser, child, scope); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := redactSQLLogTables(ctx, parser, child, ctes); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyCTEScope(ctes map[string]bool) map[string]bool {
	scope := make(map[string]bool, len(ctes)+2)
	for name, visible := range ctes {
		scope[name] = visible
	}
	return scope
}
