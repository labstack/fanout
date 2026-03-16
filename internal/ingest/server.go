package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/lake"
)

type Server struct {
	cfg        config.Config
	outSpans   chan<- lake.SpanRow
	outLogs    chan<- lake.LogRow
	outMetrics chan<- lake.MetricRow
}

type traceService struct {
	collectortrace.UnimplementedTraceServiceServer
	srv *Server
}

type logsService struct {
	collectorlogs.UnimplementedLogsServiceServer
	srv *Server
}

type metricsService struct {
	collectormetrics.UnimplementedMetricsServiceServer
	srv *Server
}

func NewServer(cfg config.Config, spans chan<- lake.SpanRow, logs chan<- lake.LogRow, metrics chan<- lake.MetricRow) *Server {
	return &Server{cfg: cfg, outSpans: spans, outLogs: logs, outMetrics: metrics}
}

func RegisterOTLP(s grpc.ServiceRegistrar, srv *Server) {
	collectortrace.RegisterTraceServiceServer(s, &traceService{srv: srv})
	collectorlogs.RegisterLogsServiceServer(s, &logsService{srv: srv})
	collectormetrics.RegisterMetricsServiceServer(s, &metricsService{srv: srv})
}

// ---- Trace ingest ----

func (ts *traceService) Export(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	cfg := ts.srv.cfg
	now := time.Now().UnixNano()
	for _, rs := range req.ResourceSpans {
		resourceJSON := toJSON(rs.Resource)
		svc := getServiceName(rs.Resource)
		namespace := getServiceNamespace(rs.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNS
		}
		for _, ss := range rs.ScopeSpans {
			scopeName, scopeVer := scopeInfo(ss.Scope)
			for _, sp := range ss.Spans {
				row := lake.SpanRow{
					TenantID:       cfg.TenantID.String(),
					Namespace:      namespace,
					TraceID:        fmt.Sprintf("%x", sp.TraceId),
					SpanID:         fmt.Sprintf("%x", sp.SpanId),
					ParentSpanID:   hexOrEmpty(sp.ParentSpanId),
					ServiceName:    svc,
					Name:           sp.Name,
					Kind:           sp.Kind.String(),
					StartUnixNanos: int64(sp.StartTimeUnixNano),
					EndUnixNanos:   int64(sp.EndTimeUnixNano),
					DurationMs:     float64(sp.EndTimeUnixNano-sp.StartTimeUnixNano) / 1e6,
					StatusCode:     sp.Status.Code.String(),
					StatusMsg:      sp.Status.Message,
					ResourceJSON:   resourceJSON,
					AttributesJSON: toJSON(sp.Attributes),
					EventsJSON:     eventsToJSON(sp.Events),
					LinksJSON:      linksToJSON(sp.Links),
					TraceState:     sp.TraceState,
					Flags:          sp.Flags,
					ScopeName:      scopeName,
					ScopeVersion:   scopeVer,
					IngestedAt:     now,
					// Pre-extracted OTel semantic convention attributes.
					HTTPMethod:     spanAttr(sp.Attributes, "http.method", "http.request.method"),
					HTTPStatusCode: spanAttrInt(sp.Attributes, "http.status_code", "http.response.status_code"),
					HTTPRoute:      spanAttr(sp.Attributes, "http.route"),
					DBSystem:       spanAttr(sp.Attributes, "db.system"),
					RPCMethod:      spanAttr(sp.Attributes, "rpc.method"),
					RPCService:     spanAttr(sp.Attributes, "rpc.service"),
					PeerService:    spanAttr(sp.Attributes, "peer.service", "server.address"),
					ServiceVersion: getResourceAttr(rs.Resource, "service.version"),
					DeploymentEnv:  getResourceAttr(rs.Resource, "deployment.environment"),
				}
				// Partial ingest is acceptable: OTLP clients retry the full batch on error.
				select {
				case ts.srv.outSpans <- row:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

// ---- Logs ingest ----

func (ls *logsService) Export(ctx context.Context, req *collectorlogs.ExportLogsServiceRequest) (*collectorlogs.ExportLogsServiceResponse, error) {
	cfg := ls.srv.cfg
	now := time.Now().UnixNano()
	for _, rl := range req.ResourceLogs {
		resourceJSON := toJSON(rl.Resource)
		svc := getServiceName(rl.Resource)
		namespace := getServiceNamespace(rl.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNS
		}
		for _, sl := range rl.ScopeLogs {
			scopeName, scopeVer := scopeInfo(sl.Scope)
			for _, lr := range sl.LogRecords {
				row := lake.LogRow{
					TenantID:          cfg.TenantID.String(),
					Namespace:         namespace,
					TimeUnixNanos:     int64(lr.TimeUnixNano),
					ObservedTimeNanos: int64(lr.ObservedTimeUnixNano),
					Severity:          lr.SeverityText,
					SeverityNumber:    int32(lr.SeverityNumber),
					Body:              bodyString(lr.Body),
					ServiceName:       svc,
					TraceID:           hexOrEmpty(lr.TraceId),
					SpanID:            hexOrEmpty(lr.SpanId),
					Flags:             lr.Flags,
					ResourceJSON:      resourceJSON,
					AttributesJSON:    toJSON(lr.Attributes),
					ScopeName:         scopeName,
					ScopeVersion:      scopeVer,
					IngestedAt:        now,
				}
				select {
				case ls.srv.outLogs <- row:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
	}
	return &collectorlogs.ExportLogsServiceResponse{}, nil
}

// ---- Metrics ingest ----

func (ms *metricsService) Export(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	cfg := ms.srv.cfg
	now := time.Now().UnixNano()
	for _, rm := range req.ResourceMetrics {
		resourceJSON := toJSON(rm.Resource)
		svc := getServiceName(rm.Resource)
		namespace := getServiceNamespace(rm.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNS
		}
		for _, sm := range rm.ScopeMetrics {
			scopeName, scopeVer := scopeInfo(sm.Scope)
			for _, m := range sm.Metrics {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					for _, dp := range d.Gauge.DataPoints {
						row := lake.MetricRow{
							TenantID:       cfg.TenantID.String(),
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							Description:    m.Description,
							Unit:           m.Unit,
							MType:          "gauge",
							ServiceName:    svc,
							Value:          number(dp.Value),
							ExemplarsJSON:  exemplarsToJSON(dp.Exemplars),
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case ms.srv.outMetrics <- row:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				case *metricspb.Metric_Sum:
					kind := "sum"
					if d.Sum.AggregationTemporality == metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
						kind = "sum_delta"
					}
					for _, dp := range d.Sum.DataPoints {
						row := lake.MetricRow{
							TenantID:       cfg.TenantID.String(),
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							Description:    m.Description,
							Unit:           m.Unit,
							MType:          kind,
							ServiceName:    svc,
							Value:          number(dp.Value),
							ExemplarsJSON:  exemplarsToJSON(dp.Exemplars),
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case ms.srv.outMetrics <- row:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				case *metricspb.Metric_Histogram:
					for _, dp := range d.Histogram.DataPoints {
						histSum := 0.0
						if dp.Sum != nil {
							histSum = *dp.Sum
						}
						row := lake.MetricRow{
							TenantID:       cfg.TenantID.String(),
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							Description:    m.Description,
							Unit:           m.Unit,
							MType:          "histogram",
							ServiceName:    svc,
							HistBoundsJSON: toJSON(dp.ExplicitBounds),
							HistCountsJSON: toJSON(dp.BucketCounts),
							HistCount:      int64(dp.Count),
							HistSum:        histSum,
							ExemplarsJSON:  exemplarsToJSON(dp.Exemplars),
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case ms.srv.outMetrics <- row:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				case *metricspb.Metric_ExponentialHistogram:
					for _, dp := range d.ExponentialHistogram.DataPoints {
						histSum := 0.0
						if dp.Sum != nil {
							histSum = *dp.Sum
						}
						row := lake.MetricRow{
							TenantID:       cfg.TenantID.String(),
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							Description:    m.Description,
							Unit:           m.Unit,
							MType:          "exp_histogram",
							ServiceName:    svc,
							HistBoundsJSON: toJSON(expHistBuckets(dp)),
							HistCountsJSON: toJSON(expHistCounts(dp)),
							HistCount:      int64(dp.Count),
							HistSum:        histSum,
							ExemplarsJSON:  exemplarsToJSON(dp.Exemplars),
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case ms.srv.outMetrics <- row:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				case *metricspb.Metric_Summary:
					for _, dp := range d.Summary.DataPoints {
						row := lake.MetricRow{
							TenantID:       cfg.TenantID.String(),
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							Description:    m.Description,
							Unit:           m.Unit,
							MType:          "summary",
							ServiceName:    svc,
							HistBoundsJSON: toJSON(summaryQuantiles(dp)),
							HistCountsJSON: toJSON(summaryValues(dp)),
							HistCount:      int64(dp.Count),
							HistSum:        dp.Sum,
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case ms.srv.outMetrics <- row:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				}
			}
		}
	}
	return &collectormetrics.ExportMetricsServiceResponse{}, nil
}

// ---- helpers ----

func toJSON(v interface{}) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("json marshal failed", "err", err)
		return []byte("null")
	}
	return b
}

func bodyString(v *common.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue
	default:
		b, err := json.Marshal(v)
		if err != nil {
			slog.Error("json marshal body failed", "err", err)
			return ""
		}
		return string(b)
	}
}

// spanAttr extracts a string attribute value from a span's KeyValue list.
// Keys are checked in priority order (e.g. http.method before http.request.method).
func spanAttr(attrs []*common.KeyValue, keys ...string) string {
	for _, key := range keys {
		for _, kv := range attrs {
			if kv.Key == key {
				if s := asString(kv.Value); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// spanAttrInt extracts an integer attribute as a string (e.g. http.status_code: 200 → "200").
// Keys are checked in priority order.
func spanAttrInt(attrs []*common.KeyValue, keys ...string) string {
	for _, key := range keys {
		for _, kv := range attrs {
			if kv.Key == key {
				if s := asString(kv.Value); s != "" {
					return s
				}
				// Handle integer values (common for status codes)
				if kv.Value != nil {
					if iv, ok := kv.Value.Value.(*common.AnyValue_IntValue); ok {
						return fmt.Sprintf("%d", iv.IntValue)
					}
				}
			}
		}
	}
	return ""
}

func getServiceName(r *resourcepb.Resource) string {
	return getResourceAttr(r, "service.name")
}

func getServiceNamespace(r *resourcepb.Resource) string {
	return getResourceAttr(r, "service.namespace")
}

func getResourceAttr(r *resourcepb.Resource, key string) string {
	if r == nil {
		return ""
	}
	for _, kv := range r.Attributes {
		if kv.Key == key {
			if s := asString(kv.Value); s != "" {
				return s
			}
		}
	}
	return ""
}

func asString(v *common.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue
	default:
		return ""
	}
}

func number(v interface{}) float64 {
	switch x := v.(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		return x.AsDouble
	case *metricspb.NumberDataPoint_AsInt:
		return float64(x.AsInt)
	default:
		return 0
	}
}

func hexOrEmpty(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return fmt.Sprintf("%x", b)
}

func eventsToJSON(events []*tracepb.Span_Event) []byte {
	if len(events) == 0 {
		return nil
	}
	type evt struct {
		Time       int64             `json:"time_unix_nano"`
		Name       string            `json:"name"`
		Attributes map[string]string `json:"attributes,omitempty"`
	}
	out := make([]evt, 0, len(events))
	for _, e := range events {
		ev := evt{
			Time: int64(e.TimeUnixNano),
			Name: e.Name,
		}
		if len(e.Attributes) > 0 {
			ev.Attributes = make(map[string]string, len(e.Attributes))
			for _, kv := range e.Attributes {
				ev.Attributes[kv.Key] = asString(kv.Value)
			}
		}
		out = append(out, ev)
	}
	b, err := json.Marshal(out)
	if err != nil {
		slog.Error("json marshal events failed", "err", err)
		return nil
	}
	return b
}

func linksToJSON(links []*tracepb.Span_Link) []byte {
	if len(links) == 0 {
		return nil
	}
	type link struct {
		TraceID    string            `json:"trace_id"`
		SpanID     string            `json:"span_id"`
		TraceState string            `json:"trace_state,omitempty"`
		Attributes map[string]string `json:"attributes,omitempty"`
	}
	out := make([]link, 0, len(links))
	for _, l := range links {
		ln := link{
			TraceID:    fmt.Sprintf("%x", l.TraceId),
			SpanID:     fmt.Sprintf("%x", l.SpanId),
			TraceState: l.TraceState,
		}
		if len(l.Attributes) > 0 {
			ln.Attributes = make(map[string]string, len(l.Attributes))
			for _, kv := range l.Attributes {
				ln.Attributes[kv.Key] = asString(kv.Value)
			}
		}
		out = append(out, ln)
	}
	b, err := json.Marshal(out)
	if err != nil {
		slog.Error("json marshal links failed", "err", err)
		return nil
	}
	return b
}

func scopeInfo(scope *common.InstrumentationScope) (name, version string) {
	if scope == nil {
		return "", ""
	}
	return scope.Name, scope.Version
}

func exemplarsToJSON(exemplars []*metricspb.Exemplar) []byte {
	if len(exemplars) == 0 {
		return nil
	}
	type ex struct {
		Time       int64             `json:"time_unix_nano"`
		TraceID    string            `json:"trace_id,omitempty"`
		SpanID     string            `json:"span_id,omitempty"`
		Value      float64           `json:"value"`
		Attributes map[string]string `json:"attributes,omitempty"`
	}
	out := make([]ex, 0, len(exemplars))
	for _, e := range exemplars {
		val := 0.0
		switch v := e.Value.(type) {
		case *metricspb.Exemplar_AsDouble:
			val = v.AsDouble
		case *metricspb.Exemplar_AsInt:
			val = float64(v.AsInt)
		}
		item := ex{
			Time:    int64(e.TimeUnixNano),
			TraceID: hexOrEmpty(e.TraceId),
			SpanID:  hexOrEmpty(e.SpanId),
			Value:   val,
		}
		if len(e.FilteredAttributes) > 0 {
			item.Attributes = make(map[string]string, len(e.FilteredAttributes))
			for _, kv := range e.FilteredAttributes {
				item.Attributes[kv.Key] = asString(kv.Value)
			}
		}
		out = append(out, item)
	}
	b, err := json.Marshal(out)
	if err != nil {
		slog.Error("json marshal exemplars failed", "err", err)
		return nil
	}
	return b
}

func expHistBuckets(dp *metricspb.ExponentialHistogramDataPoint) []float64 {
	// Convert exponential histogram to explicit buckets for simplified storage
	// This is a lossy conversion but provides compatibility with standard histogram queries
	if dp.Positive == nil || len(dp.Positive.BucketCounts) == 0 {
		return nil
	}
	base := math.Pow(2, math.Pow(2, float64(-dp.Scale)))
	offset := int(dp.Positive.Offset)
	buckets := make([]float64, len(dp.Positive.BucketCounts))
	for i := range dp.Positive.BucketCounts {
		buckets[i] = math.Pow(base, float64(offset+i))
	}
	return buckets
}

func expHistCounts(dp *metricspb.ExponentialHistogramDataPoint) []uint64 {
	if dp.Positive == nil {
		return nil
	}
	return dp.Positive.BucketCounts
}

func summaryQuantiles(dp *metricspb.SummaryDataPoint) []float64 {
	quantiles := make([]float64, len(dp.QuantileValues))
	for i, qv := range dp.QuantileValues {
		quantiles[i] = qv.Quantile
	}
	return quantiles
}

func summaryValues(dp *metricspb.SummaryDataPoint) []float64 {
	values := make([]float64, len(dp.QuantileValues))
	for i, qv := range dp.QuantileValues {
		values[i] = qv.Value
	}
	return values
}
