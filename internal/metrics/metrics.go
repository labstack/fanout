package metrics

import (
	"database/sql"
	"math"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type RollupComponent string

const (
	RollupService  RollupComponent = "service"
	RollupEndpoint RollupComponent = "endpoint"
	RollupEdge     RollupComponent = "edge"
)

type RollupResult string

const (
	RollupSuccess  RollupResult = "success"
	RollupError    RollupResult = "error"
	RollupNoop     RollupResult = "noop"
	RollupDisabled RollupResult = "disabled"
)

type TelemetryOperation string

const (
	TelemetryCompaction  TelemetryOperation = "compaction"
	TelemetryMaintenance TelemetryOperation = "maintenance"
)

type TelemetryResult string

const (
	TelemetrySuccess   TelemetryResult = "success"
	TelemetryError     TelemetryResult = "error"
	TelemetryDisabled  TelemetryResult = "disabled"
	TelemetryThrottled TelemetryResult = "throttled"
	TelemetryNoop      TelemetryResult = "noop"
)

var (
	// Ingest metrics
	IngestTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_ingest_rows_total",
		Help: "Total rows ingested",
	}, []string{"signal"})

	IngestQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_ingest_queue_depth",
		Help: "Current ingest submission queue depth",
	}, []string{"signal"})

	FlushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_flush_total",
		Help: "Total flush operations",
	}, []string{"signal"})

	FlushDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_flush_duration_seconds",
		Help:    "Flush duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"signal"})

	FlushErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_flush_errors_total",
		Help: "Total flush write errors",
	}, []string{"signal"})

	RowsDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_rows_dropped_total",
		Help: "Total rows left uncommitted after a permanent flush failure",
	}, []string{"signal"})

	WriteGateWait = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_write_gate_wait_seconds",
		Help:    "Time spent waiting to enter the Telemetry catalog write critical section",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"operation"})

	WriteGateHold = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_write_gate_hold_seconds",
		Help:    "Time spent inside the Telemetry catalog write critical section",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"operation"})

	// Query metrics
	QueryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_query_total",
		Help: "Total queries executed",
	}, []string{"endpoint", "status"})

	QueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_query_duration_seconds",
		Help:    "Query duration in seconds",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"endpoint"})

	// Rollup metrics
	RollupTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fanout_rollup_total",
		Help: "Total rollup operations",
	})

	RollupDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fanout_rollup_duration_seconds",
		Help:    "Rollup duration in seconds",
		Buckets: []float64{.1, .5, 1, 2, 5, 10, 30, 60},
	})

	RollupRows = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fanout_rollup_rows_total",
		Help: "Total rows processed by rollups",
	})

	RollupLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fanout_rollup_last_success_timestamp",
		Help: "Timestamp of last successful rollup",
	})

	RollupComponentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_rollup_component_total",
		Help: "Rollup component executions by bounded outcome",
	}, []string{"rollup", "result"})

	// Duration is measured end to end and therefore includes time spent waiting
	// for the catalog write gate. That is deliberate: it is the latency an
	// operator cares about, and fanout_write_gate_wait_seconds already isolates
	// the waiting half. Timing only the held section would duplicate
	// fanout_write_gate_hold_seconds.
	RollupComponentDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_rollup_component_duration_seconds",
		Help:    "Rollup component duration in seconds, including write-gate wait",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"rollup"})

	RollupComponentRows = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_rollup_component_rows_total",
		Help: "Rows materialized by each rollup component",
	}, []string{"rollup"})

	RollupEnabled = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_rollup_enabled",
		Help: "Whether the rollup component is enabled",
	}, []string{"rollup"})

	RollupWatermark = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_rollup_watermark_timestamp_seconds",
		Help: "Latest completed rollup watermark as a Unix timestamp",
	}, []string{"rollup"})

	RollupSourceMax = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_rollup_source_timestamp_seconds",
		Help: "Latest source ingest timestamp visible to the rollup",
	}, []string{"rollup"})

	RollupLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_rollup_lag_seconds",
		Help: "Source ingest time not yet covered by the completed rollup watermark",
	}, []string{"rollup"})

	RollupBacklogChunks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_rollup_backlog_chunks",
		Help: "Estimated bounded catch-up chunks remaining for the rollup",
	}, []string{"rollup"})

	TelemetryOperationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_telemetry_operation_total",
		Help: "Telemetry compaction and maintenance calls by bounded outcome",
	}, []string{"operation", "result"})

	TelemetryOperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_telemetry_operation_duration_seconds",
		Help:    "Executed telemetry compaction and maintenance duration in seconds",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"operation"})

	// Storage metrics
	ParquetSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_parquet_size_bytes",
		Help: "Total size of telemetry Parquet files in bytes",
	}, []string{"signal"})

	ParquetFiles = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_parquet_files",
		Help: "Number of telemetry Parquet files per signal",
	}, []string{"signal"})

	ParquetPublishWaiters = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fanout_parquet_publish_waiters",
		Help: "Current Parquet publications waiting for active readers",
	})

	ParquetPublishWait = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fanout_parquet_publish_wait_seconds",
		Help:    "Time spent waiting to publish a new Parquet file set",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	})

	ParquetPublishTimeouts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fanout_parquet_publish_timeouts_total",
		Help: "Parquet publications abandoned after reader wait timeout",
	})

	// The publisher's side of Parquet publication was measured and the
	// reader's side was not, which left the one question a stalled query asks
	// — "how long did I wait to enter the snapshot?" — unanswerable. These
	// three complete the pair.
	ParquetPublishHold = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fanout_parquet_publish_hold_seconds",
		Help:    "Time readers were excluded while a new Parquet file set was swapped in",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	})

	ParquetReadWait = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_parquet_read_wait_seconds",
		Help:    "Time a reader spent waiting to enter the Parquet snapshot",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"reader"})

	ParquetReadRefusals = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_parquet_read_refusals_total",
		Help: "Readers that gave up waiting to enter the Parquet snapshot",
	}, []string{"reader"})

	// An explicit CHECKPOINT blocks new transactions for its duration, and the
	// non-FORCE form fails outright while any transaction is open — so both
	// how long it takes and how often it does not happen are worth knowing.
	DuckDBCheckpoint = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fanout_duckdb_checkpoint_seconds",
		Help:    "Duration of an explicit DuckDB checkpoint on the rollup cache",
		Buckets: []float64{.001, .005, .01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	})

	DuckDBCheckpointFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fanout_duckdb_checkpoint_failures_total",
		Help: "Explicit DuckDB checkpoints that returned an error",
	})

	// HTTP metrics
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_http_requests_total",
		Help: "Total HTTP requests",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fanout_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// Authentication and authorization metrics.
	BrowserSessions = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_auth_browser_sessions",
		Help: "Browser session rows by current lifecycle state",
	}, []string{"state"})

	AuthAuditWriteFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fanout_auth_audit_write_failures_total",
		Help: "Security audit events that could not be persisted",
	})
)

