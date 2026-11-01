package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// blockedSQLFunctions are built-ins telemetry SQL must not call: they mutate
// state, read files or settings, or return catalog contents and file paths.
// sql_functions_test.go fails when an engine upgrade adds or changes built-ins,
// so each new one is reviewed against this list.
var blockedSQLFunctions = map[string]bool{
	"nextval": true, "setval": true, "setseed": true,
	"query": true, "query_table": true, "json_execute_serialized_sql": true,
	"current_setting": true, "getvariable": true, "getenv": true,
	"read_csv": true, "read_csv_auto": true, "read_json": true, "read_json_auto": true,
	"read_text": true, "read_blob": true, "read_parquet": true, "read_single_parquet_file": true,
	"glob": true, "http_get": true, "write_file": true, "write_log": true,
	"current_query": true, "current_query_id": true,
	"pg_get_viewdef": true, "pg_get_constraintdef": true, "format_type": true, "get_block_size": true,
	// Edit distances and Jaro similarities cost the product of their inputs'
	// lengths per row; see patternFunctions.
	"levenshtein": true, "editdist3": true, "damerau_levenshtein": true,
	"jaro_similarity": true, "jaro_winkler_similarity": true,
}

// DuckDB checks for interrupts only between chunks of up to 2,048 rows, so
// one chunk's work must stay within a deadline. Linear per-row work is
// bounded by the memory the chunk's values occupy, which the memory limit
// caps; work that grows with the product of two input sizes is not. Measured
// per chunk of 20,000-character values: levenshtein 21 minutes and
// damerau_levenshtein 60; jaro_similarity, linear on varied text, took
// 146 s on 200,000-character repeated values (all blocked: every argument is
// a value). A computed
// 10,000-character LIKE pattern, compiled per row, 2.5 minutes and a computed
// SIMILAR TO pattern 25; a literal 10,000-byte regular expression 13 to 27
// minutes, a 1 KiB one about a second, a 256-byte one 0.06 s. Patterns must
// therefore be literals of at most MaxPatternBytes, or bound values whose
// callers check that length when binding. Linear functions (jaccard,
// hamming, contains, list set operations) took 4 to 20 s a chunk of 20,000
// to 40,000 elements.
//
// patternFunctions maps each pattern match to its name in errors.
var patternFunctions = map[string]string{
	"~~": "LIKE", "!~~": "NOT LIKE", "~~*": "ILIKE", "!~~*": "NOT ILIKE", "~~~": "GLOB",
	"like_escape": "like_escape", "not_like_escape": "not_like_escape",
	"ilike_escape": "ilike_escape", "not_ilike_escape": "not_ilike_escape",
	"~": "regexp_matches", "regexp_matches": "regexp_matches", "regexp_full_match": "regexp_full_match",
	"regexp_replace": "regexp_replace", "regexp_extract": "regexp_extract", "regexp_extract_all": "regexp_extract_all",
	"regexp_split_to_array": "regexp_split_to_array", "regexp_split_to_table": "regexp_split_to_table",
	"string_split_regex": "string_split_regex", "str_split_regex": "str_split_regex",
}

// MaxPatternBytes bounds a pattern's length.
const MaxPatternBytes = 256

// boundedPattern reports whether a pattern is a string literal of at most
// MaxPatternBytes or a bound value. Telemetry SQL binds only values its
// callers checked: panel windows and variables, bounded by PatternParams.
func boundedPattern(node map[string]any) bool {
	switch node["class"] {
	case "PARAMETER":
		return true
	case "CONSTANT":
		literal, _ := node["literal"].(map[string]any)
		text, _ := literal["text"].(string)
		return len(text) <= MaxPatternBytes
	}
	return false
}

