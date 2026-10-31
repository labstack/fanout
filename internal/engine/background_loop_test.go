package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

// Failing background passes back off instead of retrying every second: under
// memory pressure each retry reallocates up to the limit and starves queries.
func TestBackgroundLoopBacksOffAfterFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		start := time.Now()
		var at []time.Duration
		fail := true
		done := make(chan struct{})
		go func() {
			defer close(done)
			runBackgroundLoop(ctx, func(context.Context) (bool, error) {
				at = append(at, time.Since(start))
				if fail && len(at) <= 8 {
					return false, errors.New("Out of Memory Error")
				}
				return false, nil
			}, func(error) {})
		}()
		time.Sleep(4 * time.Minute)
		synctest.Wait()
		cancel()
		<-done
		var gaps []time.Duration
		for i := 1; i < len(at) && i <= 9; i++ {
			gaps = append(gaps, at[i]-at[i-1])
		}
		want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute, time.Second}
		if !slices.Equal(gaps, want) {
			t.Fatalf("gaps=%v want %v", gaps, want)
		}
	})
}

// DuckDB refuses all work after a fatal error until the process restarts, so
// the first one a background pass sees is reported once for shutdown.
func TestFatalEngineErrorIsReportedOnce(t *testing.T) {
	d := &Duck{fatal: make(chan error, 1)}
	d.noteFailure(fmt.Errorf("service rollup: %w", &duckdb.Error{Type: duckdb.ErrorTypeOutOfMemory, Msg: "Out of Memory Error: could not allocate block"}))
	select {
	case err := <-d.Fatal():
		t.Fatalf("out of memory reported as fatal: %v", err)
	default:
	}
	invalidated := errors.Join(errors.New("rollup"), &duckdb.Error{Type: duckdb.ErrorTypeFatal, Msg: "FATAL Error: Failed: database has been invalidated because of a previous fatal error."})
	d.noteFailure(invalidated)
	d.noteFailure(invalidated)
	select {
	case err := <-d.Fatal():
		if !errors.Is(err, invalidated) && err != invalidated {
			t.Fatalf("reported %v", err)
		}
	default:
		t.Fatal("fatal engine error was not reported")
	}
	select {
	case err := <-d.Fatal():
		t.Fatalf("reported twice: %v", err)
	default:
	}
}
