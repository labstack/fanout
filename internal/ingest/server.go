package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
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
					IngestedAt:     now,
				}
				ts.srv.outSpans <- row
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
			for _, lr := range sl.LogRecords {
				row := lake.LogRow{
					TenantID:       cfg.TenantID.String(),
					Namespace:      namespace,
					TimeUnixNanos:  int64(lr.TimeUnixNano),
					Severity:       lr.SeverityText,
					Body:           bodyString(lr.Body),
					ServiceName:    svc,
					TraceID:        hexOrEmpty(lr.TraceId),
					SpanID:         hexOrEmpty(lr.SpanId),
					ResourceJSON:   resourceJSON,
					AttributesJSON: toJSON(lr.Attributes),
					IngestedAt:     now,
				}
				ls.srv.outLogs <- row
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
			for _, m := range sm.Metrics {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					for _, dp := range d.Gauge.DataPoints {
						row := lake.MetricRow{
							TenantID:       cfg.TenantID.String(),
							Namespace:      namespace,
							TimeUnixNanos:  int64(dp.TimeUnixNano),
							Name:           m.Name,
							MType:          "gauge",
							ServiceName:    svc,
							Value:          number(dp.Value),
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							IngestedAt:     now,
						}
						ms.srv.outMetrics <- row
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
							MType:          kind,
							ServiceName:    svc,
							Value:          number(dp.Value),
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							IngestedAt:     now,
						}
						ms.srv.outMetrics <- row
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
							MType:          "histogram",
							ServiceName:    svc,
							HistBoundsJSON: toJSON(dp.ExplicitBounds),
							HistCountsJSON: toJSON(dp.BucketCounts),
							AttributesJSON: toJSON(dp.Attributes),
							ResourceJSON:   resourceJSON,
							IngestedAt:     now,
							HistCount:      int64(dp.Count),
							HistSum:        histSum,
						}
						ms.srv.outMetrics <- row
					}
				default:
					// ignore other types in v1.0
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
	b, _ := json.Marshal(v)
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
		b, _ := json.Marshal(v)
		return string(b)
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
