package storagebench

import (
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/telemetry/segment"
)

const DayNanos = int64(24 * time.Hour)

func Rows(offset, count, total int, base int64) []segment.Span {
	rows := make([]segment.Span, count)
	methods := [...]string{"GET", "POST", "PUT", "DELETE"}
	for j := range rows {
		i := offset + j
		service := fmt.Sprintf("service-%02d", i%50)
		route := fmt.Sprintf("/api/v1/resource/%02d", i%20)
		statusCode, statusMessage := "OK", ""
		exceptionType, exceptionMessage := "", ""
		if i%20 == 0 {
			statusCode, statusMessage = "ERROR", "upstream request failed"
			exceptionType, exceptionMessage = "TimeoutError", "deadline exceeded while calling dependency"
		}
		start := base + int64(i)*DayNanos/int64(total)
		duration := float64(1+(i%5000)) / 10
		rows[j] = segment.Span{
			Namespace: "default", TraceID: TraceID(uint64(i / 5)), SpanID: fmt.Sprintf("%016x", i),
			ParentSpanID: fmt.Sprintf("%016x", max(i-1, 0)), ServiceName: service,
			Name: methods[i%len(methods)] + " " + route, Kind: "SERVER",
			StartUnixNanos: start, EndUnixNanos: start + int64(duration*float64(time.Millisecond)), DurationMS: duration,
			StatusCode: statusCode, StatusMsg: statusMessage,
			ResourceJSON:   []byte(fmt.Sprintf(`{"service.name":"%s","host.name":"node-%02d"}`, service, i%16)),
			AttributesJSON: []byte(fmt.Sprintf(`{"tenant":"tenant-%03d","region":"us-west-2","http.request.method":"%s"}`, i%200, methods[i%len(methods)])),
			EventsJSON:     []byte(`[]`), LinksJSON: []byte(`[]`), TraceState: "vendor=opaque", Flags: 1,
			ScopeName: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", ScopeVersion: "0.63.0", IngestedAt: start + int64(time.Second),
			HTTPMethod: methods[i%len(methods)], HTTPStatusCode: fmt.Sprintf("%d", 200+(i%5)), HTTPRoute: route,
			PeerService: fmt.Sprintf("dependency-%02d", i%10), ServiceVersion: "2026.8.0", DeploymentEnv: "production",
			ExceptionType: exceptionType, ExceptionMessage: exceptionMessage,
		}
	}
	return rows
}

func TraceID(value uint64) string { return fmt.Sprintf("%016x%016x", value*0x9e3779b97f4a7c15, value) }
