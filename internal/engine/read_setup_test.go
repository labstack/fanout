package engine

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/queryrows"
)

// Panic at the first setup operation after readConn has acquired its pin.
type panicReadWindowContext struct{ context.Context }

func (c panicReadWindowContext) Value(key any) any {
	if fmt.Sprintf("%T", key) == "queryrows.windowKey" {
		panic("read setup")
	}
	return c.Context.Value(key)
}

func TestQuerySetupPanicReleasesPinAndConnection(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Add(-time.Minute)
	commitCurrentBatches(t, repo, "panic", 8, at)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("setup did not panic")
			}
		}()
		_, _ = d.QueryContext(panicReadWindowContext{t.Context()}, "SELECT 1")
	}()
	requireReadsReleased(t, d)
	if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
		t.Fatalf("compact: %d %v", n, err)
	}
	if n, err := repo.CollectRetiredParquet(); err != nil || n != 8 {
		t.Fatalf("panic left retired inputs pinned: collected=%d err=%v", n, err)
	}
}

// panicValue panics while database/sql converts it, inside the statement
// call, where database/sql holds the connection's close lock.
type panicValue struct{}

func (panicValue) Value() (driver.Value, error) { panic("statement") }

func TestStatementPanicUnregistersStatement(t *testing.T) {
	for _, name := range []string{"query", "window", "row"} {
		t.Run(name, func(t *testing.T) {
			d, _ := newBatchCacheTest(t)
			progress := d.progress.Load()
			run := func() {
				if name != "row" {
					ctx := t.Context()
					if name == "window" {
						ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: time.Unix(0, 0), End: time.Now()})
					}
					_, _ = d.QueryContext(ctx, "SELECT ?", panicValue{})
					return
				}
				var v any
				_ = d.QueryRowScan(t.Context(), []any{&v}, "SELECT ?", panicValue{})
			}
			finished := make(chan any, 1)
			go func() {
				defer func() {
					finished <- recover()
				}()
				run()
			}()
			select {
			case value := <-finished:
				if value != "statement" {
					t.Fatalf("panic = %v, want statement", value)
				}
			case <-time.After(time.Second):
				t.Fatalf("panic cleanup blocked with %d registered statements", d.inFlight())
			}
			if n := d.inFlight(); n != 0 {
				t.Errorf("%s: panic left %d registered statements", name, n)
			}
			if d.Overruns() != 0 || d.held() {
				t.Errorf("%s: panic left the read pool held: overruns=%d", name, d.Overruns())
			}
			if d.progress.Load() != progress {
				t.Error("panic was recorded as engine progress")
			}
			// The pool still serves reads.
			var one int
			if err := d.QueryRowScan(t.Context(), []any{&one}, "SELECT 1"); err != nil || one != 1 {
				t.Fatalf("read after panics: %d %v", one, err)
			}
		})
	}
}

// requireReadsReleased waits for abandoned reads to return to the pool.
func requireReadsReleased(t *testing.T, d *Duck) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for d.DB.Stats().InUse != 0 || d.abandoned.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("panic leaked %d read connections", d.DB.Stats().InUse)
		}
		time.Sleep(time.Millisecond)
	}
}
