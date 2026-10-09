package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

func assertRetryable(t *testing.T, result Result, want bool) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Retryable bool `json:"retryable"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Retryable != want {
		t.Fatalf("retryable=%v want %v: %s", wire.Retryable, want, raw)
	}
}

func TestFailedClassifiesDriverErrorTypes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      duckdb.ErrorType
		retryable bool
	}{
		{"out of memory", duckdb.ErrorTypeOutOfMemory, true},
		{"io", duckdb.ErrorTypeIO, true},
		{"interrupt", duckdb.ErrorTypeInterrupt, true},
		{"binder", duckdb.ErrorTypeBinder, false},
		{"parser", duckdb.ErrorTypeParser, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The misleading message guards against classifying message text.
			err := fmt.Errorf("wrapped: %w", &duckdb.Error{Type: tc.kind, Msg: "Binder Error: Out of Memory Error: IO Error"})
			assertRetryable(t, failed(Result{ID: "p"}, err, nil), tc.retryable)
		})
	}
}

func TestFailedValidationIsNotRetryable(t *testing.T) {
	assertRetryable(t, failed(Result{ID: "p"}, Problems{{Path: "query", Message: "Out of Memory Error"}}, nil), false)
	assertRetryable(t, failed(Result{ID: "p"}, errors.New("IO Error: not a typed driver error"), nil), false)
}

func TestQueryDeadlineIsRetryable(t *testing.T) {
	e := newFixtureExecutor(t)
	e.engine = &slowBatchEngine{Engine: e.engine}
	e.timeout = 5 * time.Millisecond
	results, err := e.Run(t.Context(), RunRequest{Dashboard: Dashboard{Name: "Timeout", Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}}}}}})
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	if results[0].Error != "The query took longer than 10 seconds. Narrow the time range or add filters." {
		t.Fatalf("result=%+v", results[0])
	}
	assertRetryable(t, results[0], true)
	assertRetryable(t, failed(Result{}, context.DeadlineExceeded, context.DeadlineExceeded), false)
}

func TestRealEngineOutOfMemoryIsRetryable(t *testing.T) {
	db, err := sql.Open("duckdb", "?memory_limit=64KB&threads=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "SET temp_directory=''"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(t.Context(), "SELECT i, count(*) FROM range(100000) t(i) GROUP BY i")
	if rows != nil {
		rows.Close()
	}
	var engineError *duckdb.Error
	if !errors.As(err, &engineError) || engineError.Type != duckdb.ErrorTypeOutOfMemory {
		t.Fatalf("expected typed out of memory, got %v", err)
	}
	assertRetryable(t, failed(Result{ID: "p"}, err, nil), true)
}
