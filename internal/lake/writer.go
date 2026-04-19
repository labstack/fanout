package lake

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/metrics"
)

type SpanRow struct {
	Namespace        string
	TraceID          string
	SpanID           string
	ParentSpanID     string
	ServiceName      string
	Name             string
	Kind             string
	StartUnixNanos   int64
	EndUnixNanos     int64
	DurationMs       float64
	StatusCode       string
	StatusMsg        string
	ResourceJSON     []byte
	AttributesJSON   []byte
	EventsJSON       []byte
	LinksJSON        []byte
	TraceState       string
	Flags            uint32
	ScopeName        string
	ScopeVersion     string
	IngestedAt       int64
	HTTPMethod       string
	HTTPStatusCode   string
	HTTPRoute        string
	DBSystem         string
	RPCMethod        string
	RPCService       string
	PeerService      string
	ServiceVersion   string
	DeploymentEnv    string
	ExceptionType    string
	ExceptionMessage string
}

type LogRow struct {
	Namespace         string
	TimeUnixNanos     int64
	ObservedTimeNanos int64
	Severity          string
	SeverityNumber    int32
	Body              string
	ServiceName       string
	TraceID           string
	SpanID            string
	Flags             uint32
	ResourceJSON      []byte
	AttributesJSON    []byte
	ScopeName         string
	ScopeVersion      string
	IngestedAt        int64
	BodyTemplate      string
}

type MetricRow struct {
	Namespace      string
	TimeUnixNanos  int64
	Name           string
	Description    string
	Unit           string
	MType          string
	ServiceName    string
	Value          float64
	HistBoundsJSON []byte
	HistCountsJSON []byte
	HistCount      int64
	HistSum        float64
	ExemplarsJSON  []byte
	AttributesJSON []byte
	ResourceJSON   []byte
	ScopeName      string
	ScopeVersion   string
	IngestedAt     int64
}

type Writer struct {
	cfg        env.Config
	db         *sql.DB
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

func NewWriter(cfg env.Config, db *sql.DB, spans <-chan SpanRow, logs <-chan LogRow, metricsCh <-chan MetricRow) *Writer {
	return &Writer{
		cfg:       cfg,
		db:        db,
		chSpans:   spans,
		chLogs:    logs,
		chMetrics: metricsCh,
		lastFlush: time.Now(),
		done:      make(chan struct{}),
	}
}

// Wait blocks until Run() has returned (final flush complete).
func (w *Writer) Wait() {
	<-w.done
}

func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)

	ticker := time.NewTicker(time.Duration(w.cfg.FlushSeconds) * time.Second)
	defer ticker.Stop()

	spansCh := w.chSpans
	logsCh := w.chLogs
	metricsCh := w.chMetrics

	for {
		select {
		case r, ok := <-spansCh:
			if !ok {
				spansCh = nil
				continue
			}
			w.mu.Lock()
			w.bufSpans = append(w.bufSpans, r)
			metrics.RecordIngest("spans", 1)
			metrics.UpdateQueueDepth("spans", len(spansCh))
			w.maybeFlush()
			w.mu.Unlock()
		case r, ok := <-logsCh:
			if !ok {
				logsCh = nil
				continue
			}
			w.mu.Lock()
			w.bufLogs = append(w.bufLogs, r)
			metrics.RecordIngest("logs", 1)
			metrics.UpdateQueueDepth("logs", len(logsCh))
			w.maybeFlush()
			w.mu.Unlock()
		case r, ok := <-metricsCh:
			if !ok {
				metricsCh = nil
				continue
			}
			w.mu.Lock()
			w.bufMetrics = append(w.bufMetrics, r)
			metrics.RecordIngest("metrics", 1)
			metrics.UpdateQueueDepth("metrics", len(metricsCh))
			w.maybeFlush()
			w.mu.Unlock()
		case <-ticker.C:
			w.mu.Lock()
			w.flushLocked()
			w.mu.Unlock()
		case <-ctx.Done():
			w.mu.Lock()
			w.drainChannels(spansCh, logsCh, metricsCh)
			w.flushLocked()
			w.mu.Unlock()
			return nil
		}

		if spansCh == nil && logsCh == nil && metricsCh == nil {
			w.mu.Lock()
			w.flushLocked()
			w.mu.Unlock()
			return nil
		}
	}
}

