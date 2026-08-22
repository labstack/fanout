package ingest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	return ts.srv.exportTraces(ctx, req)
}

func (s *Server) exportTraces(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	cfg := s.cfg
	now := time.Now().UnixNano()
	for _, rs := range req.ResourceSpans {
		resourceJSON := resourceAttrsJSON(rs.Resource)
		svc := getServiceName(rs.Resource)
		namespace := getServiceNamespace(rs.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNamespace
		}
		for _, ss := range rs.ScopeSpans {
			scopeName, scopeVer := scopeInfo(ss.Scope)
			for _, sp := range ss.Spans {
				row := lake.SpanRow{
					Namespace:      namespace,
					TraceID:        fmt.Sprintf("%x", sp.TraceId),
					SpanID:         fmt.Sprintf("%x", sp.SpanId),
					ParentSpanID:   hexOrEmpty(sp.ParentSpanId),
					ServiceName:    svc,
					Name:           sp.Name,
					Kind:           sp.Kind.String(),
					StartUnixNanos: int64(sp.StartTimeUnixNano),
					EndUnixNanos:   int64(sp.EndTimeUnixNano),
					DurationMs:     spanDurationMs(sp.StartTimeUnixNano, sp.EndTimeUnixNano),
					StatusCode:     sp.Status.Code.String(),
					StatusMsg:      sp.Status.Message,
					ResourceJSON:   resourceJSON,
					AttributesJSON: attrsJSON(sp.Attributes),
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
				// Pre-extracted exception info from span events.
				excType, excMsg := extractException(sp.Events)
				row.ExceptionType = excType
				row.ExceptionMessage = excMsg
				// Partial ingest is acceptable: OTLP clients retry the full batch on error.
				select {
				case s.outSpans <- row:
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
	return ls.srv.exportLogs(ctx, req)
}

func (s *Server) exportLogs(ctx context.Context, req *collectorlogs.ExportLogsServiceRequest) (*collectorlogs.ExportLogsServiceResponse, error) {
	cfg := s.cfg
	now := time.Now().UnixNano()
	for _, rl := range req.ResourceLogs {
		resourceJSON := resourceAttrsJSON(rl.Resource)
		svc := getServiceName(rl.Resource)
		namespace := getServiceNamespace(rl.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNamespace
		}
		for _, sl := range rl.ScopeLogs {
			scopeName, scopeVer := scopeInfo(sl.Scope)
			for _, lr := range sl.LogRecords {
				body := bodyString(lr.Body)
				tmpl := safeNormalizeTemplate(body)
				row := lake.LogRow{
					Namespace:         namespace,
					TimeUnixNanos:     int64(lr.TimeUnixNano),
					ObservedTimeNanos: int64(lr.ObservedTimeUnixNano),
					Severity:          normalizeSeverity(lr.SeverityText, int32(lr.SeverityNumber)),
					SeverityNumber:    int32(lr.SeverityNumber),
					Body:              body,
					BodyTemplate:      tmpl,
					ServiceName:       svc,
					TraceID:           hexOrEmpty(lr.TraceId),
					SpanID:            hexOrEmpty(lr.SpanId),
					Flags:             lr.Flags,
					ResourceJSON:      resourceJSON,
					AttributesJSON:    attrsJSON(lr.Attributes),
					ScopeName:         scopeName,
					ScopeVersion:      scopeVer,
					IngestedAt:        now,
				}
				select {
				case s.outLogs <- row:
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
	return ms.srv.exportMetrics(ctx, req)
}

func (s *Server) exportMetrics(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	cfg := s.cfg
	now := time.Now().UnixNano()
	for _, rm := range req.ResourceMetrics {
		resourceJSON := resourceAttrsJSON(rm.Resource)
		svc := getServiceName(rm.Resource)
		namespace := getServiceNamespace(rm.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNamespace
		}
		for _, sm := range rm.ScopeMetrics {
			scopeName, scopeVer := scopeInfo(sm.Scope)
			for _, m := range sm.Metrics {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					for _, dp := range d.Gauge.DataPoints {
						row := lake.MetricRow{
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							Description:    m.Description,
							Unit:           m.Unit,
							MType:          "gauge",
							ServiceName:    svc,
							Value:          number(dp.Value),
							ExemplarsJSON:  exemplarsToJSON(dp.Exemplars),
							AttributesJSON: attrsJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case s.outMetrics <- row:
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
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							Description:    m.Description,
							Unit:           m.Unit,
							MType:          kind,
							ServiceName:    svc,
							Value:          number(dp.Value),
							ExemplarsJSON:  exemplarsToJSON(dp.Exemplars),
							AttributesJSON: attrsJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case s.outMetrics <- row:
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
							AttributesJSON: attrsJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case s.outMetrics <- row:
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
							AttributesJSON: attrsJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case s.outMetrics <- row:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				case *metricspb.Metric_Summary:
					for _, dp := range d.Summary.DataPoints {
						row := lake.MetricRow{
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
							AttributesJSON: attrsJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							ScopeName:      scopeName,
							ScopeVersion:   scopeVer,
							IngestedAt:     now,
						}
						select {
						case s.outMetrics <- row:
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

// spanDurationMs computes a span's duration in milliseconds, guarding against
// the unsigned underflow that a malformed span (end before start) would produce:
// uint64 subtraction wraps to a huge positive value, which would otherwise be
// stored as a multi-century duration. Such spans are clamped to 0.
func spanDurationMs(startNano, endNano uint64) float64 {
	if endNano < startNano {
		return 0
	}
	return float64(endNano-startNano) / 1e6
}

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

// attrsJSON flattens an OTLP attribute list into a flat JSON object keyed by the
// literal (dotted) attribute name, e.g. {"http.method":"GET","http.status_code":200}.
// This is the shape the attr() macro and json_extract_string(col, '$."key"') paths
// expect. Marshaling the raw []*KeyValue (as the old toJSON path did) produced an
// array of {Key,Value} structs that no JSON-path query could read. Returns nil for
// an empty list so the column stays NULL.
var attrBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// attrsJSON flattens an OTLP attribute list into a flat JSON object. The fast
// path writes the object directly into a pooled buffer, skipping the
// map[string]any + interface boxing + reflection that json.Marshal needs — that
// path dominated ingest allocations under load (profiled: ~7GB / 14% of
// alloc_space at 175k rows/s). Attributes whose values are nested (array/kvlist)
// or non-finite floats fall back to the reflection encoder for exactness.
//
// The fast path preserves the OTLP attribute order and keeps duplicate keys,
// whereas the reflect fallback (a map) sorts keys and keeps last-wins. This is
// immaterial for fanout: OTLP attribute keys are unique by spec, and queries
// read attributes_json by key via attr()/json_extract (order-independent).
func attrsJSON(attrs []*common.KeyValue) []byte {
	if len(attrs) == 0 {
		return nil
	}
	if attrsNeedReflect(attrs) {
		return attrsJSONReflect(attrs)
	}

	buf := attrBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	buf.WriteByte('{')
	n := 0
	for _, kv := range attrs {
		if kv == nil || kv.Key == "" {
			continue
		}
		if n > 0 {
			buf.WriteByte(',')
		}
		appendJSONString(buf, kv.Key)
		buf.WriteByte(':')
		appendScalarJSON(buf, kv.Value)
		n++
	}
	var out []byte
	if n > 0 {
		buf.WriteByte('}')
		out = append([]byte(nil), buf.Bytes()...) // copy out before returning buf to the pool
	}
	attrBufPool.Put(buf)
	return out
}

// attrsNeedReflect reports whether any value is nested (array/kvlist) or a
// non-finite float — the cases the direct encoder doesn't handle.
func attrsNeedReflect(attrs []*common.KeyValue) bool {
	for _, kv := range attrs {
		if kv == nil || kv.Value == nil {
			continue
		}
		switch x := kv.Value.Value.(type) {
		case *common.AnyValue_ArrayValue, *common.AnyValue_KvlistValue:
			return true
		case *common.AnyValue_DoubleValue:
			if math.IsInf(x.DoubleValue, 0) || math.IsNaN(x.DoubleValue) {
				return true
			}
		}
	}
	return false
}

// appendScalarJSON writes a scalar OTLP value as JSON. Callers guarantee no
// nested/non-finite values reach here (see attrsNeedReflect).
func appendScalarJSON(buf *bytes.Buffer, v *common.AnyValue) {
	if v == nil {
		buf.WriteString("null")
		return
	}
	var tmp [32]byte
	switch x := v.Value.(type) {
	case *common.AnyValue_StringValue:
		appendJSONString(buf, x.StringValue)
	case *common.AnyValue_IntValue:
		buf.Write(strconv.AppendInt(tmp[:0], x.IntValue, 10))
	case *common.AnyValue_DoubleValue:
		buf.Write(strconv.AppendFloat(tmp[:0], x.DoubleValue, 'g', -1, 64))
	case *common.AnyValue_BoolValue:
		if x.BoolValue {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case *common.AnyValue_BytesValue:
		appendJSONString(buf, base64.StdEncoding.EncodeToString(x.BytesValue))
	default:
		buf.WriteString("null")
	}
}

// jsonHTMLSafe[b] is true for ASCII bytes that need no escaping — matching
// encoding/json's default htmlSafeSet (escapes " \ control chars and < > &).
var jsonHTMLSafe = func() (s [utf8.RuneSelf]bool) {
	for b := 0; b < utf8.RuneSelf; b++ {
		s[b] = b >= 0x20 && b != '"' && b != '\\' && b != '<' && b != '>' && b != '&'
	}
	return
}()

const jsonHex = "0123456789abcdef"

// appendJSONString writes s as a JSON string, byte-for-byte identical to
// encoding/json.Marshal(s) with the default HTML escaping (verified by test).
func appendJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if jsonHTMLSafe[b] {
				i++
				continue
			}
			if start < i {
				buf.WriteString(s[start:i])
			}
			switch b {
			case '\\', '"':
				buf.WriteByte('\\')
				buf.WriteByte(b)
			case '\n':
				buf.WriteString(`\n`)
			case '\r':
				buf.WriteString(`\r`)
			case '\t':
				buf.WriteString(`\t`)
			case '\b':
				buf.WriteString(`\b`)
			case '\f':
				buf.WriteString(`\f`)
			default:
				buf.WriteString(`\u00`)
				buf.WriteByte(jsonHex[b>>4])
				buf.WriteByte(jsonHex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			if start < i {
				buf.WriteString(s[start:i])
			}
			buf.WriteString(`\ufffd`)
			i += size
			start = i
			continue
		}
		// U+2028/U+2029 are valid JSON but break JS; encoding/json escapes them.
		if c == '\u2028' || c == '\u2029' {
			if start < i {
				buf.WriteString(s[start:i])
			}
			buf.WriteString(`\u202`)
			buf.WriteByte(jsonHex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		buf.WriteString(s[start:])
	}
	buf.WriteByte('"')
}

// attrsJSONReflect is the reflection-based encoder, retained for nested values.
func attrsJSONReflect(attrs []*common.KeyValue) []byte {
	m := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		if kv == nil || kv.Key == "" {
			continue
		}
		m[kv.Key] = attrValue(kv.Value)
	}
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		slog.Error("attrs json marshal failed", "err", err)
		return nil
	}
	return b
}

// resourceAttrsJSON flattens a resource's attributes into the same flat object
// shape as attrsJSON, so attr(resource_json, 'key') resolves.
func resourceAttrsJSON(r *resourcepb.Resource) []byte {
	if r == nil {
		return nil
	}
	return attrsJSON(r.Attributes)
}

// attrValue converts an OTLP AnyValue into a native Go value for JSON encoding,
// preserving scalar types (string/int/double/bool) and recursing into arrays and
// key-value lists.
func attrValue(v *common.AnyValue) any {
	if v == nil {
		return nil
	}
	switch x := v.Value.(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue
	case *common.AnyValue_IntValue:
		return x.IntValue
	case *common.AnyValue_DoubleValue:
		return x.DoubleValue
	case *common.AnyValue_BoolValue:
		return x.BoolValue
	case *common.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue)
	case *common.AnyValue_ArrayValue:
		if x.ArrayValue == nil {
			return nil
		}
		arr := make([]any, 0, len(x.ArrayValue.Values))
		for _, e := range x.ArrayValue.Values {
			arr = append(arr, attrValue(e))
		}
		return arr
	case *common.AnyValue_KvlistValue:
		if x.KvlistValue == nil {
			return nil
		}
		m := make(map[string]any, len(x.KvlistValue.Values))
		for _, e := range x.KvlistValue.Values {
			if e == nil || e.Key == "" {
				continue
			}
			m[e.Key] = attrValue(e.Value)
		}
		return m
	default:
		return nil
	}
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

// extractException finds the first exception event in a span's events list
// and returns (exception.type, exception.message).
func extractException(events []*tracepb.Span_Event) (string, string) {
	for _, e := range events {
		if e == nil || e.Name != "exception" {
			continue
		}
		var excType, excMsg string
		for _, kv := range e.Attributes {
			switch kv.Key {
			case "exception.type":
				excType = anyValueString(kv.Value)
			case "exception.message":
				excMsg = anyValueString(kv.Value)
			}
		}
		if excType != "" || excMsg != "" {
			return excType, excMsg
		}
	}
	return "", ""
}

// anyValueString extracts a string from any AnyValue type (not just StringValue).
func anyValueString(v *common.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue
	case *common.AnyValue_IntValue:
		return fmt.Sprintf("%d", x.IntValue)
	case *common.AnyValue_BoolValue:
		return fmt.Sprintf("%t", x.BoolValue)
	case *common.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", x.DoubleValue)
	default:
		return ""
	}
}

// normalizeSeverity returns uppercase severity text. If text is empty,
// derives it from the OTel severity number (1-24).
func normalizeSeverity(text string, number int32) string {
	s := strings.ToUpper(text)
	if s != "" {
		return s
	}
	switch {
	case number >= 21:
		return "FATAL"
	case number >= 17:
		return "ERROR"
	case number >= 13:
		return "WARN"
	case number >= 9:
		return "INFO"
	case number >= 5:
		return "DEBUG"
	case number >= 1:
		return "TRACE"
	default:
		return ""
	}
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
