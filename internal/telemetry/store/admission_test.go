package store

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestAdmissionDebtAIMD(t *testing.T) {
	now := time.Unix(1, 0)
	debt := 0
	a := newIngestAdmission(func() int { return debt }, nil)
	a.now = func() time.Time { return now }
	a.Admit(100000)
	now = now.Add(time.Second)
	a.update()
	a.Admit(100000)
	debt = admissionBandStart
	now = now.Add(admissionInterval)
	a.update()
	if a.rate != 100000 {
		t.Fatalf("entry rate=%v", a.rate)
	}
	debt++
	now = now.Add(admissionInterval)
	a.update()
	if a.rate != 85000 {
		t.Fatalf("decrease=%v", a.rate)
	}
	debt--
	now = now.Add(admissionInterval)
	a.update()
	if a.rate < 93499.99 || a.rate > 93500.01 {
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
	busy := uint64(0)
	a := newIngestAdmission(func() int { return 0 }, func() uint64 { return busy })
	a.now = func() time.Time { return now }
	a.Admit(1000)
	for range 4 {
		now = now.Add(admissionInterval)
		a.update()
	}
	busy++
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
	for range 320 {
		busy++
		now = now.Add(admissionInterval)
		a.update()
	}
	if a.rate < 500 {
		t.Fatalf("below historical floor: %v", a.rate)
	}
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

func TestAdmissionColdPressureCanAcceptAnExport(t *testing.T) {
	now := time.Unix(1, 0)
	a := newIngestAdmission(func() int { return admissionBandStart }, nil)
	a.now = func() time.Time { return now }
	a.update()
	now = now.Add(time.Second)
	a.update()
	if _, ok := a.Admit(1000); !ok {
		t.Fatal("empty history stranded first export")
	}
	if _, ok := a.Admit(1000); ok {
		t.Fatal("second export bypassed bucket")
	}
	for range 3 {
		now = now.Add(time.Second)
		if _, ok := a.Admit(1000); !ok {
			t.Fatal("cold pressure lost the request-sized rate seed")
		}
	}
}

func TestPanelPressureDoesNotThrottleIngest(t *testing.T) {
	now := time.Unix(1, 0)
	a := newIngestAdmission(func() int { return 0 }, nil)
	a.now = func() time.Time { return now }
	for range 160 {
		if _, ok := a.Admit(250); !ok {
			t.Fatal("panel queueing refused an export")
		}
		now = now.Add(admissionInterval)
		a.update()
	}
	if a.rate != 0 {
		t.Fatalf("panel queueing capped ingest at %v rows/s", a.rate)
	}
}

func TestReadFloorNeverOverridesCompactionDebt(t *testing.T) {
	now := time.Unix(1, 0)
	debt, busy := 0, uint64(0)
	a := newIngestAdmission(func() int { return debt }, func() uint64 { return busy })
	a.now = func() time.Time { return now }
	a.Admit(200000)
	now = now.Add(admissionInterval)
	a.update()
	debt = admissionBandStart
	for range 80 {
		busy++
		debt++
		now = now.Add(admissionInterval)
		a.update()
	}
	if a.readRate < 100000 {
		t.Fatalf("read floor=%v", a.readRate)
	}
	if a.rate >= 100000 || a.rate != a.debtRate || a.reason != "debt" {
		t.Fatalf("debt cap=%v read cap=%v effective=%v reason=%s", a.debtRate, a.readRate, a.rate, a.reason)
	}
}

func TestDebtCapPreservesOneNormalBatchPerPressureWindow(t *testing.T) {
	now := time.Unix(1, 0)
	debt := 0
	a := newIngestAdmission(func() int { return debt }, nil)
	a.now = func() time.Time { return now }
	for range 4 {
		a.Admit(50000)
		now = now.Add(admissionInterval)
		a.update()
	}
	debt = admissionBandStart
	for range 40 {
		debt++
		now = now.Add(time.Second)
		a.update()
		a.Admit(50000)
	}
	if a.rate < float64(maxGroupBatchRows)/pressureAdmissionWindow.Seconds() {
		t.Fatalf("rate=%v creates smaller files without reducing their frequency", a.rate)
	}
	debt = telemetry.CompactionDebtLimit
	if _, ok := a.Admit(1); ok {
		t.Fatal("batch floor bypassed hard debt stop")
	}
}

func TestAdmissionBandEntryUsesHistoryAcrossRetryGap(t *testing.T) {
	now := time.Unix(1, 0)
	debt := 0
	a := newIngestAdmission(func() int { return debt }, nil)
	a.now = func() time.Time { return now }
	a.Admit(100000)
	now = now.Add(admissionInterval)
	a.update()
	now = now.Add(2 * time.Second)
	debt = admissionBandStart
	a.update()
	if a.rate != 100000 {
		t.Fatalf("retry gap seeded rate=%v, want measured 100000", a.rate)
	}
}

// Model telemetrygen's widest mixed topology: one trace exporter and eight
// log/metric exporters per process, 1,000 rows/export, 30s export timeout.
// The Collector helper and Go OTLP retry use max(RetryInfo, jittered backoff):
// initially 5s +/-50%, multiplying by 1.5 up to 30s.
func TestReadPressureKeepsTelemetrygenExportersAlive(t *testing.T) {
	const exporters = 64 * (1 + 8 + 8)
	now := time.Unix(1, 0)
	busy := uint64(0)
	a := newIngestAdmission(nil, func() uint64 { return busy })
	a.now = func() time.Time { return now }
	a.Admit(477000)
	now = now.Add(admissionInterval)
	a.update()
	type exporter struct {
		next, started time.Time
		backoff       time.Duration
	}
	clients := make([]exporter, exporters)
	random := rand.New(rand.NewPCG(17, 29))
	failures := 0
	for step := 0; step < 2400; step++ {
		busy++
		now = now.Add(admissionInterval)
		offset := random.IntN(exporters)
		for i := range exporters {
			c := &clients[(i+offset)%exporters]
			if now.Before(c.next) {
				continue
			}
			if c.started.IsZero() {
				c.started = now
				c.backoff = 5 * time.Second
			}
			if now.Sub(c.started) >= 30*time.Second {
				failures++
				c.started = time.Time{}
			}
			delay, ok := a.Admit(1000)
			if ok {
				c.started = time.Time{}
				// At 2,500 items/s per log or metric worker, filling a batch takes 400ms.
				c.next = now.Add(400 * time.Millisecond)
			} else {
				jitter := time.Duration(float64(c.backoff) * (0.5 + random.Float64()))
				c.next = now.Add(max(delay, jitter))
				c.backoff = min(30*time.Second, c.backoff*3/2)
			}
		}
	}
	if failures != 0 {
		t.Fatalf("read throttle killed %d exporters", failures)
	}
}

func TestReadRetryWindowPreservesDebtLimits(t *testing.T) {
	now := time.Unix(1, 0)
	debt := 0
	a := newIngestAdmission(func() int { return debt }, nil)
	a.now = func() time.Time { return now }
	a.last = now
	a.rate, a.readRate, a.readReference, a.reason = 500, 500, 1000, "reads"
	a.readLimited = true
	a.samples = []admissionSample{{at: now, rows: 1000, rate: 1000}}
	if _, ok := a.Admit(1000); ok {
		t.Fatal("expected read refusal")
	}
	// A late refusal must not move the open interval away from earlier retries.
	until := a.readRetryUntil
	now = now.Add(999 * time.Millisecond)
	if _, ok := a.Admit(1000); ok {
		t.Fatal("expected last read refusal")
	}
	if a.readRetryUntil != until {
		t.Fatal("late refusal extended closed interval")
	}
	// Include both jitter extremes, the last possible retry, and timing margin.
	for _, elapsed := range []time.Duration{2500 * time.Millisecond, 7500 * time.Millisecond, 8499 * time.Millisecond, 10999 * time.Millisecond} {
		now = time.Unix(1, 0).Add(elapsed)
		if _, ok, reason := a.admit(1000); !ok {
			t.Fatalf("first retry at %s refused by %s", elapsed, reason)
		}
	}
	debt = telemetry.CompactionDebtLimit
	if _, ok, reason := a.admit(1000); ok || reason != "debt" {
		t.Fatal("retry bypassed hard debt limit")
	}
	debt = admissionBandStart
	a.debtRate = 1
	a.tokens = 0
	if _, ok, reason := a.admit(1000); ok || reason != "debt" {
		t.Fatal("retry bypassed debt rate cap")
	}
	debt = 0
	now = time.Unix(1, 0).Add(11250 * time.Millisecond)
	if _, ok, reason := a.admit(1000); ok || reason != "reads" {
		t.Fatal("read cap did not resume after retry interval")
	}
}
