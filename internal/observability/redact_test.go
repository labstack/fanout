package observability

import (
	"database/sql"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

var redactTests = []struct {
	name string
	in   string
	want string
}{
	{
		name: "URL query value",
		in:   `request failed: https://api.example.test/data?series=gold&api_key=abc123&limit=1`,
		want: `request failed: https://api.example.test/data?series=gold&api_key=[REDACTED]&limit=1`,
	},
	{
		name: "assignment",
		in:   `client_secret="super-secret" token=opaque-value`,
		want: `client_secret=[REDACTED] token=[REDACTED]`,
	},
	{
		name: "authorization",
		in:   `Authorization: Bearer eyJhbGciOi.secret.signature`,
		want: `Authorization: [REDACTED]`,
	},
	{
		name: "ordinary log",
		in:   `worker completed 24 records in 18ms`,
		want: `worker completed 24 records in 18ms`,
	},
	{
		name: "JSON password field",
		in:   `{"level":"error","password":"hunter2","user":"ada"}`,
		want: `{"level":"error","password":"[REDACTED]","user":"ada"}`,
	},
	{
		name: "JSON api_key with spacing",
		in:   `{"api_key": "sk-live-abc123", "limit": 5}`,
		want: `{"api_key": "[REDACTED]", "limit": 5}`,
	},
	{
		name: "JSON authorization bearer collapses",
		in:   `{"Authorization":"Bearer eyJhbGciOi.secret.signature"}`,
		want: `{"Authorization":"[REDACTED]"}`,
	},
	{
		name: "JSON value with escaped quote",
		in:   `{"client_secret":"su\"per-secret"}`,
		want: `{"client_secret":"[REDACTED]"}`,
	},
	{
		name: "JSON non-secret keys untouched",
		in:   `{"token_count":12,"msg":"ok"}`,
		want: `{"token_count":12,"msg":"ok"}`,
	},
}

func TestRedactLogBody(t *testing.T) {
	for _, test := range redactTests {
		t.Run(test.name, func(t *testing.T) {
			if got := redactLogBody(test.in); got != test.want {
				t.Fatalf("redactLogBody() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestRedactSQLMatchesGo pins the Go-vs-SQL redaction parity that the search
// filter depends on (see redactedBodySQL in logs.go): if DuckDB's RE2
// dialect ever diverges from Go's regexp for these patterns, shipping
// silently different display-vs-search redaction would let telemetry viewers
// probe secrets via search=<candidate>.
func TestRedactSQLMatchesGo(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()
	expr := redactLogBodySQL("?")
	for _, test := range redactTests {
		t.Run(test.name, func(t *testing.T) {
			var got string
			if err := db.QueryRow("SELECT "+expr, test.in).Scan(&got); err != nil {
				t.Fatalf("duckdb redaction query: %v", err)
			}
			if want := redactLogBody(test.in); got != want {
				t.Fatalf("SQL redaction = %q, Go redaction = %q; they must agree", got, want)
			}
		})
	}
}
