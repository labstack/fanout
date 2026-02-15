package lake

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
)

type SpanRow struct {
	TenantID       string  `parquet:"-"` // Partitioning only
	Namespace      string  `parquet:"-"` // Partitioning only
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
	EventsJSON     []byte  `parquet:"name=events_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	LinksJSON      []byte  `parquet:"name=links_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	TraceState     string  `parquet:"name=trace_state, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	Flags          uint32  `parquet:"name=flags, type=INT32, repetitiontype=OPTIONAL"`
	ScopeName      string  `parquet:"name=scope_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ScopeVersion   string  `parquet:"name=scope_version, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	IngestedAt     int64   `parquet:"name=ingested_unix_nano, type=INT64"`
}

type LogRow struct {
	TenantID          string `parquet:"-"` // Partitioning only
	Namespace         string `parquet:"-"` // Partitioning only
	TimeUnixNanos     int64  `parquet:"name=time_unix_nano, type=INT64"`
	ObservedTimeNanos int64  `parquet:"name=observed_time_unix_nano, type=INT64, repetitiontype=OPTIONAL"`
	Severity          string `parquet:"name=severity, type=BYTE_ARRAY, convertedtype=UTF8"`
	SeverityNumber    int32  `parquet:"name=severity_number, type=INT32, repetitiontype=OPTIONAL"`
	Body              string `parquet:"name=body, type=BYTE_ARRAY, convertedtype=UTF8"`
	ServiceName       string `parquet:"name=service_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	TraceID           string `parquet:"name=trace_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	SpanID            string `parquet:"name=span_id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	Flags             uint32 `parquet:"name=flags, type=INT32, repetitiontype=OPTIONAL"`
	ResourceJSON      []byte `parquet:"name=resource_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	AttributesJSON    []byte `parquet:"name=attributes_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ScopeName         string `parquet:"name=scope_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ScopeVersion      string `parquet:"name=scope_version, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	IngestedAt        int64  `parquet:"name=ingested_unix_nano, type=INT64"`
}

type MetricRow struct {
	TenantID       string  `parquet:"-"` // Partitioning only
	Namespace      string  `parquet:"-"` // Partitioning only
	TimeUnixNanos  int64   `parquet:"name=time_unix_nano, type=INT64"`
	Name           string  `parquet:"name=name, type=BYTE_ARRAY, convertedtype=UTF8"`
	Description    string  `parquet:"name=description, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	Unit           string  `parquet:"name=unit, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	MType          string  `parquet:"name=mtype, type=BYTE_ARRAY, convertedtype=UTF8"`
	ServiceName    string  `parquet:"name=service_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	Value          float64 `parquet:"name=value, type=DOUBLE, repetitiontype=OPTIONAL"`
	HistBoundsJSON []byte  `parquet:"name=hist_bounds_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	HistCountsJSON []byte  `parquet:"name=hist_counts_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	HistCount      int64   `parquet:"name=hist_count, type=INT64, repetitiontype=OPTIONAL"`
	HistSum        float64 `parquet:"name=hist_sum, type=DOUBLE, repetitiontype=OPTIONAL"`
	ExemplarsJSON  []byte  `parquet:"name=exemplars_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	AttributesJSON []byte  `parquet:"name=attributes_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ResourceJSON   []byte  `parquet:"name=resource_json, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ScopeName      string  `parquet:"name=scope_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	ScopeVersion   string  `parquet:"name=scope_version, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	IngestedAt     int64   `parquet:"name=ingested_unix_nano, type=INT64"`
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
			metrics.RecordIngest("spans", 1)
			metrics.UpdateQueueDepth("spans", len(w.chSpans))
			w.maybeFlush()
			w.mu.Unlock()
		case r := <-w.chLogs:
			w.mu.Lock()
			w.bufLogs = append(w.bufLogs, r)
			metrics.RecordIngest("logs", 1)
			metrics.UpdateQueueDepth("logs", len(w.chLogs))
			w.maybeFlush()
			w.mu.Unlock()
		case r := <-w.chMetrics:
			w.mu.Lock()
			w.bufMetrics = append(w.bufMetrics, r)
			metrics.RecordIngest("metrics", 1)
			metrics.UpdateQueueDepth("metrics", len(w.chMetrics))
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
	if len(w.bufSpans) >= w.cfg.MaxRows ||
		len(w.bufLogs) >= w.cfg.MaxRows ||
		len(w.bufMetrics) >= w.cfg.MaxRows ||
		len(w.bufSpans)+len(w.bufLogs)+len(w.bufMetrics) >= w.cfg.MaxRows {
		w.flushLocked()
	}
	if time.Since(w.lastFlush) >= time.Duration(w.cfg.FlushSeconds)*time.Second {
		w.flushLocked()
	}
}

