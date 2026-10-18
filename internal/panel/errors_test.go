package panel

import (
	"context"
	"errors"
	"fmt"
	duckdb "github.com/duckdb/duckdb-go/v2"
	"regexp"
	"strings"
	"testing"
	"time"
)

var forbiddenPresentation = regexp.MustCompile(`(?i)duckdb|parquet|sqlite|(?:binder|catalog|conversion|io|parser) error|variant|timestamptz_ns|timestamp_ns|telemetry\.|parquet_index|/srv/|cast\(\$`)

func TestAuthorDiagnosticsPreserveSQLVocabulary(t *testing.T) {
	for _, tc := range []struct{ message, want string }{
		{`Catalog Error: Table with name telemetry.service_rollup does not exist!`, `Table with name service_rollup does not exist!`},
		{`Binder Error: Table "edge_rollup" does not have a column named "query_count"`, `Table "edge_rollup" does not have a column named "query_count"`},
		{`Binder Error: Referenced table "telemetry.resource_inventory" not found!`, `Referenced table "resource_inventory" not found!`},
		{`Binder Error: Cannot compare DOUBLE, INTEGER, BIGINT, BOOLEAN, VARCHAR, FLOAT, INT32, UINT64 and INTEGER_LITERAL`, `Cannot compare DOUBLE, INTEGER, BIGINT, BOOLEAN, VARCHAR, FLOAT, INT32, UINT64 and INTEGER_LITERAL`},
		{`Binder Error: Cannot compare TIMESTAMPTZ_NS, TIMESTAMP_NS and VARIANT with VARCHAR`, `Cannot compare timestamp, timestamp and value with VARCHAR`},
		{`Conversion Error: Could not convert query_count using CAST(query_count AS DOUBLE)`, `Could not convert query_count using CAST(query_count AS DOUBLE)`},
		{`Catalog Error: Scalar Function with name query_count does not exist!`, `Scalar Function with name query_count does not exist!`},
		{`Binder Error: query_count not found` + "\nCandidate bindings: \"telemetry.service_rollup.query_count\", \"edge_rollup.service\", \"resource_inventory.resource_name\", \"spans.duration_ms\"\nLINE 1: SELECT query_count FROM telemetry.spans\nexpanded server SQL", "query_count not found\nCandidate bindings: \"service_rollup.query_count\", \"edge_rollup.service\", \"resource_inventory.resource_name\", \"spans.duration_ms\""},
		{`Binder Error: missing_column not found` + "\n\nLINE 1: SELECT missing_column FROM telemetry.spans\nCandidate bindings: \"query_count\", \"CAST(time AS TIMESTAMPTZ_NS)\", \"/srv/data/private.parquet\"\n  ^\nSQL: SELECT * FROM telemetry.spans", "missing_column not found\nCandidate bindings: \"query_count\""},
		{`Binder Error: missing_column not found` + "\nCandidate bindings: query_count, telemetry.spans.service", "missing_column not found\nCandidate bindings: \"query_count\", \"spans.service\""},
	} {
		t.Run(tc.message, func(t *testing.T) {
			cause := &duckdb.Error{Type: duckdb.ErrorTypeConversion, Msg: tc.message}
			problems := PresentProblems(Problems{{Message: tc.message, Hint: tc.message}})
			for _, got := range []string{PresentMessage(tc.message), PresentError(cause, SQLError), PresentedError(cause, SQLError).Error(), problems[0].Message, problems[0].Hint, failed(Result{}, cause, nil).Error} {
				if got != tc.want {
					t.Errorf("got %q, want %q", got, tc.want)
				}
			}
		})
	}
}

func TestErrorPresentationKeepsAuthorMeaningAndHidesOperationalCauses(t *testing.T) {
	for _, tc := range []struct{ message, meaning string }{
		{`Binder Error: Referenced column "missing_column" not found in FROM clause!` + "\nLINE 1: SELECT missing_column FROM telemetry.spans", "missing_column"},
		{`Binder Error: Referenced column "missing_path_column" not found` + "\nLINE 9: SELECT missing_path_column FROM read_parquet('/srv/data/a.parquet')", "missing_path_column"},
		{`Catalog Error: Scalar Function with name missing_function does not exist!`, "missing_function"},
		{`Conversion Error: Could not convert string 'checkout' to INT32`, "checkout"},
		{`Parser Error: syntax error at or near "oops"` + "\nLINE 1: SELECT oops", "oops"},
		{`Binder Error: Cannot compare TIMESTAMPTZ_NS with VARCHAR: CAST($x AS TIMESTAMPTZ_NS)`, "$x"},
		{`IO Error: DuckDB cannot read /srv/data/batch.parquet`, ""},
		{`read indexed Parquet trace: open /srv/data/a.parquet: permission denied`, ""},
		{`exemplars: read SQLite /srv/data/state.sqlite: disk failed`, ""},
		{`discover spans attributes: Catalog Error: telemetry.resource_inventory unavailable`, ""},
	} {
		t.Run(tc.message, func(t *testing.T) {
			cause := &duckdb.Error{Type: duckdb.ErrorTypeConversion, Msg: tc.message}
			problems := PresentProblems(Problems{{Path: "panels[0].sql", Message: tc.message, Hint: tc.message}})
			author := []string{PresentError(cause, SQLError), problems[0].Message, problems[0].Hint, problems.Error(), failed(Result{}, cause, nil).Error}
			for _, message := range author {
				if forbiddenPresentation.MatchString(message) {
					t.Errorf("internal detail survived: %s", message)
				}
				if tc.meaning != "" && !strings.Contains(message, tc.meaning) {
					t.Errorf("lost author meaning %q: %s", tc.meaning, message)
				}
			}
			if got := PresentError(cause, ReadError); got != ReadFailure {
				t.Errorf("operational error: %s", got)
			}
			wrapped := PresentedError(cause, ReadError)
			if wrapped.Error() != ReadFailure || !errors.Is(wrapped, cause) {
				t.Errorf("lost cause identity: %v", wrapped)
			}
		})
	}
}