func (w *Writer) drainChannels(spansCh <-chan SpanRow, logsCh <-chan LogRow, metricsCh <-chan MetricRow) {
	for {
		drained := false

		select {
		case r := <-spansCh:
			w.bufSpans = append(w.bufSpans, r)
			drained = true
		default:
		}
		select {
		case r := <-logsCh:
			w.bufLogs = append(w.bufLogs, r)
			drained = true
		default:
		}
		select {
		case r := <-metricsCh:
			w.bufMetrics = append(w.bufMetrics, r)
			drained = true
		default:
		}

		if !drained {
			return
		}
	}
}

func (w *Writer) maybeFlush() {
	total := len(w.bufSpans) + len(w.bufLogs) + len(w.bufMetrics)
	if len(w.bufSpans) >= w.cfg.FlushBatchSize ||
		len(w.bufLogs) >= w.cfg.FlushBatchSize ||
		len(w.bufMetrics) >= w.cfg.FlushBatchSize ||
		total >= w.cfg.FlushBatchSize {
		w.flushLocked()
		return
	}
	if time.Since(w.lastFlush) >= time.Duration(w.cfg.FlushSeconds)*time.Second {
		w.flushLocked()
	}
}

func (w *Writer) flushLocked() {
	w.flushSpansLocked()
	w.flushLogsLocked()
	w.flushMetricsLocked()
	w.lastFlush = time.Now()
}

func (w *Writer) flushSpansLocked() {
	if len(w.bufSpans) == 0 {
		return
	}
	start := time.Now()
	if err := w.insertSpans(w.bufSpans); err != nil {
		slog.Error("write spans failed", "err", err)
		metrics.FlushErrors.WithLabelValues("spans").Inc()
		w.bufSpans = retainRows(w.bufSpans, w.retryCap(), "spans")
		return
	}
	metrics.RecordFlush("spans", 0, time.Since(start).Seconds())
	w.bufSpans = w.bufSpans[:0]
}

func (w *Writer) flushLogsLocked() {
	if len(w.bufLogs) == 0 {
		return
	}
	start := time.Now()
	if err := w.insertLogs(w.bufLogs); err != nil {
		slog.Error("write logs failed", "err", err)
		metrics.FlushErrors.WithLabelValues("logs").Inc()
		w.bufLogs = retainRows(w.bufLogs, w.retryCap(), "logs")
		return
	}
	metrics.RecordFlush("logs", 0, time.Since(start).Seconds())
	w.bufLogs = w.bufLogs[:0]
}

func (w *Writer) flushMetricsLocked() {
	if len(w.bufMetrics) == 0 {
		return
	}
	start := time.Now()
	if err := w.insertMetrics(w.bufMetrics); err != nil {
		slog.Error("write metrics failed", "err", err)
		metrics.FlushErrors.WithLabelValues("metrics").Inc()
		w.bufMetrics = retainRows(w.bufMetrics, w.retryCap(), "metrics")
		return
	}
	metrics.RecordFlush("metrics", 0, time.Since(start).Seconds())
	w.bufMetrics = w.bufMetrics[:0]
}

func (w *Writer) retryCap() int {
	maxRetry := w.cfg.FlushBatchSize * 3
	if maxRetry < 0 {
		return 0
	}
	return maxRetry
}

