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

// IndexedSpan is the narrow projection needed by trace-detail queries. The
// complete authoritative span remains in Parquet and is available through SQL;
// keeping this narrow projection small avoids decoding large JSON columns.
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
	ResourceJSON      []byte
	AttributesJSON    []byte
	ScopeName         string
	ScopeVersion      string
	IngestedAt        int64
	BodyTemplate      string
}

type Metric struct {
	Namespace      string
	EventUnixNanos int64
	TimeUnixNanos  int64
	Name           string
	Description    string
	Unit           string
	Type           string
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

func NormalizeNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}
