package query

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SQLRequest represents a direct SQL query request
type SQLRequest struct {
	Query     string `json:"query"`
	MaxRows   int    `json:"max_rows,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
	Explain   bool   `json:"explain,omitempty"`
}

// SQLResponse represents the response from a SQL query
type SQLResponse struct {
	Results         []RowMap `json:"results"`
	ExecutionTimeMs int64    `json:"execution_time_ms"`
	RowsReturned    int      `json:"rows_returned"`
	Error           string   `json:"error,omitempty"`
	QueryPlan       string   `json:"query_plan,omitempty"`
}

// RowMap represents a single row as a map of column name to value
type RowMap map[string]interface{}

// ExecuteSQL validates and executes a SQL query
func (d *Duck) ExecuteSQL(ctx context.Context, req SQLRequest) SQLResponse {
	start := time.Now()

	// Set default max rows. Guard against non-positive values too: a negative
	// MaxRows would reach make([]RowMap, 0, req.MaxRows) below and panic.
	if req.MaxRows <= 0 {
		req.MaxRows = 1000
	}

	// Set default timeout
	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	// Validate SQL (skip validation for EXPLAIN-prefixed queries that we construct)
	if err := validateSQL(req.Query); err != nil {
		return SQLResponse{
			Error: fmt.Sprintf("SQL validation failed: %v", err),
		}
	}

	// Build the query to execute
	var execQuery string
	if req.Explain {
		// EXPLAIN does not need LIMIT
		execQuery = "EXPLAIN " + req.Query
	} else {
		// Add LIMIT if not present
		execQuery = ensureLimit(req.Query, req.MaxRows)
	}

	// Add timeout to context
	queryCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Execute query
	rows, err := d.DB.QueryContext(queryCtx, execQuery)
	if err != nil {
		return SQLResponse{
			Error:           fmt.Sprintf("Query execution failed: %v", err),
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return SQLResponse{
			Error:           fmt.Sprintf("Failed to get columns: %v", err),
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}
	}

	// Read results
	results := make([]RowMap, 0, req.MaxRows)
	var planLines []string
	for rows.Next() {
		if !req.Explain && len(results) >= req.MaxRows {
			break
		}

		// Create slice of interface{} to hold column values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return SQLResponse{
				Error:           fmt.Sprintf("Failed to scan row: %v", err),
				ExecutionTimeMs: time.Since(start).Milliseconds(),
			}
		}

		if req.Explain {
			// EXPLAIN returns text rows; collect them into a plan string
			for _, val := range values {
				switch v := val.(type) {
				case []byte:
					planLines = append(planLines, string(v))
				case string:
					planLines = append(planLines, v)
				default:
					planLines = append(planLines, fmt.Sprintf("%v", v))
				}
			}
		} else {
			// Convert to map
			row := make(RowMap)
			for i, col := range columns {
				val := values[i]
				// Convert []uint8 to string for better JSON representation
				if b, ok := val.([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = val
				}
			}
			results = append(results, row)
		}
	}

	if err := rows.Err(); err != nil {
		return SQLResponse{
			Error:           fmt.Sprintf("Error reading rows: %v", err),
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}
	}

	if req.Explain {
		return SQLResponse{
			QueryPlan:       strings.Join(planLines, "\n"),
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}
	}

	return SQLResponse{
		Results:         results,
		ExecutionTimeMs: time.Since(start).Milliseconds(),
		RowsReturned:    len(results),
	}
}

// validateSQL checks if the SQL query is safe to execute
func validateSQL(query string) error {
	upperQuery := strings.ToUpper(strings.TrimSpace(query))

	// Must start with SELECT or WITH (for CTEs)
	if !strings.HasPrefix(upperQuery, "SELECT") && !strings.HasPrefix(upperQuery, "WITH") {
		return fmt.Errorf("only SELECT and WITH queries are allowed")
	}

	// Keyword/function checks run against a copy with string-literal contents
	// blanked out, so log searches like body ILIKE '%DELETE%' or scalar calls
	// like replace(body, ...) are not rejected for words that only appear inside
	// quoted strings.
	scrubbed := strings.ToUpper(stripStringLiterals(query))

	// Disallowed keywords (write operations, schema changes, etc.)
	// Note: REPLACE is intentionally omitted — it is a common scalar function
	// (replace(body, …)) and its only write forms, INSERT OR REPLACE / CREATE OR
	// REPLACE, are already blocked by INSERT / CREATE.
	disallowedKeywords := []string{
		"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER",
		"TRUNCATE", "MERGE", "EXEC", "EXECUTE",
		"GRANT", "REVOKE", "ATTACH", "DETACH",
	}

	for _, keyword := range disallowedKeywords {
		// Use word boundaries to avoid false positives (e.g., "DROP" in "DROPDUPLICATE")
		pattern := regexp.MustCompile(`\b` + keyword + `\b`)
		if pattern.MatchString(scrubbed) {
			return fmt.Errorf("keyword '%s' is not allowed", keyword)
		}
	}

	// Check for dangerous functions
	dangerousFunctions := []string{
		"COPY", "LOAD", "IMPORT", "EXPORT",
	}

	for _, fn := range dangerousFunctions {
		pattern := regexp.MustCompile(`\b` + fn + `\s*\(`)
		if pattern.MatchString(scrubbed) {
			return fmt.Errorf("function '%s' is not allowed", fn)
		}
	}

	// Block file-reading table functions (prevent arbitrary file access)
	fileReaders := []string{
		"READ_CSV", "READ_JSON", "READ_TEXT", "READ_BLOB", "READ_PARQUET",
		"READ_CSV_AUTO", "READ_JSON_AUTO",
	}
	for _, fn := range fileReaders {
		pattern := regexp.MustCompile(`\b` + fn + `\s*\(`)
		if pattern.MatchString(scrubbed) {
			return fmt.Errorf("function '%s' is not allowed", fn)
		}
	}

	// Block SQL comment syntax outside string literals
	if tokenOutsideStrings(query, "--") {
		return fmt.Errorf("SQL comments (--) are not allowed")
	}
	if tokenOutsideStrings(query, "/*") {
		return fmt.Errorf("SQL block comments (/* */) are not allowed")
	}

	return nil
}

// stripStringLiterals replaces the contents of single-quoted string literals
// (and the quotes) with spaces, preserving everything outside them and the
// overall length/structure. This lets keyword/function scanners ignore SQL
// keywords that appear only inside string literals (e.g. body = 'DROP TABLE').
// Escaped quotes (”) inside a string are handled.
func stripStringLiterals(query string) string {
	out := []byte(query)
	inString := false
	for i := 0; i < len(query); i++ {
		if query[i] == '\'' {
			if inString && i+1 < len(query) && query[i+1] == '\'' {
				out[i] = ' '
				out[i+1] = ' '
				i++
				continue
			}
			out[i] = ' '
			inString = !inString
			continue
		}
		if inString {
			out[i] = ' '
		}
	}
	return string(out)
}

// tokenOutsideStrings returns true if the given token appears outside single-quoted strings.
func tokenOutsideStrings(query, token string) bool {
	inString := false
	for i := 0; i < len(query); i++ {
		if query[i] == '\'' {
			// When already inside a string, handle escaped single quotes ('')
			// by consuming the second quote without leaving the string.
			if inString && i+1 < len(query) && query[i+1] == '\'' {
				i++ // skip escaped quote
				continue
			}
			inString = !inString
		} else if !inString && i+len(token) <= len(query) && query[i:i+len(token)] == token {
			return true
		}
	}
	return false
}

// Pre-compiled patterns for CheckQueryCost to avoid re-compilation on every call.
var (
	highCardPatterns = func() map[string]*regexp.Regexp {
		cols := []string{"TRACE_ID", "SPAN_ID", "ATTRIBUTES_JSON", "RESOURCE_JSON", "BODY", "EVENTS_JSON"}
		m := make(map[string]*regexp.Regexp, len(cols))
		for _, col := range cols {
			m[col] = regexp.MustCompile(`\b` + col + `\b`)
		}
		return m
	}()

	baseViewPatterns = map[string]*regexp.Regexp{
		"SPANS":   regexp.MustCompile(`\bSPANS\b`),
		"LOGS":    regexp.MustCompile(`\bLOGS\b`),
		"METRICS": regexp.MustCompile(`\bMETRICS\b`),
	}
)

// CheckQueryCost performs best-effort pattern analysis on a SQL query and returns
// advisory warnings about potentially expensive operations.
func CheckQueryCost(sql string) []string {
	upper := strings.ToUpper(sql)
	var warnings []string

	// 1. High-cardinality GROUP BY
	if idx := strings.Index(upper, "GROUP BY"); idx >= 0 {
		groupByClause := upper[idx:]
		for col, pat := range highCardPatterns {
			if pat.MatchString(groupByClause) {
				warnings = append(warnings, fmt.Sprintf("GROUP BY %s is high-cardinality and may produce millions of groups. Consider aggregating by service, operation, or status instead.", strings.ToLower(col)))
				break
			}
		}
	}

	// Determine which base views are referenced.
	referencedViews := make(map[string]bool, 3)
	for name, pat := range baseViewPatterns {
		if pat.MatchString(upper) {
			referencedViews[name] = true
		}
	}

	// 2. Unbounded time range on base views.
	// Check for common time-filter indicators: INTERVAL, NOW(), timestamp column names, or BUCKET (rollups).
	if len(referencedViews) > 0 {
		hasTimePred := strings.Contains(upper, "INTERVAL") ||
			strings.Contains(upper, "NOW()") ||
			strings.Contains(upper, "START_TIME") ||
			strings.Contains(upper, "TIME") ||
			strings.Contains(upper, "BUCKET")
		if !hasTimePred {
			for name := range referencedViews {
				warnings = append(warnings, fmt.Sprintf("Query references %s without a time filter. This scans all data. Add a WHERE clause with start_time/time > now() - INTERVAL.", strings.ToLower(name)))
				break
			}
		}
	}

	// 3. SELECT * without LIMIT on base views
	if len(referencedViews) > 0 && strings.Contains(upper, "SELECT *") && !strings.Contains(upper, "LIMIT") {
		warnings = append(warnings, "SELECT * without LIMIT on a base view. Add LIMIT or select specific columns to control result size.")
	}

	// 4. CROSS JOIN
	if strings.Contains(upper, "CROSS JOIN") {
		warnings = append(warnings, "CROSS JOIN produces a cartesian product. This is almost never what you want in observability queries.")
	}

	return warnings
}

// ensureLimit caps the result set at maxRows by wrapping the query in an outer
// SELECT … LIMIT. Wrapping (rather than rewriting a LIMIT in place) avoids
// clobbering LIMIT clauses inside subqueries or CTEs — a regex replace would
// rewrite an inner `LIMIT 5000` and silently change query semantics. Any
// user-supplied outer LIMIT smaller than maxRows still wins, since the outer cap
// only ever shrinks the result.
func ensureLimit(query string, maxRows int) string {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimSuffix(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)
	return fmt.Sprintf("SELECT * FROM (%s) AS _capped LIMIT %d", trimmed, maxRows)
}
