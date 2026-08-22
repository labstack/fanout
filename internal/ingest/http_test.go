package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/lake"
	"github.com/labstack/fanout/internal/settings"
)

type httpIngestFixture struct {
	handler http.Handler
	token   string
	store   *settings.Store
	spans   chan lake.SpanRow
	logs    chan lake.LogRow
	metrics chan lake.MetricRow
}

func newHTTPIngestFixture(t *testing.T, configured bool) *httpIngestFixture {
	t.Helper()
	store := newRuntimeStore(t)
	token := ""
	if configured {
		var hash string
		var err error
		token, hash, err = settings.GenerateIngestToken()
		if err != nil {
			t.Fatalf("GenerateIngestToken: %v", err)
		}
		if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
			t.Fatalf("SetIngest: %v", err)
		}
	}
	spans := make(chan lake.SpanRow, 8)
	logs := make(chan lake.LogRow, 8)
	metrics := make(chan lake.MetricRow, 8)
	srv := NewServer(config.Config{DefaultNamespace: "default"}, spans, logs, metrics)
	return &httpIngestFixture{
		handler: NewHTTPHandler(srv, store),
		token:   token,
		store:   store,
		spans:   spans,
		logs:    logs,
		metrics: metrics,
	}
}

func TestHTTPIngestAcceptsAllStableSignals(t *testing.T) {
	f := newHTTPIngestFixture(t, true)

	traceReq := testTraceRequest()
	assertOTLPHTTPSuccess(t, f.handler, "/v1/traces", traceReq, f.token, &collectortrace.ExportTraceServiceResponse{})
	if row := <-f.spans; row.ServiceName != "checkout" || row.Name != "GET /cart" || row.Namespace != "default" {
		t.Fatalf("trace row = %+v", row)
	}

	logsReq := testLogsRequest()
	assertOTLPHTTPSuccess(t, f.handler, "/v1/logs", logsReq, f.token, &collectorlogs.ExportLogsServiceResponse{})
	if row := <-f.logs; row.ServiceName != "checkout" || row.Body != "cart failed" || row.Namespace != "default" {
		t.Fatalf("log row = %+v", row)
	}

	metricsReq := testMetricsRequest()
	assertOTLPHTTPSuccess(t, f.handler, "/v1/metrics", metricsReq, f.token, &collectormetrics.ExportMetricsServiceResponse{})
	if row := <-f.metrics; row.ServiceName != "checkout" || row.Name != "cart.items" || row.Value != 3 || row.Namespace != "default" {
		t.Fatalf("metric row = %+v", row)
	}
}

func TestHTTPIngestAcceptsGzipAndBearer(t *testing.T) {
	f := newHTTPIngestFixture(t, true)
	for _, tc := range []struct {
		name    string
		path    string
		request proto.Message
		drain   func()
	}{
		{name: "traces", path: "/v1/traces", request: testTraceRequest(), drain: func() { <-f.spans }},
		{name: "logs", path: "/v1/logs", request: testLogsRequest(), drain: func() { <-f.logs }},
		{name: "metrics", path: "/v1/metrics", request: testMetricsRequest(), drain: func() { <-f.metrics }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := proto.Marshal(tc.request)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, tc.path, compressedBody(t, payload))
			req.Header.Set("Content-Type", otlpProtobufContentType)
			req.Header.Set("Content-Encoding", "gzip")
			req.Header.Set("Authorization", "bearer "+f.token)
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			tc.drain()
		})
	}
}

func TestHTTPIngestAuthentication(t *testing.T) {
	t.Run("pre-setup", func(t *testing.T) {
		f := newHTTPIngestFixture(t, false)
		assertOTLPHTTPStatus(t, f.handler, http.MethodPost, "/v1/traces", testTraceRequest(), "", http.StatusUnauthorized)
	})
	t.Run("missing token", func(t *testing.T) {
		f := newHTTPIngestFixture(t, true)
		assertOTLPHTTPStatus(t, f.handler, http.MethodPost, "/v1/traces", testTraceRequest(), "", http.StatusUnauthorized)
	})
	t.Run("wrong token", func(t *testing.T) {
		f := newHTTPIngestFixture(t, true)
		assertOTLPHTTPStatus(t, f.handler, http.MethodPost, "/v1/traces", testTraceRequest(), "fo_wrong", http.StatusUnauthorized)
	})
	t.Run("rotation invalidates old token", func(t *testing.T) {
		f := newHTTPIngestFixture(t, true)
		oldToken := f.token
		newToken, hash, err := settings.GenerateIngestToken()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
			t.Fatal(err)
		}
		assertOTLPHTTPStatus(t, f.handler, http.MethodPost, "/v1/traces", testTraceRequest(), oldToken, http.StatusUnauthorized)
		assertOTLPHTTPStatus(t, f.handler, http.MethodPost, "/v1/traces", testTraceRequest(), newToken, http.StatusOK)
		<-f.spans
	})
}

