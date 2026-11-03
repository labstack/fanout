package store

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
)

const (
	admissionInterval         = 250 * time.Millisecond
	admissionBandStart        = telemetry.CompactionDebtLimit / 2
	admissionDecrease         = 0.85
	admissionIncrease         = 1.10
	admissionFloorFraction    = 0.50
	admissionStarvationWindow = 3 * time.Second
	retryAfterMin             = time.Second
	retryAfterMax             = 5 * time.Second
)

// ErrIngestThrottled refuses the whole export without retaining its payload.
type ErrIngestThrottled struct {
	RetryAfter time.Duration
	Reason     string
}

func (e *ErrIngestThrottled) Error() string {
	return fmt.Sprintf("telemetry ingest throttled by %s pressure", e.Reason)
}

type admissionSample struct {
	at   time.Time
	rows float64
	rate float64
}
type ingestAdmission struct {
	mu                                           sync.Mutex
	now                                          func() time.Time
	debt                                         func() int
	interactiveBusy                              func() uint64
	last, lastReadAdjustment, busyAt             time.Time
	lastBusy                                     uint64
	previousDebt                                 int
	rate, tokens, burst                          float64
	readLimited                                  bool
	debtRate, debtFloor, readRate, readReference float64
	reason                                       string
	samples                                      []admissionSample
}

func newIngestAdmission(debt func() int, busy func() uint64) *ingestAdmission {
	return &ingestAdmission{now: time.Now, debt: debt, interactiveBusy: busy}
}

// SetAdmission must be called before Run or any submissions.
func (w *Writer) SetAdmission(debt func() int, busy func() uint64) {
	w.admission = newIngestAdmission(debt, busy)
}

// WithIngestAdmission gives one export a receipt for pre-conversion admission.
// Submit consumes it once, so direct callers and repeated submissions still pay.
func WithIngestAdmission(ctx context.Context) context.Context {
	return context.WithValue(ctx, admissionKey{}, &admissionReceipt{})
}

type admissionKey struct{}
type admissionReceipt struct {
	mu     sync.Mutex
	writer *Writer
	rows   int
}

func consumeAdmission(ctx context.Context, w *Writer, rows int) bool {
	r, _ := ctx.Value(admissionKey{}).(*admissionReceipt)
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writer != w || rows > r.rows {
		return false
	}
	r.writer = nil
	return true
}

func (w *Writer) Admit(ctx context.Context, rows int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rows <= 0 {
		return nil
	}
	if w.admission != nil {
		delay, ok, reason := w.admission.admit(rows)
		if !ok {
			metrics.IngestThrottled.WithLabelValues(reason).Inc()
			return &ErrIngestThrottled{RetryAfter: delay, Reason: reason}
		}
	}
	if r, _ := ctx.Value(admissionKey{}).(*admissionReceipt); r != nil {
		r.mu.Lock()
		r.writer, r.rows = w, rows
		r.mu.Unlock()
	}
	return nil
}

func (a *ingestAdmission) update() { a.mu.Lock(); defer a.mu.Unlock(); a.tick(a.now()) }
func (a *ingestAdmission) Admit(rows int) (time.Duration, bool) {
	d, ok, _ := a.admit(rows)
	return d, ok
}
func (a *ingestAdmission) admit(rows int) (time.Duration, bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.tick(now)
	debt := 0
	if a.debt != nil {
		debt = a.debt()
	}
	if debt >= telemetry.CompactionDebtLimit {
		return 2 * time.Second, false, "debt"
	}
	if a.rate > 0 {
		// An existing pressured store can start before any export has supplied a
		// rate sample. Seed that empty history with one export so its retry can
		// fit; subsequent exports use the measured rate and the normal bucket.
		if len(a.samples) == 0 {
			if a.debtRate > 0 {
				a.debtRate = max(a.debtRate, float64(rows))
				a.debtFloor = min(a.debtRate, float64(maxGroupBatchRows)/pressureAdmissionWindow.Seconds())
			}
			if a.readLimited {
				a.readReference = max(a.readReference, float64(rows))
				a.readRate = max(a.readRate, float64(rows))
			}
			a.rate = max(a.rate, float64(rows))
			a.tokens = max(a.tokens, float64(rows))
			metrics.IngestAdmissionRate.Set(a.rate)
		}
		// Grow the burst for a single large export: it must eventually fit even
		// when its row count exceeds a second of the current admission rate.
		a.burst = max(a.burst, float64(rows))
		if a.tokens < float64(rows) {
			delay := time.Duration(math.Ceil((float64(rows) - a.tokens) / a.rate * float64(time.Second)))
			return min(retryAfterMax, max(retryAfterMin, delay)), false, a.reason
		}
		a.tokens -= float64(rows)
	}
	at := now.Truncate(admissionInterval)
	if len(a.samples) > 0 && a.samples[len(a.samples)-1].at.Equal(at) {
		a.samples[len(a.samples)-1].rows += float64(rows)
	} else {
		a.samples = append(a.samples, admissionSample{at: at, rows: float64(rows)})
	}
	return 0, true, ""
}

