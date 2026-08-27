package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/metrics"
)

const (
	commitQueueDepth     = 256
	commitRetryLimit     = 3
	maxCommitWorkers     = 4
	submissionQueueDepth = 256
	writerShutdownGrace  = 30 * time.Second
)

type batchCommitter interface {
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

type commitJob struct {
	batches []Batch
	acks    []chan error
}

func NewWriter(repository *Repository, batchSize int) *Writer {
	return &Writer{repository: repository, batchSize: batchSize, done: make(chan struct{}), submissions: make(chan submission, submissionQueueDepth)}
}

func (w *Writer) Wait() { <-w.done }

// Submit returns after every row in the request belongs to a durably published
// atomic Parquet batch directory.
func (w *Writer) Submit(ctx context.Context, batch Batch) error {
	if batchRows(batch) == 0 {
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
	jobs := make(chan commitJob, commitQueueDepth)
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	fatal := make(chan error, 1)
	var workers sync.WaitGroup
	for range min(maxCommitWorkers, max(1, runtime.GOMAXPROCS(0))) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			w.commitWorker(workerCtx, jobs, fatal)
		}()
	}

	finish := func(graceful bool) error {
		close(jobs)
		if !graceful {
			cancelWorkers()
		}
		finished := make(chan struct{})
		go func() { workers.Wait(); close(finished) }()
		grace := w.shutdownGrace
		if grace <= 0 {
			grace = writerShutdownGrace
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-finished:
		case <-timer.C:
			cancelWorkers()
			<-finished
		}
		select {
		case err := <-fatal:
			return err
		default:
			return nil
		}
	}

	for {
		select {
		case request := <-w.submissions:
			if err := w.enqueueSubmissions(request, jobs, fatal); err != nil {
				return errors.Join(err, finish(false))
			}
		case <-ctx.Done():
			return finish(true)
		case err := <-fatal:
			cancelWorkers()
			return errors.Join(err, finish(false))
		}
	}
}

func (w *Writer) enqueueSubmissions(request submission, out chan<- commitJob, fatal <-chan error) error {
	requests := []submission{request}
	for len(requests) < submissionQueueDepth {
		select {
		case next := <-w.submissions:
			requests = append(requests, next)
		default:
			goto drained
		}
	}

drained:
	limit := w.batchLimit()
	for len(requests) > 0 {
		firstRows := batchRows(requests[0].batch)
		if firstRows > limit {
			oversized := requests[0]
			requests = requests[1:]
			chunks := splitBatch(oversized.batch, limit)
			for i := range chunks {
				chunks[i].ID = uuid.NewString()
			}
			if err := enqueueJob(out, fatal, commitJob{batches: chunks, acks: []chan error{oversized.ack}}); err != nil {
				return err
			}
			continue
		}
		// A full batch, a lone request, or a request that cannot share the
		// next batch needs no row copy through the group-commit buffer.
		if firstRows == limit || len(requests) == 1 || firstRows+batchRows(requests[1].batch) > limit {
			direct := requests[0]
			requests = requests[1:]
			direct.batch.ID = uuid.NewString()
			if err := enqueueJob(out, fatal, commitJob{batches: []Batch{direct.batch}, acks: []chan error{direct.ack}}); err != nil {
				return err
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
		acks := make([]chan error, len(group))
		for i := range group {
			acks[i] = group[i].ack
		}
		if err := enqueueJob(out, fatal, commitJob{batches: []Batch{batch}, acks: acks}); err != nil {
			return err
		}
	}
	return nil
}

func enqueueJob(out chan<- commitJob, fatal <-chan error, job commitJob) error {
	select {
	case out <- job:
		return nil
	case err := <-fatal:
		return err
	}
}

func (w *Writer) commitWorker(ctx context.Context, jobs <-chan commitJob, fatal chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			if err := w.commitJob(ctx, job); err != nil {
				for _, ack := range job.acks {
					ack <- err
				}
				select {
				case fatal <- err:
				default:
				}
				return
			}
			for _, batch := range job.batches {
				metrics.RecordIngest("spans", len(batch.Spans))
				metrics.RecordIngest("logs", len(batch.Logs))
				metrics.RecordIngest("metrics", len(batch.Metrics))
			}
			for _, ack := range job.acks {
				ack <- nil
			}
		}
	}
}

func (w *Writer) commitJob(ctx context.Context, job commitJob) error {
	for _, batch := range job.batches {
		var lastErr error
		for attempt := 0; attempt < commitRetryLimit; attempt++ {
			if err := w.repository.Commit(batch); err == nil {
				lastErr = nil
				break
			} else {
				lastErr = err
			}
			metrics.FlushErrors.WithLabelValues("batch").Inc()
			slog.Warn("telemetry batch commit failed; retrying", "batch_id", batch.ID, "attempt", attempt+1, "error", lastErr)
			if attempt+1 == commitRetryLimit {
				break
			}
			delay := defaultCommitRetryDelay(attempt)
			if w.retryDelay != nil {
				delay = w.retryDelay(attempt)
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
		}
		if lastErr != nil {
			metrics.FlushErrors.WithLabelValues("failed").Inc()
			return fmt.Errorf("commit telemetry batch %s after %d attempts: %w", batch.ID, commitRetryLimit, lastErr)
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

func defaultCommitRetryDelay(attempt int) time.Duration {
	return min(100*time.Millisecond*time.Duration(1<<min(attempt, 6)), 5*time.Second)
}
