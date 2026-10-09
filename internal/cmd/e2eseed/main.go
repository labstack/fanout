// Command e2eseed sends deterministic, near-now smoke telemetry to a disposable server.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func main() {
	endpoint := flag.String("endpoint", "", "Disposable Fanout base URL")
	tokenFile := flag.String("token-file", "", "Temporary ingest credential file")
	flag.Parse()
	if err := run(*endpoint, *tokenFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(endpoint, tokenFile string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || tokenFile == "" {
		return errors.New("endpoint and token-file are required")
	}
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("read token file: %w", err)
	}
	if strings.TrimSpace(string(token)) == "" {
		return errors.New("empty token file")
	}
	data, err := payloads(13, time.Now().UTC())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, signal := range []string{"traces", "logs"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/v1/"+signal, bytes.NewReader(data[signal]))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("post %s failed", signal)
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("post %s: HTTP %d", signal, resp.StatusCode)
		}
		if readErr != nil || closeErr != nil {
			return fmt.Errorf("read %s response failed", signal)
		}
	}
	return nil
}

func attribute(key, value string) *common.KeyValue {
	return &common.KeyValue{Key: key, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: value}}}
}

func identifier(n uint64, size int) []byte {
	b := make([]byte, size)
	binary.BigEndian.PutUint64(b[size-8:], n)
	return b
}

func payloads(seed int64, base time.Time) (map[string][]byte, error) {
	rng := rand.New(rand.NewSource(seed))
	traces := &collectortrace.ExportTraceServiceRequest{}
	logRequest := &collectorlogs.ExportLogsServiceRequest{}
	services := []string{"frontend", "checkout", "payment", "inventory", "shipping", "notifications"}
	// One complete distributed trace each minute. Versions split exactly one hour ago.
	for minute := range 120 {
		at := base.Add(-2*time.Hour + time.Duration(minute)*time.Minute)
		traceID := identifier(uint64(minute+1), 16)
		for i, service := range services {
			version := "1.0.0"
			if service == "checkout" && !at.Before(base.Add(-time.Hour)) {
				version = "1.1.0"
			}
			res := &resource.Resource{Attributes: []*common.KeyValue{attribute("service.name", service), attribute("service.namespace", "smoke"), attribute("service.version", version)}}
			spanID := identifier(uint64(minute*10+i+1), 8)
			start := at.Add(time.Duration(i) * time.Millisecond)
			status := trace.Status_STATUS_CODE_OK
			if (minute*6+i)%20 == 0 {
				status = trace.Status_STATUS_CODE_ERROR
			}
			span := &trace.Span{TraceId: traceID, SpanId: spanID, Name: "GET /" + service, Kind: trace.Span_SPAN_KIND_SERVER,
				StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(time.Duration(20+rng.Intn(500)) * time.Millisecond).UnixNano()),
				Status: &trace.Status{Code: status}, Attributes: []*common.KeyValue{attribute("http.request.method", "GET"), attribute("http.route", "/"+service)}}
			if i > 0 {
				span.ParentSpanId = identifier(uint64(minute*10+i), 8)
			}
			switch service {
			case "inventory":
				span.Kind = trace.Span_SPAN_KIND_CLIENT
				span.Name = "SELECT inventory"
				span.Attributes = []*common.KeyValue{attribute("db.system", "postgresql")}
			case "shipping":
				span.Kind = trace.Span_SPAN_KIND_PRODUCER
				span.Name = "orders publish"
				span.Attributes = []*common.KeyValue{attribute("messaging.system", "kafka"), attribute("messaging.destination.name", "orders"), attribute("messaging.operation.type", "send")}
			case "notifications":
				span.Kind = trace.Span_SPAN_KIND_CONSUMER
				span.Name = "orders process"
				span.Attributes = []*common.KeyValue{attribute("messaging.system", "kafka"), attribute("messaging.destination.name", "orders"), attribute("messaging.operation.type", "process")}
			}
			traces.ResourceSpans = append(traces.ResourceSpans, &trace.ResourceSpans{Resource: res, ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{span}}}})
			severity := []logs.SeverityNumber{logs.SeverityNumber_SEVERITY_NUMBER_DEBUG, logs.SeverityNumber_SEVERITY_NUMBER_INFO, logs.SeverityNumber_SEVERITY_NUMBER_WARN, logs.SeverityNumber_SEVERITY_NUMBER_ERROR}[(minute+i)%4]
			body := []string{"request accepted", "order completed", "retry scheduled", "payment failed"}[(minute+i)%4]
			logRequest.ResourceLogs = append(logRequest.ResourceLogs, &logs.ResourceLogs{Resource: res, ScopeLogs: []*logs.ScopeLogs{{LogRecords: []*logs.LogRecord{{TimeUnixNano: uint64(start.UnixNano()), ObservedTimeUnixNano: uint64(start.UnixNano()), SeverityNumber: severity, SeverityText: strings.TrimPrefix(severity.String(), "SEVERITY_NUMBER_"), Body: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: body}}, TraceId: traceID, SpanId: spanID}}}}})
		}
	}
	out := map[string][]byte{}
	var err error
	out["traces"], err = (proto.MarshalOptions{Deterministic: true}).Marshal(traces)
	if err != nil {
		return nil, err
	}
	out["logs"], err = (proto.MarshalOptions{Deterministic: true}).Marshal(logRequest)
	return out, err
}
