package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

type recoveringCommitter struct {
	mu       sync.Mutex
	failures int
	calls    int
	staged   []Batch
	batches  []Batch
}

func (c *recoveringCommitter) Stage(batch Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staged = append(c.staged, batch)
	return nil
}

func (c *recoveringCommitter) Commit(batch Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls <= c.failures {
		return errors.New("disk temporarily unavailable")
	}
	c.batches = append(c.batches, batch)
	return nil
}

func testWriter(committer batchCommitter, batchSize int) *Writer {
	return &Writer{repository: committer, batchSize: batchSize, done: make(chan struct{}), submissions: make(chan submission, submissionQueueDepth)}
}

type blockingStageCommitter struct {
	entered chan struct{}
	release chan struct{}
}

func (c *blockingStageCommitter) Stage(Batch) error {
	close(c.entered)
	<-c.release
	return nil
}

func (c *blockingStageCommitter) Commit(Batch) error { return nil }

func TestWriterAcknowledgesSubmissionOnlyAfterDurableStage(t *testing.T) {
	committer := &blockingStageCommitter{entered: make(chan struct{}), release: make(chan struct{})}
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
		t.Fatal("Stage was not called")
	}
	select {
	case err := <-submitDone:
		t.Fatalf("Submit returned before Stage completed: %v", err)
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

func TestWriterGroupCommitsConcurrentSubmissions(t *testing.T) {
	committer := &recoveringCommitter{}
	w := testWriter(committer, maxBatchRows)
	results := make(chan error, 2)
	for i := range 2 {
		go func() {
			results <- w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i)}}})
		}()
	}
	deadline := time.Now().Add(time.Second)
	for len(w.submissions) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("submissions were not queued")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	for range 2 {
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
	if len(committer.staged) != 1 || len(committer.staged[0].Spans) != 2 {
		t.Fatalf("staged batches = %#v, want one two-row group commit", committer.staged)
	}
}

func TestWriterSplitsOversizedSubmissionBeforeAcknowledging(t *testing.T) {
	committer := &recoveringCommitter{}
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
	if len(committer.staged) != 2 {
		t.Fatalf("staged batches = %d, want 2", len(committer.staged))
	}
	total := 0
	for _, batch := range committer.staged {
		if rows := batchRows(batch); rows > maxBatchRows {
			t.Fatalf("staged batch rows = %d", rows)
		}
		total += batchRows(batch)
	}
	if total != maxBatchRows+1 {
		t.Fatalf("staged rows = %d, want %d", total, maxBatchRows+1)
	}
}

type secondStageFailCommitter struct {
	mu     sync.Mutex
	stages []Batch
}

func (c *secondStageFailCommitter) Stage(batch Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.stages) == 1 {
		return errors.New("WAL device failed mid-request")
	}
	c.stages = append(c.stages, batch)
	return nil
}
func (*secondStageFailCommitter) Commit(Batch) error { return nil }

func TestWriterStopsAfterPartialOversizedSubmissionStage(t *testing.T) {
	committer := &secondStageFailCommitter{}
	w := testWriter(committer, maxBatchRows)
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(context.Background()) }()
	if err := w.Submit(context.Background(), Batch{Spans: make([]telemetry.Span, maxBatchRows+1)}); err == nil {
		t.Fatal("Submit returned nil after only a prefix became durable")
	}
	if err := <-runDone; err == nil || !strings.Contains(err.Error(), "durable chunks") {
		t.Fatalf("Run error = %v, want fatal partial-stage error", err)
	}
}

func TestWriterRetainsBatchAcrossCommitFailures(t *testing.T) {
	committer := &recoveringCommitter{failures: 6}
	w := testWriter(committer, 1)
	w.retryDelay = func(int) time.Duration { return 0 }
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if committer.calls != 7 || len(committer.staged) != 1 || len(committer.batches) != 1 {
		t.Fatalf("calls=%d staged=%d committed=%d", committer.calls, len(committer.staged), len(committer.batches))
	}
}

type durableFailCommitter struct {
	repository *Repository
	attempted  chan struct{}
	once       sync.Once
}

func (c *durableFailCommitter) Stage(batch Batch) error { return c.repository.Stage(batch) }
func (c *durableFailCommitter) Commit(Batch) error {
	c.once.Do(func() { close(c.attempted) })
	return errors.New("storage stalled")
}

