package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

type recoveringCommitter struct {
	mu       sync.Mutex
	failures int
	calls    int
	batches  []Batch
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
	if len(committer.batches) != 1 || len(committer.batches[0].Spans) != 1 {
		t.Fatalf("committed batches = %#v, want original batch exactly once", committer.batches)
	}
}
