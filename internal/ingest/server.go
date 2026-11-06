package ingest

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
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
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

type batchSubmitter interface {
	Admit(context.Context, int) error
	Submit(context.Context, telemetrystore.Batch) error
}

type Server struct {
	cfg       config.Config
	submitter batchSubmitter
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

func NewServer(cfg config.Config, submitter batchSubmitter) *Server {
	return &Server{cfg: cfg, submitter: submitter}
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
	count := 0
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			count += len(ss.Spans)
		}
	}
	ctx = telemetrystore.WithIngestAdmission(ctx)
	if err := s.submitter.Admit(ctx, count); err != nil {
		return nil, ingestStatusError(err)
	}
	batch := telemetrystore.Batch{}
	if count > 0 {
		batch.Spans = make([]telemetry.Span, 0, count)
	}
	for _, rs := range req.ResourceSpans {
		resources, err := resourceAttributes(rs.Resource)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		svc := getServiceName(rs.Resource)
		namespace := getServiceNamespace(rs.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNamespace
		}
		for _, ss := range rs.ScopeSpans {
			scopeName, scopeVer := scopeInfo(ss.Scope)
			for _, sp := range ss.Spans {
				attrs, err := attributes(sp.Attributes)
				if err != nil {
					return nil, status.Error(codes.InvalidArgument, err.Error())
				}
				events, err := spanEvents(sp.Events)
				if err != nil {
					return nil, status.Error(codes.InvalidArgument, err.Error())
				}
				links, err := spanLinks(sp.Links)
				if err != nil {
					return nil, status.Error(codes.InvalidArgument, err.Error())
				}
				row := telemetry.Span{
					Namespace:      namespace,
					TraceID:        hex.EncodeToString(sp.TraceId),
					SpanID:         hex.EncodeToString(sp.SpanId),
					ParentSpanID:   hexOrEmpty(sp.ParentSpanId),
					ServiceName:    svc,
					Name:           sp.Name,
					Kind:           sp.Kind.String(),
					StartUnixNanos: int64(sp.StartTimeUnixNano),
					EndUnixNanos:   int64(sp.EndTimeUnixNano),
					DurationMS:     spanDurationMS(sp.StartTimeUnixNano, sp.EndTimeUnixNano),
					// status is optional in OTLP and most SDKs omit it entirely
					// for an unset status, so these read through generated
					// getters: dereferencing sp.Status panicked the handler and
					// lost the whole export, not just the span.
					StatusCode:   sp.GetStatus().GetCode().String(),
					StatusMsg:    sp.GetStatus().GetMessage(),
					Resource:     resources,
					Attributes:   attrs,
					Events:       events,
					Links:        links,
					TraceState:   sp.TraceState,
					Flags:        sp.Flags,
					ScopeName:    scopeName,
					ScopeVersion: scopeVer,
					RPCService:   spanAttr(sp.Attributes, "rpc.service"),
					PeerService:  spanAttr(sp.Attributes, "peer.service", "server.address"),
				}
				// gRPC and Connect RPC (current and deprecated spellings) fold rpc.service into a qualified rpc.method.
				if _, ok := attrs["rpc.service"]; !ok {
					rpcSystem := spanAttr(sp.Attributes, "rpc.system.name", "rpc.system")
					method := spanAttr(sp.Attributes, "rpc.method")
					if i := strings.LastIndex(method, "/"); i >= 0 && (rpcSystem == "grpc" || rpcSystem == "connectrpc" || rpcSystem == "connect_rpc") {
						row.RPCService = method[:i]
					}
				}
				// Pre-extracted exception info from span events.
				excType, excMsg := extractException(sp.Events)
				row.ExceptionType = excType
				row.ExceptionMessage = excMsg
				batch.Spans = append(batch.Spans, row)
			}
		}
	}
	if err := s.submitter.Submit(ctx, batch); err != nil {
		return nil, ingestStatusError(err)
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

// shedRetryDelay is how long a shed sender is asked to wait before resending.
const shedRetryDelay = time.Second

// ingestStatusError maps a refusal to a status a sender knows how to act on.
// RESOURCE_EXHAUSTED is what the OTLP specification tells an exporter to back
// off and retry on, which is the entire point of shedding rather than dying:
// the data is not lost, it arrives a moment later. Exporters retry it only
// when it carries RetryInfo, so it always does.
func ingestStatusError(err error) error {
	delay := shedRetryDelay
	var throttled *telemetrystore.ErrIngestThrottled
	limited := errors.As(err, &throttled)
	if limited {
		delay = throttled.RetryAfter
	}
	if limited || errors.Is(err, telemetrystore.ErrIngestOverBudget) {
		st, detailErr := status.New(codes.ResourceExhausted, err.Error()).
			WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(delay)})
		if detailErr != nil {
			return status.Error(codes.ResourceExhausted, err.Error())
		}
		return st.Err()
	}
	return err
}

