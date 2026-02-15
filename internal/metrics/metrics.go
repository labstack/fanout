package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Ingest metrics
	IngestTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_ingest_rows_total",
		Help: "Total rows ingested",
	}, []string{"signal"})

	IngestQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_ingest_queue_depth",
		Help: "Current queue depth per signal",
	}, []string{"signal"})

	FlushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_flush_total",
		Help: "Total flush operations",
	}, []string{"signal"})

	FlushBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fanout_flush_bytes_total",
		Help: "Total bytes flushed",
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
		Help: "Total rows dropped due to retry buffer overflow",
	}, []string{"signal"})

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

	// Storage metrics
	LakeSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_lake_size_bytes",
		Help: "Total size of lake data in bytes",
	}, []string{"signal"})

	LakePartitions = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fanout_lake_partitions",
		Help: "Number of partitions per signal",
	}, []string{"signal"})

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
)

// RecordIngest records an ingest event
func RecordIngest(signal string, count int) {
	IngestTotal.WithLabelValues(signal).Add(float64(count))
}

// RecordFlush records a flush event
func RecordFlush(signal string, bytes int64, durationSec float64) {
	FlushTotal.WithLabelValues(signal).Inc()
	FlushBytes.WithLabelValues(signal).Add(float64(bytes))
	FlushDuration.WithLabelValues(signal).Observe(durationSec)
}

// RecordQuery records a query event
func RecordQuery(endpoint, status string, durationSec float64) {
	QueryTotal.WithLabelValues(endpoint, status).Inc()
	QueryDuration.WithLabelValues(endpoint).Observe(durationSec)
}

// RecordRollup records a rollup event
func RecordRollup(rows int, durationSec float64) {
	RollupTotal.Inc()
	RollupDuration.Observe(durationSec)
	RollupRows.Add(float64(rows))
	RollupLastSuccess.SetToCurrentTime()
}

// UpdateLakeStats updates lake storage metrics
func UpdateLakeStats(signal string, bytes int64, partitions int) {
	LakeSize.WithLabelValues(signal).Set(float64(bytes))
	LakePartitions.WithLabelValues(signal).Set(float64(partitions))
}

// UpdateQueueDepth updates queue depth metric
func UpdateQueueDepth(signal string, depth int) {
	IngestQueueDepth.WithLabelValues(signal).Set(float64(depth))
}
