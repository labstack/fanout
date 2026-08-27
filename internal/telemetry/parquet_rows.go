package telemetry

type spanParquetRow struct {
	Namespace        string  `parquet:"namespace"`
	TraceID          string  `parquet:"trace_id"`
	TraceHash        uint64  `parquet:"_trace_hash"`
	SpanID           string  `parquet:"span_id"`
	ParentSpanID     string  `parquet:"parent_span_id"`
	Service          string  `parquet:"service"`
	Operation        string  `parquet:"operation"`
	Kind             string  `parquet:"kind"`
	StartTime        int64   `parquet:"start_time,timestamp(nanosecond)"`
	EndTime          int64   `parquet:"end_time,timestamp(nanosecond)"`
	StartUnixNano    int64   `parquet:"start_unix_nano"`
	EndUnixNano      int64   `parquet:"end_unix_nano"`
	DurationMS       float64 `parquet:"duration_ms"`
	Status           string  `parquet:"status"`
	StatusMessage    string  `parquet:"status_message"`
	ResourceJSON     string  `parquet:"resource_json,dict"`
	AttributesJSON   string  `parquet:"attributes_json,dict"`
	EventsJSON       string  `parquet:"events_json,dict"`
	LinksJSON        string  `parquet:"links_json,dict"`
	TraceState       string  `parquet:"trace_state,dict"`
	Flags            int64   `parquet:"flags"`
	ScopeName        string  `parquet:"scope_name,dict"`
	ScopeVersion     string  `parquet:"scope_version,dict"`
	IngestedAt       int64   `parquet:"ingested_at,timestamp(nanosecond)"`
	IngestedUnixNano int64   `parquet:"ingested_unix_nano"`
	HTTPMethod       string  `parquet:"http_method,dict"`
	HTTPStatusCode   string  `parquet:"http_status_code,dict"`
	HTTPRoute        string  `parquet:"http_route,dict"`
	DBSystem         string  `parquet:"db_system,dict"`
	RPCMethod        string  `parquet:"rpc_method,dict"`
	RPCService       string  `parquet:"rpc_service,dict"`
	PeerService      string  `parquet:"peer_service,dict"`
	ServiceVersion   string  `parquet:"service_version,dict"`
	DeploymentEnv    string  `parquet:"deployment_env,dict"`
	ExceptionType    string  `parquet:"exception_type,dict"`
	ExceptionMessage string  `parquet:"exception_message,dict"`
}

func makeSpanParquetRow(r Span) spanParquetRow {
	return spanParquetRow{
		Namespace: r.Namespace, TraceID: r.TraceID, SpanID: r.SpanID, ParentSpanID: r.ParentSpanID,
		Service: r.ServiceName, Operation: r.Name, Kind: r.Kind, StartTime: r.StartUnixNanos,
		EndTime: r.EndUnixNanos, StartUnixNano: r.StartUnixNanos, EndUnixNano: r.EndUnixNanos,
		DurationMS: r.DurationMS, Status: r.StatusCode, StatusMessage: r.StatusMsg,
		ResourceJSON: string(r.ResourceJSON), AttributesJSON: string(r.AttributesJSON), EventsJSON: string(r.EventsJSON), LinksJSON: string(r.LinksJSON),
		TraceState: r.TraceState, Flags: int64(r.Flags), ScopeName: r.ScopeName, ScopeVersion: r.ScopeVersion,
		IngestedAt: r.IngestedAt, IngestedUnixNano: r.IngestedAt, HTTPMethod: r.HTTPMethod,
		HTTPStatusCode: r.HTTPStatusCode, HTTPRoute: r.HTTPRoute, DBSystem: r.DBSystem, RPCMethod: r.RPCMethod,
		RPCService: r.RPCService, PeerService: r.PeerService, ServiceVersion: r.ServiceVersion,
		DeploymentEnv: r.DeploymentEnv, ExceptionType: r.ExceptionType, ExceptionMessage: r.ExceptionMessage,
	}
}

type indexedSpanParquetRow struct {
	Namespace     string  `parquet:"namespace"`
	TraceID       string  `parquet:"trace_id"`
	SpanID        string  `parquet:"span_id"`
	ParentSpanID  string  `parquet:"parent_span_id"`
	Service       string  `parquet:"service"`
	Operation     string  `parquet:"operation"`
	Kind          string  `parquet:"kind"`
	StartUnixNano int64   `parquet:"start_unix_nano"`
	DurationMS    float64 `parquet:"duration_ms"`
	Status        string  `parquet:"status"`
	StatusMessage string  `parquet:"status_message"`
}

func (r indexedSpanParquetRow) span() IndexedSpan {
	return IndexedSpan{
		Namespace: r.Namespace, TraceID: r.TraceID, SpanID: r.SpanID, ParentSpanID: r.ParentSpanID,
		ServiceName: r.Service, Name: r.Operation, Kind: r.Kind, StartUnixNanos: r.StartUnixNano,
		DurationMS: r.DurationMS, StatusCode: r.Status, StatusMsg: r.StatusMessage,
	}
}