// RecordIngest records an ingest event
func RecordIngest(signal string, count int) {
	IngestTotal.WithLabelValues(signal).Add(float64(count))
}

// RecordFlush records a flush event
func RecordFlush(signal string, durationSec float64) {
	FlushTotal.WithLabelValues(signal).Inc()
	FlushDuration.WithLabelValues(signal).Observe(durationSec)
}

// duckDBPoolStats is the live source for the connection-pool gauges below.
//
// A holder rather than a promauto.NewGaugeFunc closure over the pool: the
// gauges are declared once for the process, while a *Duck is constructed many
// times in a test binary, and registering a collector per construction panics
// on the second one. Unset, every gauge reads zero.
var duckDBPoolStats atomic.Pointer[func() sql.DBStats]

// SetDuckDBPoolSource points the connection-pool gauges at a pool. The last
// caller wins, which is what a test binary that builds several wants.
func SetDuckDBPoolSource(stats func() sql.DBStats) {
	if stats == nil {
		duckDBPoolStats.Store(nil)
		return
	}
	duckDBPoolStats.Store(&stats)
}

func poolStats() sql.DBStats {
	if fn := duckDBPoolStats.Load(); fn != nil {
		return (*fn)()
	}
	return sql.DBStats{}
}

// The read pool is the one that can starve: background writers, the detector,
// the alert engine and every dashboard widget draw from it, and a request that
// waits for a connection waits invisibly — database/sql counts that wait and
// nothing exported it.
var (
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "fanout_duckdb_pool_in_use",
		Help: "DuckDB read connections currently in use",
	}, func() float64 { return float64(poolStats().InUse) })

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "fanout_duckdb_pool_idle",
		Help: "DuckDB read connections currently idle",
	}, func() float64 { return float64(poolStats().Idle) })

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "fanout_duckdb_pool_max_open",
		Help: "Configured DuckDB read connection limit",
	}, func() float64 { return float64(poolStats().MaxOpenConnections) })

	_ = promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "fanout_duckdb_pool_waits_total",
		Help: "Times a caller waited for a free DuckDB read connection",
	}, func() float64 { return float64(poolStats().WaitCount) })

	_ = promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "fanout_duckdb_pool_wait_seconds_total",
		Help: "Total time callers spent waiting for a free DuckDB read connection",
	}, func() float64 { return poolStats().WaitDuration.Seconds() })
)

// DuckDBPoolStatsForTest exposes what the pool gauges are reading, so a test
// can prove they point at the read pool rather than the write handle.
func DuckDBPoolStatsForTest() sql.DBStats { return poolStats() }

