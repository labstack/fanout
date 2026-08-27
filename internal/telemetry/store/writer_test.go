package store

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

type recordingCommitter struct {
	mu       sync.Mutex
	failures int
	calls    int
	batches  []Batch
}

func (c *recordingCommitter) Commit(batch Batch) error {
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
		repository: committer, batchSize: batchSize, done: make(chan struct{}),
		submissions: make(chan submission, submissionQueueDepth),
	}
}

type blockingCommitter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingCommitter) Commit(Batch) error {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return nil
}

func TestWriterAcknowledgesOnlyAfterAtomicCommit(t *testing.T) {
	committer := &blockingCommitter{entered: make(chan struct{}), release: make(chan struct{})}
	w := testWriter(committer, maxBatchRows)
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
	w := testWriter(committer, maxBatchRows)
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

func TestWriterSplitsOversizedSubmission(t *testing.T) {
	committer := &recordingCommitter{}
	w := testWriter(committer, maxBatchRows)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	if err := w.Submit(context.Background(), Batch{Spans: make([]telemetry.Span, maxBatchRows+1)}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if len(committer.batches) != 2 {
		t.Fatalf("committed batches = %d, want 2", len(committer.batches))
	}
	if batchRows(committer.batches[0])+batchRows(committer.batches[1]) != maxBatchRows+1 {
		t.Fatal("split lost rows")
	}
	for _, batch := range committer.batches {
		if batch.ID == "" || batchRows(batch) > maxBatchRows {
			t.Fatalf("invalid chunk: id=%q rows=%d", batch.ID, batchRows(batch))
		}
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

func TestWriterSurfacesPermanentFailureToSubmitAndRun(t *testing.T) {
	committer := &recordingCommitter{failures: commitRetryLimit + 1}
	w := testWriter(committer, 1)
	w.retryDelay = func(int) time.Duration { return 0 }
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(context.Background()) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace"}}}); err == nil {
		t.Fatal("Submit succeeded after permanent storage failure")
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run succeeded after permanent storage failure")
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if committer.calls != commitRetryLimit || len(committer.batches) != 0 {
		t.Fatalf("calls=%d committed=%d", committer.calls, len(committer.batches))
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

func (c *parallelCommitter) Commit(Batch) error {
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
