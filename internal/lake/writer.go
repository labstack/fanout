package lake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/labstack/fanout/internal/config"
)

type SpanRow struct {
	TraceID        string  `parquet:"name=trace_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	SpanID         string  `parquet:"name=span_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	ParentSpanID   string  `parquet:"name=parent_span_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ServiceName    string  `parquet:"name=service_name, type=BYTE_ARRAY, convertedtype=UTF8"`
	Name           string  `parquet:"name=name, type=BYTE_ARRAY, convertedtype=UTF8"`
	Kind           string  `parquet:"name=kind, type=BYTE_ARRAY, convertedtype=UTF8"`
	StartUnixNanos int64   `parquet:"name=start_unix_nano, type=INT64"`
	EndUnixNanos   int64   `parquet:"name=end_unix_nano, type=INT64"`
	DurationMs     float64 `parquet:"name=duration_ms, type=DOUBLE"`
	StatusCode     string  `parquet:"name=status_code, type=BYTE_ARRAY, convertedtype=UTF8"`
	StatusMsg      string  `parquet:"name=status_msg, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ResourceJSON   []byte  `parquet:"name=resource_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	AttributesJSON []byte  `parquet:"name=attributes_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	TenantID       string  `parquet:"name=tenant_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	IngestedAt     int64   `parquet:"name=ingested_unix_nano, type=INT64"`
}

type LogRow struct {
	TimeUnixNanos  int64  `parquet:"name=time_unix_nano, type=INT64"`
	Severity       string `parquet:"name=severity, type=BYTE_ARRAY, convertedtype=UTF8"`
	Body           string `parquet:"name=body, type=BYTE_ARRAY, convertedtype=UTF8"`
	ServiceName    string `parquet:"name=service_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	TraceID        string `parquet:"name=trace_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	SpanID         string `parquet:"name=span_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ResourceJSON   []byte `parquet:"name=resource_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	AttributesJSON []byte `parquet:"name=attributes_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	TenantID       string `parquet:"name=tenant_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	IngestedAt     int64  `parquet:"name=ingested_unix_nano, type=INT64"`
}

type MetricRow struct {
	TimeUnixNanos  int64   `parquet:"name=time_unix_nano, type=INT64"`
	Name           string  `parquet:"name=name, type=BYTE_ARRAY, convertedtype=UTF8"`
	MType          string  `parquet:"name=mtype, type=BYTE_ARRAY, convertedtype=UTF8"`
	ServiceName    string  `parquet:"name=service_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	Value          float64 `parquet:"name=value, type=DOUBLE, repetitiontype=OPTIONAL"`
	HistBoundsJSON []byte  `parquet:"name=hist_bounds_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	HistCountsJSON []byte  `parquet:"name=hist_counts_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	AttributesJSON []byte  `parquet:"name=attributes_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ResourceJSON   []byte  `parquet:"name=resource_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	TenantID       string  `parquet:"name=tenant_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	IngestedAt     int64   `parquet:"name=ingested_unix_nano, type=INT64"`
	HistCount      int64   `parquet:"name=hist_count, type=INT64, repetitiontype=OPTIONAL"`
	HistSum        float64 `parquet:"name=hist_sum, type=DOUBLE, repetitiontype=OPTIONAL"`
}

type Writer struct {
	cfg        config.Config
	chSpans    <-chan SpanRow
	chLogs     <-chan LogRow
	chMetrics  <-chan MetricRow
	mu         sync.Mutex
	bufSpans   []SpanRow
	bufLogs    []LogRow
	bufMetrics []MetricRow
	lastFlush  time.Time
}

func NewWriter(cfg config.Config, spans <-chan SpanRow, logs <-chan LogRow, metrics <-chan MetricRow) *Writer {
	return &Writer{cfg: cfg, chSpans: spans, chLogs: logs, chMetrics: metrics, lastFlush: time.Now()}
}

func (w *Writer) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(w.cfg.FlushSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case r := <-w.chSpans:
			w.mu.Lock()
			w.bufSpans = append(w.bufSpans, r)
			w.maybeFlush()
			w.mu.Unlock()
		case r := <-w.chLogs:
			w.mu.Lock()
			w.bufLogs = append(w.bufLogs, r)
			w.maybeFlush()
			w.mu.Unlock()
		case r := <-w.chMetrics:
			w.mu.Lock()
			w.bufMetrics = append(w.bufMetrics, r)
			w.maybeFlush()
			w.mu.Unlock()
		case <-ticker.C:
			w.mu.Lock()
			w.flushLocked()
			w.mu.Unlock()
		case <-ctx.Done():
			w.mu.Lock()
			w.flushLocked()
			w.mu.Unlock()
			return nil
		}
	}
}

func (w *Writer) maybeFlush() {
	if len(w.bufSpans)+len(w.bufLogs)+len(w.bufMetrics) >= w.cfg.MaxRows {
		w.flushLocked()
	}
	if time.Since(w.lastFlush) >= time.Duration(w.cfg.FlushSeconds)*time.Second {
		w.flushLocked()
	}
}

func (w *Writer) flushLocked() {
	now := time.Now()
	if len(w.bufSpans) > 0 {
		_ = writeParquet(filepath.Join(w.cfg.LakeDir, "spans"), now, w.bufSpans)
		w.bufSpans = w.bufSpans[:0]
	}
	if len(w.bufLogs) > 0 {
		_ = writeParquet(filepath.Join(w.cfg.LakeDir, "logs"), now, w.bufLogs)
		w.bufLogs = w.bufLogs[:0]
	}
	if len(w.bufMetrics) > 0 {
		_ = writeParquet(filepath.Join(w.cfg.LakeDir, "metrics"), now, w.bufMetrics)
		w.bufMetrics = w.bufMetrics[:0]
	}
	w.lastFlush = now
}

func writeParquet[T any](base string, ts time.Time, rows []T) error {
	year, month, day := ts.Date()
	hour := ts.Hour()
	dir := filepath.Join(base,
		fmt.Sprintf("year=%04d", year),
		fmt.Sprintf("month=%02d", int(month)),
		fmt.Sprintf("day=%02d", day),
		fmt.Sprintf("hour=%02d", hour),
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, fmt.Sprintf("part-%d.parquet", ts.UnixNano()))
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()

	w := parquet.NewWriter(f)
	defer w.Close()

	for i := range rows {
		if err := w.Write(rows[i]); err != nil {
			return err
		}
	}
	return w.Close()
}
