// Package telemetry defines the canonical, storage-independent rows emitted by
// the OTLP decoder. These are the only telemetry write contracts in Fanout.
package telemetry

type Span struct {
	Namespace        string
	TraceID          string
	SpanID           string
	ParentSpanID     string
	ServiceName      string
	Name             string
	Kind             string
	StartUnixNanos   int64
	EndUnixNanos     int64
	DurationMS       float64
	StatusCode       string
	StatusMsg        string
	Resource         map[string]any
	Attributes       map[string]any
	Events           []Event
	Links            []Link
	TraceState       string
	Flags            uint32
	ScopeName        string
	ScopeVersion     string
	IngestedAt       int64
	RPCService       string
	PeerService      string
	ExceptionType    string
	ExceptionMessage string
}

// Event is a span event with its typed attributes.
type Event struct {
	TimeUnixNanos int64
	Name          string
	Attributes    map[string]any
}

// Link is a span link with its typed attributes.
type Link struct {
	TraceID    string
	SpanID     string
	TraceState string
	Attributes map[string]any
}

// IndexedSpan is the narrow projection needed by trace-detail queries. The
// complete authoritative span remains in Parquet and is available through SQL;
// keeping this narrow projection small avoids decoding large attribute columns.
type IndexedSpan struct {
	Namespace      string
	TraceID        string
	SpanID         string
	ParentSpanID   string
	ServiceName    string
	Name           string
	Kind           string
	StartUnixNanos int64
	DurationMS     float64
	StatusCode     string
	StatusMsg      string
}

type Log struct {
	Namespace         string
	EventUnixNanos    int64
	TimeUnixNanos     int64
	ObservedTimeNanos int64
	Severity          string
	SeverityNumber    int32
	Body              string
	ServiceName       string
	TraceID           string
	SpanID            string
	Flags             uint32
	Resource          map[string]any
	Attributes        map[string]any
	ScopeName         string
	ScopeVersion      string
	IngestedAt        int64
	BodyTemplate      string
}

// Metric is one OTLP data point. Type is the user-visible kind: gauge,
// counter, updown, histogram (explicit or exponential) or summary.
type Metric struct {
	SeriesID           uint64
	Namespace          string
	EventUnixNanos     int64
	TimeUnixNanos      int64
	Name               string
	Description        string
	Unit               string
	Type               string
	Temporality        string // cumulative or delta; empty for gauges and summaries
	StartTimeUnixNanos int64
	ServiceName        string
	Flags              uint32
	Value              float64 // gauge and sum points; integers become doubles
	Histogram
	Exemplars    []Exemplar
	Attributes   map[string]any
	Resource     map[string]any
	ScopeName    string
	ScopeVersion string
	IngestedAt   int64
}

// Histogram holds the aggregate of a histogram or summary point.
type Histogram struct {
	Count          uint64
	Sum            float64
	Min            float64
	Max            float64
	HasSum         bool
	HasMin         bool
	HasMax         bool
	Bounds         []float64 // explicit bucket upper bounds
	Counts         []uint64  // explicit bucket counts, one more than Bounds
	Exponential    bool
	Scale          int32
	ZeroCount      uint64
	ZeroThreshold  float64
	PositiveOffset int32
	PositiveCounts []uint64
	NegativeOffset int32
	NegativeCounts []uint64
	Quantiles      []float64 // summary quantiles in [0, 1]
	QuantileValues []float64
}

// Exemplar is a sampled measurement with its typed filtered attributes.
type Exemplar struct {
	TimeUnixNanos int64
	Value         float64 // integers become doubles
	TraceID       string
	SpanID        string
	Attributes    map[string]any
}

func NormalizeNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}

// FirstPositiveNanos returns the first usable timestamp in priority order.
func FirstPositiveNanos(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
