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

func TestWriterRetainsBatchAcrossCommitFailures(t *testing.T) {
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	spans <- telemetry.Span{TraceID: "trace", SpanID: "span"}
	close(spans)
	close(logs)
	close(metricRows)
	committer := &recoveringCommitter{failures: 6}
	w := &Writer{
		repository: committer, interval: time.Hour, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
		retryDelay: func(int) time.Duration { return 0 },
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if committer.calls != 7 {
		t.Fatalf("Commit calls = %d, want 7", committer.calls)
	}
	if len(committer.staged) != 1 {
		t.Fatalf("staged batches = %d, want 1", len(committer.staged))
	}
	if len(committer.batches) != 1 || len(committer.batches[0].Spans) != 1 {
		t.Fatalf("committed batches = %#v, want original batch exactly once", committer.batches)
	}
}

type durableFailCommitter struct {
	repository *Repository
	attempted  chan struct{}
	staged     chan struct{}
	once       sync.Once
}

func (c *durableFailCommitter) Stage(batch Batch) error {
	if err := c.repository.Stage(batch); err != nil {
		return err
	}
	c.staged <- struct{}{}
	return nil
}

func (c *durableFailCommitter) Commit(Batch) error {
	c.once.Do(func() { close(c.attempted) })
	return errors.New("storage stalled")
}

func TestWriterShutdownReplaysEveryStagedBatch(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	spans := make(chan telemetry.Span, 5)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	for i := range 5 {
		spans <- telemetry.Span{TraceID: "trace", SpanID: fmt.Sprintf("span-%d", i), StartUnixNanos: int64(100 + i)}
	}
	committer := &durableFailCommitter{repository: repository, attempted: make(chan struct{}), staged: make(chan struct{}, 5)}
	w := &Writer{
		repository: committer, interval: time.Hour, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
		retryDelay: func(int) time.Duration { return time.Hour }, shutdownGrace: 25 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- w.Run(ctx) }()
	for range 5 {
		select {
		case <-committer.staged:
		case <-time.After(time.Second):
			t.Fatal("queued batch was not staged")
		}
	}
	select {
	case <-committer.attempted:
	case <-time.After(time.Second):
		t.Fatal("commit attempt did not start")
	}
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

func TestWriterDropsPoisonBatchAfterBoundedRetries(t *testing.T) {
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	spans <- telemetry.Span{TraceID: "trace", SpanID: "span"}
	close(spans)
	close(logs)
	close(metricRows)
	committer := &recoveringCommitter{failures: commitRetryLimit + 1}
	w := &Writer{
		repository: committer, interval: time.Hour, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
		retryDelay: func(int) time.Duration { return 0 },
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if committer.calls != commitRetryLimit {
		t.Fatalf("Commit calls = %d, want %d", committer.calls, commitRetryLimit)
	}
	if len(committer.batches) != 0 {
		t.Fatalf("committed poison batches = %#v", committer.batches)
	}
}

func TestWriterCancellationInterruptsCommitBackoff(t *testing.T) {
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	spans <- telemetry.Span{TraceID: "trace", SpanID: "span"}
	committer := &recoveringCommitter{failures: commitRetryLimit + 1}
	w := &Writer{
		repository: committer, interval: time.Hour, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
		retryDelay: func(int) time.Duration { return time.Hour }, shutdownGrace: 25 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- w.Run(ctx) }()
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

type stageFailCommitter struct {
	mu      sync.Mutex
	stages  int
	commits int
}

func (c *stageFailCommitter) Stage(Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stages++
	return errors.New("wal device unavailable")
}

func (c *stageFailCommitter) Commit(Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commits++
	return nil
}

func TestWriterSurvivesStageFailure(t *testing.T) {
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	spans <- telemetry.Span{TraceID: "trace", SpanID: "span"}
	close(spans)
	close(logs)
	close(metricRows)
	committer := &stageFailCommitter{}
	w := &Writer{
		repository: committer, interval: time.Hour, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
		retryDelay: func(int) time.Duration { return 0 },
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v, want nil: an unstageable batch must be dropped with accounting, not kill the writer", err)
	}
	if committer.stages == 0 {
		t.Fatal("Stage was never attempted")
	}
	if committer.commits != 0 {
		t.Fatalf("Commit calls = %d, want 0 for an unstaged batch", committer.commits)
	}
}

type ioFailCommitter struct {
	repository *Repository
	mu         sync.Mutex
	attempts   int
}

func (c *ioFailCommitter) Stage(batch Batch) error { return c.repository.Stage(batch) }

func (c *ioFailCommitter) Commit(Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	return errors.New("storage unavailable")
}

func TestWriterKeepsWALWhenCommitRetriesAreExhausted(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	spans <- telemetry.Span{TraceID: "trace", SpanID: "span", StartUnixNanos: 100, IngestedAt: 100}
	close(spans)
	close(logs)
	close(metricRows)
	committer := &ioFailCommitter{repository: repository}
	w := &Writer{
		repository: committer, interval: time.Hour, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
		retryDelay: func(int) time.Duration { return 0 },
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
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
		t.Fatalf("retained WAL files = %d, want 1: an I/O failure must leave the batch replayable, not delete its only durable copy", kept)
	}
}

func TestRecoverQuarantinesBatchThatCannotApply(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	if err := repository.Stage(batch); err != nil {
		t.Fatal(err)
	}
	// Make the hot span directory unusable so replay's apply always fails.
	spanDir := filepath.Join(dir, "hot", "spans")
	if err := os.RemoveAll(spanDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spanDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open succeeded with an unusable hot span directory")
	}
	if err := os.Remove(spanDir); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open error = %v: a batch that cannot apply must be quarantined, not crash-loop every boot", err)
	}
	defer reopened.Close()
}

type transientStageCommitter struct {
	repository *Repository
	mu         sync.Mutex
	failures   int
	stages     int
	committed  []Batch
}

func (c *transientStageCommitter) Stage(batch Batch) error {
	c.mu.Lock()
	c.stages++
	fail := c.stages <= c.failures
	c.mu.Unlock()
	if fail {
		return errors.New("wal device busy")
	}
	return c.repository.Stage(batch)
}

func (c *transientStageCommitter) Commit(batch Batch) error {
	if err := c.repository.Commit(batch); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.committed = append(c.committed, batch)
	return nil
}

func TestWriterCarriesRowsForwardAcrossTransientStageFailure(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	spans := make(chan telemetry.Span, 2)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	spans <- telemetry.Span{TraceID: "trace", SpanID: "a", StartUnixNanos: 100, IngestedAt: 100}
	committer := &transientStageCommitter{repository: repository, failures: 1}
	w := &Writer{
		repository: committer, interval: 5 * time.Millisecond, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
		retryDelay: func(int) time.Duration { return 0 },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- w.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		committer.mu.Lock()
		done := len(committer.committed)
		committer.mu.Unlock()
		if done > 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-finished
			t.Fatal("rows were not retried after a transient Stage failure; they were dropped instead of carried forward")
		}
		time.Sleep(time.Millisecond)
	}
	close(spans)
	close(logs)
	close(metricRows)
	cancel()
	<-finished
	committer.mu.Lock()
	defer committer.mu.Unlock()
	total := 0
	for _, batch := range committer.committed {
		total += len(batch.Spans)
	}
	if total != 1 {
		t.Fatalf("committed spans = %d, want the single carried-forward row", total)
	}
}
