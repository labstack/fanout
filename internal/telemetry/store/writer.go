package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/metrics"
)

const (
	commitQueueDepth     = 4
	commitRetryLimit     = 8
	writerShutdownGrace  = 5 * time.Second
	submissionQueueDepth = 256
)

type batchCommitter interface {
	Stage(Batch) error
	Commit(Batch) error
}

type Writer struct {
	repository    batchCommitter
	batchSize     int
	retryDelay    func(int) time.Duration
	shutdownGrace time.Duration
	done          chan struct{}
	submissions   chan submission
}

type submission struct {
	batch Batch
	ack   chan error
}

func NewWriter(repository *Repository, batchSize int) *Writer {
	return &Writer{repository: repository, batchSize: batchSize, done: make(chan struct{}), submissions: make(chan submission, submissionQueueDepth)}
}

func (w *Writer) Wait() { <-w.done }

// Submit accepts one decoded OTLP request. It returns only after the complete
// request is fsynced to the WAL, so a successful OTLP response is durable even
// though publication to the query projections continues asynchronously.
func (w *Writer) Submit(ctx context.Context, batch Batch) error {
	if len(batch.Spans)+len(batch.Logs)+len(batch.Metrics) == 0 {
		return nil
	}
	request := submission{batch: batch, ack: make(chan error, 1)}
	select {
	case w.submissions <- request:
	case <-w.done:
		return errors.New("telemetry writer is stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.ack:
		return err
	case <-w.done:
		return errors.New("telemetry writer stopped before durable acknowledgement")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)
	commits := make(chan Batch, commitQueueDepth)
	workerDone := make(chan error, 1)
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	go w.commitWorker(workerCtx, commits, workerDone)
	finish := func() error {
		close(commits)
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
		case request := <-w.submissions:
			if err := w.stageSubmission(request, commits, workerDone); err != nil {
				return err
			}
		case <-ctx.Done():
			return finishBounded()
		case err := <-workerDone:
			return err
		}
	}
}

func (w *Writer) stageSubmission(request submission, out chan<- Batch, workerDone <-chan error) error {
	requests := []submission{request}
	draining := true
	for draining && len(requests) < submissionQueueDepth {
		select {
		case next := <-w.submissions:
			requests = append(requests, next)
		default:
			draining = false
		}
	}
	limit := w.batchLimit()
	for len(requests) > 0 {
		if batchRows(requests[0].batch) > limit {
			oversized := requests[0]
			requests = requests[1:]
			chunks := splitBatch(oversized.batch, limit)
			staged := chunks[:0]
			var stageErr error
			for _, chunk := range chunks {
				chunk.ID = uuid.NewString()
				if stageErr = w.repository.Stage(chunk); stageErr != nil {
					metrics.FlushErrors.WithLabelValues("stage").Inc()
					break
				}
				staged = append(staged, chunk)
			}
			if stageErr != nil {
				// Already-staged chunks remain replayable. Do not enqueue only a
				// prefix for live publication; recovery will publish that durable
				// prefix after the storage fault is repaired.
				oversized.ack <- stageErr
				if len(staged) > 0 {
					return fmt.Errorf("stage oversized telemetry request after %d durable chunks: %w", len(staged), stageErr)
				}
				continue
			}
			metrics.RecordIngest("spans", len(oversized.batch.Spans))
			metrics.RecordIngest("logs", len(oversized.batch.Logs))
			metrics.RecordIngest("metrics", len(oversized.batch.Metrics))
			oversized.ack <- nil
			for _, chunk := range staged {
				select {
				case out <- chunk:
				case err := <-workerDone:
					return err
				}
			}
			continue
		}
		batch := Batch{ID: uuid.NewString()}
		group := make([]submission, 0, len(requests))
		rows := 0
		for len(requests) > 0 {
			next := requests[0]
			nextRows := batchRows(next.batch)
			if len(group) > 0 && rows+nextRows > limit {
				break
			}
			requests = requests[1:]
			group = append(group, next)
			rows += nextRows
			batch.Spans = append(batch.Spans, next.batch.Spans...)
			batch.Logs = append(batch.Logs, next.batch.Logs...)
			batch.Metrics = append(batch.Metrics, next.batch.Metrics...)
			if rows >= limit {
				break
			}
		}
		if err := w.repository.Stage(batch); err != nil {
			metrics.FlushErrors.WithLabelValues("stage").Inc()
			for _, item := range group {
				item.ack <- err
			}
			continue
		}
		metrics.RecordIngest("spans", len(batch.Spans))
		metrics.RecordIngest("logs", len(batch.Logs))
		metrics.RecordIngest("metrics", len(batch.Metrics))
		for _, item := range group {
			item.ack <- nil
		}
		select {
		case out <- batch:
		case err := <-workerDone:
			return err
		}
	}
	return nil
}

func batchRows(batch Batch) int {
	return len(batch.Spans) + len(batch.Logs) + len(batch.Metrics)
}

func (w *Writer) batchLimit() int {
	limit := min(w.batchSize, maxBatchRows)
	if limit <= 0 {
		return maxBatchRows
	}
	return limit
}

// splitBatch partitions one request without copying telemetry payloads. Each
// chunk is independently WAL-safe and no chunk exceeds the projection limit.
func splitBatch(batch Batch, limit int) []Batch {
	chunks := make([]Batch, 0, (batchRows(batch)+limit-1)/limit)
	for batchRows(batch) > 0 {
		chunk := Batch{}
		remaining := limit
		if count := min(remaining, len(batch.Spans)); count > 0 {
			chunk.Spans, batch.Spans = batch.Spans[:count], batch.Spans[count:]
			remaining -= count
		}
		if count := min(remaining, len(batch.Logs)); count > 0 {
			chunk.Logs, batch.Logs = batch.Logs[:count], batch.Logs[count:]
			remaining -= count
		}
		if count := min(remaining, len(batch.Metrics)); count > 0 {
			chunk.Metrics, batch.Metrics = batch.Metrics[:count], batch.Metrics[count:]
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func (w *Writer) commitWorker(ctx context.Context, in <-chan Batch, done chan<- error) {
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
			// The batch stays in the WAL. Its invisible Parquet staging files may
			// already be durable, and replay can finish publication and register the
			// batch without re-encoding them.
			metrics.FlushErrors.WithLabelValues("deferred").Inc()
			slog.Error("telemetry batch commit failed after bounded retries; stopping ingest with WAL retained for replay", "batch_id", batch.ID, "attempts", commitRetryLimit, "spans", len(batch.Spans), "logs", len(batch.Logs), "metrics", len(batch.Metrics), "error", lastErr)
			done <- fmt.Errorf("commit telemetry batch %s after %d attempts: %w", batch.ID, commitRetryLimit, lastErr)
			return
		}
	}
}

func defaultCommitRetryDelay(attempt int) time.Duration {
	shift := min(attempt, 6)
	return min(100*time.Millisecond*time.Duration(1<<shift), 5*time.Second)
}
