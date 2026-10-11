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
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/metrics/v1"
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
	base := time.Now().UTC()
	data, err := payloads(13, base)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, signal := range []string{"traces", "logs", "metrics"} {
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
	fmt.Printf("seed_window=%s,%s\n", base.Add(-24*time.Hour).Format(time.RFC3339Nano), base.Format(time.RFC3339Nano))
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
	for minute := range 1440 {
		at := base.Add(-24*time.Hour + time.Duration(minute)*time.Minute)
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
				if minute >= 1380 {
					span.Name = "GET warehouse-api"
					span.Attributes = []*common.KeyValue{attribute("http.request.method", "GET"), attribute("peer.service", "warehouse-api.example.test"), attribute("server.address", "warehouse-api.example.test")}
					span.Status.Code = trace.Status_STATUS_CODE_ERROR
				}
			case "shipping":
				span.Kind = trace.Span_SPAN_KIND_PRODUCER
				span.Name = "orders publish"
				span.Attributes = []*common.KeyValue{attribute("messaging.system", "kafka"), attribute("messaging.destination.name", "orders"), attribute("messaging.operation.type", "send")}
				if minute >= 1380 {
					// The failed client call has no instrumented downstream span.
					span.ParentSpanId = identifier(uint64(minute*10+3), 8)
				}
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
	if err != nil {
		return nil, err
	}
	out["metrics"], err = (proto.MarshalOptions{Deterministic: true}).Marshal(systemMetrics(base))
	return out, err
}

func systemMetrics(base time.Time) *collectormetrics.ExportMetricsServiceRequest {
	request := &collectormetrics.ExportMetricsServiceRequest{}
	const receiver = "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/"
	start := base.Add(-10*time.Minute - 15*time.Second)
	add := func(res *resource.Resource, scope, name, unit, kind string, points []*metrics.NumberDataPoint) {
		metric := &metrics.Metric{Name: name, Unit: unit}
		if kind == "gauge" {
			metric.Data = &metrics.Metric_Gauge{Gauge: &metrics.Gauge{DataPoints: points}}
		} else {
			metric.Data = &metrics.Metric_Sum{Sum: &metrics.Sum{DataPoints: points, IsMonotonic: kind == "counter", AggregationTemporality: metrics.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE}}
		}
		request.ResourceMetrics = append(request.ResourceMetrics, &metrics.ResourceMetrics{Resource: res, ScopeMetrics: []*metrics.ScopeMetrics{{Scope: &common.InstrumentationScope{Name: receiver + scope}, Metrics: []*metrics.Metric{metric}}}})
	}
	points := func(stopped bool, kind string, attrs []*common.KeyValue, value func(int) float64) []*metrics.NumberDataPoint {
		var out []*metrics.NumberDataPoint
		for i := range 21 {
			at := start.Add(time.Duration(i) * 30 * time.Second)
			if stopped && at.After(base.Add(-3*time.Minute-15*time.Second)) {
				break
			}
			p := &metrics.NumberDataPoint{TimeUnixNano: uint64(at.UnixNano()), Attributes: attrs, Value: &metrics.NumberDataPoint_AsDouble{AsDouble: value(i)}}
			if kind != "gauge" {
				p.StartTimeUnixNano = uint64(start.Add(-time.Hour).UnixNano())
			}
			out = append(out, p)
		}
		return out
	}
	for _, host := range []string{"disk-full-host", "stopped-host"} {
		res := &resource.Resource{Attributes: []*common.KeyValue{attribute("service.namespace", "smoke"), attribute("host.name", host)}}
		stopped := host == "stopped-host"
		for _, state := range []string{"idle", "user"} {
			add(res, "hostmetricsreceiver/internal/scraper/cpuscraper", "system.cpu.time", "s", "counter", points(stopped, "counter", []*common.KeyValue{attribute("cpu", "0"), attribute("state", state)}, func(i int) float64 {
				if state == "idle" {
					return 1000 + float64(i)*24
				}
				return 1000 + float64(i)*6
			}))
		}
		for _, state := range []string{"used", "free"} {
			add(res, "hostmetricsreceiver/internal/scraper/memoryscraper", "system.memory.usage", "By", "updown", points(stopped, "updown", []*common.KeyValue{attribute("state", state)}, func(int) float64 { return 512 * 1024 * 1024 }))
			add(res, "hostmetricsreceiver/internal/scraper/filesystemscraper", "system.filesystem.usage", "By", "updown", points(stopped, "updown", []*common.KeyValue{attribute("state", state), attribute("mountpoint", "/data"), attribute("device", "/dev/sda1"), attribute("type", "ext4"), attribute("mode", "rw")}, func(i int) float64 {
				used := 70.0 + float64(i)*1.25 // 95% at the end.
				if state == "free" {
					return (100 - used) * 1024 * 1024
				}
				return used * 1024 * 1024
			}))
		}
	}
	container := &resource.Resource{Attributes: []*common.KeyValue{attribute("service.namespace", "smoke"), attribute("host.name", "disk-full-host"), attribute("container.id", "smoke-container"), attribute("container.name", "checkout")}}
	for _, m := range []struct {
		name, unit string
		value      float64
	}{{"container.cpu.utilization", "%", 20}, {"container.memory.usage.total", "By", 128 * 1024 * 1024}, {"container.memory.usage.limit", "By", 512 * 1024 * 1024}} {
		add(container, "dockerstatsreceiver", m.name, m.unit, "gauge", points(false, "gauge", nil, func(int) float64 { return m.value }))
	}
	vantage := &resource.Resource{Attributes: []*common.KeyValue{attribute("service.namespace", "smoke"), attribute("host.name", "outside-collector")}}
	for _, target := range []string{"down", "stopped"} {
		attrs := []*common.KeyValue{attribute("http.url", "https://"+target+".example.test/health"), attribute("http.method", "GET")}
		for _, class := range []string{"1xx", "2xx", "3xx", "4xx", "5xx"} {
			statusAttrs := append(append([]*common.KeyValue{}, attrs...), attribute("http.status_class", class))
			add(vantage, "httpcheckreceiver", "httpcheck.status", "1", "updown", points(target == "stopped", "updown", statusAttrs, func(i int) float64 {
				if class == "2xx" && (target == "stopped" || i < 16) {
					return 1
				}
				return 0 // An unreachable target has zero successes in every class.
			}))
		}
		add(vantage, "httpcheckreceiver", "httpcheck.duration", "ms", "gauge", points(target == "stopped", "gauge", attrs, func(i int) float64 {
			if target == "down" && i >= 16 {
				return 5000
			}
			return 25
		}))
	}
	return request
}
