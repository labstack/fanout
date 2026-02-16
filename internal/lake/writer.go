package lake

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	done       chan struct{}
}

func NewWriter(cfg config.Config, spans <-chan SpanRow, logs <-chan LogRow, metrics <-chan MetricRow) *Writer {
	return &Writer{cfg: cfg, chSpans: spans, chLogs: logs, chMetrics: metrics, lastFlush: time.Now(), done: make(chan struct{})}
}

// Wait blocks until Run() has returned (final flush complete).
func (w *Writer) Wait() {
	<-w.done
}

func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)

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
			// Drain any remaining items from buffered channels
			w.mu.Lock()
			w.drainChannels()
			w.flushLocked()
			w.mu.Unlock()
			return nil
		}
	}
}

// drainChannels reads any remaining items from the buffered input channels.
// Must be called with w.mu held.
func (w *Writer) drainChannels() {
	for {
		select {
		case r := <-w.chSpans:
			w.bufSpans = append(w.bufSpans, r)
		case r := <-w.chLogs:
			w.bufLogs = append(w.bufLogs, r)
		case r := <-w.chMetrics:
			w.bufMetrics = append(w.bufMetrics, r)
		default:
			return
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
	if maxRetry < 0 {
		maxRetry = 0
	}

	// Group spans by tenant/namespace
	if len(w.bufSpans) > 0 {
		byPartition := make(map[string][]SpanRow)
		for _, r := range w.bufSpans {
			if r.TenantID == "" {
				r.TenantID = "default"
			}
			if r.Namespace == "" {
				r.Namespace = "default"
			}
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
				slog.Error("write spans parquet failed", "tenant", r.TenantID, "namespace", r.Namespace, "err", err)
				metrics.FlushErrors.WithLabelValues("spans").Inc()
				failed = append(failed, rows...)
			} else {
				totalBytes += bytes
			}
		}
		metrics.RecordFlush("spans", totalBytes, time.Since(start).Seconds())
		if len(failed) > maxRetry {
			dropped := len(failed) - maxRetry
			slog.Error("spans data loss: retry buffer full, dropping rows", "buffered", len(failed), "dropped", dropped)
			metrics.RowsDropped.WithLabelValues("spans").Add(float64(dropped))
			failed = failed[:maxRetry]
		}
		w.bufSpans = append(w.bufSpans[:0], failed...)
	}

	// Group logs by tenant/namespace
	if len(w.bufLogs) > 0 {
		byPartition := make(map[string][]LogRow)
		for _, r := range w.bufLogs {
			if r.TenantID == "" {
				r.TenantID = "default"
			}
			if r.Namespace == "" {
				r.Namespace = "default"
			}
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
				slog.Error("write logs parquet failed", "tenant", r.TenantID, "namespace", r.Namespace, "err", err)
				metrics.FlushErrors.WithLabelValues("logs").Inc()
				failed = append(failed, rows...)
			} else {
				totalBytes += bytes
			}
		}
		metrics.RecordFlush("logs", totalBytes, time.Since(start).Seconds())
		if len(failed) > maxRetry {
			dropped := len(failed) - maxRetry
			slog.Error("logs data loss: retry buffer full, dropping rows", "buffered", len(failed), "dropped", dropped)
			metrics.RowsDropped.WithLabelValues("logs").Add(float64(dropped))
			failed = failed[:maxRetry]
		}
		w.bufLogs = append(w.bufLogs[:0], failed...)
	}

	// Group metrics by tenant/namespace
	if len(w.bufMetrics) > 0 {
		byPartition := make(map[string][]MetricRow)
		for _, r := range w.bufMetrics {
			if r.TenantID == "" {
				r.TenantID = "default"
			}
			if r.Namespace == "" {
				r.Namespace = "default"
			}
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
				slog.Error("write metrics parquet failed", "tenant", r.TenantID, "namespace", r.Namespace, "err", err)
				metrics.FlushErrors.WithLabelValues("metrics").Inc()
				failed = append(failed, rows...)
			} else {
				totalBytes += bytes
			}
		}
		metrics.RecordFlush("metrics", totalBytes, time.Since(start).Seconds())
		if len(failed) > maxRetry {
			dropped := len(failed) - maxRetry
			slog.Error("metrics data loss: retry buffer full, dropping rows", "buffered", len(failed), "dropped", dropped)
			metrics.RowsDropped.WithLabelValues("metrics").Add(float64(dropped))
			failed = failed[:maxRetry]
		}
		w.bufMetrics = append(w.bufMetrics[:0], failed...)
	}

	w.lastFlush = now
}

// CleanupTempFiles removes orphaned .tmp-*.parquet files left by
// a previous crash. Call before starting the writer.
func CleanupTempFiles(lakeDir string) {
	err := filepath.Walk(lakeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if !info.IsDir() && strings.HasPrefix(info.Name(), ".tmp-") && strings.HasSuffix(info.Name(), ".parquet") {
			if rmErr := os.Remove(path); rmErr != nil {
				slog.Warn("failed to remove temp file", "path", path, "err", rmErr)
			} else {
				slog.Info("removed orphaned temp file", "path", path)
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("temp file cleanup walk failed", "err", err)
	}
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
	_ = os.Chmod(tmpPath, 0o644)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}

	info, err := os.Stat(finalPath)
	if err != nil {
		return finalPath, 0, nil
	}
	return finalPath, info.Size(), nil
}
