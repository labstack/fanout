package writegate

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestWriteGateSerializesHoldersInAcquisitionOrder(t *testing.T) {
	t.Parallel()

	var gate WriteGate
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	secondEntered := make(chan struct{})
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		unlock := gate.Lock(WriteMerge)
		close(firstEntered)
		<-releaseFirst
		unlock()
	}()
	<-firstEntered

	go func() {
		defer func() { done <- struct{}{} }()
		close(secondStarted)
		defer gate.Lock(WriteMaintenance)()
		close(secondEntered)
	}()
	<-secondStarted

	select {
	case <-secondEntered:
		t.Fatal("second holder entered while the first held the gate")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second holder did not enter after the first released the gate")
	}
	<-done
	<-done
}

// A panicking holder must not strand the gate. Recovering and re-acquiring in
// the same test proves the mutex is free, which a leaked lock would deadlock on.
func TestWriteGateReleasesAfterPanic(t *testing.T) {
	t.Parallel()

	var gate WriteGate
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic to propagate")
			}
		}()
		defer gate.Lock(WriteMerge)()
		panic("boom")
	}()

	acquired := make(chan struct{})
	go func() {
		defer gate.Lock(WriteMerge)()
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("gate was still held after the holder panicked")
	}
}

// The observation must happen after Unlock. A Prometheus histogram takes its
// own lock, so observing inside the critical section would serialize every
// catalog write behind the metrics registry. Asserting the gate is acquirable
// from inside the observer is what makes the ordering detectable — timing the
// release cannot, because a Prometheus write is only microseconds.
func TestWriteGateObservesOutsideTheCriticalSection(t *testing.T) {
	var gate WriteGate

	var freeDuringObserve bool
	restore := swapObserver(t, func(string, float64, float64) {
		if gate.mu.TryLock() {
			freeDuringObserve = true
			gate.mu.Unlock()
		}
	})
	defer restore()

	gate.Lock(WriteMerge)()

	if !freeDuringObserve {
		t.Fatal("gate was still held while the observation ran — move the Unlock above observe()")
	}
}

// Both halves must reach Prometheus: wait time without hold time cannot answer
// whether ingest is stalling behind rollups, which is the question this
// instrumentation exists to answer.
func TestWriteGateRecordsBothWaitAndHoldHistograms(t *testing.T) {
	beforeWait := histogramCount(t, "fanout_write_gate_wait_seconds", WriteMerge)
	beforeHold := histogramCount(t, "fanout_write_gate_hold_seconds", WriteMerge)

	var gate WriteGate
	gate.Lock(WriteMerge)()

	if after := histogramCount(t, "fanout_write_gate_wait_seconds", WriteMerge); after != beforeWait+1 {
		t.Errorf("wait sample count = %d, want %d", after, beforeWait+1)
	}
	if after := histogramCount(t, "fanout_write_gate_hold_seconds", WriteMerge); after != beforeHold+1 {
		t.Errorf("hold sample count = %d, want %d", after, beforeHold+1)
	}
}

// swapObserver installs a test observer. These tests must not run in parallel
// with each other: the seam is package-level.
func swapObserver(t *testing.T, fn func(string, float64, float64)) func() {
	t.Helper()
	original := observe
	observe = fn
	return func() { observe = original }
}

// histogramCount reports how many samples one write-gate histogram holds for an
// operation. Deltas rather than Reset() keep this immune to test order.
func histogramCount(t *testing.T, name string, operation WriteOperation) uint64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if matchesOperation(metric, string(operation)) {
				return metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func matchesOperation(metric *dto.Metric, operation string) bool {
	for _, label := range metric.GetLabel() {
		if label.GetName() == "operation" && label.GetValue() == operation {
			return true
		}
	}
	return false
}
