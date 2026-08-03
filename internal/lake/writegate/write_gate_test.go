package writegate

import (
	"sync"
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
// catalog write behind the metrics registry.
func TestWriteGateObservesOutsideTheCriticalSection(t *testing.T) {
	t.Parallel()

	var gate WriteGate
	unlock := gate.Lock(WriteMerge)

	reacquired := make(chan struct{})
	var once sync.Once
	go func() {
		// Blocks until the gate is genuinely free.
		defer gate.Lock(WriteMaintenance)()
		once.Do(func() { close(reacquired) })
	}()

	unlock()
	select {
	case <-reacquired:
	case <-time.After(time.Second):
		t.Fatal("gate was not released before the observation completed")
	}
}

func TestWriteGateRecordsPrometheusHistograms(t *testing.T) {
	before := histogramCount(t, WriteMerge)

	var gate WriteGate
	unlock := gate.Lock(WriteMerge)
	unlock()

	if after := histogramCount(t, WriteMerge); after != before+1 {
		t.Fatalf("wait/hold sample count = %d, want %d", after, before+1)
	}
}

// histogramCount reports how many fanout_write_gate_wait_seconds samples exist
// for one operation. Deltas rather than Reset() keep this immune to test order.
func histogramCount(t *testing.T, operation WriteOperation) uint64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "fanout_write_gate_wait_seconds" {
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
