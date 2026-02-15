package query

import "testing"

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
