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

// flushQueueDepth bounds how many filled batches can be queued for the flush
// worker before the receive loop blocks. Blocking here is the intended
// backpressure: it propagates to the ingest channels rather than letting memory
// grow unbounded when the database can't keep up.
const flushQueueDepth = 4

type Writer struct {
	cfg       env.Config
	db        *sql.DB
	chSpans   <-chan SpanRow
	chLogs    <-chan LogRow
	chMetrics <-chan MetricRow
	// Buffers are owned exclusively by the Run goroutine — no mutex needed. On
	// flush they are detached (handed to the flush worker) and replaced.
	bufSpans   []SpanRow
	bufLogs    []LogRow
	bufMetrics []MetricRow
	done       chan struct{}
	// writeMu, when set, serializes appender flushes against the query layer's
	// rollup/maintenance commits so two connections never commit to the DuckLake
	// catalog at once on a multi-connection pool. Nil is fine (single-connection
	// pools already serialize through one handle).
	writeMu *sync.Mutex
}

// UseWriteLock shares the query layer's write-serialization mutex with the
// writer. Call before Run when the DuckDB pool may hold more than one connection.
func (w *Writer) UseWriteLock(mu *sync.Mutex) { w.writeMu = mu }

// flushBatch is a detached set of rows handed to the flush worker. The worker
// owns the slices once sent.
type flushBatch struct {
	spans   []SpanRow
	logs    []LogRow
	metrics []MetricRow
}

func NewWriter(cfg env.Config, db *sql.DB, spans <-chan SpanRow, logs <-chan LogRow, metricsCh <-chan MetricRow) *Writer {
	return &Writer{
		cfg:       cfg,
		db:        db,
		chSpans:   spans,
		chLogs:    logs,
		chMetrics: metricsCh,
		done:      make(chan struct{}),
	}
}

// Wait blocks until Run() has returned (final flush complete).
func (w *Writer) Wait() {
	<-w.done
}

func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)

	// A multi-connection pool requires the shared write lock so appender flushes
	// don't commit concurrently with rollups. Fail loudly rather than silently
	// running unserialized writes (which surface only as catalog-lock errors
	// under load). Single-connection pools serialize through the one handle, so a
	// nil lock is fine there.
	if w.cfg.DuckDBMaxConns > 1 && w.writeMu == nil {
		return fmt.Errorf("lake writer: DUCKDB_MAX_CONNS=%d requires a shared write lock; call UseWriteLock before Run", w.cfg.DuckDBMaxConns)
	}

	// Flushes run on a dedicated worker so a slow database insert never stalls the
	// receive loop below (which would stop draining the ingest channels and apply
	// backpressure all the way to the gRPC handlers). Run detaches a filled batch
	// and hands it off; the worker serializes the actual writes and retries.
	flushCh := make(chan flushBatch, flushQueueDepth)
	workerDone := make(chan struct{})
	go w.flushWorker(flushCh, workerDone)

	ticker := time.NewTicker(time.Duration(w.cfg.FlushSeconds) * time.Second)
	defer ticker.Stop()

	spansCh := w.chSpans
	logsCh := w.chLogs
	metricsCh := w.chMetrics

	finish := func() {
		w.drainChannels(&spansCh, &logsCh, &metricsCh)
		w.flush(flushCh)
		close(flushCh)
		<-workerDone
	}

	for {
		select {
		case r, ok := <-spansCh:
			if !ok {
				spansCh = nil
				continue
			}
			w.bufSpans = append(w.bufSpans, r)
			metrics.RecordIngest("spans", 1)
			metrics.UpdateQueueDepth("spans", len(spansCh))
			if w.shouldFlush() {
				w.flush(flushCh)
			}
		case r, ok := <-logsCh:
			if !ok {
				logsCh = nil
				continue
			}
			w.bufLogs = append(w.bufLogs, r)
			metrics.RecordIngest("logs", 1)
			metrics.UpdateQueueDepth("logs", len(logsCh))
			if w.shouldFlush() {
				w.flush(flushCh)
			}
		case r, ok := <-metricsCh:
			if !ok {
				metricsCh = nil
				continue
			}
			w.bufMetrics = append(w.bufMetrics, r)
			metrics.RecordIngest("metrics", 1)
			metrics.UpdateQueueDepth("metrics", len(metricsCh))
			if w.shouldFlush() {
				w.flush(flushCh)
			}
		case <-ticker.C:
			w.flush(flushCh)
		case <-ctx.Done():
			finish()
			return nil
		}

		if spansCh == nil && logsCh == nil && metricsCh == nil {
			finish()
			return nil
		}
	}
}

// shouldFlush reports whether any buffer has reached the configured batch size.
func (w *Writer) shouldFlush() bool {
	total := len(w.bufSpans) + len(w.bufLogs) + len(w.bufMetrics)
	return len(w.bufSpans) >= w.cfg.FlushBatchSize ||
		len(w.bufLogs) >= w.cfg.FlushBatchSize ||
		len(w.bufMetrics) >= w.cfg.FlushBatchSize ||
		total >= w.cfg.FlushBatchSize
}

