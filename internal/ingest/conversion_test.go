package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/labstack/fanout/internal/config"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

type conversionSubmitter func(context.Context, telemetrystore.Batch) error

func (f conversionSubmitter) Submit(ctx context.Context, batch telemetrystore.Batch) error {
	return f(ctx, batch)
}

func conversionExports(n int) (*collectortrace.ExportTraceServiceRequest, *collectorlogs.ExportLogsServiceRequest, *collectormetrics.ExportMetricsServiceRequest) {
	traces := &collectortrace.ExportTraceServiceRequest{}
	logs := &collectorlogs.ExportLogsServiceRequest{}
	metrics := &collectormetrics.ExportMetricsServiceRequest{}
	text := func(k, v string) *common.KeyValue {
		return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
	}
	attrs := []*common.KeyValue{
		text("http.method", "POST"),
		text("http.request.method", "GET"),
		text("http.route", "/orders"),
		{Key: "http.status_code", Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: 503}}},
		{Key: "flag", Value: &common.AnyValue{Value: &common.AnyValue_BoolValue{BoolValue: true}}},
		{Key: "bytes", Value: &common.AnyValue{Value: &common.AnyValue_BytesValue{BytesValue: []byte{0, 255}}}},
	}
	for r := 0; r < 2; r++ {
		resource := &resourcepb.Resource{Attributes: []*common.KeyValue{text("service.name", []string{"api", "worker"}[r]), text("service.version", []string{"v1", "v2"}[r]), text("deployment.environment", []string{"prod", "test"}[r]), {Key: "nested", Value: &common.AnyValue{Value: &common.AnyValue_ArrayValue{ArrayValue: &common.ArrayValue{Values: []*common.AnyValue{{Value: &common.AnyValue_IntValue{IntValue: 7}}, {Value: &common.AnyValue_BoolValue{BoolValue: false}}}}}}}}}
		if r == 0 {
			resource.Attributes = append(resource.Attributes, text("service.namespace", "shop"))
		}
		rs := &tracepb.ResourceSpans{Resource: resource}
		rl := &logspb.ResourceLogs{Resource: resource}
		rm := &metricspb.ResourceMetrics{Resource: resource}
		for scope := 0; scope < 2; scope++ {
			info := &common.InstrumentationScope{Name: []string{"http", "jobs"}[scope], Version: []string{"1.0", "2.0"}[scope]}
			ss := &tracepb.ScopeSpans{Scope: info}
			sl := &logspb.ScopeLogs{Scope: info}
			sm := &metricspb.ScopeMetrics{Scope: info}
			for i := 0; i < n; i++ {
				ss.Spans = append(ss.Spans, &tracepb.Span{
					TraceId: []byte{1, 2, byte(r)}, SpanId: []byte{3, byte(scope), byte(i)}, ParentSpanId: []byte{4},
					Name: "operation", Kind: tracepb.Span_SPAN_KIND_CLIENT,
					StartTimeUnixNano: 1790856000000000017, EndTimeUnixNano: 1790856000001000019,
					Status:     &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "failed"},
					Attributes: attrs, TraceState: "vendor=state", Flags: 257,
					Events: []*tracepb.Span_Event{{
						Name: "exception", TimeUnixNano: 1790856000000000020,
						Attributes: []*common.KeyValue{text("exception.type", "Timeout"), text("exception.message", "late")},
					}},
					Links: []*tracepb.Span_Link{{
						TraceId: []byte{5}, SpanId: []byte{6}, TraceState: "a=b",
						Attributes: []*common.KeyValue{text("link", "value")},
					}},
				})
				sl.LogRecords = append(sl.LogRecords, &logspb.LogRecord{TimeUnixNano: 1790856000000000017, ObservedTimeUnixNano: 1790856000000000019, SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, Body: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "order 123 failed"}}, TraceId: []byte{1, 2}, SpanId: []byte{3, 4}, Flags: 1, Attributes: attrs})
			}
			point := &metricspb.NumberDataPoint{TimeUnixNano: 1790856000000000017, StartTimeUnixNano: 1790855940000000009, Value: &metricspb.NumberDataPoint_AsInt{AsInt: 12}, Attributes: attrs}
			points := make([]*metricspb.NumberDataPoint, n)
			for i := range points {
				points[i] = point
			}
			switch r*2 + scope {
			case 0:
				sm.Metrics = []*metricspb.Metric{{Name: "gauge", Description: "description", Unit: "ms", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}}}, {Name: "sum", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{IsMonotonic: true, AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, DataPoints: points}}}}
			case 1:
				sm.Metrics = []*metricspb.Metric{{Name: "histogram", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: []*metricspb.HistogramDataPoint{{TimeUnixNano: 1790856000000000017, Count: 2, ExplicitBounds: []float64{1}, BucketCounts: []uint64{1, 1}, Attributes: attrs}}}}}}
			case 2:
				sm.Metrics = []*metricspb.Metric{{Name: "exponential", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, DataPoints: []*metricspb.ExponentialHistogramDataPoint{{TimeUnixNano: 1790856000000000017, Count: 1, Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: []uint64{1}}, Attributes: attrs}}}}}}
			case 3:
				sm.Metrics = []*metricspb.Metric{{Name: "summary", Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: []*metricspb.SummaryDataPoint{{TimeUnixNano: 1790856000000000017, Count: 1, Sum: 3, QuantileValues: []*metricspb.SummaryDataPoint_ValueAtQuantile{{Quantile: 0.5, Value: 3}}, Attributes: attrs}}}}}}
			}
			rs.ScopeSpans = append(rs.ScopeSpans, ss)
			rl.ScopeLogs = append(rl.ScopeLogs, sl)
			rm.ScopeMetrics = append(rm.ScopeMetrics, sm)
		}
		traces.ResourceSpans = append(traces.ResourceSpans, rs)
		logs.ResourceLogs = append(logs.ResourceLogs, rl)
		metrics.ResourceMetrics = append(metrics.ResourceMetrics, rm)
	}
	return traces, logs, metrics
}

