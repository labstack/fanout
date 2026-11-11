package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A stall that lasts stallExit fires once; a shorter one, or none, never does.
func TestStallWatchFiresOnceAfterAContinuousStall(t *testing.T) {
	var w stallWatch
	start := time.Unix(1000, 0)
	if w.observe(start, "") {
		t.Fatal("fired without a stall")
	}
	if w.observe(start, "read") || w.observe(start.Add(stallExit-time.Second), "read") {
		t.Fatal("fired before the stall lasted stallExit")
	}
	// The stall cleared, so the next one starts its own clock.
	w.observe(start.Add(stallExit-time.Second), "")
	if w.observe(start.Add(stallExit+time.Second), "read") {
		t.Fatal("a stall that cleared kept its start")
	}
	later := start.Add(2*stallExit + time.Second)
	if !w.observe(later, "read") {
		t.Fatal("a continuous stall did not fire")
	}
	if w.observe(later.Add(time.Hour), "read") {
		t.Fatal("fired twice")
	}
}

// watchFor runs the watchdog on a clock that advances a second per tick, for
// up to n ticks, and returns what it delivered on Fatal, if anything.
func watchFor(t *testing.T, d *Duck, n int) error {
	t.Helper()
	d.fatal = make(chan error, 1)
	ctx, stop := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { d.watch(ctx, ticks); close(done) }()
	defer func() { stop(); <-done }()
	now := time.Now()
	for i := range n {
		select {
		case err := <-d.Fatal():
			return err
		case ticks <- now.Add(time.Duration(i) * time.Second):
		case <-done:
		}
		// Let an asynchronous ping report before the next tick.
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case err := <-d.Fatal():
		return err
	default:
		return nil
	}
}

var watchTicks = int((stallExit + 5*time.Second) / time.Second)

// A publication that storage stopped serving delivers ErrStalled on Fatal,
// the channel that flushes ingest and exits nonzero.
func TestWatchExitsOnAStalledPublication(t *testing.T) {
	d, _ := mockDuck(t)
	d.noteProgress()
	d.publishing.Store(time.Now().Add(-publishStall - time.Second).UnixNano())
	if err := watchFor(t, d, watchTicks); !errors.Is(err, ErrStalled) {
		t.Fatalf("fatal = %v, want ErrStalled", err)
	}
}

// A costly query running far past its deadline is the user's, not Pitot's:
// the watchdog reports it but never exits.
func TestWatchNeverExitsOnAQueryOverrun(t *testing.T) {
	d, _ := mockDuck(t)
	late, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Hour))
	defer cancel()
	defer d.begin(late, "SELECT levenshtein(a, b)")(nil)
	// Long idle, and every ping fails: the overrun holds the pool.
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	if err := watchFor(t, d, 2*watchTicks); err != nil {
		t.Fatalf("a query overrun fired the watchdog: %v", err)
	}
}

// Overload is no stall: reads within their deadlines never fire it.
func TestWatchIgnoresReadsWithinTheirDeadlines(t *testing.T) {
	d, _ := mockDuck(t)
	busy, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	for range 8 {
		defer d.begin(busy, "SELECT 1")(nil)
	}
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	if err := watchFor(t, d, 2*watchTicks); err != nil {
		t.Fatalf("overload fired the watchdog: %v", err)
	}
}

// An idle engine that cannot answer a ping while nothing holds the pool is
// Pitot's stall.
func TestWatchExitsWhenAnIdleEngineCannotAnswer(t *testing.T) {
	d, _ := mockDuck(t)
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	// No expectations: every ping fails.
	if err := watchFor(t, d, watchTicks); !errors.Is(err, ErrStalled) {
		t.Fatalf("fatal = %v, want ErrStalled", err)
	}
}

// A ping still waiting for a connection holds none. With a one-connection
// pool held by a caller reading rows, the wait is no stall.
func TestWatchIgnoresAPingWaitingForAHeldPool(t *testing.T) {
	d, _ := mockDuck(t)
	d.DB.SetMaxOpenConns(1)
	held, err := d.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	if err := watchFor(t, d, 2*watchTicks); err != nil {
		t.Fatalf("a ping waiting for a held pool fired the watchdog: %v", err)
	}
}