// ---- Logs ingest ----

func (ls *logsService) Export(ctx context.Context, req *collectorlogs.ExportLogsServiceRequest) (*collectorlogs.ExportLogsServiceResponse, error) {
	return ls.srv.exportLogs(ctx, req)
}

func (s *Server) exportLogs(ctx context.Context, req *collectorlogs.ExportLogsServiceRequest) (*collectorlogs.ExportLogsServiceResponse, error) {
	cfg := s.cfg
	count := 0
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			count += len(sl.LogRecords)
		}
	}
	ctx = telemetrystore.WithIngestAdmission(ctx)
	if err := s.submitter.Admit(ctx, count); err != nil {
		return nil, ingestStatusError(err)
	}
	batch := telemetrystore.Batch{}
	if count > 0 {
		batch.Logs = make([]telemetry.Log, 0, count)
	}
	for _, rl := range req.ResourceLogs {
		resources, err := resourceAttributes(rl.Resource)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		svc := getServiceName(rl.Resource)
		namespace := getServiceNamespace(rl.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNamespace
		}
		for _, sl := range rl.ScopeLogs {
			scopeName, scopeVer := scopeInfo(sl.Scope)
			for _, lr := range sl.LogRecords {
				attrs, err := attributes(lr.Attributes)
				if err != nil {
					return nil, status.Error(codes.InvalidArgument, err.Error())
				}
				body := bodyString(lr.Body)
				tmpl := templates.template(body)
				row := telemetry.Log{
					Namespace:         namespace,
					TimeUnixNanos:     int64(lr.TimeUnixNano),
					ObservedTimeNanos: int64(lr.ObservedTimeUnixNano),
					Severity:          telemetry.NormalizeSeverity(lr.SeverityText, int32(lr.SeverityNumber)),
					SeverityNumber:    int32(lr.SeverityNumber),
					Body:              body,
					BodyTemplate:      tmpl,
					ServiceName:       svc,
					TraceID:           hexOrEmpty(lr.TraceId),
					SpanID:            hexOrEmpty(lr.SpanId),
					Flags:             lr.Flags,
					Resource:          resources,
					Attributes:        attrs,
					ScopeName:         scopeName,
					ScopeVersion:      scopeVer,
				}
				batch.Logs = append(batch.Logs, row)
			}
		}
	}
	if err := s.submitter.Submit(ctx, batch); err != nil {
		return nil, ingestStatusError(err)
	}
	return &collectorlogs.ExportLogsServiceResponse{}, nil
}

// ---- Metrics ingest ----

func (ms *metricsService) Export(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	return ms.srv.exportMetrics(ctx, req)
}

func (s *Server) exportMetrics(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	cfg := s.cfg
	count := 0
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					count += len(d.Gauge.DataPoints)
				case *metricspb.Metric_Sum:
					count += len(d.Sum.DataPoints)
				case *metricspb.Metric_Histogram:
					count += len(d.Histogram.DataPoints)
				case *metricspb.Metric_ExponentialHistogram:
					count += len(d.ExponentialHistogram.DataPoints)
				case *metricspb.Metric_Summary:
					count += len(d.Summary.DataPoints)
				}
			}
		}
	}
	ctx = telemetrystore.WithIngestAdmission(ctx)
	if err := s.submitter.Admit(ctx, count); err != nil {
		return nil, ingestStatusError(err)
	}
	batch := telemetrystore.Batch{}
	if count > 0 {
		batch.Metrics = make([]telemetry.Metric, 0, count)
	}
	for _, rm := range req.ResourceMetrics {
		resources, err := resourceAttributes(rm.Resource)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		svc := getServiceName(rm.Resource)
		namespace := getServiceNamespace(rm.Resource)
		if namespace == "" {
			namespace = cfg.DefaultNamespace
		}
		for _, sm := range rm.ScopeMetrics {
			scopeName, scopeVer := scopeInfo(sm.Scope)
			for _, m := range sm.Metrics {
				base := telemetry.Metric{
					Namespace: namespace, Name: m.Name, Description: m.Description, Unit: m.Unit,
					ServiceName: svc, Resource: resources, ScopeName: scopeName, ScopeVersion: scopeVer,
				}
				if batch.Metrics, err = appendMetricPoints(batch.Metrics, base, m); err != nil {
					return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("metric %q: %v", m.Name, err))
				}
			}
		}
	}
	if err := s.submitter.Submit(ctx, batch); err != nil {
		return nil, ingestStatusError(err)
	}
	return &collectormetrics.ExportMetricsServiceResponse{}, nil
}

