package store

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type recordingCommitter struct {
	mu       sync.Mutex
	failures int
	calls    int
	batches  []Batch
}

func (c *recordingCommitter) Commit(_ context.Context, batch Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls <= c.failures {
		return errors.New("storage unavailable")
	}
	c.batches = append(c.batches, batch)
	return nil
}

func testWriter(committer batchCommitter, batchSize int) *Writer {
	return &Writer{
		repository: committer, batchSize: batchSize, groupWindow: groupAdmissionWindow, done: make(chan struct{}), stopping: make(chan struct{}),
		submissions: make(chan *submission, submissionQueueDepth),
	}
}

type blockingCommitter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingCommitter) Commit(context.Context, Batch) error {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return nil
}

func TestWriterAcknowledgesOnlyAfterAtomicCommit(t *testing.T) {
	committer := &blockingCommitter{entered: make(chan struct{}), release: make(chan struct{})}
	w := testWriter(committer, maxGroupBatchRows)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}})
	}()
	select {
	case <-committer.entered:
	case <-time.After(time.Second):
		t.Fatal("commit did not start")
	}
	select {
	case err := <-submitDone:
		t.Fatalf("Submit returned before commit completed: %v", err)
	default:
	}
	close(committer.release)
	if err := <-submitDone; err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
}

func TestWriterGroupsQueuedSubmissions(t *testing.T) {
	committer := &recordingCommitter{}
	w := testWriter(committer, maxGroupBatchRows)
	w.SetInFlightBudget(8192)
	results := make(chan error, 4)
	for i := range 4 {
		go func() {
			results <- w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i)}}})
		}()
	}
	waitForQueue(t, w, 4)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	for range 4 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := w.inFlightBytes.Load(); got != 0 {
		t.Fatalf("grouped commit retained %d bytes", got)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if len(committer.batches) != 1 || len(committer.batches[0].Spans) != 4 {
		t.Fatalf("committed batches = %#v", committer.batches)
	}
}

func TestWriterAdmitsSubmissionArrivingDuringBoundedWindow(t *testing.T) {
	committer := &recordingCommitter{}
	w := testWriter(committer, maxGroupBatchRows)
	w.groupWindow = 100 * time.Millisecond
	first := newSubmission(context.Background(), Batch{Spans: []telemetry.Span{{SpanID: "first"}}})
	second := newSubmission(context.Background(), Batch{Spans: []telemetry.Span{{SpanID: "second"}}})
	out := make(chan commitJob, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		time.Sleep(time.Millisecond)
		w.submissions <- second
	}()
	<-started
	if err := w.enqueueSubmissions(context.Background(), first, out); err != nil {
		t.Fatal(err)
	}
	job := <-out
	if batchRows(job.batch()) != 2 || len(job.requests) != 2 {
		t.Fatalf("admitted job = %#v", job)
	}
}

func TestWriterCommitsOversizedSubmissionAtomically(t *testing.T) {
	committer := &recordingCommitter{}
	w := testWriter(committer, maxGroupBatchRows)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	if err := w.Submit(context.Background(), Batch{Spans: make([]telemetry.Span, maxGroupBatchRows+1)}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if len(committer.batches) != 1 {
		t.Fatalf("committed batches = %d, want one atomic request", len(committer.batches))
	}
	if batchRows(committer.batches[0]) != maxGroupBatchRows+1 {
		t.Fatal("atomic oversized commit lost rows")
	}
	if committer.batches[0].ID == "" {
		t.Fatal("oversized batch has no commit ID")
	}
}

func TestWriterRetriesTransientCommitFailure(t *testing.T) {
	committer := &recordingCommitter{failures: 2}
	w := testWriter(committer, 1)
	w.retryDelay = func(int) time.Duration { return 0 }
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace"}}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if committer.calls != 3 || len(committer.batches) != 1 {
		t.Fatalf("calls=%d committed=%d", committer.calls, len(committer.batches))
	}
}

func TestWriterRecordsLiveQueueAndFlushMetrics(t *testing.T) {
	metrics.FlushTotal.Reset()
	metrics.IngestQueueDepth.Reset()
	committer := &recordingCommitter{}
	w := testWriter(committer, 10)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	batch := Batch{
		Spans:   []telemetry.Span{{SpanID: "span"}},
		Logs:    []telemetry.Log{{Body: "log"}},
		Metrics: []telemetry.Metric{{Name: "metric"}},
	}
	if err := w.Submit(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if got := testutil.ToFloat64(metrics.FlushTotal.WithLabelValues(signal)); got != 1 {
			t.Fatalf("flush total for %s = %v, want 1", signal, got)
		}
	}
	if got := testutil.ToFloat64(metrics.IngestQueueDepth.WithLabelValues("batch")); got != 0 {
		t.Fatalf("submission queue depth after shutdown = %v, want 0", got)
	}
}

func TestWriterFailsOneSubmissionAndKeepsServing(t *testing.T) {
	metrics.RowsDropped.Reset()
	committer := &recordingCommitter{failures: commitRetryLimit}
	w := testWriter(committer, 1)
	w.retryDelay = func(int) time.Duration { return 0 }
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace"}}}); err == nil {
		t.Fatal("Submit succeeded after permanent storage failure")
	}
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "recovered"}}}); err != nil {
		t.Fatalf("writer did not recover after a failed submission: %v", err)
	}
	select {
	case err := <-runDone:
		t.Fatalf("writer stopped after one failed submission: %v", err)
	default:
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if committer.calls != commitRetryLimit+1 || len(committer.batches) != 1 {
		t.Fatalf("calls=%d committed=%d", committer.calls, len(committer.batches))
	}
	if got := testutil.ToFloat64(metrics.RowsDropped.WithLabelValues("spans")); got != 1 {
		t.Fatalf("dropped span rows = %v, want 1", got)
	}
}

