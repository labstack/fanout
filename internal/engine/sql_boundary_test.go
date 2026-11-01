package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		"SELECT * FROM read_single_parquet_file('/etc/passwd')",
		"SELECT read_single_parquet_file('/etc/passwd')",
		"SELECT * FROM (SHOW ALL TABLES)",
		"SELECT * FROM read_batches",
		"SELECT * FROM read_span_events",
		"SELECT * FROM read_log_times",
		"SELECT * FROM read_traces",
		"SELECT * FROM read_trace_candidates",
		"SELECT * FROM read_cache_version",
		"SELECT * FROM read_edge_buckets",
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
	d := &Duck{DB: db, writeDB: db, repository: emptyRepository(t)}
	queries := []string{
		`SELECT '{"dotted.key":9223372036854775807,"zero":0,"false":false,"null":null,"nested":[1,{"a":true}]}'::JSON::VARIANT AS data`,
		`SELECT [1::VARIANT, '"text"'::JSON::VARIANT] AS data`,
		`SELECT {'value': 9223372036854775807::BIGINT::VARIANT} AS data`,
		`SELECT map(['key'], [1::VARIANT]) AS data`,
		`SELECT {'value': 'NaN'::DOUBLE, 'text': 'NaN'} AS data`,
		`SELECT 'Infinity'::DOUBLE AS data`,
	}
	for _, query := range queries {
		rows := mustRunTelemetrySQL(t, t.Context(), d, query, 1000)
		if len(rows) != 1 {
			t.Fatalf("%s: %#v", query, rows)
		}
		if _, serialized := rows[0]["data"].(string); serialized {
			jsonValue(t, rows[0]["data"])
		}
	}
	value := jsonValue(t, mustRunTelemetrySQL(t, t.Context(), d, queries[0], 1000)[0]["data"]).(map[string]any)
	if value["dotted.key"] != json.Number("9223372036854775807") || value["zero"] != json.Number("0") || value["false"] != false || value["null"] != nil {
		t.Fatalf("typed values changed: %#v", value)
	}
	if _, err := runTelemetrySQL(t.Context(), d, "SELECT 1 AS x, 2 AS x", 1000); err == nil {
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

func TestTelemetrySQLRejectsEngineMetadataReads(t *testing.T) {
	d := &Duck{DB: openTestDuck(t)}
	for _, statement := range []string{
		"SELECT * FROM (SHOW ALL TABLES)",
		"SELECT * FROM read_batches",
		"SELECT * FROM read_span_events",
		"SELECT * FROM read_log_times",
		"SELECT * FROM read_traces",
		"SELECT * FROM read_trace_candidates",
		"SELECT * FROM read_cache_version",
		"SELECT * FROM read_edge_buckets",
		"SELECT current_setting('allowed_directories')",
		"SELECT getvariable('operator_secret')",
	} {
		if _, _, err := d.PrepareTelemetrySQL(t.Context(), statement, statement, 10); err == nil {
			t.Errorf("executed forbidden query %s", statement)
		}
	}
}

// DuckDB checks for interrupts only between chunks of up to 2,048 rows, so a
// function whose per-row cost grows with the product of two input sizes can
// run a statement for most of an hour past its deadline. Edit distances are
// blocked; pattern matches take a short literal pattern.
func TestSQLBoundaryRejectsQuadraticRowCosts(t *testing.T) {
	db := openTestDuck(t)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	long := "'" + strings.Repeat("a", MaxPatternBytes+1) + "'"
	for query, want := range map[string]string{
		"SELECT max(levenshtein(repeat('a',20000), repeat('b',20000)||x::varchar)) FROM range(2048) t(x)": `function "levenshtein" is not available to telemetry SQL`,
		"SELECT damerau_levenshtein(service_name, 'x') FROM spans":                                        `function "damerau_levenshtein" is not available`,
		"SELECT jaro_similarity(service_name, 'x') FROM spans":                                            `function "jaro_similarity" is not available`,
		"SELECT jaro_winkler_similarity(service_name, 'x') FROM spans":                                    `function "jaro_winkler_similarity" is not available`,
		"SELECT editdist3(service_name, 'x') FROM spans":                                                  `function "editdist3" is not available`,
		"SELECT * FROM spans WHERE service_name LIKE '%' || repeat('a', 10000)":                           "pattern of LIKE must be a literal",
		"SELECT * FROM spans WHERE service_name ILIKE name":                                               "pattern of ILIKE must be a literal",
		"SELECT * FROM spans WHERE service_name NOT LIKE name":                                            "pattern of NOT LIKE must be a literal",
		"SELECT * FROM spans WHERE service_name GLOB name":                                                "pattern of GLOB must be a literal",
		"SELECT * FROM spans WHERE service_name SIMILAR TO name":                                          "pattern of regexp_full_match must be a literal",
		"SELECT * FROM spans WHERE service_name ~ name":                                                   "pattern of regexp_matches must be a literal",
		"SELECT regexp_replace(name, service_name, '') FROM spans":                                        "pattern of regexp_replace must be a literal",
		"SELECT * FROM spans WHERE like_escape(name, service_name, '\\')":                                 "pattern of like_escape must be a literal",
		"SELECT * FROM spans WHERE regexp_matches(name, " + long + ")":                                    "at most 256 bytes",
		"SELECT * FROM spans WHERE list_has_any(['x'], list_transform(['y'], s -> s LIKE name))":          "pattern of LIKE must be a literal",
	} {
		err := validateSQLAST(ctx, conn, query)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", query, err, want)
		}
	}
	for _, query := range []string{
		"SELECT * FROM spans WHERE name LIKE 'GET %' AND name NOT ILIKE '%health%'",
		"SELECT * FROM spans WHERE regexp_matches(name, 'error.*') OR name SIMILAR TO 'a.*'",
		"SELECT regexp_extract(name, '([a-z]+)', 1), string_split_regex(name, '[/ ]') FROM spans",
		"SELECT * FROM spans WHERE name LIKE 'x%'::VARCHAR",
		"SELECT contains(name, service_name), jaccard(name, 'checkout') FROM spans",
		"SELECT * FROM spans WHERE name LIKE ?::VARCHAR AND regexp_matches(name, ?)",
	} {
		if err := validateSQLAST(ctx, conn, query); err != nil {
			t.Errorf("rejected %s: %v", query, err)
		}
	}
}
