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
	if stallExit < overrunGrace {
		t.Fatalf("stallExit %s is shorter than the overrun grace", stallExit)
	}
}

// A statement stuck past its deadline makes the watchdog deliver ErrStalled on
// Fatal, the channel that flushes ingest and exits nonzero.
func TestWatchDeliversAStallOnFatal(t *testing.T) {
	d, _ := mockDuck(t)
	d.fatal = make(chan error, 1)
	late, cancel := context.WithDeadline(t.Context(), time.Now().Add(-overrunGrace-time.Second))
	defer cancel()
	defer d.begin(late)(nil)

	now := time.Now()
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { d.watch(t.Context(), ticks); close(done) }()
	ticks <- now
	ticks <- now.Add(stallExit + time.Second)
	select {
	case err := <-d.Fatal():
		if !errors.Is(err, ErrStalled) {
			t.Fatalf("fatal = %v, want ErrStalled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no fatal error after a continuous stall")
	}
	<-done
}

// Overload is no stall: statements within their deadlines never fire it, and
// the watchdog stops with its context.
func TestWatchIgnoresStatementsWithinTheirDeadlines(t *testing.T) {
	d, _ := mockDuck(t)
	d.fatal = make(chan error, 1)
	busy, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	for range 8 {
		defer d.begin(busy)(nil)
	}
	ctx, stop := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { d.watch(ctx, ticks); close(done) }()
	now := time.Now()
	for i := range 4 {
		ticks <- now.Add(time.Duration(i) * stallExit)
	}
	stop()
	<-done
	select {
	case err := <-d.Fatal():
		t.Fatalf("overload fired the watchdog: %v", err)
	default:
	}
}