func TestHTTPIngestRejectsInvalidRequests(t *testing.T) {
	f := newHTTPIngestFixture(t, true)

	t.Run("method", func(t *testing.T) {
		assertOTLPHTTPStatus(t, f.handler, http.MethodGet, "/v1/traces", testTraceRequest(), f.token, http.StatusMethodNotAllowed)
	})
	t.Run("unknown path", func(t *testing.T) {
		assertOTLPHTTPStatus(t, f.handler, http.MethodPost, "/v1/profiles", testTraceRequest(), f.token, http.StatusNotFound)
	})
	t.Run("content type", func(t *testing.T) {
		payload, _ := proto.Marshal(testTraceRequest())
		req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-fanout-ingest-token", f.token)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status = %d, want 415", rec.Code)
		}
	})
	t.Run("content encoding", func(t *testing.T) {
		payload, _ := proto.Marshal(testTraceRequest())
		req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(payload))
		req.Header.Set("Content-Type", otlpProtobufContentType)
		req.Header.Set("Content-Encoding", "br")
		req.Header.Set("x-fanout-ingest-token", f.token)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status = %d, want 415", rec.Code)
		}
	})
	t.Run("malformed protobuf", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader([]byte{0xff}))
		req.Header.Set("Content-Type", otlpProtobufContentType)
		req.Header.Set("x-fanout-ingest-token", f.token)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("malformed gzip", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader([]byte("not gzip")))
		req.Header.Set("Content-Type", otlpProtobufContentType)
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("x-fanout-ingest-token", f.token)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("decompressed body too large", func(t *testing.T) {
		handler := f.handler.(*httpIngestHandler)
		originalLimit := handler.maxBodyBytes
		handler.maxBodyBytes = 4
		defer func() { handler.maxBodyBytes = originalLimit }()
		for _, tc := range []struct {
			name     string
			body     io.Reader
			encoding string
		}{
			{name: "plain", body: bytes.NewReader([]byte("12345"))},
			{name: "gzip", body: compressedBody(t, []byte("12345")), encoding: "gzip"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/v1/traces", tc.body)
				req.Header.Set("Content-Type", otlpProtobufContentType)
				req.Header.Set("x-fanout-ingest-token", f.token)
				if tc.encoding != "" {
					req.Header.Set("Content-Encoding", tc.encoding)
				}
				rec := httptest.NewRecorder()
				f.handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusRequestEntityTooLarge {
					t.Fatalf("status = %d, want 413", rec.Code)
				}
			})
		}
	})
}

func TestReadOTLPHTTPBodyLimitsDecompressedSize(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("12345")))
		if _, err := readOTLPHTTPBody(req, 4); !errors.Is(err, errOTLPHTTPBodyTooLarge) {
			t.Fatalf("error = %v, want body-too-large", err)
		}
	})
	t.Run("gzip", func(t *testing.T) {
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		_, _ = io.WriteString(zw, "12345")
		_ = zw.Close()
		req := httptest.NewRequest(http.MethodPost, "/", &compressed)
		req.Header.Set("Content-Encoding", "gzip")
		if _, err := readOTLPHTTPBody(req, 4); !errors.Is(err, errOTLPHTTPBodyTooLarge) {
			t.Fatalf("error = %v, want body-too-large", err)
		}
	})
}

