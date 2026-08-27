package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
)

const (
	flushQueueDepth     = 4
	commitRetryLimit    = 8
	writerShutdownGrace = 5 * time.Second
)

type batchCommitter interface {
	Stage(Batch) error
	Commit(Batch) error
	Discard(string) error
}

type Writer struct {
	repository    batchCommitter
	interval      time.Duration
	batchSize     int
	spans         <-chan telemetry.Span
	logs          <-chan telemetry.Log
	metricRows    <-chan telemetry.Metric
	bufSpans      []telemetry.Span
	bufLogs       []telemetry.Log
	bufMetrics    []telemetry.Metric
	retryDelay    func(int) time.Duration
	shutdownGrace time.Duration
	done          chan struct{}
}

func NewWriter(repository *Repository, interval time.Duration, batchSize int, spans <-chan telemetry.Span, logs <-chan telemetry.Log, metricRows <-chan telemetry.Metric) *Writer {
	return &Writer{repository: repository, interval: interval, batchSize: batchSize, spans: spans, logs: logs, metricRows: metricRows, done: make(chan struct{})}
}

func (w *Writer) Wait() { <-w.done }

func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)
	flushes := make(chan Batch, flushQueueDepth)
	workerDone := make(chan error, 1)
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	go w.flushWorker(workerCtx, flushes, workerDone)
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
	finishBounded := func() error {
		grace := w.shutdownGrace
		if grace <= 0 {
			grace = writerShutdownGrace
		}
		timer := time.AfterFunc(grace, cancelWorker)
		defer timer.Stop()
		return finish()
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
			return finishBounded()
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
	// Establish durability before the asynchronous handoff. From this point on,
	// cancellation may stop retries or leave batches queued, but every row is
	// replayable from WAL on the next start. When the WAL itself is unwritable
	// no durability exists to protect; drop this batch with visible accounting
	// and keep the process serving rather than shutting everything down.
	if err := w.repository.Stage(batch); err != nil {
		metrics.FlushErrors.WithLabelValues("stage").Inc()
		recordDroppedBatch(batch)
		slog.Error("telemetry batch could not be staged durably; dropping", "batch_id", batch.ID, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", err)
		w.bufSpans = w.bufSpans[:0]
		w.bufLogs = w.bufLogs[:0]
		w.bufMetrics = w.bufMetrics[:0]
		return nil
	}
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

func (w *Writer) flushWorker(ctx context.Context, in <-chan Batch, done chan<- error) {
	for {
		var batch Batch
		select {
		case <-ctx.Done():
			done <- ctx.Err()
			return
		case next, ok := <-in:
			if !ok {
				done <- nil
				return
			}
			batch = next
		}
		committed := false
		var lastErr error
		for attempt := 0; attempt < commitRetryLimit; attempt++ {
			err := w.repository.Commit(batch)
			if err == nil {
				committed = true
				break
			}
			lastErr = err
			metrics.FlushErrors.WithLabelValues("batch").Inc()
			// Log immediately and then at powers of two so a persistent storage
			// outage stays visible without producing an unbounded log storm.
			if attempt == 0 || attempt&(attempt-1) == 0 {
				slog.Warn("telemetry batch commit failed; retrying", "batch_id", batch.ID, "attempt", attempt+1, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", err)
			}
			delay := defaultCommitRetryDelay(attempt)
			if w.retryDelay != nil {
				delay = w.retryDelay(attempt)
			}
			if attempt+1 == commitRetryLimit {
				break
			}
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					done <- ctx.Err()
					return
				case <-timer.C:
				}
			}
		}
		if !committed {
			if err := w.repository.Discard(batch.ID); err != nil {
				done <- fmt.Errorf("discard poison telemetry batch %s: %w", batch.ID, err)
				return
			}
			recordDroppedBatch(batch)
			slog.Error("telemetry batch permanently failed; dropping after bounded retries", "batch_id", batch.ID, "attempts", commitRetryLimit, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", lastErr)
		}
	}
}

func recordDroppedBatch(batch Batch) {
	metrics.RowsDropped.WithLabelValues("spans").Add(float64(len(batch.Spans)))
	metrics.RowsDropped.WithLabelValues("logs").Add(float64(len(batch.Logs)))
	metrics.RowsDropped.WithLabelValues("metrics").Add(float64(len(batch.Metrics)))
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
