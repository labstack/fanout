package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type blockedCompactionCommitter struct {
	entered, release, committed chan struct{}
	once                        sync.Once
}

func (s *blockedCompactionCommitter) Commit(context.Context, Batch) error {
	s.once.Do(func() { close(s.committed) })
	return nil
}
func (s *blockedCompactionCommitter) CompactionDebt() int { return 0 }

func (s *blockedCompactionCommitter) WaitForCompaction(ctx context.Context) (time.Duration, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	at := time.Now()
	select {
	case <-s.release:
		return time.Since(at), nil
	case <-ctx.Done():
		return time.Since(at), ctx.Err()
	}
}
func TestWriterBackpressureKeepsSubmissionsQueuedAndHonorsContexts(t *testing.T) {
	before := testutil.ToFloat64(metrics.IngestBackpressureSeconds)
	s := &blockedCompactionCommitter{entered: make(chan struct{}, 1), release: make(chan struct{}), committed: make(chan struct{})}
	w := NewWriter(nil, 50000)
	w.repository = s
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-s.entered
	submitCtx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if err := w.Submit(submitCtx, testBatch()); err != context.DeadlineExceeded {
		t.Fatalf("submit=%v", err)
	}
	if len(w.submissions) != 1 {
		t.Fatalf("queued=%d", len(w.submissions))
	}
	select {
	case <-s.committed:
		t.Fatal("committed while debt blocked")
	default:
	}
	close(s.release)
	select {
	case <-s.committed:
	case <-time.After(time.Second):
		t.Fatal("retirement did not unblock writer")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(metrics.IngestBackpressureSeconds) <= before {
		t.Fatal("writer wait time not counted")
	}
}
func TestWriterBackpressureStopsOnShutdown(t *testing.T) {
	s := &blockedCompactionCommitter{entered: make(chan struct{}, 1), release: make(chan struct{}), committed: make(chan struct{})}
	w := NewWriter(nil, 50000)
	w.repository = s
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-s.entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stuck behind debt")
	}
}
func TestCompactionPressureDrainsGenerationZeroBeforeHigherGenerations(t *testing.T) {
	for _, mode := range []string{"current", "finished", "higher-same-day", "higher-older-day"} {
		t.Run(mode, func(t *testing.T) {
			var batches []telemetry.BatchMetadata
			count := telemetry.CompactionDebtLimit + telemetry.CompactionBatchLimit - 1
			for i := 0; i < count; i++ {
				batches = append(batches, telemetry.BatchMetadata{ID: fmt.Sprint(i), Spans: 5000, MaxIngestedNanos: 1})
			}
			if mode == "finished" {
				batches = append(batches, telemetry.BatchMetadata{ID: "new-day", Spans: 1, MaxIngestedNanos: int64(24*time.Hour) + 1})
			}
			if mode == "higher-same-day" || mode == "higher-older-day" {
				stamp := int64(1)
				if mode == "higher-older-day" {
					// Keep all stamps positive; generation 0 belongs to day 1.
					for i := range batches {
						batches[i].MaxIngestedNanos += int64(24 * time.Hour)
					}
					stamp = 1
				}
				for i := 0; i < 128; i++ {
					batches = append(batches, telemetry.BatchMetadata{ID: fmt.Sprint("higher", i), Generation: 1, Spans: 5000, MaxIngestedNanos: stamp})
				}
			}
			debt := telemetry.CompactionDebtLimit
			for pass := 0; debt >= telemetry.CompactionDebtLimit/2; pass++ {
				selected := selectCompactionBatches(batches, 128, debt)
				if len(selected) < 2 {
					t.Fatal("positive debt has no selection")
				}
				if selected[0].Generation != 0 {
					t.Fatalf("debt=%d selected generation %d before eligible generation 0", debt, selected[0].Generation)
				}
				debt -= len(selected)
				remove := map[string]bool{}
				for _, b := range selected {
					remove[b.ID] = true
				}
				var next []telemetry.BatchMetadata
				for _, b := range batches {
					if !remove[b.ID] {
						next = append(next, b)
					}
				}
				output := selected[0]
				output.ID = fmt.Sprint("output", pass)
				output.Generation++
				output.Spans *= len(selected)
				next = append(next, output)
				batches = next
			}
			if strings.HasPrefix(mode, "higher-") {
				selected := selectCompactionBatches(batches, 128, debt)
				if len(selected) < 2 || selected[0].Generation == 0 {
					t.Fatalf("higher generations did not resume at debt=%d: %v", debt, selected)
				}
			}
		})
	}
	// Finished days with eight batches are selectable, but fewer than eight
	// per day and individually oversized inputs prove a raw-count stall unsafe.
	var small []telemetry.BatchMetadata
	for i := 0; i < 8; i++ {
		small = append(small, telemetry.BatchMetadata{ID: fmt.Sprint(i), Spans: 1, MaxIngestedNanos: 1})
	}
	small = append(small, telemetry.BatchMetadata{ID: "today", Spans: 1, MaxIngestedNanos: int64(24*time.Hour) + 1})
	if len(selectCompactionBatches(small, 128, 0)) != 8 {
		t.Fatal("finished eight-input group stranded")
	}
	var terminal []telemetry.BatchMetadata
	for i := 0; i < 1024; i++ {
		terminal = append(terminal, telemetry.BatchMetadata{ID: fmt.Sprint(i), Spans: maxCompactionRows, MaxIngestedNanos: 1})
	}
	if len(selectCompactionBatches(terminal, 128, 0)) != 0 {
		t.Fatal("terminal counterexample changed")
	}
}

