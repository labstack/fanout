package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
)

const flushQueueDepth = 4

type batchCommitter interface {
	Commit(Batch) error
}

type Writer struct {
	repository batchCommitter
	interval   time.Duration
	batchSize  int
	spans      <-chan telemetry.Span
	logs       <-chan telemetry.Log
	metricRows <-chan telemetry.Metric
	bufSpans   []telemetry.Span
	bufLogs    []telemetry.Log
	bufMetrics []telemetry.Metric
	retryDelay func(int) time.Duration
	done       chan struct{}
}

func NewWriter(repository *Repository, interval time.Duration, batchSize int, spans <-chan telemetry.Span, logs <-chan telemetry.Log, metricRows <-chan telemetry.Metric) *Writer {
	return &Writer{repository: repository, interval: interval, batchSize: batchSize, spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{})}
}

func (w *Writer) Wait() { <-w.done }

func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)
	flushes := make(chan Batch, flushQueueDepth)
	workerDone := make(chan error, 1)
	go w.flushWorker(flushes, workerDone)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	spans, logs, metricRows := w.spans, w.logs, w.metricRows
	finish := func() error {
		w.drain(&spans, &logs, &metricRows)
		if err := w.flush(flushes, workerDone); err != nil {
			return err
		}
		close(flushes)
		return <-workerDone
	}
	for {
		select {
		case row, ok := <-spans:
			if !ok {
				spans = nil
			} else {
				w.bufSpans = append(w.bufSpans, row)
				metrics.RecordIngest("spans", 1)
			}
		case row, ok := <-logs:
			if !ok {
				logs = nil
			} else {
				w.bufLogs = append(w.bufLogs, row)
				metrics.RecordIngest("logs", 1)
			}
		case row, ok := <-metricRows:
			if !ok {
				metricRows = nil
			} else {
				w.bufMetrics = append(w.bufMetrics, row)
				metrics.RecordIngest("metrics", 1)
			}
		case <-ticker.C:
			if err := w.flush(flushes, workerDone); err != nil {
				return err
			}
		case <-ctx.Done():
			return finish()
		case err := <-workerDone:
			return err
		}
		if len(w.bufSpans)+len(w.bufLogs)+len(w.bufMetrics) >= w.batchSize {
			if err := w.flush(flushes, workerDone); err != nil {
				return err
			}
		}
		if spans == nil && logs == nil && metricRows == nil {
			return finish()
		}
	}
}

func (w *Writer) flush(out chan<- Batch, workerDone <-chan error) error {
	if len(w.bufSpans)+len(w.bufLogs)+len(w.bufMetrics) == 0 {
		return nil
	}
	batch := Batch{ID: uuid.NewString(), Spans: append([]telemetry.Span(nil), w.bufSpans...), Logs: append([]telemetry.Log(nil), w.bufLogs...), Metrics: append([]telemetry.Metric(nil), w.bufMetrics...)}
	select {
	case out <- batch:
		w.bufSpans = w.bufSpans[:0]
		w.bufLogs = w.bufLogs[:0]
		w.bufMetrics = w.bufMetrics[:0]
		return nil
	case err := <-workerDone:
		return err
	}
}

func (w *Writer) flushWorker(in <-chan Batch, done chan<- error) {
	for batch := range in {
		for attempt := 0; ; attempt++ {
			err := w.repository.Commit(batch)
			if err == nil {
				break
			}
			metrics.FlushErrors.WithLabelValues("batch").Inc()
			// Log immediately and then at powers of two so a persistent storage
			// outage stays visible without producing an unbounded log storm. The
			// batch remains at the head of this bounded worker queue, applying
			// backpressure until the same durable transaction commits.
			if attempt == 0 || attempt&(attempt-1) == 0 {
				slog.Warn("telemetry batch commit failed; retrying", "batch_id", batch.ID, "attempt", attempt+1, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", err)
			}
			delay := defaultCommitRetryDelay(attempt)
			if w.retryDelay != nil {
				delay = w.retryDelay(attempt)
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}
	done <- nil
}

func defaultCommitRetryDelay(attempt int) time.Duration {
	shift := min(attempt, 6)
	return min(100*time.Millisecond*time.Duration(1<<shift), 5*time.Second)
}

func (w *Writer) drain(spans *<-chan telemetry.Span, logs *<-chan telemetry.Log, metricRows *<-chan telemetry.Metric) {
	for {
		drained := false
		select {
		case row, ok := <-*spans:
			if ok {
				w.bufSpans = append(w.bufSpans, row)
				drained = true
			} else {
				*spans = nil
			}
		default:
		}
		select {
		case row, ok := <-*logs:
			if ok {
				w.bufLogs = append(w.bufLogs, row)
				drained = true
			} else {
				*logs = nil
			}
		default:
		}
		select {
		case row, ok := <-*metricRows:
			if ok {
				w.bufMetrics = append(w.bufMetrics, row)
				drained = true
			} else {
				*metricRows = nil
			}
		default:
		}
		if !drained {
			return
		}
	}
}
