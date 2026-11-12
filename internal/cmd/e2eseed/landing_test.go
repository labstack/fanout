package main

import (
	"bytes"
	"testing"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestLandingSeedMatchesCaptureWorkload(t *testing.T) {
	base := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	batches, err := landingPayloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := landingPayloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 60 {
		t.Fatalf("minute batches = %d", len(batches))
	}
	services := make(map[string]int)
	count, failures, errorLogs := 0, 0, 0
	var durations [100]int
	for index, batch := range batches {
		for _, signal := range []string{"traces", "logs"} {
			if !bytes.Equal(batch[signal], repeated[index][signal]) {
				t.Fatalf("%s is not deterministic", signal)
			}
			if len(batch[signal]) > 1<<20 {
				t.Fatalf("%s batch exceeds 1 MiB", signal)
			}
		}
		var traces collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(batch["traces"], &traces); err != nil {
			t.Fatal(err)
		}
		for _, resource := range traces.ResourceSpans {
			service := resource.Resource.Attributes[0].Value.GetStringValue()
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					services[service]++
					count++
					if span.Kind != trace.Span_SPAN_KIND_SERVER {
						t.Fatal("landing requests must be server spans")
					}
					if span.StartTimeUnixNano < uint64(base.Add(-time.Hour).UnixNano()) || span.StartTimeUnixNano >= uint64(base.UnixNano()) {
						t.Fatal("span falls outside the hour")
					}
					if span.Status.Code == trace.Status_STATUS_CODE_ERROR {
						failures++
					}
					durations[(span.EndTimeUnixNano-span.StartTimeUnixNano)/uint64(time.Millisecond)]++
				}
			}
		}
		var logRequest collectorlogs.ExportLogsServiceRequest
		if err := proto.Unmarshal(batch["logs"], &logRequest); err != nil {
			t.Fatal(err)
		}
		for _, resource := range logRequest.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					if record.SeverityNumber == logs.SeverityNumber_SEVERITY_NUMBER_ERROR {
						errorLogs++
					}
				}
			}
		}
	}
	if count < 38*3600 || count > 40*3600 || failures != 14 || errorLogs != 21 {
		t.Fatalf("spans=%d errors=%d error_logs=%d", count, failures, errorLogs)
	}
	for _, service := range []string{"cart", "currency", "flagd", "frontend", "frontend-proxy", "product-catalog", "checkout", "recommendation", "email", "payment", "shipping", "ad", "quote", "accounting"} {
		if services[service] < 1000 {
			t.Fatalf("service %s is not densely seeded: %d spans", service, services[service])
		}
	}
	if len(services) != 14 {
		t.Fatalf("services = %d", len(services))
	}
	seen, p95 := 0, 0
	for milliseconds, n := range durations {
		seen += n
		if seen*100 >= count*95 {
			p95 = milliseconds
			break
		}
	}
	if p95 < 49 || p95 > 54 {
		t.Fatalf("p95 = %d ms", p95)
	}
}
