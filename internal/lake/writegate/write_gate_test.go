package writegate

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestWriteGateSerializesCallbacksInAcquisitionOrder(t *testing.T) {
	t.Parallel()

	gate := newWriteGate(time.Now, func(WriteOperation, time.Duration, time.Duration) {})
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	secondEntered := make(chan struct{})
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})

	go func() {
		defer close(firstDone)
		if err := gate.With(WriteMerge, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		}); err != nil {
			t.Errorf("first callback: %v", err)
		}
	}()
	<-firstEntered

	go func() {
		defer close(secondDone)
		close(secondStarted)
		if err := gate.With(WriteMaintenance, func() error {
			close(secondEntered)
			return nil
		}); err != nil {
			t.Errorf("second callback: %v", err)
		}
	}()
	<-secondStarted

	select {
	case <-secondEntered:
		t.Fatal("second callback entered while the first held the gate")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second callback did not enter after the first released the gate")
	}
	<-firstDone
	<-secondDone
}

func TestWriteGateReleasesAfterReturnedError(t *testing.T) {
	t.Parallel()

	gate := newWriteGate(time.Now, func(WriteOperation, time.Duration, time.Duration) {})
	want := errors.New("write failed")
	if err := gate.With(WriteIngestSpans, func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("With() error = %v, want %v", err, want)
	}

	entered := false
	if err := gate.With(WriteIngestLogs, func() error {
		entered = true
		return nil
	}); err != nil {
		t.Fatalf("second With(): %v", err)
	}
	if !entered {
		t.Fatal("gate remained locked after callback error")
	}
}

func TestWriteGateReleasesAfterPanic(t *testing.T) {
	t.Parallel()

	gate := newWriteGate(time.Now, func(WriteOperation, time.Duration, time.Duration) {})
	func() {
		defer func() {
			if got := recover(); got != "boom" {
				t.Fatalf("recover() = %v, want boom", got)
			}
		}()
		_ = gate.With(WriteIngestMetrics, func() error { panic("boom") })
	}()

	if err := gate.With(WriteRollupService, func() error { return nil }); err != nil {
		t.Fatalf("gate remained locked after panic: %v", err)
	}
}

func TestWriteGateRecordsWaitAndHoldForEveryCall(t *testing.T) {
	t.Parallel()

	base := time.Unix(100, 0)
	times := []time.Time{
		base,
		base.Add(3 * time.Millisecond),
		base.Add(10 * time.Millisecond),
	}
	var nowIndex atomic.Int64
	now := func() time.Time {
		index := int(nowIndex.Add(1) - 1)
		return times[index]
	}

	type observation struct {
		operation WriteOperation
		wait      time.Duration
		hold      time.Duration
	}
	var mu sync.Mutex
	var got []observation
	gate := newWriteGate(now, func(operation WriteOperation, wait, hold time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, observation{operation: operation, wait: wait, hold: hold})
	})

	if err := gate.With(WriteRollupEndpoint, func() error { return nil }); err != nil {
		t.Fatalf("With(): %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("observations = %d, want 1", len(got))
	}
	if got[0].operation != WriteRollupEndpoint {
		t.Errorf("operation = %q, want %q", got[0].operation, WriteRollupEndpoint)
	}
	if got[0].wait != 3*time.Millisecond {
		t.Errorf("wait = %s, want 3ms", got[0].wait)
	}
	if got[0].hold != 7*time.Millisecond {
		t.Errorf("hold = %s, want 7ms", got[0].hold)
	}
}

func TestWriteGateRejectsUnboundedOperationLabel(t *testing.T) {
	t.Parallel()

	gate := newWriteGate(time.Now, func(WriteOperation, time.Duration, time.Duration) {})
	defer func() {
		if recover() == nil {
			t.Fatal("With() accepted an operation outside the bounded label set")
		}
	}()
	_ = gate.With(WriteOperation("tenant-supplied-value"), func() error { return nil })
}

func TestWriteGateDisabledInstrumentationPreservesLockWithoutTiming(t *testing.T) {
	original := instrumentationEnabled
	instrumentationEnabled = func() bool { return false }
	t.Cleanup(func() { instrumentationEnabled = original })

	clockReads := 0
	gate := newWriteGate(func() time.Time {
		clockReads++
		return time.Unix(0, 0)
	}, nil)
	unlock := gate.Lock(WriteIngestSpans)
	unlock()
	if clockReads != 0 {
		t.Fatalf("disabled instrumentation read the clock %d times", clockReads)
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		unlock := gate.Lock(WriteIngestLogs)
		close(locked)
		<-release
		unlock()
	}()
	<-locked
	secondEntered := make(chan struct{})
	go func() {
		unlock := gate.Lock(WriteIngestMetrics)
		close(secondEntered)
		unlock()
	}()
	select {
	case <-secondEntered:
		t.Fatal("disabled instrumentation bypassed mutual exclusion")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second lock did not proceed after release")
	}
}

func TestNewWriteGateRecordsPrometheusHistograms(t *testing.T) {
	operation := WriteRollupEdge
	waitBefore := histogramSampleCount(t, "fanout_write_gate_wait_seconds", string(operation))
	holdBefore := histogramSampleCount(t, "fanout_write_gate_hold_seconds", string(operation))

	gate := NewWriteGate()
	if err := gate.With(operation, func() error { return nil }); err != nil {
		t.Fatalf("With(): %v", err)
	}

	if got := histogramSampleCount(t, "fanout_write_gate_wait_seconds", string(operation)); got != waitBefore+1 {
		t.Errorf("wait histogram count = %d, want %d", got, waitBefore+1)
	}
	if got := histogramSampleCount(t, "fanout_write_gate_hold_seconds", string(operation)); got != holdBefore+1 {
		t.Errorf("hold histogram count = %d, want %d", got, holdBefore+1)
	}
}

func histogramSampleCount(t *testing.T, metricName, operation string) uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "operation" && label.GetValue() == operation {
					return metric.GetHistogram().GetSampleCount()
				}
			}
		}
		return 0
	}
	return 0
}
