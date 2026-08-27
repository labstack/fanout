package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
)

const (
	flushQueueDepth     = 4
	carryBatches        = 3
	commitRetryLimit    = 8
	writerShutdownGrace = 5 * time.Second
)

type batchCommitter interface {
	Stage(Batch) error
	Commit(Batch) error
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
		if err := w.flushBuffered(flushes, workerDone, true); err != nil {
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
	return w.flushBuffered(out, workerDone, false)
}

// flushBuffered publishes the buffered rows. Until a batch is staged in the
// WAL no durable copy exists, so a failed staging attempt keeps the rows
// buffered for the next tick rather than discarding them — bounded by
// carryBatches so a long storage outage cannot grow the buffer without limit.
// The final flush has no next tick, so there the rows are accounted as dropped.
func (w *Writer) flushBuffered(out chan<- Batch, workerDone <-chan error, final bool) error {
	if len(w.bufSpans)+len(w.bufLogs)+len(w.bufMetrics) == 0 {
		return nil
	}
	batch := Batch{ID: uuid.NewString(), Spans: append([]telemetry.Span(nil), w.bufSpans...), Logs: append([]telemetry.Log(nil), w.bufLogs...), Metrics: append([]telemetry.Metric(nil), w.bufMetrics...)}
	if err := w.repository.Stage(batch); err != nil {
		metrics.FlushErrors.WithLabelValues("stage").Inc()
		if final {
			recordDroppedBatch(batch)
			slog.Error("telemetry batch could not be staged durably during shutdown; dropping", "batch_id", batch.ID, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", err)
			w.resetBuffers()
			return nil
		}
		slog.Warn("telemetry batch could not be staged durably; retrying on the next flush", "batch_id", batch.ID, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", err)
		w.trimCarry()
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
			// The batch stays in the WAL. Its projections may be partly published,
			// and only replay can finish the transaction and register the batch in
			// the manifest, so deleting the WAL here would both lose the rows and
			// strand any parquet file the failed attempt already wrote.
			metrics.FlushErrors.WithLabelValues("deferred").Inc()
			slog.Error("telemetry batch commit failed after bounded retries; deferring to WAL replay on next start", "batch_id", batch.ID, "attempts", commitRetryLimit, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", lastErr)
		}
	}
}

func (w *Writer) resetBuffers() {
	w.bufSpans = w.bufSpans[:0]
	w.bufLogs = w.bufLogs[:0]
	w.bufMetrics = w.bufMetrics[:0]
}

// trimCarry bounds the rows held for a later staging attempt. Once the carry
// exceeds carryBatches worth of rows the oldest are dropped with accounting,
// so an unwritable WAL degrades to visible loss instead of unbounded memory.
func (w *Writer) trimCarry() {
	limit := w.batchSize * carryBatches
	if limit <= 0 {
		return
	}
	spans, logs, metricRows := 0, 0, 0
	if overflow := len(w.bufSpans) - limit; overflow > 0 {
		spans = overflow
		w.bufSpans = append(w.bufSpans[:0], w.bufSpans[overflow:]...)
	}
	if overflow := len(w.bufLogs) - limit; overflow > 0 {
		logs = overflow
		w.bufLogs = append(w.bufLogs[:0], w.bufLogs[overflow:]...)
	}
	if overflow := len(w.bufMetrics) - limit; overflow > 0 {
		metricRows = overflow
		w.bufMetrics = append(w.bufMetrics[:0], w.bufMetrics[overflow:]...)
	}
	if spans+logs+metricRows == 0 {
		return
	}
	recordDropped(spans, logs, metricRows)
	slog.Error("telemetry carry buffer is full; dropping the oldest rows", "spans", spans, "logs", logs, "metrics", metricRows)
}

func recordDroppedBatch(batch Batch) {
	recordDropped(len(batch.Spans), len(batch.Logs), len(batch.Metrics))
}

func recordDropped(spans, logs, metricRows int) {
	metrics.RowsDropped.WithLabelValues("spans").Add(float64(spans))
	metrics.RowsDropped.WithLabelValues("logs").Add(float64(logs))
	metrics.RowsDropped.WithLabelValues("metrics").Add(float64(metricRows))
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