type parallelCommitter struct {
	mu        sync.Mutex
	active    int
	maxActive int
	started   chan struct{}
	release   chan struct{}
	target    int
	once      sync.Once
}

func (c *parallelCommitter) Commit(context.Context, Batch) error {
	c.mu.Lock()
	c.active++
	c.maxActive = max(c.maxActive, c.active)
	if c.active == c.target {
		c.once.Do(func() { close(c.started) })
	}
	c.mu.Unlock()
	<-c.release
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return nil
}

func TestWriterCommitsIndependentBatchesInParallel(t *testing.T) {
	jobs := min(maxCommitWorkers, max(1, runtime.GOMAXPROCS(0)))
	committer := &parallelCommitter{started: make(chan struct{}), release: make(chan struct{}), target: jobs}
	w := testWriter(committer, 1)
	results := make(chan error, jobs)
	for i := range jobs {
		go func() {
			results <- w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{SpanID: fmt.Sprint(i)}}})
		}()
	}
	waitForQueue(t, w, jobs)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	select {
	case <-committer.started:
	case <-time.After(2 * time.Second):
		close(committer.release)
		t.Fatal("commit workers did not run four independent batches concurrently")
	}
	close(committer.release)
	for range jobs {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if committer.maxActive != jobs {
		t.Fatalf("maximum concurrent commits = %d, want %d", committer.maxActive, jobs)
	}
}

func waitForQueue(t *testing.T, w *Writer, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(w.submissions) != count {
		if time.Now().After(deadline) {
			t.Fatalf("queued submissions = %d, want %d", len(w.submissions), count)
		}
		time.Sleep(time.Millisecond)
	}
}

// A sender that stops waiting before its commit starts will resend. Committing
// the abandoned copy as well would store every row twice.
func TestWriterDropsSubmissionAbandonedBeforeCommit(t *testing.T) {
	committer := &recordingCommitter{}
	w := testWriter(committer, maxGroupBatchRows)
	w.SetInFlightBudget(8192)
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan error, 1)
	go func() {
		results <- w.Submit(ctx, Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "abandoned"}}})
	}()
	waitForQueue(t, w, 1)
	before := testutil.ToFloat64(metrics.IngestAbandonedTotal)
	cancel()
	if err := <-results; !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit = %v, want context.Canceled", err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(runCtx) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "kept"}}}); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if len(committer.batches) != 1 || len(committer.batches[0].Spans) != 1 || committer.batches[0].Spans[0].SpanID != "kept" {
		t.Fatalf("committed batches = %#v", committer.batches)
	}
	if got := testutil.ToFloat64(metrics.IngestAbandonedTotal) - before; got != 1 {
		t.Fatalf("abandoned counter rose by %v, want 1", got)
	}
	if got := w.inFlightBytes.Load(); got != 0 {
		t.Fatalf("abandoned submission retained %d bytes", got)
	}
}

// Once a commit has started it completes: the batch is atomic, and the sender
// that gave up meanwhile retries into at-least-once delivery.
func TestWriterFinishesCommitStartedBeforeAbandonment(t *testing.T) {
	committer := &blockingCommitter{entered: make(chan struct{}), release: make(chan struct{})}
	recorded := &recordingCommitter{}
	w := testWriter(committerFunc(func(ctx context.Context, batch Batch) error {
		if err := committer.Commit(ctx, batch); err != nil {
			return err
		}
		return recorded.Commit(ctx, batch)
	}), maxGroupBatchRows)
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(runCtx) }()
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan error, 1)
	go func() {
		results <- w.Submit(ctx, Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "started"}}})
	}()
	<-committer.entered
	cancel()
	if err := <-results; !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit = %v, want context.Canceled", err)
	}
	close(committer.release)
	stop()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	if len(recorded.batches) != 1 || len(recorded.batches[0].Spans) != 1 {
		t.Fatalf("committed batches = %#v", recorded.batches)
	}
}

func TestWriterGroupDropsOnlyAbandonedSubmissions(t *testing.T) {
	committer := &recordingCommitter{}
	w := testWriter(committer, maxGroupBatchRows)
	w.SetInFlightBudget(8192)
	abandoned, cancel := context.WithCancel(context.Background())
	results := make(chan error, 4)
	for i := range 4 {
		ctx := context.Background()
		if i%2 == 1 {
			ctx = abandoned
		}
		go func() {
			results <- w.Submit(ctx, Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i)}}})
		}()
	}
	waitForQueue(t, w, 4)
	cancel()
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(runCtx) }()
	var canceled int
	for range 4 {
		if err := <-results; errors.Is(err, context.Canceled) {
			canceled++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	stop()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if canceled != 2 {
		t.Fatalf("canceled = %d, want 2", canceled)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if len(committer.batches) != 1 {
		t.Fatalf("committed batches = %d, want one group", len(committer.batches))
	}
	got := map[string]bool{}
	for _, span := range committer.batches[0].Spans {
		got[span.SpanID] = true
	}
	if len(got) != 2 || !got["span-0"] || !got["span-2"] {
		t.Fatalf("committed spans = %v, want span-0 and span-2", got)
	}
	if got := w.inFlightBytes.Load(); got != 0 {
		t.Fatalf("group retained %d bytes", got)
	}
}

type committerFunc func(context.Context, Batch) error

func (f committerFunc) Commit(ctx context.Context, batch Batch) error { return f(ctx, batch) }