// appendMetricPoints converts one OTLP metric's points. A malformed point
// rejects the export: the quantile path relies on what is checked here.
func appendMetricPoints(out []telemetry.Metric, base telemetry.Metric, m *metricspb.Metric) ([]telemetry.Metric, error) {
	point := func(attrs []*common.KeyValue, start, at uint64, flags uint32) (telemetry.Metric, error) {
		row := base
		var err error
		row.Attributes, err = attributes(attrs)
		row.StartTimeUnixNanos, row.TimeUnixNanos, row.Flags = int64(start), int64(at), flags
		return row, err
	}
	finish := func(row telemetry.Metric) {
		row.SeriesID = telemetry.MetricSeriesID(row)
		out = append(out, row)
	}
	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range d.Gauge.DataPoints {
			row, err := point(dp.Attributes, dp.StartTimeUnixNano, dp.TimeUnixNano, dp.Flags)
			if err != nil {
				return out, err
			}
			row.Type = "gauge"
			if row.Value, err = number(dp); err != nil {
				return out, err
			}
			if row.Exemplars, err = exemplars(dp.Exemplars); err != nil {
				return out, err
			}
			finish(row)
		}
	case *metricspb.Metric_Sum:
		temporality, err := metricTemporality(d.Sum.AggregationTemporality)
		if err != nil {
			return out, err
		}
		kind := "updown"
		if d.Sum.IsMonotonic {
			kind = "counter"
		}
		for _, dp := range d.Sum.DataPoints {
			row, err := point(dp.Attributes, dp.StartTimeUnixNano, dp.TimeUnixNano, dp.Flags)
			if err != nil {
				return out, err
			}
			row.Type, row.Temporality = kind, temporality
			if row.Value, err = number(dp); err != nil {
				return out, err
			}
			if row.Exemplars, err = exemplars(dp.Exemplars); err != nil {
				return out, err
			}
			finish(row)
		}
	case *metricspb.Metric_Histogram:
		temporality, err := metricTemporality(d.Histogram.AggregationTemporality)
		if err != nil {
			return out, err
		}
		for _, dp := range d.Histogram.DataPoints {
			if err := checkBuckets(dp.ExplicitBounds, dp.BucketCounts); err != nil {
				return out, err
			}
			row, err := point(dp.Attributes, dp.StartTimeUnixNano, dp.TimeUnixNano, dp.Flags)
			if err != nil {
				return out, err
			}
			row.Type, row.Temporality = "histogram", temporality
			row.Histogram = telemetry.Histogram{
				Count: dp.Count, Sum: dp.GetSum(), HasSum: dp.Sum != nil, Min: dp.GetMin(), HasMin: dp.Min != nil,
				Max: dp.GetMax(), HasMax: dp.Max != nil, Bounds: nonNil(dp.ExplicitBounds), Counts: nonNil(dp.BucketCounts),
			}
			if row.Exemplars, err = exemplars(dp.Exemplars); err != nil {
				return out, err
			}
			finish(row)
		}
	case *metricspb.Metric_ExponentialHistogram:
		temporality, err := metricTemporality(d.ExponentialHistogram.AggregationTemporality)
		if err != nil {
			return out, err
		}
		for _, dp := range d.ExponentialHistogram.DataPoints {
			if math.IsNaN(dp.ZeroThreshold) || math.IsInf(dp.ZeroThreshold, 0) || dp.ZeroThreshold < 0 {
				return out, errors.New("exponential histogram zero_threshold must be a finite, non-negative number")
			}
			row, err := point(dp.Attributes, dp.StartTimeUnixNano, dp.TimeUnixNano, dp.Flags)
			if err != nil {
				return out, err
			}
			row.Type, row.Temporality = "histogram", temporality
			row.Histogram = telemetry.Histogram{
				Count: dp.Count, Sum: dp.GetSum(), HasSum: dp.Sum != nil, Min: dp.GetMin(), HasMin: dp.Min != nil,
				Max: dp.GetMax(), HasMax: dp.Max != nil, Exponential: true, Scale: dp.Scale,
				ZeroCount: dp.ZeroCount, ZeroThreshold: dp.ZeroThreshold,
				PositiveOffset: dp.GetPositive().GetOffset(), PositiveCounts: nonNil(dp.GetPositive().GetBucketCounts()),
				NegativeOffset: dp.GetNegative().GetOffset(), NegativeCounts: nonNil(dp.GetNegative().GetBucketCounts()),
			}
			if row.Exemplars, err = exemplars(dp.Exemplars); err != nil {
				return out, err
			}
			finish(row)
		}
	case *metricspb.Metric_Summary:
		for _, dp := range d.Summary.DataPoints {
			row, err := point(dp.Attributes, dp.StartTimeUnixNano, dp.TimeUnixNano, dp.Flags)
			if err != nil {
				return out, err
			}
			row.Type = "summary"
			row.Histogram = telemetry.Histogram{Count: dp.Count, Sum: dp.Sum, HasSum: true,
				Quantiles: make([]float64, len(dp.QuantileValues)), QuantileValues: make([]float64, len(dp.QuantileValues))}
			for i, q := range dp.QuantileValues {
				row.Quantiles[i], row.QuantileValues[i] = q.GetQuantile(), q.GetValue()
			}
			finish(row)
		}
	}
	return out, nil
}

