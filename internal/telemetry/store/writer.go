package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
)

const flushQueueDepth = 4

type Writer struct {
	repository *Repository
	interval   time.Duration
	batchSize  int
	spans      <-chan telemetry.Span
	logs       <-chan telemetry.Log
	metricRows <-chan telemetry.Metric
	bufSpans   []telemetry.Span
	bufLogs    []telemetry.Log
	bufMetrics []telemetry.Metric
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
		w.flush(flushes)
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
			w.flush(flushes)
		case <-ctx.Done():
			return finish()
		}
		if len(w.bufSpans)+len(w.bufLogs)+len(w.bufMetrics) >= w.batchSize {
			w.flush(flushes)
		}
		if spans == nil && logs == nil && metricRows == nil {
			return finish()
		}
	}
}

func (w *Writer) flush(out chan<- Batch) {
	if len(w.bufSpans)+len(w.bufLogs)+len(w.bufMetrics) == 0 {
		return
	}
	batch := Batch{ID: uuid.NewString(), Spans: append([]telemetry.Span(nil), w.bufSpans...), Logs: append([]telemetry.Log(nil), w.bufLogs...), Metrics: append([]telemetry.Metric(nil), w.bufMetrics...)}
	w.bufSpans = w.bufSpans[:0]
	w.bufLogs = w.bufLogs[:0]
	w.bufMetrics = w.bufMetrics[:0]
	out <- batch
}

func (w *Writer) flushWorker(in <-chan Batch, done chan<- error) {
	var joined error
	for batch := range in {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			err = w.repository.Commit(batch)
			if err == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("commit batch %s: %w", batch.ID, err))
		}
	}
	done <- joined
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