func TestExportConversionRowsMatchBaseline(t *testing.T) {
	traces, logs, metrics := conversionExports(1)
	var got telemetrystore.Batch
	srv := NewServer(config.Config{DefaultNamespace: "fallback"}, conversionSubmitter(func(_ context.Context, b telemetrystore.Batch) error {
		got.Spans = append(got.Spans, b.Spans...)
		got.Logs = append(got.Logs, b.Logs...)
		got.Metrics = append(got.Metrics, b.Metrics...)
		return nil
	}))
	if _, err := srv.exportTraces(t.Context(), traces); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.exportLogs(t.Context(), logs); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.exportMetrics(t.Context(), metrics); err != nil {
		t.Fatal(err)
	}
	for i := range got.Spans {
		got.Spans[i].IngestedAt = 0
	}
	for i := range got.Logs {
		got.Logs[i].IngestedAt = 0
	}
	for i := range got.Metrics {
		got.Metrics[i].IngestedAt = 0
	}
	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	path := "testdata/conversion_rows.json"

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("converted rows changed:\n%s", encoded)
	}
}
func TestTraceSemanticConventionPriority(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		current, older                        bool
		method, status, database, environment string
	}{
		{"current wins", true, true, "GET", "200", "postgresql", "production"},
		{"current only", true, false, "GET", "200", "postgresql", "production"},
		{"external older names", false, true, "POST", "503", "mysql", "staging"},
		{"absent", false, false, "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := func(k, v string) *common.KeyValue {
				return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
			}
			integer := func(k string, v int64) *common.KeyValue {
				return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: v}}}
			}
			attrs := []*common.KeyValue{text("rpc.method", "GetOrder"), text("rpc.service", "Orders")}
			resource := &resourcepb.Resource{}
			// Put the older names first to verify key priority, independent of wire order.
			if tc.older {
				attrs = append(attrs, text("http.method", "POST"), integer("http.status_code", 503), text("db.system", "mysql"))
				resource.Attributes = append(resource.Attributes, text("deployment.environment", "staging"))
			}
			if tc.current {
				attrs = append(attrs, text("http.request.method", "GET"), integer("http.response.status_code", 200), text("db.system.name", "postgresql"))
				resource.Attributes = append(resource.Attributes, text("deployment.environment.name", "production"))
			}
			var got telemetrystore.Batch
			srv := NewServer(config.Config{}, conversionSubmitter(func(_ context.Context, b telemetrystore.Batch) error { got = b; return nil }))
			req := &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: resource, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Attributes: attrs, Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}}}}}}}}
			if _, err := srv.exportTraces(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			span := got.Spans[0]
			if span.HTTPMethod != tc.method || span.HTTPStatusCode != tc.status || span.DBSystem != tc.database || span.DeploymentEnv != tc.environment {
				t.Fatalf("extracted method=%q status=%q database=%q environment=%q", span.HTTPMethod, span.HTTPStatusCode, span.DBSystem, span.DeploymentEnv)
			}
			if span.RPCMethod != "GetOrder" || span.RPCService != "Orders" || span.StatusCode != "STATUS_CODE_ERROR" {
				t.Fatalf("RPC or status changed: %+v", span)
			}
		})
	}
}

func BenchmarkExportConversion(b *testing.B) {
	traces, logs, metrics := conversionExports(250)
	srv := NewServer(config.Config{DefaultNamespace: "fallback"}, conversionSubmitter(func(context.Context, telemetrystore.Batch) error { return nil }))
	for _, name := range []string{"spans", "logs", "metrics"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				switch name {
				case "spans":
					_, err = srv.exportTraces(b.Context(), traces)
				case "logs":
					_, err = srv.exportLogs(b.Context(), logs)
				case "metrics":
					_, err = srv.exportMetrics(b.Context(), metrics)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