func (w *Writer) flushLocked() {
	now := time.Now()
	maxRetry := w.cfg.MaxRows * 3 // Cap retry buffer to prevent unbounded growth

	// Group spans by tenant/namespace
	if len(w.bufSpans) > 0 {
		byPartition := make(map[string][]SpanRow)
		for _, r := range w.bufSpans {
			key := r.TenantID + "/" + r.Namespace
			byPartition[key] = append(byPartition[key], r)
		}
		var totalBytes int64
		var failed []SpanRow
		start := time.Now()
		for _, rows := range byPartition {
			r := rows[0]
			base := filepath.Join(w.cfg.LakeDir, "spans", fmt.Sprintf("tenant=%s", r.TenantID), fmt.Sprintf("namespace=%s", r.Namespace))
			_, bytes, err := writeParquet(base, now, rows)
			if err != nil {
				log.Printf("[lake] write spans parquet (tenant=%s, namespace=%s): %v", r.TenantID, r.Namespace, err)
				metrics.FlushErrors.WithLabelValues("spans").Inc()
				failed = append(failed, rows...)
			} else {
				totalBytes += bytes
			}
		}
		metrics.RecordFlush("spans", totalBytes, time.Since(start).Seconds())
		if len(failed) > maxRetry {
			log.Printf("[lake] spans retry buffer full (%d rows), dropping %d oldest", len(failed), len(failed)-maxRetry)
			failed = failed[len(failed)-maxRetry:]
		}
		w.bufSpans = append(w.bufSpans[:0], failed...)
	}

	// Group logs by tenant/namespace
	if len(w.bufLogs) > 0 {
		byPartition := make(map[string][]LogRow)
		for _, r := range w.bufLogs {
			key := r.TenantID + "/" + r.Namespace
			byPartition[key] = append(byPartition[key], r)
		}
		var totalBytes int64
		var failed []LogRow
		start := time.Now()
		for _, rows := range byPartition {
			r := rows[0]
			base := filepath.Join(w.cfg.LakeDir, "logs", fmt.Sprintf("tenant=%s", r.TenantID), fmt.Sprintf("namespace=%s", r.Namespace))
			_, bytes, err := writeParquet(base, now, rows)
			if err != nil {
				log.Printf("[lake] write logs parquet (tenant=%s, namespace=%s): %v", r.TenantID, r.Namespace, err)
				metrics.FlushErrors.WithLabelValues("logs").Inc()
				failed = append(failed, rows...)
			} else {
				totalBytes += bytes
			}
		}
		metrics.RecordFlush("logs", totalBytes, time.Since(start).Seconds())
		if len(failed) > maxRetry {
			log.Printf("[lake] logs retry buffer full (%d rows), dropping %d oldest", len(failed), len(failed)-maxRetry)
			failed = failed[len(failed)-maxRetry:]
		}
		w.bufLogs = append(w.bufLogs[:0], failed...)
	}

	// Group metrics by tenant/namespace
	if len(w.bufMetrics) > 0 {
		byPartition := make(map[string][]MetricRow)
		for _, r := range w.bufMetrics {
			key := r.TenantID + "/" + r.Namespace
			byPartition[key] = append(byPartition[key], r)
		}
		var totalBytes int64
		var failed []MetricRow
		start := time.Now()
		for _, rows := range byPartition {
			r := rows[0]
			base := filepath.Join(w.cfg.LakeDir, "metrics", fmt.Sprintf("tenant=%s", r.TenantID), fmt.Sprintf("namespace=%s", r.Namespace))
			_, bytes, err := writeParquet(base, now, rows)
			if err != nil {
				log.Printf("[lake] write metrics parquet (tenant=%s, namespace=%s): %v", r.TenantID, r.Namespace, err)
				metrics.FlushErrors.WithLabelValues("metrics").Inc()
				failed = append(failed, rows...)
			} else {
				totalBytes += bytes
			}
		}
		metrics.RecordFlush("metrics", totalBytes, time.Since(start).Seconds())
		if len(failed) > maxRetry {
			log.Printf("[lake] metrics retry buffer full (%d rows), dropping %d oldest", len(failed), len(failed)-maxRetry)
			failed = failed[len(failed)-maxRetry:]
		}
		w.bufMetrics = append(w.bufMetrics[:0], failed...)
	}

	w.lastFlush = now
}

func writeParquet[T any](base string, ts time.Time, rows []T) (string, int64, error) {
	utc := ts.UTC()
	year, month, day := utc.Date()
	hour := utc.Hour()
	dir := filepath.Join(base,
		fmt.Sprintf("year=%04d", year),
		fmt.Sprintf("month=%02d", int(month)),
		fmt.Sprintf("day=%02d", day),
		fmt.Sprintf("hour=%02d", hour),
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	finalPath := filepath.Join(dir, fmt.Sprintf("part-%d.parquet", ts.UnixNano()))

	tmp, err := os.CreateTemp(dir, ".tmp-*.parquet")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
	}()

	if err := parquet.Write(tmp, rows, parquet.Compression(&zstd.Codec{})); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	if err := tmp.Sync(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	_ = os.Chmod(finalPath, 0o644)

	info, err := os.Stat(finalPath)
	if err != nil {
		return finalPath, 0, nil
	}
	return finalPath, info.Size(), nil
}
