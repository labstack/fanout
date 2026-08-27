package ingest

import (
	"context"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

type channelSubmitter struct {
	spans   chan<- telemetry.Span
	logs    chan<- telemetry.Log
	metrics chan<- telemetry.Metric
}

func newTestSubmitter(spans chan<- telemetry.Span, logs chan<- telemetry.Log, metrics chan<- telemetry.Metric) *channelSubmitter {
	return &channelSubmitter{spans: spans, logs: logs, metrics: metrics}
}

func (s *channelSubmitter) Submit(ctx context.Context, batch telemetrystore.Batch) error {
	for _, row := range batch.Spans {
		select {
		case s.spans <- row:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, row := range batch.Logs {
		select {
		case s.logs <- row:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, row := range batch.Metrics {
		select {
		case s.metrics <- row:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