// RecordWriteGate records one complete Telemetry catalog write critical section.
// Callers constrain operation to the fixed writegate.WriteOperation set so this
// metric cannot grow with tenant or telemetry cardinality.
func RecordWriteGate(operation string, waitSec, holdSec float64) {
	WriteGateWait.WithLabelValues(operation).Observe(waitSec)
	WriteGateHold.WithLabelValues(operation).Observe(holdSec)
}

// RecordQuery records a query event
func RecordQuery(endpoint, status string, durationSec float64) {
	QueryTotal.WithLabelValues(endpoint, status).Inc()
	QueryDuration.WithLabelValues(endpoint).Observe(durationSec)
}

// RecordRollup records a complete rollup cycle. A partial failure still records
// throughput and duration but does not advance the last-success gauge.
func RecordRollup(rows int, durationSec float64, successful bool) {
	RollupTotal.Inc()
	RollupDuration.Observe(durationSec)
	RollupRows.Add(float64(rows))
	if successful {
		RollupLastSuccess.SetToCurrentTime()
	}
}

// RecordRollupComponent records one bounded rollup component outcome.
func RecordRollupComponent(component RollupComponent, result RollupResult, rows int64, durationSec float64) {
	validateRollupComponent(component)
	validateRollupResult(result)
	if rows < 0 {
		rows = 0
	}
	RollupComponentTotal.WithLabelValues(string(component), string(result)).Inc()
	RollupComponentDuration.WithLabelValues(string(component)).Observe(durationSec)
	RollupComponentRows.WithLabelValues(string(component)).Add(float64(rows))
}

// UpdateRollupProgress records the committed watermark and the source tip seen
// by a rollup. backlogChunks is calculated by the query kernel from its bounded
// catch-up window.
func UpdateRollupProgress(component RollupComponent, enabled bool, watermarkNanos, sourceNanos int64, backlogChunks int) {
	validateRollupComponent(component)
	if watermarkNanos < 0 {
		watermarkNanos = 0
	}
	if sourceNanos < 0 {
		sourceNanos = 0
	}
	enabledValue := 0.0
	if enabled {
		enabledValue = 1
	}
	lagNanos := sourceNanos - watermarkNanos
	if lagNanos < 0 {
		lagNanos = 0
	}
	if backlogChunks < 0 {
		backlogChunks = 0
	}
	RollupEnabled.WithLabelValues(string(component)).Set(enabledValue)
	RollupWatermark.WithLabelValues(string(component)).Set(float64(watermarkNanos) / 1e9)
	RollupSourceMax.WithLabelValues(string(component)).Set(float64(sourceNanos) / 1e9)
	RollupLag.WithLabelValues(string(component)).Set(float64(lagNanos) / 1e9)
	RollupBacklogChunks.WithLabelValues(string(component)).Set(float64(backlogChunks))
}

// RecordTelemetryOperation records an outcome for compaction or maintenance. Skipped
// calls have no duration sample so throttle ticks cannot distort execution p95.
func RecordTelemetryOperation(operation TelemetryOperation, result TelemetryResult, durationSec float64) {
	validateTelemetryOperation(operation)
	validateTelemetryResult(result)
	TelemetryOperationTotal.WithLabelValues(string(operation), string(result)).Inc()
	if result == TelemetrySuccess || result == TelemetryError {
		TelemetryOperationDuration.WithLabelValues(string(operation)).Observe(math.Max(durationSec, 0))
	}
}

// UpdateParquetStats updates open Parquet storage metrics.
func UpdateParquetStats(signal string, bytes int64, partitions int) {
	ParquetSize.WithLabelValues(signal).Set(float64(bytes))
	ParquetFiles.WithLabelValues(signal).Set(float64(partitions))
}

// UpdateQueueDepth updates queue depth metric
func UpdateQueueDepth(signal string, depth int) {
	IngestQueueDepth.WithLabelValues(signal).Set(float64(depth))
}

func validateRollupComponent(component RollupComponent) {
	switch component {
	case RollupService, RollupEndpoint, RollupEdge:
		return
	default:
		panic("metrics: invalid rollup component: " + string(component))
	}
}

func validateRollupResult(result RollupResult) {
	switch result {
	case RollupSuccess, RollupError, RollupNoop, RollupDisabled:
		return
	default:
		panic("metrics: invalid rollup result: " + string(result))
	}
}

func validateTelemetryOperation(operation TelemetryOperation) {
	switch operation {
	case TelemetryCompaction, TelemetryMaintenance:
		return
	default:
		panic("metrics: invalid Telemetry operation: " + string(operation))
	}
}

func validateTelemetryResult(result TelemetryResult) {
	switch result {
	case TelemetrySuccess, TelemetryError, TelemetryDisabled, TelemetryThrottled, TelemetryNoop:
		return
	default:
		panic("metrics: invalid Telemetry result: " + string(result))
	}
}
