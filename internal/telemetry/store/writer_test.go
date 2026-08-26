package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

type failingCommitter struct{}

func (failingCommitter) Commit(Batch) error { return errors.New("disk full") }

func TestWriterSurfacesPermanentCommitFailure(t *testing.T) {
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log)
	metricRows := make(chan telemetry.Metric)
	spans <- telemetry.Span{TraceID: "trace", SpanID: "span"}
	close(spans)
	close(logs)
	close(metricRows)
	w := &Writer{
		repository: failingCommitter{}, interval: time.Hour, batchSize: 1,
		spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{}),
	}
	start := time.Now()
	err := w.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Run error = %v, want permanent commit failure", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("commit failure surfaced after %s", elapsed)
	}
}
