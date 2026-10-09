package panel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/queryrows"
)

type callerCancelledEngine struct {
	Engine
	cancel context.CancelFunc
	err    error
}

func (e *callerCancelledEngine) QueryContext(ctx context.Context, _ string, _ ...any) (queryrows.Rows, error) {
	e.cancel()
	if e.err != nil {
		return nil, e.err
	}
	return nil, ctx.Err()
}

func TestRunCallerCancellationDoesNotLogPanelError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"context canceled", nil},
		{"wrapped engine interrupt", fmt.Errorf("query interrupted: %w", &duckdb.Error{Type: duckdb.ErrorTypeInterrupt, Msg: "INTERRUPT Error: Interrupted!"})},
		{"bare engine interrupt", &duckdb.Error{Type: duckdb.ErrorTypeInterrupt, Msg: "INTERRUPT Error: Interrupted!"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixtureExecutor(t)
			d := Dashboard{Name: "Cancellation", Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}}}}}
			if err := e.Validate(t.Context(), &d); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			e.engine = &callerCancelledEngine{Engine: e.engine, cancel: cancel, err: tc.err}
			var output bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(old)
			_, err := e.Run(ctx, RunRequest{Dashboard: d})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected caller cancellation, got %v", err)
			}
			if output.Len() != 0 {
				t.Fatalf("caller cancellation logged: %s", output.String())
			}
		})
	}
}

func TestFailedKeepsErrorLogsUnlessCallerCausedCancellation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		stopped error
	}{
		{"real failure after caller stopped", errors.New("query failed"), context.Canceled},
		{"engine interrupt at batch deadline", &duckdb.Error{Type: duckdb.ErrorTypeInterrupt, Msg: "INTERRUPT Error: Interrupted!"}, context.DeadlineExceeded},
		{"cancellation with active caller", context.Canceled, nil},
		{"panel deadline", context.DeadlineExceeded, nil},
		{"batch deadline", context.DeadlineExceeded, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(old)
			result := failed(Result{ID: "p"}, tc.err, tc.stopped)
			if result.Status != StatusError || result.Error == "" {
				t.Fatalf("failure result lost: %+v", result)
			}
			for _, text := range []string{`"level":"ERROR"`, `"msg":"panel query failed"`, `"panel_id":"p"`} {
				if !strings.Contains(output.String(), text) {
					t.Errorf("missing %s in log: %s", text, output.String())
				}
			}
		})
	}
}