// checkBuckets rejects explicit histogram buckets that no quantile can read:
// bounds must be finite and strictly increasing, with one more count than
// bounds, or no buckets at all.
func checkBuckets(bounds []float64, counts []uint64) error {
	if len(counts) != len(bounds)+1 && (len(counts) > 0 || len(bounds) > 0) {
		return fmt.Errorf("histogram has %d bucket counts for %d explicit bounds", len(counts), len(bounds))
	}
	for i, b := range bounds {
		if math.IsNaN(b) || math.IsInf(b, 0) {
			return errors.New("histogram explicit bounds must be finite")
		}
		if i > 0 && b <= bounds[i-1] {
			return errors.New("histogram explicit bounds must be strictly increasing")
		}
	}
	return nil
}

// nonNil keeps a present but empty list empty rather than absent.
func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func exemplars(in []*metricspb.Exemplar) ([]telemetry.Exemplar, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]telemetry.Exemplar, len(in))
	for i, e := range in {
		attrs, err := attributes(e.FilteredAttributes)
		if err != nil {
			return nil, fmt.Errorf("exemplar %w", err)
		}
		value := e.GetAsDouble()
		if v, ok := e.Value.(*metricspb.Exemplar_AsInt); ok {
			value = float64(v.AsInt)
		}
		out[i] = telemetry.Exemplar{TimeUnixNanos: int64(e.TimeUnixNano), Value: value,
			TraceID: hexOrEmpty(e.TraceId), SpanID: hexOrEmpty(e.SpanId), Attributes: attrs}
	}
	return out, nil
}

func spanEvents(in []*tracepb.Span_Event) ([]telemetry.Event, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]telemetry.Event, len(in))
	for i, e := range in {
		attrs, err := attributes(e.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("event %w", err)
		}
		out[i] = telemetry.Event{TimeUnixNanos: int64(e.GetTimeUnixNano()), Name: e.GetName(), Attributes: attrs}
	}
	return out, nil
}

func spanLinks(in []*tracepb.Span_Link) ([]telemetry.Link, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]telemetry.Link, len(in))
	for i, l := range in {
		attrs, err := attributes(l.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("link %w", err)
		}
		out[i] = telemetry.Link{TraceID: hexOrEmpty(l.GetTraceId()), SpanID: hexOrEmpty(l.GetSpanId()), TraceState: l.GetTraceState(), Attributes: attrs}
	}
	return out, nil
}

