package intelligence

import "time"

// Pattern represents a grouped log pattern with deduplication
type Pattern struct {
	Template    string      `json:"template"`          // Log template with variables removed
	Count       int         `json:"count"`             // Number of occurrences
	FirstSeen   time.Time   `json:"first_seen"`        // First occurrence timestamp
	LastSeen    time.Time   `json:"last_seen"`         // Last occurrence timestamp
	Severity    string      `json:"severity"`          // WARN, ERROR, etc.
	ServiceName string      `json:"service_name"`      // Service generating the logs
	Summary     string      `json:"summary"`           // LLM-generated summary
	Samples     []LogSample `json:"samples,omitempty"` // Sample log messages
}

// LogSample represents a single log instance
type LogSample struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	TraceID   string    `json:"trace_id,omitempty"`
}

// Anomaly represents a detected statistical anomaly
type Anomaly struct {
	Type        AnomalyType `json:"type"`         // error_spike, latency_degradation, volume_change
	ServiceName string      `json:"service_name"` // Affected service
	Metric      string      `json:"metric"`       // error_rate, p95_latency, span_count
	Current     float64     `json:"current"`      // Current value
	Baseline    float64     `json:"baseline"`     // Expected/historical value
	ZScore      float64     `json:"z_score"`      // Statistical significance
	DetectedAt  time.Time   `json:"detected_at"`  // When anomaly was detected
	Description string      `json:"description"`  // Human-readable description
}

// AnomalyType represents the type of anomaly detected
type AnomalyType string

const (
	AnomalyErrorSpike         AnomalyType = "error_spike"
	AnomalyLatencyDegradation AnomalyType = "latency_degradation"
	AnomalyVolumeChange       AnomalyType = "volume_change"
	AnomalyErrorRateChange    AnomalyType = "error_rate_change"
)

// Insight represents a generated insight about the system
type Insight struct {
	Type     string `json:"type"`     // anomaly, trend
	Severity string `json:"severity"` // info, warning, critical
	Message  string `json:"message"`  // Description
}

// IntelligenceSnapshot represents the current system intelligence state
type IntelligenceSnapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Timeframe   string    `json:"timeframe"` // e.g., "last_15m"
	Insights    []Insight `json:"insights"`
	Patterns    []Pattern `json:"patterns"`
	Anomalies   []Anomaly `json:"anomalies"`
	Summary     string    `json:"summary"`      // Overall system health summary
	HealthScore float64   `json:"health_score"` // 0-100, lower is worse
}

// DetectorConfig holds configuration for the anomaly detector
type DetectorConfig struct {
	Enabled            bool          `json:"enabled"`
	CheckInterval      time.Duration `json:"check_interval"`       // How often to run detection
	LookbackWindow     time.Duration `json:"lookback_window"`      // Time window to analyze
	ErrorRateThreshold float64       `json:"error_rate_threshold"` // Z-score threshold for errors
	LatencyThreshold   float64       `json:"latency_threshold"`    // Z-score threshold for latency
	VolumeThreshold    float64       `json:"volume_threshold"`     // Z-score threshold for volume
}

// DefaultDetectorConfig returns sensible defaults
func DefaultDetectorConfig() DetectorConfig {
	return DetectorConfig{
		Enabled:            true,
		CheckInterval:      60 * time.Second, // Check every minute
		LookbackWindow:     15 * time.Minute, // Analyze last 15 minutes
		ErrorRateThreshold: 2.0,              // 2 standard deviations
		LatencyThreshold:   2.0,
		VolumeThreshold:    2.5,
	}
}