func TestWriterShutdownReplaysEveryAcknowledgedBatch(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	committer := &durableFailCommitter{repository: repository, attempted: make(chan struct{})}
	w := testWriter(committer, 1)
	w.retryDelay = func(int) time.Duration { return time.Hour }
	w.shutdownGrace = 25 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- w.Run(ctx) }()
	for i := range 5 {
		if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i), StartUnixNanos: int64(100 + i)}}}); err != nil {
			t.Fatal(err)
		}
	}
	<-committer.attempted
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context canceled", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if got := recovered.Spans.RowCount(); got != 5 {
		t.Fatalf("replayed spans = %d, want 5", got)
	}
}

func TestWriterSurfacesPermanentCommitFailureAfterBoundedRetries(t *testing.T) {
	committer := &recoveringCommitter{failures: commitRetryLimit + 1}
	w := testWriter(committer, 1)
	w.retryDelay = func(int) time.Duration { return 0 }
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(context.Background()) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}}); err != nil {
		t.Fatal(err)
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after permanent commit failure")
	}
	if committer.calls != commitRetryLimit || len(committer.batches) != 0 {
		t.Fatalf("calls=%d committed=%d", committer.calls, len(committer.batches))
	}
}

func TestWriterCancellationInterruptsCommitBackoff(t *testing.T) {
	committer := &recoveringCommitter{failures: commitRetryLimit + 1}
	w := testWriter(committer, 1)
	w.retryDelay = func(int) time.Duration { return time.Hour }
	w.shutdownGrace = 25 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- w.Run(ctx) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		committer.mu.Lock()
		calls := committer.calls
		committer.mu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("commit attempt did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer shutdown remained blocked in retry backoff")
	}
}

type stageFailCommitter struct{ stages, commits int }

func (c *stageFailCommitter) Stage(Batch) error {
	c.stages++
	return errors.New("wal device unavailable")
}
func (c *stageFailCommitter) Commit(Batch) error { c.commits++; return nil }

func TestWriterSurvivesRequestStageFailure(t *testing.T) {
	committer := &stageFailCommitter{}
	w := testWriter(committer, 1)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}}}); err == nil {
		t.Fatal("Submit returned nil after Stage failed")
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if committer.stages != 1 || committer.commits != 0 {
		t.Fatalf("stages=%d commits=%d", committer.stages, committer.commits)
	}
}

type ioFailCommitter struct{ repository *Repository }

func (c *ioFailCommitter) Stage(batch Batch) error { return c.repository.Stage(batch) }
func (*ioFailCommitter) Commit(Batch) error        { return errors.New("storage unavailable") }

func TestWriterKeepsWALWhenCommitRetriesAreExhausted(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := testWriter(&ioFailCommitter{repository: repository}, 1)
	w.retryDelay = func(int) time.Duration { return 0 }
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(context.Background()) }()
	if err := w.Submit(context.Background(), Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span", StartUnixNanos: 100, IngestedAt: 100}}}); err != nil {
		t.Fatal(err)
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after permanent commit failure")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".wal") {
			kept++
		}
	}
	if kept != 1 {
		t.Fatalf("retained WAL files = %d, want 1", kept)
	}
}

type transientStageCommitter struct {
	repository *Repository
	mu         sync.Mutex
	stages     int
	committed  []Batch
}

func (c *transientStageCommitter) Stage(batch Batch) error {
	c.mu.Lock()
	c.stages++
	first := c.stages == 1
	c.mu.Unlock()
	if first {
		return errors.New("wal device busy")
	}
	return c.repository.Stage(batch)
}
func (c *transientStageCommitter) Commit(batch Batch) error {
	if err := c.repository.Commit(batch); err != nil {
		return err
	}
	c.mu.Lock()
	c.committed = append(c.committed, batch)
	c.mu.Unlock()
	return nil
}

func TestWriterAcceptsCallerRetryAfterTransientStageFailure(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	committer := &transientStageCommitter{repository: repository}
	w := testWriter(committer, 1)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()
	batch := Batch{Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span", StartUnixNanos: 100, IngestedAt: 100}}}
	if err := w.Submit(context.Background(), batch); err == nil {
		t.Fatal("first Submit returned nil")
	}
	if err := w.Submit(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	committer.mu.Lock()
	defer committer.mu.Unlock()
	if len(committer.committed) != 1 {
		t.Fatalf("committed batches = %d, want 1", len(committer.committed))
	}
}
