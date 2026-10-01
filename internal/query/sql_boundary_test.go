package query

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLBoundaryRejectsHiddenReadsAndMutations(t *testing.T) {
	db := openTestDuck(t)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	output := filepath.Join(t.TempDir(), "copy.csv")
	for _, query := range []string{
		"SELECT 1; SELECT 2",
		"WITH rollup_state AS (SELECT 1) SELECT * FROM main.rollup_state",
		"WITH rollup_state AS (SELECT * FROM rollup_state) SELECT * FROM rollup_state",
		"SELECT * FROM (WITH rollup_state AS (SELECT 1) SELECT * FROM rollup_state) x, rollup_state",
		"SELECT * FROM query('SELECT * FROM rollup_state')",
		"SELECT * FROM read_parquet('/etc/passwd')",
		"SELECT * FROM (SHOW ALL TABLES)",
		"SELECT * FROM read_batches",
		"SELECT * FROM read_span_events",
		"SELECT * FROM read_log_times",
		"SELECT * FROM read_traces",
		"SELECT * FROM read_trace_candidates",
		"SELECT * FROM read_cache_version",
		"SELECT * FROM endpoint_rollup",
		"SELECT current_setting('allowed_directories')",
		"SELECT getvariable('operator_secret')",
		"WITH x AS MATERIALIZED (COPY (SELECT 1) TO '" + output + "') SELECT * FROM x",
	} {
		if err := validateSQLAST(ctx, conn, query); err == nil {
			t.Errorf("accepted %s", query)
		}
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("COPY created output: %v", err)
	}
	for _, query := range []string{
		"WITH x AS (SELECT 1 AS n) SELECT * FROM x",
		"WITH RECURSIVE x(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM x WHERE n<3) SELECT * FROM x",
	} {
		if err := validateSQLAST(ctx, conn, query); err != nil {
			t.Errorf("rejected %s: %v", query, err)
		}
	}
}

func TestSQLBoundarySerializesNestedVariants(t *testing.T) {
	db := openTestDuck(t)
	db.SetMaxOpenConns(1)
	d := &Duck{DB: db}
	queries := []string{
		`SELECT '{"dotted.key":9223372036854775807,"zero":0,"false":false,"null":null,"nested":[1,{"a":true}]}'::JSON::VARIANT AS data`,
		`SELECT [1::VARIANT, '"text"'::JSON::VARIANT] AS data`,
		`SELECT {'value': 9223372036854775807::BIGINT::VARIANT} AS data`,
		`SELECT map(['key'], [1::VARIANT]) AS data`,
		`SELECT {'value': 'NaN'::DOUBLE, 'text': 'NaN'} AS data`,
		`SELECT 'Infinity'::DOUBLE AS data`,
	}
	for _, query := range queries {
		resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: query})
		if resp.Error != "" {
			t.Fatalf("%s: %s", query, resp.Error)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("%s: %#v", query, resp)
		}
		if _, err := json.Marshal(resp.Results); err != nil {
			t.Fatal(err)
		}
	}
	resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: queries[0]})
	value := resp.Results[0]["data"].(map[string]any)
	if value["dotted.key"] != json.Number("9223372036854775807") || value["zero"] != json.Number("0") || value["false"] != false || value["null"] != nil {
		t.Fatalf("typed values changed: %#v", value)
	}
	if resp := d.ExecuteSQL(context.Background(), SQLRequest{Query: "SELECT 1 AS x, 2 AS x"}); resp.Error == "" {
		t.Fatal("duplicate aliases accepted")
	}
}

func TestLogicalTypeDetectionSkipsFieldNames(t *testing.T) {
	for _, test := range []struct {
		expression string
		want       bool
	}{
		{`STRUCT("VARIANT" INTEGER)`, false},
		{`STRUCT("a" VARIANT)[]`, true},
		{`MAP(VARCHAR, VARIANT)`, true},
		{`STRUCT("VARIANT" STRUCT("VARIANT" VARCHAR))`, false},
	} {
		if got := containsLogicalType(test.expression, "VARIANT"); got != test.want {
			t.Errorf("%s: %v", test.expression, got)
		}
	}
}

func TestExecuteSQLRejectsEngineMetadataReads(t *testing.T) {
	d := &Duck{DB: openTestDuck(t)}
	for _, statement := range []string{
		"SELECT * FROM (SHOW ALL TABLES)",
		"SELECT * FROM read_batches",
		"SELECT * FROM read_span_events",
		"SELECT * FROM read_log_times",
		"SELECT * FROM read_traces",
		"SELECT * FROM read_trace_candidates",
		"SELECT * FROM read_cache_version",
		"SELECT * FROM endpoint_rollup",
		"SELECT current_setting('allowed_directories')",
		"SELECT getvariable('operator_secret')",
	} {
		if response := d.ExecuteSQL(t.Context(), SQLRequest{Query: statement}); response.Error == "" {
			t.Errorf("executed forbidden query %s", statement)
		}
	}
}