func TestNativePanelSQLFailuresHaveSafePreciseDiagnostics(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, tc := range []struct{ name, sql, meaning string }{
		{"binder", `SELECT missing_column FROM spans WHERE $__window(start_time)`, "missing_column"},
		{"catalog", `SELECT missing_function(duration_ms) FROM spans WHERE $__window(start_time)`, "missing_function"},
		{"conversion", `SELECT CAST(service AS INTEGER) FROM spans WHERE $__window(start_time)`, "checkout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Panel{ID: "p", Title: "Calls", Viz: "table", SQL: tc.sql}
			got := e.runPanel(t.Context(), &p, nil, Time{Range: "1h"}, fixtureStart.Add(time.Hour), nil, 800, false)
			if got.Status != StatusError || !strings.Contains(got.Error, tc.meaning) || forbiddenPresentation.MatchString(got.Error) {
				t.Fatalf("unsafe or imprecise native diagnostic: %+v", got)
			}
			if tc.name == "binder" && (!strings.Contains(got.Error, "Candidate bindings:") || strings.Contains(got.Error, "telemetry.") || strings.Contains(got.Error, "_result.") || strings.Contains(got.Error, "LINE 1:")) {
				t.Fatalf("missing or unsanitized native candidate bindings: %s", got.Error)
			}
			if tc.name != "conversion" {
				err := e.Validate(t.Context(), &Dashboard{Name: "Calls", Panels: []Panel{p}})
				if err == nil || !strings.Contains(err.Error(), tc.meaning) || forbiddenPresentation.MatchString(err.Error()) {
					t.Fatalf("check: %v", err)
				}
			}
		})
	}
}

func TestStructuredFilterRuntimeFailureKeepsAuthorDiagnostic(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Calls", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Calls", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}, Where: []string{"CAST(service AS INTEGER) > 0"}}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	got := results[0]
	if got.Status != StatusError || !strings.Contains(got.Error, "checkout") || !strings.Contains(got.Error, "INT32") || !strings.Contains(got.Error, "service") || forbiddenPresentation.MatchString(got.Error) {
		t.Fatalf("lost structured filter diagnostic: %+v", got)
	}
}

func TestOperationalExecutionCausesStayReadFailures(t *testing.T) {
	for _, kind := range []duckdb.ErrorType{duckdb.ErrorTypeIO, duckdb.ErrorTypeOutOfMemory, duckdb.ErrorTypeInternal, duckdb.ErrorTypeInterrupt} {
		err := fmt.Errorf("wrapped: %w", &duckdb.Error{Type: kind, Msg: "misleading authored diagnostic"})
		if got := failed(Result{}, err, context.Canceled); got.Error != ReadFailure {
			t.Errorf("kind=%v: %+v", kind, got)
		}
	}
}

func TestNativeCandidateBindingsPreserveDottedAliases(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, sql := range []string{`SELECT missing FROM (SELECT 1 AS "http.route") q`, `SELECT missing FROM (SELECT 1 AS "http.route", 2 AS other) q`} {
		rows, err := e.engine.QueryContext(t.Context(), sql)
		if rows != nil {
			rows.Close()
		}
		if err == nil {
			t.Fatal("expected native binder error")
		}
		got := PresentError(err, SQLError)
		if !strings.Contains(got, `"http.route"`) || strings.Contains(got, `"route"`) {
			t.Fatalf("lost literal dotted candidate: %s (cause: %v)", got, err)
		}
	}
}

func TestNativeCandidateBindingsPreserveRelationNamedAliases(t *testing.T) {
	e := newFixtureExecutor(t)
	rows, err := e.engine.QueryContext(t.Context(), `SELECT missing FROM (SELECT 1 AS "spans.duration_ms") q`)
	if rows != nil {
		rows.Close()
	}
	if err == nil {
		t.Fatal("expected native binder error")
	}
	got := PresentError(err, SQLError)
	if !strings.Contains(got, `"spans.duration_ms"`) || strings.Contains(got, `"duration_ms"`) {
		t.Fatalf("lost literal relation-prefixed candidate: %s (cause: %v)", got, err)
	}
}

func TestCandidateBindingsDropOnlyInternalQualifiers(t *testing.T) {
	for _, tc := range []struct{ binding, want string }{
		{`"http.route"`, `"http.route"`},
		{`"custom.http.route"`, `"custom.http.route"`},
		{`"spans.duration_ms"`, `"spans.duration_ms"`},
		{`"q.x"`, `"q.x"`},
		{`"telemetry.spans.http.route"`, `"spans.http.route"`},
		{`"service_rollup.http.route"`, `"service_rollup.http.route"`},
		{`"unnamed_subquery.http.route"`, `"unnamed_subquery.http.route"`},
		{`"unnamed_subquery2.http.route"`, `"unnamed_subquery2.http.route"`},
		{`"_result.http.route"`, `"http.route"`},
		{`"telemetry._result.http.route"`, `"http.route"`},
	} {
		got := PresentMessage("Binder Error: missing not found\nCandidate bindings: " + tc.binding)
		if !strings.HasSuffix(got, "Candidate bindings: "+tc.want) {
			t.Errorf("%s => %s, want %s", tc.binding, got, tc.want)
		}
	}
}