// PatternParams names the parameters a statement uses as patterns, so a
// caller can bound their values when it binds them.
func PatternParams(value any) map[string]bool {
	names := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if name, ok := node["function_name"].(string); ok {
				if parameter := patternArgument(node, name); parameter != nil && parameter["class"] == "PARAMETER" {
					identifier, _ := parameter["identifier"].(string)
					names[identifier] = true
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(value)
	return names
}

// patternArgument returns a pattern function's pattern, under any casts.
func patternArgument(node map[string]any, name string) map[string]any {
	if _, ok := patternFunctions[strings.ToLower(name)]; !ok {
		return nil
	}
	arguments, _ := node["arguments"].([]any)
	if len(arguments) < 2 {
		return nil
	}
	argument, _ := arguments[1].(map[string]any)
	expression, _ := argument["expression"].(map[string]any)
	for expression["class"] == "CAST" {
		child, _ := expression["child"].(map[string]any)
		expression = child
	}
	if expression == nil {
		return map[string]any{}
	}
	return expression
}

func checkPattern(node map[string]any, name string) error {
	pattern := patternArgument(node, name)
	if pattern == nil || boundedPattern(pattern) {
		return nil
	}
	return fmt.Errorf("the pattern of %s must be a literal of at most %d bytes", patternFunctions[strings.ToLower(name)], MaxPatternBytes)
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// parseStatement parses one statement with DuckDB's own parser. The query is
// a bound value, so nothing is prepared or executed, including statements
// before a SELECT.
func parseStatement(ctx context.Context, db rowQueryer, query string) (map[string]any, error) {
	var text string
	if err := db.QueryRowContext(ctx, "SELECT json_serialize_sql(?::VARCHAR)::VARCHAR", query).Scan(&text); err != nil {
		return nil, err
	}
	var parsed struct {
		Error      bool   `json:"error"`
		Message    string `json:"error_message"`
		Statements []struct {
			Node map[string]any `json:"node"`
		} `json:"statements"`
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, err
	}
	if parsed.Error {
		return nil, fmt.Errorf("SQL parsing failed: %s", parsed.Message)
	}
	if len(parsed.Statements) != 1 {
		return nil, fmt.Errorf("exactly one SELECT statement is required")
	}
	return parsed.Statements[0].Node, nil
}

func validateSQLAST(ctx context.Context, conn *sql.Conn, query string) error {
	node, err := parseStatement(ctx, conn, query)
	if err != nil {
		return err
	}
	return validateSQLNode(node, nil)
}

// describeProjection describes a query and returns, per result column, the
// projection that keeps nested and VARIANT values away from the Go driver.
func describeProjection(ctx context.Context, conn *sql.Conn, query string) (projection, aliases []string, serialized []bool, err error) {
	rows, err := conn.QueryContext(ctx, "DESCRIBE "+query)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name, logicalType string
		var nullable, key, defaultValue, extra sql.NullString
		if err := rows.Scan(&name, &logicalType, &nullable, &key, &defaultValue, &extra); err != nil {
			return nil, nil, nil, err
		}
		if names[strings.ToLower(name)] {
			return nil, nil, nil, fmt.Errorf("duplicate result column %q; use distinct aliases", name)
		}
		names[strings.ToLower(name)] = true
		alias := fmt.Sprintf("_c%d", len(aliases))
		aliases = append(aliases, alias)
		expression := alias
		encode := containsLogicalType(logicalType, "VARIANT") || containsLogicalType(logicalType, "JSON") ||
			containsLogicalType(logicalType, "STRUCT") || containsLogicalType(logicalType, "MAP") || containsLogicalType(logicalType, "UNION") || strings.Contains(logicalType, "[")
		if encode {
			expression = "to_json(" + expression + ")::VARCHAR"
		}
		if logicalType == "TIMESTAMPTZ_NS" {
			expression = expression + "::TIMESTAMP_NS"
		}
		projection = append(projection, expression+" AS "+sqlIdentifier(name))
		serialized = append(serialized, encode)
	}
	return projection, aliases, serialized, rows.Err()
}

func sqlIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// DESCRIBE types can contain quoted field names. A field named "VARIANT"
// is an identifier, not a variant type. Skip quoted identifiers before testing
// logical type tokens, including types inside lists, structs and maps.
func containsLogicalType(expression, name string) bool {
	var token strings.Builder
	quoted := false
	for i := 0; i < len(expression); i++ {
		c := expression[i]
		if c == '"' {
			if quoted && i+1 < len(expression) && expression[i+1] == '"' {
				i++
				continue
			}
			quoted = !quoted
		}
		if !quoted && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_') {
			token.WriteByte(c)
		} else {
			if strings.EqualFold(token.String(), name) {
				return true
			}
			token.Reset()
		}
	}
	return strings.EqualFold(token.String(), name)
}

func validateSQLNode(value any, ctes map[string]bool) error {
	switch node := value.(type) {
	case map[string]any:
		// Definitions see prior and outer CTEs. A nonrecursive definition cannot
		// see itself: DuckDB would resolve that reference to a physical table.
		if cteMap, ok := node["cte_map"].(map[string]any); ok {
			scope := make(map[string]bool, len(ctes))
			for name := range ctes {
				scope[name] = true
			}
			entries, _ := cteMap["map"].([]any)
			for _, entry := range entries {
				entry, ok := entry.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid CTE definition")
				}
				name, ok := entry["key"].(string)
				definition, defined := entry["value"].(map[string]any)
				if !ok || !defined || definition["query_node"] == nil {
					return fmt.Errorf("invalid CTE definition")
				}
				if err := validateSQLNode(definition, scope); err != nil {
					return err
				}
				scope[strings.ToLower(name)] = true
			}
			ctes = scope
		}
		kind, _ := node["type"].(string)
		_, hasSample := node["sample"]
		_, hasAlias := node["alias"]
		// TableRef's common serialized fields distinguish it from QueryNode
		// (which also has sample) and Expression (which also has alias).
		if hasSample && hasAlias {
			switch kind {
			case "BASE_TABLE", "SUBQUERY", "JOIN", "TABLE_FUNCTION", "EMPTY", "EXPRESSION_LIST", "PIVOT":
			default:
				return fmt.Errorf("table reference %q is not available to telemetry SQL", kind)
			}
		}
		switch kind {
		case "INSERT_QUERY_NODE", "UPDATE_QUERY_NODE", "DELETE_QUERY_NODE", "COPY_QUERY_NODE", "MERGE_QUERY_NODE", "STATEMENT_NODE":
			return fmt.Errorf("mutating SQL is not allowed")
		case "BASE_TABLE":
			name, _ := node["table_name"].(string)
			schema, _ := node["schema_name"].(string)
			catalog, _ := node["catalog_name"].(string)
			allowed := map[string]bool{"spans": true, "logs": true, "metrics": true, "service_rollup": true, "edge_rollup": true}
			if strings.EqualFold(name, "resource_inventory") {
				if schema != "" && schema != "main" {
					return fmt.Errorf("resource_inventory is available only to panel SQL in its logical window")
				}
				allowed["resource_inventory"] = true
			}
			cte := catalog == "" && ((schema == "" && ctes[strings.ToLower(name)]) ||
				(schema == "recurring" && ctes["recurring:"+strings.ToLower(name)]))
			if !cte && (catalog != "" || (schema != "" && schema != "main" && schema != "telemetry") || !allowed[strings.ToLower(name)]) {
				return fmt.Errorf("table %q is not available to telemetry SQL", name)
			}
		case "SELECT_NODE":
			// DuckDB 2.0.0 groups GROUP BY ALL keys without VARIANT collation, so
			// equal attribute values split into separate groups. Explicit keys
			// are collated; TestVariantGroupByAllSplitsEqualValues tracks the
			// upstream fix case by case.
			if node["aggregate_handling"] == "FORCE_AGGREGATES" {
				return fmt.Errorf("GROUP BY ALL is not available to telemetry SQL; list the grouping keys explicitly")
			}
		case "TABLE_FUNCTION":
			function, _ := node["function"].(map[string]any)
			name, _ := function["function_name"].(string)
			switch strings.ToLower(name) {
			case "range", "generate_series", "unnest":
			default:
				return fmt.Errorf("table function %q is not available to telemetry SQL", name)
			}
		}
		if node["class"] == "COLUMN_REF" {
			names, _ := node["column_names"].([]any)
			if len(names) > 0 {
				name, _ := names[len(names)-1].(string)
				if k, internal := internalColumn(name); internal {
					if k.Name != "" {
						return fmt.Errorf("column %q is internal; write %s['%s']", name, keyMap(k), k.Name)
					}
					return fmt.Errorf("column %q is internal to telemetry storage", name)
				}
			}
		}
		if name, ok := node["function_name"].(string); ok {
			if blockedSQLFunctions[strings.ToLower(name)] {
				return fmt.Errorf("function %q is not available to telemetry SQL", name)
			}
			if err := checkPattern(node, name); err != nil {
				return err
			}
		}
		for key, child := range node {
			if key == "cte_map" {
				continue
			} // Definitions were validated in binding order.
			scope := ctes
			if kind == "RECURSIVE_CTE_NODE" && key == "right" {
				scope = make(map[string]bool, len(ctes)+2)
				for name := range ctes {
					scope[name] = true
				}
				name, _ := node["cte_name"].(string)
				scope[strings.ToLower(name)] = true
				scope["recurring:"+strings.ToLower(name)] = true
			}
			if err := validateSQLNode(child, scope); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := validateSQLNode(child, ctes); err != nil {
				return err
			}
		}
	}
	return nil
}