// tick is called under mu, from both the writer's timer and requests. Elapsed
// time, rather than request frequency, drives the controller and token refill.
func (a *ingestAdmission) tick(now time.Time) {
	if a.last.IsZero() {
		a.last = now
		return
	}
	elapsed := now.Sub(a.last)
	if elapsed < admissionInterval {
		return
	}
	oldRate := a.rate
	if oldRate > 0 {
		a.tokens = min(a.burst, a.tokens+oldRate*elapsed.Seconds())
		metrics.IngestBackpressureSeconds.Add(elapsed.Seconds())
	}
	for len(a.samples) > 0 && now.Sub(a.samples[0].at) >= time.Minute {
		a.samples = a.samples[1:]
	}
	recent, peak := 0.0, 0.0
	for i := range a.samples {
		if now.Sub(a.samples[i].at) < time.Second {
			recent += a.samples[i].rows
		}
		peak = max(peak, a.samples[i].rate)
	}
	if len(a.samples) > 0 {
		a.samples[len(a.samples)-1].rate = max(a.samples[len(a.samples)-1].rate, recent)
	}
	peak = max(peak, recent)
	// A cold start in pressure has no historical rate. One row/second is a
	// finite seed; the burst can grow to the first request's size.
	seed := max(1, recent)
	peak = max(1, peak)
	debt := 0
	if a.debt != nil {
		debt = a.debt()
	}
	if a.interactiveBusy != nil {
		busy := a.interactiveBusy()
		if busy > a.lastBusy {
			a.busyAt = now
		}
		a.lastBusy = busy
	}
	starving := !a.busyAt.IsZero() && now.Sub(a.busyAt) < admissionStarvationWindow
	if debt >= admissionBandStart {
		if a.debtRate == 0 {
			// A retry gap can leave the last second empty. Use the measured
			// history rather than stranding a warm store at the cold seed.
			a.debtRate = max(seed, peak)
			a.debtFloor = min(a.debtRate, float64(maxGroupBatchRows)/pressureAdmissionWindow.Seconds())
		} else if debt > a.previousDebt {
			a.debtRate *= admissionDecrease
		} else if debt < a.previousDebt {
			a.debtRate = min(a.debtRate*admissionIncrease, 1.5*peak)
		}
		// Below one normal batch per pressure window, further row cuts only
		// shrink files: the bounded group deadline still creates one per window.
		// Preserve useful batches; the hard debt limit remains unconditional.
		a.debtRate = max(a.debtFloor, a.debtRate)
	} else {
		a.debtRate, a.debtFloor = 0, 0
	}
	if a.lastReadAdjustment.IsZero() || now.Sub(a.lastReadAdjustment) >= time.Second {
		if starving {
			if !a.readLimited {
				// Freeze the pre-starvation rate: a prolonged read incident must not
				// erase its own floor as throttled samples replace healthy history.
				a.readReference = max(peak, seed)
				a.readRate = a.readReference
			}
			a.readRate = max(admissionFloorFraction*a.readReference, a.readRate*admissionDecrease)
			a.readLimited = true
		} else if a.readLimited {
			a.readRate *= admissionIncrease
			if a.readRate >= a.readReference {
				a.readLimited = false
				a.readRate = 0
			}
		}
		a.lastReadAdjustment = now
	}
	a.rate, a.reason = a.debtRate, "debt"
	if a.readLimited && (a.rate == 0 || a.readRate < a.rate) {
		a.rate, a.reason = a.readRate, "reads"
	}
	if a.rate > 0 {
		a.burst = max(a.burst, a.rate)
		if oldRate == 0 {
			a.tokens = a.rate
		}
		a.tokens = min(a.tokens, a.burst)
	} else {
		a.tokens, a.burst = 0, 0
	}
	metrics.IngestAdmissionRate.Set(a.rate)
	a.previousDebt, a.last = debt, now
}
