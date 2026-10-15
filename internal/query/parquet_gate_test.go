package query

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock lets the gate's grace period be crossed deterministically.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func waitingParquetWriters(g *parquetReadGate) int {
	g.init()
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.waiting)
}

func TestParquetGateRemovesCanceledPublisher(t *testing.T) {
	var gate parquetReadGate
	mustRLock(t, &gate)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gate.LockContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("LockContext error = %v, want deadline exceeded", err)
	}
	if got := waitingParquetWriters(&gate); got != 0 {
		t.Fatalf("canceled publisher remained queued: %d", got)
	}
	gate.RUnlock()
	if !gate.TryRLock(parquetReadQuery) {
		t.Fatal("canceled publisher continued blocking readers")
	}
	gate.RUnlock()
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func waitForQueuedWriter(t *testing.T, gate *parquetReadGate) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for waitingParquetWriters(gate) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("publisher never queued")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForQueuedWriters(t *testing.T, gate *parquetReadGate, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for waitingParquetWriters(gate) < count {
		if time.Now().After(deadline) {
			t.Fatalf("publishers queued = %d, want %d", waitingParquetWriters(gate), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestParquetGateAdmitsReadersWhileWriterGraceRuns(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	if !gate.TryRLock(parquetReadQuery) {
		t.Fatal("first reader was not admitted")
	}
	go mustLock(gate)
	waitForQueuedWriter(t, gate)
	clock.Advance(defaultWriterGrace / 2)
	if !gate.TryRLock(parquetReadQuery) {
		t.Fatal("reader refused while the publisher was still inside its grace period")
	}
	gate.RUnlock()
	gate.RUnlock()
}

func TestParquetGateQueuesReadersOnceWriterGraceExpires(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	if !gate.TryRLock(parquetReadQuery) {
		t.Fatal("first reader was not admitted")
	}
	go mustLock(gate)
	waitForQueuedWriter(t, gate)
	clock.Advance(defaultWriterGrace + time.Second)
	if gate.TryRLock(parquetReadQuery) {
		gate.RUnlock()
		gate.RUnlock()
		t.Fatal("reader admitted past an expired publisher grace; maintenance can starve indefinitely")
	}
	gate.RUnlock()
}

func TestParquetGatePublishesAfterOverlappingReadersDrain(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	mustRLock(t, gate)
	published := make(chan struct{})
	go func() {
		mustLock(gate)
		close(published)
		gate.Unlock()
	}()
	waitForQueuedWriter(t, gate)
	// Overlapping traffic keeps arriving, but only within the grace period.
	if !gate.TryRLock(parquetReadQuery) {
		t.Fatal("reader refused inside the grace period")
	}
	clock.Advance(defaultWriterGrace + time.Second)
	gate.RUnlock()
	gate.RUnlock()
	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher never ran after readers drained")
	}
}

func TestParquetGateBackgroundArrivalsYieldDuringQueryGrace(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	gate := &parquetReadGate{now: clock.Now}
	for range 3 {
		if !gate.TryRLock(parquetReadBackground) {
			t.Fatal("idle gate refused background work")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	acquired := make(chan error, 1)
	release := make(chan struct{})
	defer close(release)
	go func() {
		err := gate.LockContext(ctx)
		acquired <- err
		if err == nil {
			<-release
			gate.Unlock()
		}
	}()
	waitForQueuedWriter(t, gate)
	var admitted atomic.Int64
	var wg sync.WaitGroup
	defer wg.Wait()
	stop := make(chan struct{})
	defer close(stop)
	// Keep trying while the publisher is both waiting and active.
	for range 4 {
		attempted := make(chan struct{})
		wg.Go(func() {
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			first := true
			for {
				if gate.TryRLock(parquetReadBackground) {
					admitted.Add(1)
					gate.RUnlock()
				}
				if first {
					close(attempted)
					first = false
				}
				select {
				case <-stop:
					return
				case <-ticker.C:
				}
			}
		})
		<-attempted
	}
	for range 10 {
		if !gate.TryRLock(parquetReadQuery) {
			t.Fatal("ordinary reader refused inside the publisher's grace")
		}
		gate.RUnlock()
	}
	for range 3 {
		gate.RUnlock()
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("publisher did not acquire after in-flight background readers drained")
	}
	if gate.TryRLock(parquetReadBackground) {
		gate.RUnlock()
		t.Fatal("background reader entered during publication")
	}
	if got := admitted.Load(); got != 0 {
		t.Fatalf("background arrivals admitted ahead of publisher: %d", got)
	}
}

func TestParquetGateBackgroundWaitResumesAfterPublisherCancellation(t *testing.T) {
	var gate parquetReadGate
	mustRLock(t, &gate)
	defer gate.RUnlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	publisher := make(chan error, 1)
	go func() { publisher <- gate.LockContext(ctx) }()
	waitForQueuedWriter(t, &gate)
	waitCtx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if err := gate.RLockContext(waitCtx, parquetReadBackground); !errors.Is(err, context.DeadlineExceeded) {
		if err == nil {
			gate.RUnlock()
		}
		t.Fatalf("background admission with publisher queued: %v", err)
	}
	background := make(chan error, 1)
	go func() { background <- gate.RLockContext(t.Context(), parquetReadBackground) }()
	cancel()
	if err := <-publisher; !errors.Is(err, context.Canceled) {
		t.Fatalf("publisher cancellation: %v", err)
	}
	select {
	case err := <-background:
		if err != nil {
			t.Fatal(err)
		}
		gate.RUnlock()
	case <-time.After(time.Second):
		t.Fatal("background reader did not resume after publisher canceled")
	}
}

func TestParquetGateDistinguishesWritersQueuedAtSameInstant(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	mustRLock(t, gate)
	acquired := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	for range 2 {
		go func() {
			mustLock(gate)
			acquired <- struct{}{}
			<-release
			gate.Unlock()
		}()
	}
	waitForQueuedWriters(t, gate, 2)
	gate.RUnlock()
	for range 2 {
		select {
		case <-acquired:
			release <- struct{}{}
		case <-time.After(2 * time.Second):
			t.Fatal("publisher never acquired gate")
		}
	}
	deadline := time.Now().Add(time.Second)
	for waitingParquetWriters(gate) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("stale publisher remained queued: %d", waitingParquetWriters(gate))
		}
		time.Sleep(time.Millisecond)
	}
	deadline = time.Now().Add(time.Second)
	for !gate.TryRLock(parquetReadQuery) {
		if time.Now().After(deadline) {
			t.Fatal("reader refused after both publishers completed")
		}
		time.Sleep(time.Millisecond)
	}
	gate.RUnlock()
}

func mustRLock(t *testing.T, gate *parquetReadGate) {
	t.Helper()
	if err := gate.RLockContext(context.Background(), parquetReadQuery); err != nil {
		t.Fatalf("RLockContext: %v", err)
	}
}

func mustLock(gate *parquetReadGate) {
	if err := gate.LockContext(context.Background()); err != nil {
		panic(err)
	}
}