type pressureRecordingCommitter struct {
	*recordingCommitter
	debt atomic.Int64
}

func (c *pressureRecordingCommitter) WaitForCompaction(ctx context.Context) (time.Duration, error) {
	return 0, ctx.Err()
}
func (c *pressureRecordingCommitter) CompactionDebt() int { return int(c.debt.Load()) }

func TestWriterAdaptsAdmissionToReclaimableDebt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &pressureRecordingCommitter{recordingCommitter: &recordingCommitter{}}
		w := testWriter(c, maxGroupBatchRows)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		defer func() {
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}()
		for _, debt := range []int{telemetry.CompactionDebtLimit/2 - 1, telemetry.CompactionDebtLimit / 2, telemetry.CompactionDebtLimit/2 + 1, 0} {
			c.debt.Store(int64(debt))
			before := len(c.batches)
			acks := make(chan error, 2)
			var latencies [2]time.Duration
			submit := func(i int) {
				at := time.Now()
				err := w.Submit(ctx, testBatch())
				latencies[i] = time.Since(at)
				acks <- err
			}
			go submit(0)
			synctest.Wait()
			time.Sleep(15 * time.Millisecond)
			go submit(1)
			synctest.Wait()
			time.Sleep(300 * time.Millisecond)
			for range 2 {
				if err := <-acks; err != nil {
					t.Fatal(err)
				}
			}
			synctest.Wait()
			want := 2
			wantLatency := [2]time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
			if debt >= telemetry.CompactionDebtLimit/2 {
				want = 1
				wantLatency = [2]time.Duration{35 * time.Millisecond, 20 * time.Millisecond}
			}
			if latencies != wantLatency {
				t.Fatalf("debt=%d acknowledgement latencies=%v want=%v", debt, latencies, wantLatency)
			}
			if got := len(c.batches) - before; got != want {
				t.Fatalf("debt=%d batches=%d want=%d", debt, got, want)
			}
			rows := 0
			for _, b := range c.batches[before:] {
				rows += batchRows(b)
			}
			if rows != 2*batchRows(testBatch()) {
				t.Fatalf("lost rows at debt=%d: %d", debt, rows)
			}
		}
	})
}

func TestWriterPressureAdmissionClosesAtEveryBound(t *testing.T) {
	for _, bound := range []string{"rows", "requests", "bytes"} {
		t.Run(bound, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := &pressureRecordingCommitter{recordingCommitter: &recordingCommitter{}}
				c.debt.Store(telemetry.CompactionDebtLimit / 2)
				w := testWriter(c, maxGroupBatchRows)
				w.compaction = c
				w.submissions = make(chan submission, maxAdmissionRequests)
				request := submission{batch: Batch{Spans: []telemetry.Span{{SpanID: "first"}}}, ack: make(chan error, 1)}
				switch bound {
				case "rows":
					request.batch.Spans = make([]telemetry.Span, maxGroupBatchRows)
				case "requests":
					for range maxAdmissionRequests - 1 {
						w.submissions <- request
					}
				case "bytes":
					request.bytes = maxGroupBatchBytes / 2
					w.submissions <- request
				}
				out := make(chan commitJob, maxAdmissionRequests)
				at := time.Now()
				if err := w.enqueueSubmissions(t.Context(), request, out); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(at); elapsed != 0 {
					t.Fatalf("%s bound waited %v", bound, elapsed)
				}
				if len(out) != 1 {
					t.Fatalf("jobs=%d", len(out))
				}
				job := <-out
				if len(job.acks) > maxAdmissionRequests || batchRows(job.batches[0]) > maxGroupBatchRows {
					t.Fatal("group bound exceeded")
				}
			})
		})
	}
}

func TestWriterPressureAdmissionClosesOnIdleOrDeadline(t *testing.T) {
	for _, interval := range []time.Duration{15 * time.Millisecond, 25 * time.Millisecond} {
		t.Run(interval.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := &pressureRecordingCommitter{recordingCommitter: &recordingCommitter{}}
				c.debt.Store(telemetry.CompactionDebtLimit / 2)
				w := testWriter(c, maxGroupBatchRows)
				w.compaction = c
				request := submission{batch: testBatch(), ack: make(chan error, 1)}
				out := make(chan commitJob, 1)
				done := make(chan error, 1)
				at := time.Now()
				var elapsed time.Duration
				go func() {
					err := w.enqueueSubmissions(t.Context(), request, out)
					elapsed = time.Since(at)
					done <- err
				}()
				synctest.Wait()
				// Keep arrivals below the idle cutoff, or let the first group idle.
				for i := 1; i <= 16; i++ {
					time.Sleep(interval)
					w.submissions <- request
					synctest.Wait()
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				job := <-out
				wantRequests := 17
				wantElapsed := pressureAdmissionWindow
				if interval > pressureAdmissionIdle {
					wantRequests = 1
					wantElapsed = pressureAdmissionIdle
				}
				if elapsed != wantElapsed {
					t.Fatalf("dispatch after %v want=%v", elapsed, wantElapsed)
				}
				if len(job.acks) != wantRequests {
					t.Fatalf("requests=%d want=%d", len(job.acks), wantRequests)
				}
			})
		})
	}
}
