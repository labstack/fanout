package main

import (
	"math"
	"math/rand"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// landingPayloads is a fictional store workload, not a captured customer export.
// One minute per request keeps the dense hour below the normal OTLP body limit.
func landingPayloads(seed int64, base time.Time) ([]map[string][]byte, error) {
	services := []string{"cart", "currency", "flagd", "frontend", "frontend-proxy", "product-catalog", "checkout", "recommendation", "email", "payment", "shipping", "ad", "quote", "accounting"}
	weights := []int{16, 3, 4, 22, 18, 9, 6, 8, 1, 4, 3, 2, 2, 2}
	rng := rand.New(rand.NewSource(seed))
	var batches []map[string][]byte
	ordinal := 0
	for minute := range 60 {
		at := base.Add(-time.Hour + time.Duration(minute)*time.Minute)
		count := 2340 + int(260*math.Sin(float64(minute)*2*math.Pi/10)) + int(150*math.Cos(float64(minute)*2*math.Pi/15))
		spans := make([][]*trace.Span, len(services))
		for index := range count {
			service, ticket := 0, rng.Intn(100)
			for service < len(weights)-1 && ticket >= weights[service] {
				ticket -= weights[service]
				service++
			}
			start := at.Add(time.Duration(index) * time.Minute / time.Duration(count))
			duration := time.Duration(20+rng.Intn(33)) * time.Millisecond
			status := trace.Status_STATUS_CODE_OK
			if ordinal%10000 == 9999 {
				status = trace.Status_STATUS_CODE_ERROR
			}
			span := &trace.Span{TraceId: identifier(uint64(ordinal/14+1), 16), SpanId: identifier(uint64(ordinal+1), 8),
				Name: "GET /" + services[service], Kind: trace.Span_SPAN_KIND_SERVER,
				StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(duration).UnixNano()),
				Status: &trace.Status{Code: status}, Attributes: []*common.KeyValue{attribute("http.request.method", "GET"), attribute("http.route", "/"+services[service])}}
			if ordinal%14 != 0 {
				span.ParentSpanId = identifier(uint64(ordinal), 8)
			}
			spans[service] = append(spans[service], span)
			ordinal++
		}
		traces := &collectortrace.ExportTraceServiceRequest{}
		logRequest := &collectorlogs.ExportLogsServiceRequest{}
		for index, service := range services {
			res := &resource.Resource{Attributes: []*common.KeyValue{attribute("service.name", service), attribute("service.namespace", "shop"), attribute("service.version", "1.0.0")}}
			traces.ResourceSpans = append(traces.ResourceSpans, &trace.ResourceSpans{Resource: res, ScopeSpans: []*trace.ScopeSpans{{Spans: spans[index]}}})
			var records []*logs.LogRecord
			for sample := range 2 {
				severity, label, body := logs.SeverityNumber_SEVERITY_NUMBER_INFO, "INFO", "request completed"
				if (minute*28+index*2+sample)%80 == 0 {
					severity, label, body = logs.SeverityNumber_SEVERITY_NUMBER_ERROR, "ERROR", "request failed; retry scheduled"
				}
				stamp := uint64(at.Add(time.Duration(sample*30+index) * time.Second).UnixNano())
				records = append(records, &logs.LogRecord{TimeUnixNano: stamp, ObservedTimeUnixNano: stamp, SeverityNumber: severity, SeverityText: label, Body: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: body}}})
			}
			logRequest.ResourceLogs = append(logRequest.ResourceLogs, &logs.ResourceLogs{Resource: res, ScopeLogs: []*logs.ScopeLogs{{LogRecords: records}}})
		}
		traceBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(traces)
		if err != nil {
			return nil, err
		}
		logBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(logRequest)
		if err != nil {
			return nil, err
		}
		batches = append(batches, map[string][]byte{"traces": traceBytes, "logs": logBytes})
	}
	return batches, nil
}
