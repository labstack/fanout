package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/labstack/fanout/internal/config"
	_ "github.com/labstack/fanout/internal/duckdb"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type nestingSubmitter func(context.Context, telemetrystore.Batch) error

func (f nestingSubmitter) Submit(ctx context.Context, batch telemetrystore.Batch) error {
	return f(ctx, batch)
}

func nestedAttribute(depth int, object bool) *common.AnyValue {
	v := kvInt("", 7).Value
	for range depth {
		if object {
			v = &common.AnyValue{Value: &common.AnyValue_KvlistValue{KvlistValue: &common.KeyValueList{Values: []*common.KeyValue{{Key: "child", Value: v}}}}}
		} else {
			v = &common.AnyValue{Value: &common.AnyValue_ArrayValue{ArrayValue: &common.ArrayValue{Values: []*common.AnyValue{v}}}}
		}
	}
	return v
}

// Decode actual OTLP wire payloads at depths accepted by the default protobuf
// decoder, then exercise every canonical attributes/resource ingest path.
func TestExportsBoundAttributeNestingBeforeSubmit(t *testing.T) {
	for _, object := range []bool{false, true} {
		deep := 4800
		if object {
			deep = 3000
		}
		for _, depth := range []int{maxAttributeNesting, maxAttributeNesting + 1, deep} {
			for _, resource := range []bool{false, true} {
				attrs := []*common.KeyValue{{Key: "nested", Value: nestedAttribute(depth, object)}}
				r := &resourcepb.Resource{}
				if resource {
					r.Attributes, attrs = attrs, nil
				}
				requests := []proto.Message{
					&collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: r, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{}, {Attributes: attrs}}}}}}},
					&collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{Resource: r, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{}, {Attributes: attrs}}}}}}},
				}
				for _, m := range []*metricspb.Metric{
					{Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{}, {Attributes: attrs}}}}},
					{Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{DataPoints: []*metricspb.NumberDataPoint{{}, {Attributes: attrs}}}}},
					{Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{DataPoints: []*metricspb.HistogramDataPoint{{}, {Attributes: attrs}}}}},
					{Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{DataPoints: []*metricspb.ExponentialHistogramDataPoint{{}, {Attributes: attrs}}}}},
					{Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: []*metricspb.SummaryDataPoint{{}, {Attributes: attrs}}}}},
				} {
					requests = append(requests, &collectormetrics.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{Resource: r, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{m}}}}}})
				}
				for i, request := range requests {
					t.Run(fmt.Sprintf("object=%t/depth=%d/resource=%t/signal=%d", object, depth, resource, i), func(t *testing.T) {
						wire, err := proto.Marshal(request)
						if err != nil {
							t.Fatal(err)
						}
						decoded := request.ProtoReflect().Type().New().Interface()
						if err := proto.Unmarshal(wire, decoded); err != nil {
							t.Fatalf("fixture must decode with default OTLP options: %v", err)
						}
						submitted := false
						srv := NewServer(config.Config{}, nestingSubmitter(func(_ context.Context, _ telemetrystore.Batch) error { submitted = true; return nil }))
						switch req := decoded.(type) {
						case *collectortrace.ExportTraceServiceRequest:
							_, err = srv.exportTraces(context.Background(), req)
						case *collectorlogs.ExportLogsServiceRequest:
							_, err = srv.exportLogs(context.Background(), req)
						case *collectormetrics.ExportMetricsServiceRequest:
							_, err = srv.exportMetrics(context.Background(), req)
						}
						if depth <= maxAttributeNesting {
							if err != nil || !submitted {
								t.Fatalf("valid nesting rejected: %v", err)
							}
						} else if status.Code(err) != codes.InvalidArgument || submitted {
							t.Fatalf("invalid nesting: error=%v submitted=%t", err, submitted)
						}
					})
				}
			}
		}
	}
}

func TestMaximumAttributeNestingRoundTripsNativeVariant(t *testing.T) {
	for _, object := range []bool{false, true} {
		t.Run(fmt.Sprintf("object=%t", object), func(t *testing.T) {
			attrs, err := attributes([]*common.KeyValue{{Key: "nested", Value: nestedAttribute(maxAttributeNesting, object)}})
			if err != nil {
				t.Fatal(err)
			}
			repo, err := telemetrystore.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			srv := NewServer(config.Config{}, nestingSubmitter(func(ctx context.Context, batch telemetrystore.Batch) error {
				batch.ID = "nested"
				return repo.Commit(ctx, batch)
			}))
			raw := []*common.KeyValue{{Key: "nested", Value: nestedAttribute(maxAttributeNesting, object)}}
			req := &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: &resourcepb.Resource{Attributes: raw}, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Attributes: raw}}}}}}}
			if _, err := srv.exportTraces(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("duckdb", "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var attributeJSON, resourceJSON string
			if err := db.QueryRow("SELECT to_json(attributes)::VARCHAR, to_json(resource)::VARCHAR FROM read_parquet(?)", filepath.Join(repo.Parquet.BatchPath("nested"), "spans.parquet")).Scan(&attributeJSON, &resourceJSON); err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(attrs)
			if err != nil {
				t.Fatal(err)
			}
			for _, got := range []string{attributeJSON, resourceJSON} {
				var actual, expected any
				if err := json.Unmarshal([]byte(got), &actual); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(want, &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Fatal("nested VARIANT types or values changed")
				}
			}
		})
	}
}

func TestHTTPRejectsExcessiveAttributeNestingWithoutRetry(t *testing.T) {
	for _, resource := range []bool{false, true} {
		attrs := []*common.KeyValue{{Key: "nested", Value: nestedAttribute(4800, false)}}
		r := &resourcepb.Resource{}
		if resource {
			r.Attributes, attrs = attrs, nil
		}
		for _, test := range []struct {
			path    string
			request proto.Message
		}{
			{"/v1/traces", &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: r, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{}, {Attributes: attrs}}}}}}}},
			{"/v1/logs", &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{Resource: r, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{}, {Attributes: attrs}}}}}}}},
			{"/v1/metrics", &collectormetrics.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{Resource: r, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{}, {Attributes: attrs}}}}}}}}}}}},
		} {
			t.Run(fmt.Sprintf("resource=%t/%s", resource, test.path), func(t *testing.T) {
				fixture := newHTTPIngestFixture(t, true)
				assertOTLPHTTPStatus(t, fixture.handler, http.MethodPost, test.path, test.request, fixture.token, http.StatusBadRequest)
				if len(fixture.spans)+len(fixture.logs)+len(fixture.metrics) != 0 {
					t.Fatal("invalid export partially submitted")
				}
			})
		}
	}
}
