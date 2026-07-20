package observability

import "time"

const (
	OverviewSchema    = "fanout.overview.result@1"
	TopologySchema    = "fanout.topology.result@1"
	PerformanceSchema = "fanout.performance.result@1"
	TraceSchema       = "fanout.trace.result@1"
	LogsSchema        = "fanout.logs.result@1"
)

// Scope is the mandatory boundary for every telemetry query. Callers derive
// Namespace from authenticated application state; models never choose it.
type Scope struct {
	Namespace string    `json:"namespace"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
}

// Provenance makes every rendered answer traceable to its source query.
type Provenance struct {
	QueryID    string    `json:"query_id"`
	Window     string    `json:"window"`
	Generated  time.Time `json:"generated_at"`
	Complete   bool      `json:"complete"`
	DataSource string    `json:"data_source"`
}

// Result is the canonical query envelope shared by REST and MCP.
// Summary is deliberately compact model context; Data remains authoritative.
// UI selection is deliberately absent: MCP Apps link tools to UI resources
// through standard tool metadata rather than a Fanout-specific view registry.
type Result[T any] struct {
	Schema     string     `json:"schema"`
	Summary    string     `json:"summary"`
	Data       T          `json:"data"`
	Provenance Provenance `json:"provenance"`
}

type Health string

const (
	HealthHealthy   Health = "healthy"
	HealthDegraded  Health = "degraded"
	HealthUnhealthy Health = "unhealthy"
)

type ServiceHealth struct {
	Service     string  `json:"service"`
	Health      Health  `json:"health"`
	Spans       int64   `json:"spans"`
	ErrorRate   float64 `json:"error_rate"`
	P50MS       float64 `json:"p50_ms"`
	P95MS       float64 `json:"p95_ms"`
	LogCount    int64   `json:"log_count"`
	MetricCount int64   `json:"metric_count"`
}

type HealthCounts struct {
	Healthy   int `json:"healthy"`
	Degraded  int `json:"degraded"`
	Unhealthy int `json:"unhealthy"`
}

type Overview struct {
	Health       Health          `json:"health"`
	Counts       HealthCounts    `json:"counts"`
	TotalSpans   int64           `json:"total_spans"`
	ErrorRate    float64         `json:"error_rate"`
	Services     []ServiceHealth `json:"services"`
	ServiceCount int             `json:"service_count"`
}

type Edge struct {
	Caller    string  `json:"caller"`
	Callee    string  `json:"callee"`
	Type      string  `json:"type"`
	Calls     int64   `json:"calls"`
	AverageMS float64 `json:"average_ms"`
	ErrorRate float64 `json:"error_rate"`
}

type Topology struct {
	Nodes []ServiceHealth `json:"nodes"`
	Edges []Edge          `json:"edges"`
}

type PerformancePoint struct {
	Time        time.Time `json:"time"`
	Spans       int64     `json:"spans"`
	ErrorRate   float64   `json:"error_rate"`
	P50MS       float64   `json:"p50_ms"`
	P95MS       float64   `json:"p95_ms"`
	LogCount    int64     `json:"log_count"`
	MetricCount int64     `json:"metric_count"`
}

type Endpoint struct {
	Method    string  `json:"method"`
	Path      string  `json:"path"`
	Calls     int64   `json:"calls"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
	P99MS     float64 `json:"p99_ms"`
	ErrorRate float64 `json:"error_rate"`
	Health    Health  `json:"health"`
}

type HeatmapPoint struct {
	Time    time.Time `json:"time"`
	Service string    `json:"service"`
	P95MS   float64   `json:"p95_ms"`
}

type ComparisonMetric struct {
	Label       string  `json:"label"`
	Unit        string  `json:"unit"`
	Before      float64 `json:"before"`
	After       float64 `json:"after"`
	ChangePct   float64 `json:"change_pct"`
	Direction   string  `json:"direction"`
	Significant bool    `json:"significant"`
}

type Performance struct {
	Service    string             `json:"service,omitempty"`
	Points     []PerformancePoint `json:"points"`
	Endpoints  []Endpoint         `json:"endpoints"`
	Heatmap    []HeatmapPoint     `json:"heatmap"`
	Comparison []ComparisonMetric `json:"comparison"`
}

type TraceSpan struct {
	SpanID        string    `json:"span_id"`
	ParentSpanID  string    `json:"parent_span_id,omitempty"`
	Service       string    `json:"service"`
	Operation     string    `json:"operation"`
	Kind          string    `json:"kind"`
	Start         time.Time `json:"start"`
	DurationMS    float64   `json:"duration_ms"`
	Status        string    `json:"status"`
	StatusMessage string    `json:"status_message,omitempty"`
}

type LogEntry struct {
	Time     time.Time `json:"time"`
	Severity string    `json:"severity"`
	Service  string    `json:"service"`
	Body     string    `json:"body"`
	TraceID  string    `json:"trace_id,omitempty"`
	SpanID   string    `json:"span_id,omitempty"`
}

type TraceDetail struct {
	TraceID    string      `json:"trace_id"`
	DurationMS float64     `json:"duration_ms"`
	HasError   bool        `json:"has_error"`
	Services   []string    `json:"services"`
	Spans      []TraceSpan `json:"spans"`
	Logs       []LogEntry  `json:"logs"`
}

type LogBucket struct {
	Time     time.Time `json:"time"`
	Severity string    `json:"severity"`
	Count    int64     `json:"count"`
}

type Logs struct {
	Entries []LogEntry  `json:"entries"`
	Buckets []LogBucket `json:"buckets"`
}
