package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var panelQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "fanout_panel_query_duration_seconds",
	Help:    "Executed panel duration and outcome; panel is a built-in System overview ID or custom. Excludes result-cache hits and admission wait.",
	Buckets: []float64{.005, .01, .025, .05, .1, .2, .5, 1, 2, 5, 10},
}, []string{"panel", "status"})

var panelAdmissionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "fanout_panel_admission_duration_seconds",
	Help:    "Panel admission wait by bounded panel ID and outcome. Excludes result-cache hits.",
	Buckets: []float64{.005, .01, .025, .05, .1, .2, .5, 1, 2, 5, 10},
}, []string{"panel", "status"})

// RecordPanelQuery admits only a fixed panel vocabulary. User-authored IDs and
// unknown outcomes must never become metric labels.
func RecordPanelQuery(id, status string, seconds float64) {
	switch status {
	case "ok", "empty", "error":
	default:
		status = "other"
	}
	panelQueryDuration.WithLabelValues(panelLabel(id), status).Observe(seconds)
}

// RecordPanelAdmission separates connection pressure from SQL execution time.
func RecordPanelAdmission(id, status string, seconds float64) {
	switch status {
	case "admitted", "rejected", "canceled", "timeout":
	default:
		status = "other"
	}
	panelAdmissionDuration.WithLabelValues(panelLabel(id), status).Observe(seconds)
}

func panelLabel(id string) string {
	switch id {
	case "requests", "errors", "latency_p95", "error_logs", "traffic", "latency", "error_services", "endpoints", "check_states", "check_latency", "service_health", "service_map", "hosts", "containers", "versions":
	default:
		id = "custom"
	}
	return id
}
