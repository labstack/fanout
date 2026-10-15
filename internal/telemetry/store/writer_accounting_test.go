package store

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func accountingBatch() Batch {
	return Batch{Logs: []telemetry.Log{{Body: strings.Repeat("x", 4096)}}}
}

func TestWriterRetainsQueuedReservationAfterCallerTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := testWriter(&recordingCommitter{}, 1)
		w.SetInFlightBudget(8192)
		batch := accountingBatch()
		for range 8 {
			ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
			err := w.Submit(ctx, batch)
			cancel()
			if err != context.DeadlineExceeded && !errors.Is(err, ErrIngestOverBudget) {
				t.Fatalf("submit: %v", err)
			}
		}
		if got := w.inFlightBytes.Load(); got != int64(len(w.submissions)*batchBytes(batch)) || got > 8192 || got == 0 {
			t.Fatalf("retained queue=%d bytes=%d, budget=8192", len(w.submissions), got)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if got := w.inFlightBytes.Load(); got != 0 || len(w.submissions) != 0 {
			t.Fatalf("shutdown retained queue=%d bytes=%d", len(w.submissions), got)
		}
	})
}

func TestWriterRetainsCommittingReservationAfterCallerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &blockingCommitter{entered: make(chan struct{}), release: make(chan struct{})}
		w := testWriter(c, 1)
		w.SetInFlightBudget(1) // One oversized request is still admitted.
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		defer func() {
			select {
			case <-c.release:
			default:
				close(c.release)
			}
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}()
		submitCtx, stop := context.WithCancel(t.Context())
		result := make(chan error, 1)
		batch := accountingBatch()
		go func() { result <- w.Submit(submitCtx, batch) }()
		<-c.entered
		stop()
		if err := <-result; err != context.Canceled {
			t.Fatalf("submit=%v", err)
		}
		if got := w.inFlightBytes.Load(); got != int64(batchBytes(batch)) {
			t.Fatalf("commit reservation released early: %d", got)
		}
		if err := w.Submit(t.Context(), batch); !errors.Is(err, ErrIngestOverBudget) {
			t.Fatalf("retry bypassed retained commit: %v", err)
		}
		close(c.release)
		synctest.Wait()
		if got := w.inFlightBytes.Load(); got != 0 {
			t.Fatalf("completed commit retained %d bytes", got)
		}
	})
}

func TestWriterReleasesUnenqueuedAndDiscardedJobsExactlyOnce(t *testing.T) {
	for _, mode := range []string{"admission", "direct", "grouped"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := testWriter(&recordingCommitter{}, 1)
				w.SetInFlightBudget(1 << 20)
				w.groupWindow = time.Hour
				if mode == "admission" {
					w.batchSize = 100
				}
				if mode == "grouped" {
					w.batchSize = 3
				}
				requests := make([]submission, 4)
				for i := range requests {
					batch := accountingBatch()
					requests[i] = submission{batch: batch, bytes: batchBytes(batch), ack: make(chan error, 1)}
					if err := w.reserve(requests[i].bytes); err != nil {
						t.Fatal(err)
					}
					if i > 0 {
						w.submissions <- requests[i]
					}
				}
				capacity := 0
				if mode == "direct" {
					capacity = 1 // Transfer one job, then fail the next enqueue.
				}
				out := make(chan commitJob, capacity)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- w.enqueueSubmissions(ctx, requests[0], out) }()
				synctest.Wait()
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("enqueue=%v", err)
				}
				want := int64(len(out) * requests[0].bytes)
				if got := w.inFlightBytes.Load(); got != want {
					t.Fatalf("enqueue error retained %d bytes, want queued job bytes %d", got, want)
				}
				close(out)
				// Canceled workers must discard and release jobs already queued.
				w.commitWorker(ctx, out)
				if got := w.inFlightBytes.Load(); got != 0 {
					t.Fatalf("discard retained %d bytes", got)
				}
				for _, request := range requests {
					select {
					case err := <-request.ack:
						if err == nil {
							t.Fatal("discard acknowledged as durable")
						}
					default:
						t.Fatal("discarded request was not acknowledged")
					}
				}
			})
		})
	}
}

func TestWriterReleasesReservationOnCommitFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := testWriter(&recordingCommitter{failures: commitRetryLimit}, 1)
		w.SetInFlightBudget(8192)
		w.retryDelay = func(int) time.Duration { return 0 }
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		if err := w.Submit(t.Context(), accountingBatch()); err == nil {
			t.Fatal("failed commit acknowledged as durable")
		}
		if got := w.inFlightBytes.Load(); got != 0 {
			t.Fatalf("failed commit retained %d bytes", got)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestWriterReleasesReservationsDuringConcurrentShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := testWriter(&recordingCommitter{}, 1)
		w.SetInFlightBudget(1 << 20)
		w.submissions = make(chan submission, 1)
		results := make(chan error, 16)
		for range cap(results) {
			go func() { results <- w.Submit(t.Context(), accountingBatch()) }()
		}
		synctest.Wait() // One queued request and fifteen blocked enqueues.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		for range cap(results) {
			<-results
		}
		if err := w.Submit(t.Context(), accountingBatch()); err == nil {
			t.Fatal("stopped writer accepted work")
		}
		if got := w.inFlightBytes.Load(); got != 0 || len(w.submissions) != 0 {
			t.Fatalf("shutdown retained queue=%d bytes=%d", len(w.submissions), got)
		}
	})
}

type shutdownCommitter struct {
	entered chan struct{}
}

func (c *shutdownCommitter) Commit(ctx context.Context, _ Batch) error {
	c.entered <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func TestWriterReleasesActiveAndQueuedJobsAfterShutdownGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workers := min(maxCommitWorkers, max(1, runtime.GOMAXPROCS(0)))
		c := &shutdownCommitter{entered: make(chan struct{}, 2*workers)}
		w := testWriter(c, 1)
		w.shutdownGrace = time.Millisecond
		w.SetInFlightBudget(1 << 20)
		results := make(chan error, 2*workers)
		for range cap(results) {
			go func() { results <- w.Submit(t.Context(), accountingBatch()) }()
		}
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		for range workers {
			<-c.entered
		}
		synctest.Wait() // All workers committing, the remaining jobs queued.
		if got := w.inFlightBytes.Load(); got != int64(cap(results)*batchBytes(accountingBatch())) {
			t.Fatalf("active and queued bytes=%d", got)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		for range cap(results) {
			if err := <-results; err == nil {
				t.Fatal("shutdown discard acknowledged as durable")
			}
		}
		if got := w.inFlightBytes.Load(); got != 0 {
			t.Fatalf("shutdown grace retained %d bytes", got)
		}
	})
}