// flush detaches the current buffers and hands them to the flush worker. Sending
// on flushCh blocks if the worker is behind, which is the intended backpressure.
func (w *Writer) flush(flushCh chan<- flushBatch) {
	batch := flushBatch{}
	if len(w.bufSpans) > 0 {
		batch.spans = w.bufSpans
		w.bufSpans = nil
	}
	if len(w.bufLogs) > 0 {
		batch.logs = w.bufLogs
		w.bufLogs = nil
	}
	if len(w.bufMetrics) > 0 {
		batch.metrics = w.bufMetrics
		w.bufMetrics = nil
	}
	if batch.spans == nil && batch.logs == nil && batch.metrics == nil {
		return
	}
	flushCh <- batch
}

// flushWorker serializes all database writes. It carries rows that failed to
// insert forward and prepends them to the next batch so a transient error
// doesn't drop data (until the retry buffer cap is exceeded — see retainRows).
func (w *Writer) flushWorker(flushCh <-chan flushBatch, workerDone chan<- struct{}) {
	defer close(workerDone)
	var carry flushBatch
	for batch := range flushCh {
		carry.spans = append(carry.spans, batch.spans...)
		carry.logs = append(carry.logs, batch.logs...)
		carry.metrics = append(carry.metrics, batch.metrics...)
		carry.spans = writeRows(carry.spans, "spans", w.insertSpans, w.retryCap())
		carry.logs = writeRows(carry.logs, "logs", w.insertLogs, w.retryCap())
		carry.metrics = writeRows(carry.metrics, "metrics", w.insertMetrics, w.retryCap())
	}
}

// writeRows inserts a batch and returns the rows to carry forward: empty on
// success, or the retry-capped remainder on failure.
func writeRows[T any](rows []T, signal string, insert func([]T) error, retryCap int) []T {
	if len(rows) == 0 {
		return rows[:0]
	}
	start := time.Now()
	if err := insert(rows); err != nil {
		slog.Error("write failed", "signal", signal, "err", err)
		metrics.FlushErrors.WithLabelValues(signal).Inc()
		return retainRows(rows, retryCap, signal)
	}
	metrics.RecordFlush(signal, 0, time.Since(start).Seconds())
	return rows[:0]
}

// drainChannels non-blockingly pulls any buffered rows into the local buffers
// during shutdown. It honors the ok flag so a closed channel is retired (set to
// nil) instead of spinning on zero values.
func (w *Writer) drainChannels(spansCh *<-chan SpanRow, logsCh *<-chan LogRow, metricsCh *<-chan MetricRow) {
	for {
		drained := false

		select {
		case r, ok := <-*spansCh:
			if ok {
				w.bufSpans = append(w.bufSpans, r)
				drained = true
			} else {
				*spansCh = nil
			}
		default:
		}
		select {
		case r, ok := <-*logsCh:
			if ok {
				w.bufLogs = append(w.bufLogs, r)
				drained = true
			} else {
				*logsCh = nil
			}
		default:
		}
		select {
		case r, ok := <-*metricsCh:
			if ok {
				w.bufMetrics = append(w.bufMetrics, r)
				drained = true
			} else {
				*metricsCh = nil
			}
		default:
		}

		if !drained {
			return
		}
	}
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
	return withAppender(ctx, w.db, w.writeMu, "spans", func(a *duckdb.Appender) error {
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
				// Skip the malformed row rather than aborting the batch: the
				// appender flushes rows already added on Close, so returning here
				// would commit the prefix and then re-append it on retry (a
				// duplicate), and a permanently-bad row would poison every flush.
				slog.Error("skip malformed span row", "err", err)
				metrics.RowsDropped.WithLabelValues("spans").Inc()
				continue
			}
		}
		return nil
	})
}

func (w *Writer) insertLogs(rows []LogRow) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return withAppender(ctx, w.db, w.writeMu, "logs", func(a *duckdb.Appender) error {
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
				slog.Error("skip malformed log row", "err", err)
				metrics.RowsDropped.WithLabelValues("logs").Inc()
				continue
			}
		}
		return nil
	})
}

func (w *Writer) insertMetrics(rows []MetricRow) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return withAppender(ctx, w.db, w.writeMu, "metrics", func(a *duckdb.Appender) error {
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
				slog.Error("skip malformed metric row", "err", err)
				metrics.RowsDropped.WithLabelValues("metrics").Inc()
				continue
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

func withAppender(ctx context.Context, db *sql.DB, mu *sync.Mutex, table string, fn func(a *duckdb.Appender) error) error {
	// Acquire the write lock before the connection so lock ordering matches the
	// query layer (writeMu → conn); the two write paths therefore can't deadlock
	// against each other. (A writer holding writeMu can still wait on a connection
	// behind long-running readers on a small pool — that's throughput, not a
	// deadlock, since readers never take writeMu.)
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
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
