package ingest

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/telemetry"
)

func TestBodyString(t *testing.T) {
	tests := []struct {
		name     string
		input    *common.AnyValue
		expected string
	}{
		{"nil value", nil, ""},
		{"string value", &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "hello"}}, "hello"},
		{"int value", &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: 42}}, `{"Value":{"IntValue":42}}`},
		{"bool value", &common.AnyValue{Value: &common.AnyValue_BoolValue{BoolValue: true}}, `{"Value":{"BoolValue":true}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := bodyString(tc.input)
			if result != tc.expected {
				t.Errorf("bodyString() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestGetServiceName(t *testing.T) {
	tests := []struct {
		name     string
		resource *resourcepb.Resource
		expected string
	}{
		{"nil resource", nil, ""},
		{"empty resource", &resourcepb.Resource{}, ""},
		{"with service name", &resourcepb.Resource{
			Attributes: []*common.KeyValue{
				{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "my-service"}}},
			},
		}, "my-service"},
		{"without service name", &resourcepb.Resource{
			Attributes: []*common.KeyValue{
				{Key: "other.attr", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "value"}}},
			},
		}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getServiceName(tc.resource)
			if result != tc.expected {
				t.Errorf("getServiceName() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestGetServiceNamespace(t *testing.T) {
	tests := []struct {
		name     string
		resource *resourcepb.Resource
		expected string
	}{
		{"nil resource", nil, ""},
		{"with namespace", &resourcepb.Resource{
			Attributes: []*common.KeyValue{
				{Key: "service.namespace", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "production"}}},
			},
		}, "production"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getServiceNamespace(tc.resource)
			if result != tc.expected {
				t.Errorf("getServiceNamespace() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestAsString(t *testing.T) {
	tests := []struct {
		name     string
		input    *common.AnyValue
		expected string
	}{
		{"nil value", nil, ""},
		{"string value", &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "test"}}, "test"},
		{"int value", &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: 42}}, ""},
		{"empty string", &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: ""}}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := asString(tc.input)
			if result != tc.expected {
				t.Errorf("asString() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestNumber(t *testing.T) {
	flag := uint32(metricspb.DataPointFlags_DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK)
	tests := []struct {
		name     string
		input    *metricspb.NumberDataPoint
		expected float64
		invalid  bool
	}{
		{"double value", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 3.14}}, 3.14, false},
		{"int value", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsInt{AsInt: 42}}, 42.0, false},
		{"no value", &metricspb.NumberDataPoint{}, 0, true},
		{"no recorded value", &metricspb.NumberDataPoint{Flags: flag}, 0, false},
		{"no recorded value ignores value", &metricspb.NumberDataPoint{Flags: flag, Value: &metricspb.NumberDataPoint_AsInt{AsInt: 42}}, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := number(tc.input)
			if (err != nil) != tc.invalid || result != tc.expected {
				t.Errorf("number() = %f, %v; want %f, invalid=%v", result, err, tc.expected, tc.invalid)
			}
		})
	}
}

func TestHexOrEmpty(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{"empty", []byte{}, ""},
		{"nil", nil, ""},
		{"single byte", []byte{0xff}, "ff"},
		{"multiple bytes", []byte{0x01, 0x02, 0x03}, "010203"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := hexOrEmpty(tc.input)
			if result != tc.expected {
				t.Errorf("hexOrEmpty() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestScopeInfo(t *testing.T) {
	tests := []struct {
		name         string
		scope        *common.InstrumentationScope
		expectedName string
		expectedVer  string
	}{
		{"nil scope", nil, "", ""},
		{"empty scope", &common.InstrumentationScope{}, "", ""},
		{"with name", &common.InstrumentationScope{Name: "my-scope"}, "my-scope", ""},
		{"with version", &common.InstrumentationScope{Name: "my-scope", Version: "1.0.0"}, "my-scope", "1.0.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, ver := scopeInfo(tc.scope)
			if name != tc.expectedName {
				t.Errorf("scopeInfo() name = %q, want %q", name, tc.expectedName)
			}
			if ver != tc.expectedVer {
				t.Errorf("scopeInfo() version = %q, want %q", ver, tc.expectedVer)
			}
		})
	}
}

func TestSpanAttr(t *testing.T) {
	attrs := []*common.KeyValue{
		{Key: "http.request.method", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "POST"}}},
		{Key: "http.method", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "GET"}}},
		{Key: "db.system", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "postgresql"}}},
	}

	tests := []struct {
		name string
		keys []string
		want string
	}{
		{"first key matches", []string{"db.system"}, "postgresql"},
		{"priority order respected", []string{"http.request.method", "http.method"}, "POST"},
		{"fallback key used", []string{"http.route", "http.request.method"}, "POST"},
		{"no match", []string{"rpc.method"}, ""},
		{"nil attrs", nil, ""},
		{"empty keys", []string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := attrs
			if tt.name == "nil attrs" {
				input = nil
			}
			got := spanAttr(input, tt.keys...)
			if got != tt.want {
				t.Errorf("spanAttr() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractException(t *testing.T) {
	events := []*tracepb.Span_Event{
		{Name: "log", Attributes: []*common.KeyValue{
			{Key: "message", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "debug"}}},
		}},
		{Name: "exception", Attributes: []*common.KeyValue{
			{Key: "exception.type", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "ConnectionRefusedError"}}},
			{Key: "exception.message", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "payment:5432 refused"}}},
		}},
	}

	excType, excMsg := extractException(events)
	if excType != "ConnectionRefusedError" {
		t.Errorf("excType = %q, want ConnectionRefusedError", excType)
	}
	if excMsg != "payment:5432 refused" {
		t.Errorf("excMsg = %q, want 'payment:5432 refused'", excMsg)
	}

	// No exception event
	excType, excMsg = extractException(nil)
	if excType != "" || excMsg != "" {
		t.Errorf("expected empty for nil events, got %q %q", excType, excMsg)
	}
}

func TestTraceExportContextCancellation(t *testing.T) {
	// Use an unbuffered channel so the send blocks
	spans := make(chan telemetry.Span)
	logs := make(chan telemetry.Log, 1)
	metrics := make(chan telemetry.Metric, 1)

	srv := NewServer(config.Config{}, newTestSubmitter(spans, logs, metrics))
	ts := &traceService{srv: srv}

	// Cancel the context before calling Export
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*common.KeyValue{
						{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "test"}}},
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Spans: []*tracepb.Span{
							{
								TraceId: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
								SpanId:  []byte{1, 2, 3, 4, 5, 6, 7, 8},
								Name:    "test-span",
								Status:  &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	_, err := ts.Export(ctx, req)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestNormalizeSeverityCanonicalizesEmitterSpellings(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		number int32
		want   string
	}{
		{"otel short name", "INFO", 9, "INFO"},
		{"number wins over display text", "Information", 9, "INFO"},
		{"dotnet long form without a number", "Information", 0, "INFO"},
		{"python style warning", "WARNING", 0, "WARN"},
		{"syslog critical", "crit", 0, "FATAL"},
		{"numbered short name", "ERROR2", 0, "ERROR"},
		{"envoy access log carries neither", "", 0, "UNSPECIFIED"},
		{"unrecognised level is kept", "audit", 0, "AUDIT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := telemetry.NormalizeSeverity(tt.text, tt.number); got != tt.want {
				t.Errorf("normalizeSeverity(%q, %d) = %q, want %q", tt.text, tt.number, got, tt.want)
			}
		})
	}
}

func TestExportedLogsCarryNormalizedBands(t *testing.T) {
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log, 6)
	metrics := make(chan telemetry.Metric, 1)
	srv := NewServer(config.Config{}, newTestSubmitter(spans, logs, metrics))
	pairs := []struct {
		text   string
		number int32
		want   string
	}{{"ERROR", 9, "INFO"}, {"FATAL", 0, "FATAL"}, {"WARN2", 14, "WARN"}, {"info", 0, "INFO"}, {"custom", 0, "CUSTOM"}, {"", 0, "UNSPECIFIED"}}
	var records []*logspb.LogRecord
	for _, p := range pairs {
		records = append(records, &logspb.LogRecord{SeverityText: p.text, SeverityNumber: logspb.SeverityNumber(p.number), TimeUnixNano: 1})
	}
	_, err := srv.exportLogs(context.Background(), &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: records}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		got := <-logs
		if got.Severity != p.want || got.Severity != telemetry.NormalizeSeverity(p.text, p.number) {
			t.Errorf("stored severity = %q, want %q", got.Severity, p.want)
		}
	}
}

func TestTraceExportAcceptsSpanWithoutStatus(t *testing.T) {
	// status is optional in OTLP and most SDKs omit it for an unset status.
	// Reading it through the struct field panicked the handler, so a single
	// ordinary span took the whole export down with it.
	spans := make(chan telemetry.Span, 1)
	logs := make(chan telemetry.Log, 1)
	metrics := make(chan telemetry.Metric, 1)
	srv := NewServer(config.Config{}, newTestSubmitter(spans, logs, metrics))

	req := &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*common.KeyValue{
				{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "checkout"}}},
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
				TraceId: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
				SpanId:  []byte{1, 2, 3, 4, 5, 6, 7, 8},
				Name:    "POST /checkout",
			}}}},
		}},
	}

	if _, err := (&traceService{srv: srv}).Export(context.Background(), req); err != nil {
		t.Fatalf("Export: %v", err)
	}
	select {
	case row := <-spans:
		if row.StatusCode != tracepb.Status_STATUS_CODE_UNSET.String() {
			t.Fatalf("status code = %q, want the unset status", row.StatusCode)
		}
		if row.StatusMsg != "" {
			t.Fatalf("status message = %q, want empty", row.StatusMsg)
		}
	default:
		t.Fatal("span was dropped")
	}
}

// Event, link and exemplar attributes keep their OTLP types, and integer
// exemplar values become doubles like point values.
func TestEventsLinksAndExemplarsKeepTypedAttributes(t *testing.T) {
	integer := func(k string, v int64) *common.KeyValue {
		return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: v}}}
	}
	events, err := spanEvents([]*tracepb.Span_Event{{TimeUnixNano: 5, Name: "retry", Attributes: []*common.KeyValue{integer("attempt", 2)}}, {Name: "bare"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []telemetry.Event{{TimeUnixNanos: 5, Name: "retry", Attributes: map[string]any{"attempt": int64(2)}}, {Name: "bare"}}) {
		t.Fatalf("events = %+v", events)
	}
	links, err := spanLinks([]*tracepb.Span_Link{{TraceId: []byte{1}, SpanId: []byte{2}, TraceState: "a=b", Attributes: []*common.KeyValue{integer("hops", 3)}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(links, []telemetry.Link{{TraceID: "01", SpanID: "02", TraceState: "a=b", Attributes: map[string]any{"hops": int64(3)}}}) {
		t.Fatalf("links = %+v", links)
	}
	exemplars, err := exemplars([]*metricspb.Exemplar{{TimeUnixNano: 7, Value: &metricspb.Exemplar_AsInt{AsInt: 4}, TraceId: []byte{3}, FilteredAttributes: []*common.KeyValue{integer("user", 9)}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exemplars, []telemetry.Exemplar{{TimeUnixNanos: 7, Value: 4, TraceID: "03", Attributes: map[string]any{"user": int64(9)}}}) {
		t.Fatalf("exemplars = %+v", exemplars)
	}
}

// Explicit buckets that no quantile can read reject the export.
func TestHistogramRejectsMalformedBuckets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bounds []float64
		counts []uint64
	}{
		{"missing count", []float64{1, 2}, []uint64{1, 1}},
		{"extra count", []float64{1}, []uint64{1, 1, 1}},
		{"counts without bounds or overflow", nil, []uint64{1, 2}},
		{"unordered", []float64{2, 1}, []uint64{1, 1, 1}},
		{"repeated", []float64{1, 1}, []uint64{1, 1, 1}},
		{"infinite", []float64{1, math.Inf(1)}, []uint64{1, 1, 1}},
		{"not a number", []float64{math.NaN()}, []uint64{1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkBuckets(tc.bounds, tc.counts); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, ok := range [][2]int{{0, 0}, {0, 1}, {2, 3}} {
		bounds, counts := make([]float64, ok[0]), make([]uint64, ok[1])
		for i := range bounds {
			bounds[i] = float64(i)
		}
		if err := checkBuckets(bounds, counts); err != nil {
			t.Fatalf("%d bounds, %d counts: %v", ok[0], ok[1], err)
		}
	}
}
