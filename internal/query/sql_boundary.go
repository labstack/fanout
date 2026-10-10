package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/queryrows"
)

// querySQL validates, describes and executes one statement on one connection
// while holding the same immutable Parquet snapshot throughout.

// blockedSQLFunctions are built-ins telemetry SQL must not call: they mutate
// state, read files or settings, or return catalog contents and file paths.
// sql_functions_test.go fails when an engine upgrade adds or changes built-ins,
// so each new one is reviewed against this list.
var blockedSQLFunctions = map[string]bool{
	"nextval": true, "setval": true, "setseed": true,
	"query": true, "query_table": true, "json_execute_serialized_sql": true,
	"current_setting": true, "getvariable": true, "getenv": true,
	"read_csv": true, "read_csv_auto": true, "read_json": true, "read_json_auto": true,
	"read_text": true, "read_blob": true, "read_parquet": true, "glob": true,
	"http_get": true, "write_file": true, "write_log": true,
	"current_query": true, "current_query_id": true,
	"pg_get_viewdef": true, "pg_get_constraintdef": true, "format_type": true, "get_block_size": true,
}

func (d *Duck) querySQL(ctx context.Context, query string, maxRows int, explain bool) (queryrows.Rows, []bool, error) {
	if err := d.lockParquetRead(ctx, readerQuery); err != nil {
		return nil, nil, err
	}
	conn, err := d.acquireRead(ctx)
	if err != nil {
		d.parquetMu.RUnlock()
		return nil, nil, err
	}
	release := func() { _ = conn.Close(); d.parquetMu.RUnlock() }
	failed := true
	defer func() {
		if failed {
			release()
		}
	}()
	if err := validateSQLAST(ctx, conn, query); err != nil {
		return nil, nil, err
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	var serialized []bool
	execQuery := "EXPLAIN " + query
	if !explain {
		execQuery, serialized, err = projectSQLResults(ctx, conn, query, maxRows)
		if err != nil {
			return nil, nil, err
		}
	}
	started := time.Now()
	rows, err := conn.QueryContext(ctx, execQuery)
	metrics.DuckDBStatement.Observe(time.Since(started).Seconds())
	if err != nil {
		return nil, nil, err
	}
	failed = false
	return &lockedRows{Rows: rows, unlock: release}, serialized, nil
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

func projectSQLResults(ctx context.Context, conn *sql.Conn, query string, maxRows int) (string, []bool, error) {
	projection, aliases, serialized, err := describeProjection(ctx, conn, query)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT %s FROM (%s) AS _result(%s) LIMIT %d", strings.Join(projection, ","), query, strings.Join(aliases, ","), maxRows), serialized, nil
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
			cte := catalog == "" && ((schema == "" && ctes[strings.ToLower(name)]) ||
				(schema == "recurring" && ctes["recurring:"+strings.ToLower(name)]))
			if !cte && (catalog != "" || (schema != "" && schema != "main" && schema != "telemetry") || !allowed[strings.ToLower(name)]) {
				return fmt.Errorf("table %q is not available to telemetry SQL", name)
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
		if name, ok := node["function_name"].(string); ok && blockedSQLFunctions[strings.ToLower(name)] {
			return fmt.Errorf("function %q is not available to telemetry SQL", name)
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
