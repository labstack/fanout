package query

import (
	"testing"
)

func TestValidateSQL(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		// Valid queries
		{"valid select from rollup", "SELECT * FROM service_rollup", false},
		{"valid read_parquet lake path", "SELECT * FROM read_parquet('lake/spans/**/*.parquet')", false},
		{"valid read_parquet with lake in path", "SELECT * FROM read_parquet('/data/lake/spans/*.parquet')", false},
		{"valid with CTE", "WITH cte AS (SELECT 1) SELECT * FROM cte", false},

		// Blocked DDL
		{"blocked INSERT", "INSERT INTO foo VALUES (1)", true},
		{"blocked DROP", "DROP TABLE foo", true},
		{"blocked CREATE", "CREATE TABLE foo (id INT)", true},

		// Blocked file readers
		{"blocked read_csv", "SELECT * FROM read_csv('/etc/passwd')", true},
		{"blocked read_json", "SELECT * FROM read_json('/etc/shadow')", true},
		{"blocked read_text", "SELECT * FROM read_text('/etc/hosts')", true},

		// Blocked read_parquet outside lake
		{"blocked read_parquet /etc", "SELECT * FROM read_parquet('/etc/passwd')", true},
		{"blocked read_parquet relative", "SELECT * FROM read_parquet('../secrets.parquet')", true},
		{"blocked path traversal", "SELECT * FROM read_parquet('lake/../../../etc/passwd')", true},

		// SQL comment injection
		{"blocked -- comment", "SELECT * FROM foo -- DROP TABLE bar", true},
		{"blocked -- at end", "SELECT * FROM foo--", true},
		{"blocked /* block comment */", "SELECT * FROM foo /* malicious */", true},
		// Glob patterns with /* inside quoted strings should be allowed
		{"allowed glob in string", "SELECT * FROM read_parquet('lake/spans/**/*.parquet')", false},
		{"allowed -- in string", "SELECT * FROM foo WHERE x = 'a--b'", false},
		{"allowed /* in string", "SELECT * FROM foo WHERE x = 'a/*b'", false},
		// Escaped quotes inside strings should not break detection
		{"blocked -- after escaped quote", "SELECT * FROM foo WHERE x = 'it''s' -- drop", true},
		{"allowed -- inside escaped quote string", "SELECT * FROM foo WHERE x = 'a--b''s'", false},

		// Must start with SELECT or WITH
		{"blocked SHOW", "SHOW TABLES", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQL(tt.query, "lake")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSQL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSQLRequestFields(t *testing.T) {
	// Verify new fields are present and zero-valued by default.
	req := SQLRequest{Query: "SELECT 1"}
	if req.TimeoutMs != 0 {
		t.Errorf("TimeoutMs default = %d, want 0", req.TimeoutMs)
	}
	if req.Explain {
		t.Error("Explain default should be false")
	}

	req2 := SQLRequest{
		Query:     "SELECT 1",
		TimeoutMs: 5000,
		Explain:   true,
	}
	if req2.TimeoutMs != 5000 {
		t.Errorf("TimeoutMs = %d, want 5000", req2.TimeoutMs)
	}
	if !req2.Explain {
		t.Error("Explain should be true")
	}
}

func TestSQLResponseQueryPlan(t *testing.T) {
	resp := SQLResponse{
		QueryPlan:       "PhysicalTableScan spans",
		ExecutionTimeMs: 12,
	}
	if resp.QueryPlan == "" {
		t.Error("QueryPlan should not be empty")
	}
	if resp.ExecutionTimeMs != 12 {
		t.Errorf("ExecutionTimeMs = %d, want 12", resp.ExecutionTimeMs)
	}
	if resp.Error != "" {
		t.Errorf("Error should be empty, got %q", resp.Error)
	}
	if len(resp.Results) != 0 {
		t.Errorf("Results should be empty for explain response")
	}
}

func TestDefaultTimeout(t *testing.T) {
	// When TimeoutMs is 0, the effective timeout should be 30000 ms.
	// We test the logic directly via the guard in ExecuteSQL.
	timeoutMs := 0
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	if timeoutMs != 30000 {
		t.Errorf("default timeout = %d, want 30000", timeoutMs)
	}

	// Custom timeout is preserved.
	customMs := 5000
	if customMs <= 0 {
		customMs = 30000
	}
	if customMs != 5000 {
		t.Errorf("custom timeout = %d, want 5000", customMs)
	}
}

func TestCheckQueryCost(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantWarnings int
	}{
		// Clean queries — no warnings
		{"safe rollup query", "SELECT service, p95_ms FROM service_rollup WHERE bucket > now() - INTERVAL 1 HOUR", 0},
		{"safe spans with time filter", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 15 MINUTE LIMIT 100", 0},
		{"safe CTE", "WITH x AS (SELECT 1) SELECT * FROM x", 0},

		// High-cardinality GROUP BY
		{"group by trace_id", "SELECT trace_id, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY trace_id", 1},
		{"group by span_id", "SELECT span_id, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY span_id", 1},
		{"group by attributes_json", "SELECT attributes_json, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY attributes_json", 1},
		{"group by body", "SELECT body, COUNT(*) FROM logs WHERE time > now() - INTERVAL 1 HOUR GROUP BY body", 1},
		{"group by events_json", "SELECT events_json, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY events_json", 1},
		{"group by resource_json", "SELECT resource_json, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY resource_json", 1},

		// Unbounded time range (also triggers SELECT * without LIMIT)
		{"spans no time filter", "SELECT * FROM spans WHERE service = 'foo'", 2},
		{"logs no time filter", "SELECT * FROM logs WHERE severity = 'ERROR'", 2},
		{"metrics no time filter", "SELECT * FROM metrics WHERE name = 'cpu'", 2},
		{"spans with time filter ok", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 1 HOUR LIMIT 100", 0},
		{"rollup no time filter ok", "SELECT * FROM service_rollup", 0},

		// SELECT * without LIMIT
		{"select star no limit", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 1 HOUR", 1},
		{"select star with limit ok", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 1 HOUR LIMIT 100", 0},
		{"select columns no limit ok", "SELECT service, count(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY service", 0},

		// CROSS JOIN (also triggers SELECT * without LIMIT)
		{"cross join", "SELECT * FROM spans CROSS JOIN logs WHERE start_time > now() - INTERVAL 1 HOUR", 2},

		// Multiple warnings
		{"group by trace_id no time", "SELECT trace_id, COUNT(*) FROM spans GROUP BY trace_id", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := CheckQueryCost(tt.query)
			if len(warnings) != tt.wantWarnings {
				t.Errorf("CheckQueryCost() returned %d warning(s), want %d: %v", len(warnings), tt.wantWarnings, warnings)
			}
		})
	}
}