func TestHTTPAndGRPCPathsProduceEquivalentRows(t *testing.T) {
	t.Run("traces", func(t *testing.T) {
		request := testTraceRequest()
		grpcRows := make(chan lake.SpanRow, 1)
		grpcSrv := NewServer(config.Config{DefaultNamespace: "default"}, grpcRows, make(chan lake.LogRow, 1), make(chan lake.MetricRow, 1))
		if _, err := grpcSrv.exportTraces(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		grpcRow := <-grpcRows
		httpFixture := newHTTPIngestFixture(t, true)
		assertOTLPHTTPSuccess(t, httpFixture.handler, "/v1/traces", request, httpFixture.token, &collectortrace.ExportTraceServiceResponse{})
		httpRow := <-httpFixture.spans
		grpcRow.IngestedAt, httpRow.IngestedAt = 0, 0
		if !reflect.DeepEqual(grpcRow, httpRow) {
			t.Fatalf("gRPC row != HTTP row\ngRPC: %+v\nHTTP: %+v", grpcRow, httpRow)
		}
	})

	t.Run("logs", func(t *testing.T) {
		request := testLogsRequest()
		grpcRows := make(chan lake.LogRow, 1)
		grpcSrv := NewServer(config.Config{DefaultNamespace: "default"}, make(chan lake.SpanRow, 1), grpcRows, make(chan lake.MetricRow, 1))
		if _, err := grpcSrv.exportLogs(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		grpcRow := <-grpcRows
		httpFixture := newHTTPIngestFixture(t, true)
		assertOTLPHTTPSuccess(t, httpFixture.handler, "/v1/logs", request, httpFixture.token, &collectorlogs.ExportLogsServiceResponse{})
		httpRow := <-httpFixture.logs
		grpcRow.IngestedAt, httpRow.IngestedAt = 0, 0
		if !reflect.DeepEqual(grpcRow, httpRow) {
			t.Fatalf("gRPC row != HTTP row\ngRPC: %+v\nHTTP: %+v", grpcRow, httpRow)
		}
	})

	t.Run("metrics", func(t *testing.T) {
		request := testMetricsRequest()
		grpcRows := make(chan lake.MetricRow, 1)
		grpcSrv := NewServer(config.Config{DefaultNamespace: "default"}, make(chan lake.SpanRow, 1), make(chan lake.LogRow, 1), grpcRows)
		if _, err := grpcSrv.exportMetrics(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		grpcRow := <-grpcRows
		httpFixture := newHTTPIngestFixture(t, true)
		assertOTLPHTTPSuccess(t, httpFixture.handler, "/v1/metrics", request, httpFixture.token, &collectormetrics.ExportMetricsServiceResponse{})
		httpRow := <-httpFixture.metrics
		grpcRow.IngestedAt, httpRow.IngestedAt = 0, 0
		if !reflect.DeepEqual(grpcRow, httpRow) {
			t.Fatalf("gRPC row != HTTP row\ngRPC: %+v\nHTTP: %+v", grpcRow, httpRow)
		}
	})
}

func assertOTLPHTTPSuccess(t *testing.T, handler http.Handler, path string, request proto.Message, token string, response proto.Message) {
	t.Helper()
	payload, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", otlpProtobufContentType)
	req.Header.Set("x-fanout-ingest-token", token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != otlpProtobufContentType {
		t.Fatalf("Content-Type = %q", got)
	}
	if err := proto.Unmarshal(rec.Body.Bytes(), response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func assertOTLPHTTPStatus(t *testing.T, handler http.Handler, method, path string, message proto.Message, token string, want int) {
	t.Helper()
	payload, _ := proto.Marshal(message)
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", otlpProtobufContentType)
	if token != "" {
		req.Header.Set("x-fanout-ingest-token", token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func testResource() *resourcepb.Resource {
	return &resourcepb.Resource{Attributes: []*commonpb.KeyValue{{
		Key:   "service.name",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "checkout"}},
	}}}
}

func compressedBody(t *testing.T, payload []byte) io.Reader {
	t.Helper()
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(compressed.Bytes())
}

func testTraceRequest() *collectortrace.ExportTraceServiceRequest {
	return &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: testResource(),
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
			TraceId:           []byte{1, 2, 3, 4},
			SpanId:            []byte{5, 6, 7, 8},
			Name:              "GET /cart",
			StartTimeUnixNano: 100,
			EndTimeUnixNano:   200,
			Status:            &tracepb.Status{},
		}}}},
	}}}
}

func testLogsRequest() *collectorlogs.ExportLogsServiceRequest {
	return &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource: testResource(),
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{
			TimeUnixNano: 100,
			Body:         &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "cart failed"}},
		}}}},
	}}}
}

func testMetricsRequest() *collectormetrics.ExportMetricsServiceRequest {
	return &collectormetrics.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		Resource: testResource(),
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{
			Name: "cart.items",
			Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{
				TimeUnixNano: 100,
				Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 3},
			}}}},
		}}}},
	}}}
}
