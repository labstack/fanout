package query

import (
	"sync"
	"testing"
	"time"
)

// fakeClock lets the gate's grace period be crossed deterministically.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
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