func (w *Writer) insertSpans(rows []SpanRow) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return withAppender(ctx, w.db, "spans", func(a *duckdb.Appender) error {
		for _, row := range rows {
			namespace := normalizeNamespace(row.Namespace)
			if err := a.AppendRow(
				namespace,
				row.TraceID,
				row.SpanID,
				optionalString(row.ParentSpanID),
				row.ServiceName,
				row.Name,
				row.Kind,
				optionalTime(row.StartUnixNanos),
				optionalTime(row.EndUnixNanos),
				row.StartUnixNanos,
				row.EndUnixNanos,
				row.DurationMs,
				row.StatusCode,
				optionalString(row.StatusMsg),
				optionalJSON(row.ResourceJSON),
				optionalJSON(row.AttributesJSON),
				optionalJSON(row.EventsJSON),
				optionalJSON(row.LinksJSON),
				optionalString(row.TraceState),
				int64(row.Flags),
				optionalString(row.ScopeName),
				optionalString(row.ScopeVersion),
				optionalTime(row.IngestedAt),
				row.IngestedAt,
				optionalString(row.HTTPMethod),
				optionalString(row.HTTPStatusCode),
				optionalString(row.HTTPRoute),
				optionalString(row.DBSystem),
				optionalString(row.RPCMethod),
				optionalString(row.RPCService),
				optionalString(row.PeerService),
				optionalString(row.ServiceVersion),
				optionalString(row.DeploymentEnv),
				optionalString(row.ExceptionType),
				optionalString(row.ExceptionMessage),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *Writer) insertLogs(rows []LogRow) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return withAppender(ctx, w.db, "logs", func(a *duckdb.Appender) error {
		for _, row := range rows {
			namespace := normalizeNamespace(row.Namespace)
			if err := a.AppendRow(
				namespace,
				optionalTime(row.TimeUnixNanos),
				optionalTime(row.ObservedTimeNanos),
				row.TimeUnixNanos,
				optionalInt64(row.ObservedTimeNanos),
				row.Severity,
				int64(row.SeverityNumber),
				row.Body,
				optionalString(row.ServiceName),
				optionalString(row.TraceID),
				optionalString(row.SpanID),
				int64(row.Flags),
				optionalJSON(row.ResourceJSON),
				optionalJSON(row.AttributesJSON),
				optionalString(row.ScopeName),
				optionalString(row.ScopeVersion),
				optionalTime(row.IngestedAt),
				row.IngestedAt,
				optionalString(row.BodyTemplate),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *Writer) insertMetrics(rows []MetricRow) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return withAppender(ctx, w.db, "metrics", func(a *duckdb.Appender) error {
		for _, row := range rows {
			namespace := normalizeNamespace(row.Namespace)
			if err := a.AppendRow(
				namespace,
				optionalTime(row.TimeUnixNanos),
				row.TimeUnixNanos,
				row.Name,
				optionalString(row.Description),
				optionalString(row.Unit),
				row.MType,
				optionalString(row.ServiceName),
				row.Value,
				optionalJSON(row.HistBoundsJSON),
				optionalJSON(row.HistCountsJSON),
				optionalInt64(row.HistCount),
				row.HistSum,
				optionalJSON(row.ExemplarsJSON),
				optionalJSON(row.AttributesJSON),
				optionalJSON(row.ResourceJSON),
				optionalString(row.ScopeName),
				optionalString(row.ScopeVersion),
				optionalTime(row.IngestedAt),
				row.IngestedAt,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func normalizeNamespace(namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace
}

func optionalString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func optionalJSON(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return string(v)
}

func optionalTime(unixNano int64) any {
	if unixNano <= 0 {
		return nil
	}
	return time.Unix(0, unixNano).UTC()
}

func optionalInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func withAppender(ctx context.Context, db *sql.DB, table string, fn func(a *duckdb.Appender) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.Raw(func(raw any) error {
		driverConn, ok := raw.(driver.Conn)
		if !ok {
			return fmt.Errorf("unexpected driver connection type %T", raw)
		}
		appender, err := duckdb.NewAppender(driverConn, "lake", "", table)
		if err != nil {
			return err
		}
		if err := fn(appender); err != nil {
			_ = appender.Close()
			return err
		}
		return appender.Close()
	})
}

func retainRows[T any](rows []T, maxRetry int, signal string) []T {
	if len(rows) <= maxRetry {
		return rows
	}
	dropped := len(rows) - maxRetry
	slog.Error("data loss: retry buffer full, dropping rows", "signal", signal, "buffered", len(rows), "dropped", dropped)
	metrics.RowsDropped.WithLabelValues(signal).Add(float64(dropped))
	return rows[:maxRetry]
}
