package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

// Everything else in this package makes one commit cost less. None of it makes
// the process bounded: concurrency is HTTP/2 streams times connections, each
// handler holding its decoded batch until the commit is durably acknowledged,
// and nothing counts how much is in flight at once. A large enough burst is
// still an out-of-memory kill however cheap each batch became.
//
// Submit is where that is countable -- both transports funnel through it, and
// it already blocks until durable acknowledgement, so charging it on entry and
// releasing it on return measures exactly the bytes the process is holding.
func TestSubmitRefusesWorkBeyondTheInFlightBudget(t *testing.T) {
	w := NewWriter(nil, 1000)
	w.inFlightBudget = 4096

	big := Batch{Spans: []telemetry.Span{{AttributesJSON: strings.Repeat("x", 8192)}}}
	if got := w.reserve(big); got != nil {
		t.Fatalf("a lone request must be admitted however large, got %v", got)
	}
	// Now over budget on a single oversized reservation.
	small := Batch{Spans: []telemetry.Span{{AttributesJSON: strings.Repeat("x", 16)}}}
	if err := w.reserve(small); !errors.Is(err, ErrIngestOverBudget) {
		t.Errorf("reserve while over budget = %v, want ErrIngestOverBudget", err)
	}

	w.release(big)
	if err := w.reserve(small); err != nil {
		t.Errorf("reserve after the budget was released = %v, want nil", err)
	}
	w.release(small)
	if left := w.inFlightBytes.Load(); left != 0 {
		t.Errorf("in-flight bytes = %d after every release, want 0", left)
	}
}

// A budget of zero means unbounded, so an operator who has not configured one
// gets exactly the behaviour they had before.
func TestSubmitBudgetOfZeroAdmitsEverything(t *testing.T) {
	w := NewWriter(nil, 1000)
	w.inFlightBudget = 0
	huge := Batch{Spans: []telemetry.Span{{AttributesJSON: strings.Repeat("x", 1<<20)}}}
	for range 64 {
		if err := w.reserve(huge); err != nil {
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
	batch := Batch{Spans: []telemetry.Span{{AttributesJSON: strings.Repeat("x", 4096)}}}
	if err := w.reserve(batch); err != nil {
		t.Fatalf("first reservation must be admitted: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := w.Submit(ctx, batch)
	if !errors.Is(err, ErrIngestOverBudget) {
		t.Errorf("Submit over budget = %v, want ErrIngestOverBudget", err)
	}
}
