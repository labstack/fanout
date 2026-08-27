package query

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock lets the gate's grace period be crossed deterministically.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func TestParquetGateRemovesCanceledPublisher(t *testing.T) {
	var gate parquetReadGate
	gate.RLock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gate.LockContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("LockContext error = %v, want deadline exceeded", err)
	}
	if got := gate.WaitingWriters(); got != 0 {
		t.Fatalf("canceled publisher remained queued: %d", got)
	}
	gate.RUnlock()
	if !gate.TryRLock() {
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
	for gate.WaitingWriters() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("publisher never queued")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForQueuedWriters(t *testing.T, gate *parquetReadGate, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for gate.WaitingWriters() < count {
		if time.Now().After(deadline) {
			t.Fatalf("publishers queued = %d, want %d", gate.WaitingWriters(), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestParquetGateAdmitsReadersWhileWriterGraceRuns(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	if !gate.TryRLock() {
		t.Fatal("first reader was not admitted")
	}
	go gate.Lock()
	waitForQueuedWriter(t, gate)
	clock.Advance(defaultWriterGrace / 2)
	if !gate.TryRLock() {
		t.Fatal("reader refused while the publisher was still inside its grace period")
	}
	gate.RUnlock()
	gate.RUnlock()
}

func TestParquetGateQueuesReadersOnceWriterGraceExpires(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	if !gate.TryRLock() {
		t.Fatal("first reader was not admitted")
	}
	go gate.Lock()
	waitForQueuedWriter(t, gate)
	clock.Advance(defaultWriterGrace + time.Second)
	if gate.TryRLock() {
		gate.RUnlock()
		gate.RUnlock()
		t.Fatal("reader admitted past an expired publisher grace; maintenance can starve indefinitely")
	}
	gate.RUnlock()
}

func TestParquetGatePublishesAfterOverlappingReadersDrain(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	gate.RLock()
	published := make(chan struct{})
	go func() {
		gate.Lock()
		close(published)
		gate.Unlock()
	}()
	waitForQueuedWriter(t, gate)
	// Overlapping traffic keeps arriving, but only within the grace period.
	if !gate.TryRLock() {
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

func TestParquetGateDistinguishesWritersQueuedAtSameInstant(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	gate := &parquetReadGate{now: clock.Now}
	gate.RLock()
	acquired := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	for range 2 {
		go func() {
			gate.Lock()
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
	for gate.WaitingWriters() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("stale publisher remained queued: %d", gate.WaitingWriters())
		}
		time.Sleep(time.Millisecond)
	}
	deadline = time.Now().Add(time.Second)
	for !gate.TryRLock() {
		if time.Now().After(deadline) {
			t.Fatal("reader refused after both publishers completed")
		}
		time.Sleep(time.Millisecond)
	}
	gate.RUnlock()
}