func metricTemporality(t metricspb.AggregationTemporality) (string, error) {
	switch t {
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE:
		return "cumulative", nil
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA:
		return "delta", nil
	default:
		return "", fmt.Errorf("sum and histogram metrics require cumulative or delta temporality")
	}
}

// ---- helpers ----

// spanDurationMS computes a span's duration in milliseconds, guarding against
// the unsigned underflow that a malformed span (end before start) would produce:
// uint64 subtraction wraps to a huge positive value, which would otherwise be
// stored as a multi-century duration. Such spans are clamped to 0.
func spanDurationMS(startNano, endNano uint64) float64 {
	if endNano < startNano {
		return 0
	}
	return float64(endNano-startNano) / 1e6
}

// maxAttributeNesting bounds native VARIANT decoding recursion on macOS stacks.
// Reject the whole export; never silently change or drop an attribute type.
const maxAttributeNesting = 128

// attributes owns typed OTel values; resource maps are shared within one request.
func attributes(attrs []*common.KeyValue) (map[string]any, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	values := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		if kv != nil && kv.Key != "" {
			value, err := attrValue(kv.Value, 1)
			if err != nil {
				return nil, fmt.Errorf("attribute %q: %w", kv.Key, err)
			}
			values[kv.Key] = value
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return values, nil
}

func resourceAttributes(resource *resourcepb.Resource) (map[string]any, error) {
	if resource == nil {
		return nil, nil
	}
	return attributes(resource.Attributes)
}

// attrValue preserves OTel types without an intermediate JSON encoding.
func attrValue(v *common.AnyValue, depth int) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.Value.(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue, nil
	case *common.AnyValue_IntValue:
		return x.IntValue, nil
	case *common.AnyValue_DoubleValue:
		return x.DoubleValue, nil
	case *common.AnyValue_BoolValue:
		return x.BoolValue, nil
	case *common.AnyValue_BytesValue:
		return bytes.Clone(x.BytesValue), nil
	case *common.AnyValue_ArrayValue:
		if depth > maxAttributeNesting {
			return nil, fmt.Errorf("nesting exceeds %d containers", maxAttributeNesting)
		}
		if x.ArrayValue == nil {
			return nil, nil
		}
		arr := make([]any, 0, len(x.ArrayValue.Values))
		for _, e := range x.ArrayValue.Values {
			child, err := attrValue(e, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, child)
		}
		return arr, nil
	case *common.AnyValue_KvlistValue:
		if depth > maxAttributeNesting {
			return nil, fmt.Errorf("nesting exceeds %d containers", maxAttributeNesting)
		}
		if x.KvlistValue == nil {
			return nil, nil
		}
		m := make(map[string]any, len(x.KvlistValue.Values))
		for _, e := range x.KvlistValue.Values {
			if e == nil || e.Key == "" {
				continue
			}
			child, err := attrValue(e.Value, depth+1)
			if err != nil {
				return nil, err
			}
			m[e.Key] = child
		}
		return m, nil
	default:
		return nil, nil
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

func getServiceName(r *resourcepb.Resource) string {
	return getResourceAttr(r, "service.name")
}

func getServiceNamespace(r *resourcepb.Resource) string {
	return getResourceAttr(r, "service.namespace")
}

func getResourceAttr(r *resourcepb.Resource, keys ...string) string {
	if r == nil {
		return ""
	}
	for _, key := range keys {
		for _, kv := range r.Attributes {
			if kv.Key == key {
				if s := asString(kv.Value); s != "" {
					return s
				}
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

// noRecordedValue is OTLP's DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK.
const noRecordedValue = uint32(metricspb.DataPointFlags_DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK)

// number returns a gauge or sum point's value. metrics.proto makes a point
// without a value invalid unless it is flagged as having no recorded value;
// such a point's value field is ignored and the flag is kept with the row.
func number(dp *metricspb.NumberDataPoint) (float64, error) {
	if dp.Flags&noRecordedValue != 0 {
		return 0, nil
	}
	switch x := dp.Value.(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		return x.AsDouble, nil
	case *metricspb.NumberDataPoint_AsInt:
		return float64(x.AsInt), nil
	default:
		return 0, errors.New("number data point has no value and is not flagged as having no recorded value")
	}
}

func hexOrEmpty(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

func scopeInfo(scope *common.InstrumentationScope) (name, version string) {
	if scope == nil {
		return "", ""
	}
	return scope.Name, scope.Version
}
