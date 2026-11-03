package store

import (
	"context"
	"errors"
	"github.com/labstack/fanout/internal/telemetry"
	"strings"
	"testing"
	"time"
)

func TestAdmissionDebtAIMD(t *testing.T) {
	now := time.Unix(1, 0)
	debt := 0
	a := newIngestAdmission(func() int { return debt }, nil, nil)
	a.now = func() time.Time { return now }
	a.Admit(1000)
	now = now.Add(time.Second)
	a.update()
	a.Admit(1000)
	debt = admissionBandStart
	now = now.Add(admissionInterval)
	a.update()
	if a.rate != 1000 {
		t.Fatalf("entry rate=%v", a.rate)
	}
	debt++
	now = now.Add(admissionInterval)
	a.update()
	if a.rate != 850 {
		t.Fatalf("decrease=%v", a.rate)
	}
	debt--
	now = now.Add(admissionInterval)
	a.update()
	if a.rate < 934.99 || a.rate > 935.01 {
		t.Fatalf("recovery=%v", a.rate)
	}
	debt = telemetry.CompactionDebtLimit
	if d, ok := a.Admit(1); ok || d != 2*time.Second {
		t.Fatalf("hard stop=%v %v", d, ok)
	}
	debt = 0
	now = now.Add(admissionInterval)
	a.update()
	if a.rate != 0 {
		t.Fatal("did not uncap")
	}
}

func TestAdmissionReadPressureAndRetryBounds(t *testing.T) {
	now := time.Unix(1, 0)
	pressure := false
	a := newIngestAdmission(func() int { return 0 }, func() bool { return pressure }, nil)
	a.now = func() time.Time { return now }
	a.Admit(1000)
	for range 4 {
		now = now.Add(admissionInterval)
		a.update()
	}
	pressure = true
	for range 16 {
		now = now.Add(admissionInterval)
		a.update()
	}
	if !a.readLimited || a.rate <= 0 {
		t.Fatalf("reads did not cap: %v", a.rate)
	}
	a.tokens = 0
	if d, ok := a.Admit(1); ok || d != time.Second {
		t.Fatalf("min=%v %v", d, ok)
	}
	if d, ok := a.Admit(1000000); ok || d != 5*time.Second {
		t.Fatalf("max=%v %v", d, ok)
	}
	for range 80 {
		now = now.Add(admissionInterval)
		a.update()
	}
	if a.rate < 100 {
		t.Fatalf("below historical floor: %v", a.rate)
	}
	pressure = false
	for range 120 {
		now = now.Add(admissionInterval)
		a.update()
	}
	if a.rate != 0 {
		t.Fatalf("did not recover: %v", a.rate)
	}
}

func TestAdmissionReceiptIsConsumedOnce(t *testing.T) {
	w := NewWriter(nil, 1)
	ctx := WithIngestAdmission(t.Context())
	if err := w.Admit(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if consumeAdmission(ctx, NewWriter(nil, 1), 10) {
		t.Fatal("wrong writer")
	}
	if !consumeAdmission(ctx, w, 10) || consumeAdmission(ctx, w, 10) {
		t.Fatal("receipt reused")
	}
}

// Everything else in this package makes one commit cost less. None of it makes
// the process bounded: concurrency is HTTP/2 streams times connections, each
// handler holding its decoded batch until the commit is durably acknowledged,
// and nothing counts how much is in flight at once. A large enough burst is
// still an out-of-memory kill however cheap each batch became.
//
// Submit is where that is countable -- both transports funnel through it, and
// it transfers its reservation to the queue until the writer finishes with the
// payload, even when the caller stops waiting for durable acknowledgement.
func TestSubmitRefusesWorkBeyondTheInFlightBudget(t *testing.T) {
	w := NewWriter(nil, 1000)
	w.inFlightBudget = 4096

	big := Batch{Spans: []telemetry.Span{{Attributes: map[string]any{"payload": strings.Repeat("x", 8192)}}}}
	if got := w.reserve(batchBytes(big)); got != nil {
		t.Fatalf("a lone request must be admitted however large, got %v", got)
	}
	// Now over budget on a single oversized reservation.
	small := Batch{Spans: []telemetry.Span{{Attributes: map[string]any{"payload": strings.Repeat("x", 16)}}}}
	if err := w.reserve(batchBytes(small)); !errors.Is(err, ErrIngestOverBudget) {
		t.Errorf("reserve while over budget = %v, want ErrIngestOverBudget", err)
	}

	w.release(batchBytes(big))
	if err := w.reserve(batchBytes(small)); err != nil {
		t.Errorf("reserve after the budget was released = %v, want nil", err)
	}
	w.release(batchBytes(small))
	if left := w.inFlightBytes.Load(); left != 0 {
		t.Errorf("in-flight bytes = %d after every release, want 0", left)
	}
}

// A budget of zero means unbounded, so an operator who has not configured one
// gets exactly the behaviour they had before.
func TestSubmitBudgetOfZeroAdmitsEverything(t *testing.T) {
	w := NewWriter(nil, 1000)
	w.inFlightBudget = 0
	huge := Batch{Spans: []telemetry.Span{{Attributes: map[string]any{"payload": strings.Repeat("x", 1<<20)}}}}
	for range 64 {
		if err := w.reserve(batchBytes(huge)); err != nil {
			t.Fatalf("unbounded budget refused a request: %v", err)
		}
	}
}

// Refusal has to reach the caller as a retryable condition, not a silent drop:
// an OTLP exporter that is told the collector is out of capacity backs off and
// resends, which is the whole point of shedding rather than dying.
func TestSubmitOverBudgetIsReportedToTheCaller(t *testing.T) {
	w := NewWriter(nil, 1000)
	w.inFlightBudget = 1
	batch := Batch{Spans: []telemetry.Span{{Attributes: map[string]any{"payload": strings.Repeat("x", 4096)}}}}
	if err := w.reserve(batchBytes(batch)); err != nil {
		t.Fatalf("first reservation must be admitted: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := w.Submit(ctx, batch)
	if !errors.Is(err, ErrIngestOverBudget) {
		t.Errorf("Submit over budget = %v, want ErrIngestOverBudget", err)
	}
}
