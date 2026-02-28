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
	Query   string `json:"query"`
	MaxRows int    `json:"max_rows,omitempty"`
}

// SQLResponse represents the response from a SQL query
type SQLResponse struct {
	Results         []RowMap `json:"results"`
	ExecutionTimeMs int64    `json:"execution_time_ms"`
	RowsReturned    int      `json:"rows_returned"`
	Error           string   `json:"error,omitempty"`
}

// RowMap represents a single row as a map of column name to value
type RowMap map[string]interface{}

// ExecuteSQL validates and executes a SQL query
func (d *Duck) ExecuteSQL(ctx context.Context, req SQLRequest) SQLResponse {
	start := time.Now()

	// Set default max rows
	if req.MaxRows == 0 {
		req.MaxRows = 1000
	}

	// Validate SQL
	if err := validateSQL(req.Query, d.cfg.LakeDir); err != nil {
		return SQLResponse{
			Error: fmt.Sprintf("SQL validation failed: %v", err),
		}
	}

	// Add LIMIT if not present
	query := ensureLimit(req.Query, req.MaxRows)

	// Add timeout to context
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Execute query
	rows, err := d.DB.QueryContext(queryCtx, query)
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
	for rows.Next() {
		if len(results) >= req.MaxRows {
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

	if err := rows.Err(); err != nil {
		return SQLResponse{
			Error:           fmt.Sprintf("Error reading rows: %v", err),
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
func validateSQL(query string, lakeDir string) error {
	upperQuery := strings.ToUpper(strings.TrimSpace(query))

	// Must start with SELECT or WITH (for CTEs)
	if !strings.HasPrefix(upperQuery, "SELECT") && !strings.HasPrefix(upperQuery, "WITH") {
		return fmt.Errorf("only SELECT and WITH queries are allowed")
	}

	// Disallowed keywords (write operations, schema changes, etc.)
	disallowedKeywords := []string{
		"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER",
		"TRUNCATE", "REPLACE", "MERGE", "EXEC", "EXECUTE",
		"GRANT", "REVOKE", "ATTACH", "DETACH",
	}

	for _, keyword := range disallowedKeywords {
		// Use word boundaries to avoid false positives (e.g., "DROP" in "DROPDUPLICATE")
		pattern := regexp.MustCompile(`\b` + keyword + `\b`)
		if pattern.MatchString(upperQuery) {
			return fmt.Errorf("keyword '%s' is not allowed", keyword)
		}
	}

	// Check for dangerous functions
	dangerousFunctions := []string{
		"COPY", "LOAD", "IMPORT", "EXPORT",
	}

	for _, fn := range dangerousFunctions {
		pattern := regexp.MustCompile(`\b` + fn + `\s*\(`)
		if pattern.MatchString(upperQuery) {
			return fmt.Errorf("function '%s' is not allowed", fn)
		}
	}

	// Block file-reading table functions (prevent arbitrary file access)
	// Only read_parquet with lake/ path is allowed
	fileReaders := []string{
		"READ_CSV", "READ_JSON", "READ_TEXT", "READ_BLOB",
		"READ_CSV_AUTO", "READ_JSON_AUTO",
	}
	for _, fn := range fileReaders {
		pattern := regexp.MustCompile(`\b` + fn + `\s*\(`)
		if pattern.MatchString(upperQuery) {
			return fmt.Errorf("function '%s' is not allowed; use read_parquet with lake/ paths", fn)
		}
	}

	// Validate read_parquet paths - must reference configured lake directory
	parquetPattern := regexp.MustCompile(`(?i)READ_PARQUET\s*\(\s*'([^']*)'`)
	matches := parquetPattern.FindAllStringSubmatch(query, -1)
	for _, m := range matches {
		if len(m) > 1 {
			path := m[1]
			// Allow paths that start with configured lake dir or contain it
			if !strings.HasPrefix(path, lakeDir) && !strings.Contains(path, lakeDir+"/") {
				return fmt.Errorf("read_parquet path must be within %s directory", lakeDir)
			}
			// Block path traversal
			if strings.Contains(path, "..") {
				return fmt.Errorf("path traversal not allowed")
			}
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

// ensureLimit adds or replaces LIMIT clause
func ensureLimit(query string, maxRows int) string {
	upperQuery := strings.ToUpper(query)

	// Check if LIMIT already exists
	limitPattern := regexp.MustCompile(`\bLIMIT\s+(\d+)`)
	match := limitPattern.FindStringSubmatch(upperQuery)

	if match != nil {
		// LIMIT exists, check if it's within maxRows
		var existingLimit int
		fmt.Sscanf(match[1], "%d", &existingLimit)

		if existingLimit > maxRows {
			// Replace with maxRows
			return limitPattern.ReplaceAllString(query, fmt.Sprintf("LIMIT %d", maxRows))
		}
		return query
	}

	// No LIMIT, add it
	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")
	return fmt.Sprintf("%s LIMIT %d", query, maxRows)
}

// GetQueryExamples returns example queries for documentation
func GetQueryExamples() []QueryExample {
	return []QueryExample{
		{
			Title:       "Error logs in last hour",
			Description: "Find all error and fatal logs from the last hour",
			Query: `SELECT
  epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) as timestamp,
  "name=service_name" as service_name,
  "name=severity" as severity,
  "name=body" as body
FROM read_parquet('lake/logs/tenant=*/namespace=*/**/*.parquet')
WHERE "name=severity" IN ('ERROR', 'FATAL')
  AND "name=time_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 3600) * 1000000000
ORDER BY "name=time_unix_nano" DESC
LIMIT 100`,
		},
		{
			Title:       "Slowest traces",
			Description: "Top 20 slowest traces in the last 15 minutes",
			Query: `SELECT
  "name=trace_id" as trace_id,
  "name=service_name" as service_name,
  "name=name" as operation,
  "name=duration_ms" as duration_ms,
  "name=status_code" as status_code
FROM read_parquet('lake/spans/tenant=*/namespace=*/**/*.parquet')
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
  AND ("name=parent_span_id" IS NULL OR "name=parent_span_id" = '')
ORDER BY "name=duration_ms" DESC
LIMIT 20`,
		},
		{
			Title:       "HTTP 5xx errors by endpoint",
			Description: "Count of 5xx errors grouped by service and endpoint",
			Query: `SELECT
  "name=service_name" as service_name,
  "name=name" as endpoint,
  COUNT(*) as error_count
FROM read_parquet('lake/spans/tenant=*/namespace=*/**/*.parquet')
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 1800) * 1000000000
  AND from_utf8("name=attributes_json") ILIKE '%http.status_code%'
GROUP BY "name=service_name", "name=name"
ORDER BY error_count DESC
LIMIT 20`,
		},
		{
			Title:       "Service throughput per minute",
			Description: "Request count per service per minute in last hour",
			Query: `SELECT
  DATE_TRUNC('minute', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) as minute,
  "name=service_name" as service_name,
  COUNT(*) as request_count
FROM read_parquet('lake/spans/tenant=*/namespace=*/**/*.parquet')
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 3600) * 1000000000
  AND "name=kind" = 'SPAN_KIND_SERVER'
GROUP BY minute, "name=service_name"
ORDER BY minute DESC, request_count DESC
LIMIT 100`,
		},
		{
			Title:       "Service latency from rollup",
			Description: "P50, P95 latencies by service from rollup table",
			Query: `SELECT
  service,
  AVG(CASE WHEN spans > 0 THEN p50_ms END) as p50_ms,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) as p95_ms,
  SUM(spans) as total_requests,
  AVG(CASE WHEN spans > 0 THEN error_rate END) as avg_error_rate,
  SUM(COALESCE(log_count, 0)) as total_logs,
  SUM(COALESCE(metric_count, 0)) as total_metrics
FROM service_rollup
WHERE bucket >= NOW() - INTERVAL '15 minutes'
GROUP BY service
ORDER BY total_requests DESC`,
		},
	}
}

// QueryExample represents an example query
type QueryExample struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Query       string `json:"query"`
}
