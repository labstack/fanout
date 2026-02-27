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

// TopologyResult contains service dependency map data.
type TopologyResult struct {
	Nodes []ServiceNode
	Edges []ServiceEdge
}

// ServiceNode represents a service in the topology.
type ServiceNode struct {
	Name        string
	Namespace   string
	Status      string
	SpanCount   int64
	P95Ms       float64
	ErrorRate   float64
	LogCount    int64
	MetricCount int64
	Trend       []int64 // optional sparkline data
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

// TimelineResult contains time-bucketed metrics with anomalies.
type TimelineResult struct {
	Buckets   []TimeBucket
	Anomalies []Anomaly
}

// TimeBucket represents metrics for a time window.
type TimeBucket struct {
	Time        string
	Requests    int64
	Errors      int64
	P50Ms       float64
	P95Ms       float64
	ErrorRate   float64
	IsAnomaly   bool
	AnomalyType string
}

// Anomaly represents a detected anomaly.
type Anomaly struct {
	Time        string
	Type        string
	Description string
	Service     string
	Value       float64
	Expected    float64
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
}

// ErrorInfo describes a recurring error.
type ErrorInfo struct {
	Message string
	Count   int64
	TraceID string
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
	TraceID      string
	Duration     float64
	SpanCount    int
	Services     []string
	HasError     bool
	Spans        []SpanInfo
	Logs         []LogInfo
	RootCause    *RootCause
	CriticalPath []string
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
	Time       int64
	TraceID    string
	SpanID     string
	Value      float64
	Attributes map[string]string
}
