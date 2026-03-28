package service

// StatusResult contains system health overview data.
type StatusResult struct {
	Healthy          bool
	Summary          string
	Services         ServiceSummary
	TopIssues        []TopIssue
	ThroughputPerMin int64
	P95Ms            float64
	ErrorRate        float64
}

// ServiceSummary counts services by health status.
type ServiceSummary struct {
	Total     int
	Healthy   int
	Degraded  int
	Unhealthy int
}

// TopIssue represents a service issue to highlight.
type TopIssue struct {
	Service string
	Issue   string
	Value   float64
	Detail  string
}

// OverviewResult contains the structured health overview.
type OverviewResult struct {
	Health   OverviewHealth
	Services []OverviewService
	Issues   []OverviewIssue
}

// OverviewHealth contains global health metrics.
type OverviewHealth struct {
	Score            float64
	TotalServices    int
	ByStatus         map[string]int
	ThroughputPerMin float64
	GlobalErrorRate  float64
	GlobalP95Ms      float64
}

// OverviewService contains per-service metrics.
type OverviewService struct {
	Service   string
	Status    string
	Requests  int64
	ErrorRate float64
	P50Ms     float64
	P95Ms     float64
}

// OverviewIssue represents a service issue to surface.
type OverviewIssue struct {
	Service   string
	Issue     string
	Value     float64
	Threshold float64
	Since     string // ISO8601 timestamp (optional)
}

// TopologyResult contains service dependency map data.
type TopologyResult struct {
	Nodes         []ServiceNode
	Edges         []ServiceEdge
	CriticalPaths [][]string
}

// ServiceNode represents a service in the topology.
type ServiceNode struct {
	Name            string
	Namespace       string
	Status          string
	SpanCount       int64
	P50Ms           float64
	P95Ms           float64
	ErrorRate       float64
	LogCount        int64
	MetricCount     int64
	Trend           []int64 // optional sparkline data
	UpstreamCount   int
	DownstreamCount int
	BlastRadius     float64
}

// ServiceEdge represents a call between services.
type ServiceEdge struct {
	From      string
	To        string
	CallCount int64
	AvgMs     float64
	ErrorRate float64
	Status    string
	EdgeType  string // "call" or "messaging"
}

// TopologyParams contains parameters for the Topology query.
type TopologyParams struct {
	Window          int
	EdgeType        string // "call", "messaging", "all"
	Depth           int    // BFS depth from Service; 0 means no limit
	Service         string // focus service for depth filter
	IncludeInactive bool
	Namespace       string
	TenantID        string
}

// DiagnoseResult contains detailed service diagnostics.
type DiagnoseResult struct {
	Service      string
	Status       string
	P50Ms        float64
	P95Ms        float64
	P99Ms        float64
	ErrorRate    float64
	SpanCount    int64
	TopErrors    []ErrorInfo
	SlowOps      []SlowOp
	Dependencies []Dependency

	// Enhanced fields (populated by DiagnoseEnhanced)
	SymptomDetected       string              `json:"symptom_detected,omitempty"`
	Baseline              *BaselineComparison `json:"comparison_to_baseline,omitempty"`
	ChangePoints          []ChangePoint       `json:"change_points,omitempty"`
	CorrelatedLogPatterns []LogPattern        `json:"correlated_log_patterns,omitempty"`
	SuggestedTraces       []string            `json:"suggested_traces,omitempty"`
}

// BaselineComparison compares current metrics against historical same-time-of-day baselines.
type BaselineComparison struct {
	P95Ratio       float64 `json:"p95_ratio"`
	BaselineP95Ms  float64 `json:"baseline_p95_ms"`
	BaselineWindow string  `json:"baseline_window"`
}

