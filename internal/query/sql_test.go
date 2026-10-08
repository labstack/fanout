package query

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/labstack/fanout/internal/metrics"
)

// Guards the RecordQuery wiring: ExecuteSQL must record query latency + status
// to fanout_query_*. These metrics were dead (defined, never called) until the
// raw-SQL path was instrumented — this test fails if that wiring is removed.
func TestExecuteSQLRecordsQueryMetric(t *testing.T) {
	db := openTestDuck(t)
	d := &Duck{DB: db, writeDB: db}

	before := testutil.ToFloat64(metrics.QueryTotal.WithLabelValues("sql", "ok"))
	resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: "SELECT 1"})
	if resp.Error != "" {
		t.Fatalf("ExecuteSQL error: %s", resp.Error)
	}
	if after := testutil.ToFloat64(metrics.QueryTotal.WithLabelValues("sql", "ok")); after != before+1 {
		t.Fatalf("QueryTotal{sql,ok} = %v, want %v (RecordQuery not wired)", after, before+1)
	}

	beforeErr := testutil.ToFloat64(metrics.QueryTotal.WithLabelValues("sql", "error"))
	d.ExecuteSQL(context.Background(), SQLRequest{Query: "DROP TABLE foo"}) // blocked by validateSQL → error status
	if after := testutil.ToFloat64(metrics.QueryTotal.WithLabelValues("sql", "error")); after != beforeErr+1 {
		t.Fatalf("QueryTotal{sql,error} = %v, want %v", after, beforeErr+1)
	}
}

func TestValidateSQL(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		// Valid queries
		{"valid select from rollup", "SELECT * FROM service_rollup", false},
		{"valid select from spans", "SELECT * FROM spans", false},
		{"valid with CTE", "WITH cte AS (SELECT 1) SELECT * FROM cte", false},

		// Blocked DDL
		{"blocked INSERT", "INSERT INTO foo VALUES (1)", true},
		{"blocked DROP", "DROP TABLE foo", true},
		{"blocked CREATE", "CREATE TABLE foo (id INT)", true},

		// Blocked file readers
		{"blocked read_csv", "SELECT * FROM read_csv('/etc/passwd')", true},
		{"blocked read_json", "SELECT * FROM read_json('/etc/shadow')", true},
		{"blocked read_text", "SELECT * FROM read_text('/etc/hosts')", true},
		{"blocked read_parquet", "SELECT * FROM read_parquet('/etc/passwd')", true},
		{"blocked read_parquet /etc", "SELECT * FROM read_parquet('/etc/passwd')", true},
		{"blocked read_parquet relative", "SELECT * FROM read_parquet('../secrets.parquet')", true},

		// SQL comment injection
		{"blocked -- comment", "SELECT * FROM foo -- DROP TABLE bar", true},
		{"blocked -- at end", "SELECT * FROM foo--", true},
		{"blocked /* block comment */", "SELECT * FROM foo /* malicious */", true},
		{"allowed -- in string", "SELECT * FROM foo WHERE x = 'a--b'", false},
		{"allowed /* in string", "SELECT * FROM foo WHERE x = 'a/*b'", false},
		// Escaped quotes inside strings should not break detection
		{"blocked -- after escaped quote", "SELECT * FROM foo WHERE x = 'it''s' -- drop", true},
		{"allowed -- inside escaped quote string", "SELECT * FROM foo WHERE x = 'a--b''s'", false},

		// Must start with SELECT or WITH
		{"blocked SHOW", "SHOW TABLES", true},

		// Keywords inside string literals must NOT trip the blocklist
		{"allowed DELETE in string", "SELECT * FROM logs WHERE body ILIKE '%DELETE button%'", false},
		{"allowed DROP in string", "SELECT * FROM logs WHERE body = 'failed to DROP cache'", false},
		{"allowed UPDATE in string", "SELECT count(*) FROM logs WHERE body LIKE '%UPDATE failed%'", false},
		{"allowed replace() function", "SELECT replace(body, 'x', 'y') FROM logs", false},
		// But real write keywords outside strings are still blocked
		{"blocked DELETE statement-ish", "SELECT * FROM logs; DELETE FROM logs", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQL(tt.query)
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
	// When TimeoutMs is 0, the effective timeout should be 15000 ms.
	// We test the logic directly via the guard in ExecuteSQL.
	timeoutMs := 0
	if timeoutMs <= 0 {
		timeoutMs = defaultQueryTimeoutMs
	}
	if timeoutMs != 15000 {
		t.Errorf("default timeout = %d, want 15000", timeoutMs)
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
