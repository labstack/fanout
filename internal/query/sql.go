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
	if err := validateSQL(req.Query); err != nil {
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
func validateSQL(query string) error {
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

	// Basic SQL injection checks
	if strings.Contains(query, "--") && !strings.Contains(query, "-- ") {
		return fmt.Errorf("potential SQL comment injection detected")
	}

	return nil
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
  EPOCH_MS(time_unix_nano / 1000000) as timestamp,
  service_name,
  severity,
  body
FROM read_parquet('lake/logs/**/*.parquet')
WHERE severity IN ('ERROR', 'FATAL')
  AND time_unix_nano >= (EXTRACT(EPOCH FROM NOW()) - 3600) * 1000000000
ORDER BY time_unix_nano DESC
LIMIT 100`,
		},
		{
			Title:       "Slowest traces",
			Description: "Top 20 slowest traces in the last 15 minutes",
			Query: `SELECT
  trace_id,
  service_name,
  name as operation,
  duration_ms,
  status_code
FROM read_parquet('lake/spans/**/*.parquet')
WHERE start_unix_nano >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
  AND parent_span_id IS NULL
ORDER BY duration_ms DESC
LIMIT 20`,
		},
		{
			Title:       "HTTP 5xx errors by endpoint",
			Description: "Count of 5xx errors grouped by service and endpoint",
			Query: `SELECT
  service_name,
  name as endpoint,
  COUNT(*) as error_count
FROM read_parquet('lake/spans/**/*.parquet')
WHERE start_unix_nano >= (EXTRACT(EPOCH FROM NOW()) - 1800) * 1000000000
  AND attributes_json LIKE '%"http.status_code":5__"%'
GROUP BY service_name, name
ORDER BY error_count DESC
LIMIT 20`,
		},
		{
			Title:       "Service throughput per minute",
			Description: "Request count per service per minute in last hour",
			Query: `SELECT
  DATE_TRUNC('minute', EPOCH_MS(start_unix_nano / 1000000)) as minute,
  service_name,
  COUNT(*) as request_count
FROM read_parquet('lake/spans/**/*.parquet')
WHERE start_unix_nano >= (EXTRACT(EPOCH FROM NOW()) - 3600) * 1000000000
  AND kind = 'SERVER'
GROUP BY minute, service_name
ORDER BY minute DESC, request_count DESC
LIMIT 100`,
		},
		{
			Title:       "Service latency percentiles",
			Description: "P50, P95 latencies by service from rollup table",
			Query: `SELECT
  service,
  AVG(p50_ms) as p50_ms,
  AVG(p95_ms) as p95_ms,
  SUM(spans) as total_requests,
  AVG(error_rate) as avg_error_rate
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