// ChangePoint represents a statistically significant jump in a metric.
type ChangePoint struct {
	Time   string  `json:"time"`
	Metric string  `json:"metric"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
}

// LogPattern describes a recurring log message pattern near a change point.
type LogPattern struct {
	Pattern  string `json:"pattern"`
	Count    int64  `json:"count"`
	Severity string `json:"severity"`
}

// ErrorInfo describes a recurring error with exception details.
type ErrorInfo struct {
	Operation     string `json:"operation"`
	Message       string `json:"message"`
	ExceptionType string `json:"exception_type,omitempty"`
	Count         int64  `json:"count"`
	TraceID       string `json:"trace_id"`
}

// SlowOp describes a slow operation.
type SlowOp struct {
	Name  string
	P95Ms float64
	Count int64
}

// Dependency describes a downstream service call.
type Dependency struct {
	Service   string
	CallCount int64
	ErrorRate float64
	AvgMs     float64
}

// TraceResult contains a complete distributed trace.
type TraceResult struct {
	TraceID       string
	Duration      float64
	SpanCount     int
	Services      []string
	HasError      bool
	Spans         []SpanInfo
	Logs          []LogInfo
	RootCause     *RootCause
	CriticalPath  []string
	Comparison    *TraceComparison `json:"comparison,omitempty"`
	MetricContext []MetricContext  `json:"metric_context,omitempty"`
}

// TraceComparison holds a side-by-side comparison between two traces.
type TraceComparison struct {
	OtherTraceID    string     `json:"other_trace_id"`
	OtherDurationMs float64    `json:"other_duration_ms"`
	DurationDeltaMs float64    `json:"duration_delta_ms"`
	SpanDiffs       []SpanDiff `json:"span_diffs"`
}

// SpanDiff describes the latency difference for a matched operation between two traces.
type SpanDiff struct {
	Operation string  `json:"operation"`
	Service   string  `json:"service"`
	ThisMs    float64 `json:"this_ms"`
	OtherMs   float64 `json:"other_ms"`
	DeltaMs   float64 `json:"delta_ms"`
}

// MetricContext captures rollup metric snapshots for a service around a trace's time.
type MetricContext struct {
	Service     string         `json:"service"`
	AtTraceTime MetricSnapshot `json:"at_trace_time"`
}

// MetricSnapshot is a point-in-time view of service_rollup metrics.
type MetricSnapshot struct {
	P50Ms       float64 `json:"p50_ms"`
	P95Ms       float64 `json:"p95_ms"`
	ErrorRate   float64 `json:"error_rate"`
	SpansPerMin float64 `json:"spans_per_min"`
}

// SpanInfo describes a span in a trace.
type SpanInfo struct {
	SpanID       string
	ParentID     string
	Service      string
	Name         string
	Kind         string
	Duration     float64
	SelfTime     float64
	Status       string
	StatusMsg    string
	StartTime    string
	IsCritical   bool
	Events       []SpanEvent
	Links        []SpanLink
	TraceState   string
	Flags        uint32
	ScopeName    string
	ScopeVersion string
	Attributes   map[string]any
	Resource     map[string]any `json:"resource,omitempty"`
}

// SpanEvent is an annotation within a span's lifetime.
type SpanEvent struct {
	Time       int64
	Name       string
	Attributes map[string]string
}

// SpanLink references a related span (e.g., async producer/consumer).
type SpanLink struct {
	TraceID    string
	SpanID     string
	TraceState string
	Attributes map[string]string
}

// LogInfo describes a log entry.
type LogInfo struct {
	Time           string
	ObservedTime   string
	Severity       string
	SeverityNumber int32
	Body           string
	Service        string
	TraceID        string
	SpanID         string
	Flags          uint32
	ScopeName      string
	ScopeVersion   string
	Attributes     map[string]any
}

// RootCause identifies the likely cause of an issue.
type RootCause struct {
	SpanID      string
	Service     string
	Operation   string
	Reason      string
	Description string
}

// FindResult contains search results for spans, logs, and metrics.
type FindResult struct {
	Spans   []SpanResult
	Logs    []LogResult
	Metrics []MetricInfo
	HasMore bool
}

// SpanResult is a span from search results.
type SpanResult struct {
	TraceID      string
	SpanID       string
	Service      string
	Name         string
	Duration     float64
	Status       string
	StartTime    string
	ScopeName    string
	ScopeVersion string
}

// LogResult is a log entry from search results.
type LogResult struct {
	Time           string
	ObservedTime   string
	Severity       string
	SeverityNumber int32
	Body           string
	Service        string
	TraceID        string
	SpanID         string
	ScopeName      string
	ScopeVersion   string
}

// MetricInfo describes a metric with its metadata.
type MetricInfo struct {
	Name         string
	Description  string
	Unit         string
	Type         string
	Service      string
	Value        float64
	Time         string
	Exemplars    []Exemplar
	ScopeName    string
	ScopeVersion string
}

// Exemplar links a metric data point to a trace.
type Exemplar struct {
	Time       int64             `json:"time_unix_nano"`
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	Value      float64           `json:"value"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// SpanParams contains search and aggregation parameters for the spans tool.
type SpanParams struct {
	Query            string
	Operation        string
	Service          string
	Status           string // "error", "ok", "slow", "all"
	Kind             string
	MinDurationMs    *float64
	MaxDurationMs    *float64
	Attrs            map[string]string
	GroupBy          []string // fixed fields only
	OrderBy          string
	IncludeExemplars bool
	Window           int // minutes
	Namespace        string
	TenantID         string
	Limit            int
}

// SpansResult holds either raw spans (ungrouped) or aggregated groups.
type SpansResult struct {
	// Ungrouped path
	Spans        []SpanRow
	TotalMatched int

	// Grouped path
	Groups      []SpanGroup
	TotalGroups int
}

// SpanRow is a single span in ungrouped search results.
type SpanRow struct {
	TraceID    string
	SpanID     string
	Service    string
	Operation  string
	Kind       string
	StartTime  string
	DurationMs float64
	Status     string
	Attributes map[string]string
}

// SpanGroup is one bucket in a group-by aggregation.
type SpanGroup struct {
	Key              map[string]string
	Count            int64
	ErrorCount       int64
	ErrorRate        float64
	P50Ms            float64
	P95Ms            float64
	P99Ms            float64
	ExemplarTraceIDs []string
}

// LogParams contains search and aggregation parameters for the logs tool.
type LogParams struct {
	Query     string
	Severity  []string
	TraceID   string
	Service   string
	Attrs     map[string]string
	GroupBy   []string // "service", "severity", "template"
	OrderBy   string
	Window    int // minutes
	Namespace string
	TenantID  string
	Limit     int
}

// LogsResult holds either raw log rows (ungrouped) or aggregated groups.
type LogsResult struct {
	// Ungrouped path
	Logs         []LogRow
	TotalMatched int

	// Grouped path
	Groups      []LogGroup
	TotalGroups int
}

// LogRow is a single log entry in ungrouped search results.
type LogRow struct {
	Time       string
	Service    string
	Severity   string
	Body       string
	TraceID    string
	SpanID     string
	Attributes map[string]string
}

// LogGroup is one bucket in a group-by aggregation of logs.
type LogGroup struct {
	Key            map[string]string
	Count          int64
	SampleBodies   []string
	SampleTraceIDs []string
}

// MetricListParams contains parameters for discovering available metrics.
type MetricListParams struct {
	Service   string
	Window    int // minutes
	Namespace string
	TenantID  string
	Attrs     map[string]string
	Limit     int
	GroupBy   []string
}

// MetricQueryParams contains parameters for querying metric timeseries.
type MetricQueryParams struct {
	Name        string
	Names       []string
	Aggregation string // "avg", "sum", "min", "max", "count"
	GroupBy     []string
	Granularity string // "1m", "5m", "15m", "1h", "auto"
	Service     string
	Window      int // minutes
	Namespace   string
	TenantID    string
	Attrs       map[string]string
	Limit       int
}

// MetricsListResult holds the result of the metrics list action.
type MetricsListResult struct {
	Metrics []MetricListEntry
}

// MetricListEntry describes a discovered metric.
type MetricListEntry struct {
	Name        string
	Type        string
	Unit        string
	Services    []string
	Description string
}

// MetricsQueryResult holds the result of the metrics query action.
type MetricsQueryResult struct {
	Series        []MetricSeries
	Anomalies     []MetricAnomaly
	FailedMetrics []string // metric names whose queries failed
}

// MetricSeries is one timeseries stream returned by the query action.
type MetricSeries struct {
	Labels      map[string]string
	Metric      string
	Aggregation string
	Unit        string
	Datapoints  []MetricDatapoint
}

// MetricDatapoint is a single timestamped value in a series.
type MetricDatapoint struct {
	Time  string
	Value float64
}

// HistogramResult holds the result of the metrics histogram action.
type HistogramResult struct {
	Histograms []HistogramEntry
}

// HistogramEntry is a single histogram data point.
type HistogramEntry struct {
	Metric       string    `json:"metric"`
	Service      string    `json:"service"`
	Time         string    `json:"time"`
	Bounds       []float64 `json:"bounds"`
	BucketCounts []uint64  `json:"bucket_counts"`
	Count        int64     `json:"count"`
	Sum          float64   `json:"sum"`
}

// ExemplarResult holds the result of the metrics exemplars action.
type ExemplarResult struct {
	Exemplars []ExemplarEntry
}

// ExemplarEntry links a metric data point to a trace.
type ExemplarEntry struct {
	Metric  string  `json:"metric"`
	Service string  `json:"service"`
	Time    string  `json:"time"`
	TraceID string  `json:"trace_id"`
	SpanID  string  `json:"span_id,omitempty"`
	Value   float64 `json:"value"`
}

// MetricAnomaly describes a statistical anomaly detected in metric data.
type MetricAnomaly struct {
	Time           string
	Type           string
	Value          float64
	Expected       float64
	DeviationSigma float64
}