type logParquetRow struct {
	Namespace            string `parquet:"namespace,dict"`
	LogTime              int64  `parquet:"log_time,timestamp(nanosecond)"`
	ObservedTime         int64  `parquet:"observed_time,timestamp(nanosecond)"`
	TimeUnixNano         int64  `parquet:"time_unix_nano"`
	ObservedTimeUnixNano int64  `parquet:"observed_time_unix_nano"`
	Severity             string `parquet:"severity,dict"`
	SeverityNumber       int64  `parquet:"severity_number"`
	Body                 string `parquet:"body"`
	Service              string `parquet:"service,dict"`
	TraceID              string `parquet:"trace_id"`
	SpanID               string `parquet:"span_id"`
	Flags                int64  `parquet:"flags"`
	ResourceJSON         string `parquet:"resource_json,dict"`
	AttributesJSON       string `parquet:"attributes_json,dict"`
	ScopeName            string `parquet:"scope_name,dict"`
	ScopeVersion         string `parquet:"scope_version,dict"`
	IngestedAt           int64  `parquet:"ingested_at,timestamp(nanosecond)"`
	IngestedUnixNano     int64  `parquet:"ingested_unix_nano"`
	BodyTemplate         string `parquet:"body_template,dict"`
}

func makeLogParquetRow(r Log) logParquetRow {
	return logParquetRow{
		Namespace: r.Namespace, LogTime: firstPositive(r.TimeUnixNanos, r.ObservedTimeNanos, r.IngestedAt),
		ObservedTime: firstPositive(r.ObservedTimeNanos, r.TimeUnixNanos, r.IngestedAt), TimeUnixNano: r.TimeUnixNanos,
		ObservedTimeUnixNano: r.ObservedTimeNanos, Severity: r.Severity, SeverityNumber: int64(r.SeverityNumber),
		Body: r.Body, Service: r.ServiceName, TraceID: r.TraceID, SpanID: r.SpanID, Flags: int64(r.Flags),
		ResourceJSON: string(r.ResourceJSON), AttributesJSON: string(r.AttributesJSON), ScopeName: r.ScopeName,
		ScopeVersion: r.ScopeVersion, IngestedAt: r.IngestedAt, IngestedUnixNano: r.IngestedAt, BodyTemplate: r.BodyTemplate,
	}
}

type metricParquetRow struct {
	Namespace        string  `parquet:"namespace,dict"`
	MetricTime       int64   `parquet:"metric_time,timestamp(nanosecond)"`
	TimeUnixNano     int64   `parquet:"time_unix_nano"`
	Name             string  `parquet:"name,dict"`
	Description      string  `parquet:"description,dict"`
	Unit             string  `parquet:"unit,dict"`
	MetricType       string  `parquet:"metric_type,dict"`
	Service          string  `parquet:"service,dict"`
	Value            float64 `parquet:"value"`
	HistBoundsJSON   string  `parquet:"hist_bounds_json,dict"`
	HistCountsJSON   string  `parquet:"hist_counts_json,dict"`
	HistCount        int64   `parquet:"hist_count"`
	HistSum          float64 `parquet:"hist_sum"`
	ExemplarsJSON    string  `parquet:"exemplars_json,dict"`
	AttributesJSON   string  `parquet:"attributes_json,dict"`
	ResourceJSON     string  `parquet:"resource_json,dict"`
	ScopeName        string  `parquet:"scope_name,dict"`
	ScopeVersion     string  `parquet:"scope_version,dict"`
	IngestedAt       int64   `parquet:"ingested_at,timestamp(nanosecond)"`
	IngestedUnixNano int64   `parquet:"ingested_unix_nano"`
}

func makeMetricParquetRow(r Metric) metricParquetRow {
	return metricParquetRow{
		Namespace: r.Namespace, MetricTime: firstPositive(r.TimeUnixNanos, r.IngestedAt), TimeUnixNano: r.TimeUnixNanos,
		Name: r.Name, Description: r.Description, Unit: r.Unit, MetricType: r.Type, Service: r.ServiceName,
		Value: r.Value, HistBoundsJSON: string(r.HistBoundsJSON), HistCountsJSON: string(r.HistCountsJSON),
		HistCount: r.HistCount, HistSum: r.HistSum, ExemplarsJSON: string(r.ExemplarsJSON),
		AttributesJSON: string(r.AttributesJSON), ResourceJSON: string(r.ResourceJSON), ScopeName: r.ScopeName,
		ScopeVersion: r.ScopeVersion, IngestedAt: r.IngestedAt, IngestedUnixNano: r.IngestedAt,
	}
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
