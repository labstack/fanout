package ingest

import (
	"testing"

	common "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestToJSON(t *testing.T) {
	tests := []struct {
		name   string
		input  interface{}
		nilOut bool
	}{
		{"nil input", nil, true},
		{"empty slice", []string{}, false},
		{"string slice", []string{"a", "b"}, false},
		{"map", map[string]int{"a": 1}, false},
		{"unmarshalable chan", make(chan int), true},
		{"unmarshalable func", func() {}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := toJSON(tc.input)
			if tc.nilOut && result != nil {
				t.Errorf("toJSON(%v) = %v, want nil", tc.input, result)
			}
			if !tc.nilOut && result == nil {
				t.Errorf("toJSON(%v) = nil, want non-nil", tc.input)
			}
		})
	}
}

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
	tests := []struct {
		name     string
		input    interface{}
		expected float64
	}{
		{"double value", &metricspb.NumberDataPoint_AsDouble{AsDouble: 3.14}, 3.14},
		{"int value", &metricspb.NumberDataPoint_AsInt{AsInt: 42}, 42.0},
		{"nil value", nil, 0},
		{"unknown type", "string", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := number(tc.input)
			if result != tc.expected {
				t.Errorf("number() = %f, want %f", result, tc.expected)
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

func TestEventsToJSON(t *testing.T) {
	tests := []struct {
		name   string
		events []*tracepb.Span_Event
		nilOut bool
	}{
		{"nil events", nil, true},
		{"empty events", []*tracepb.Span_Event{}, true},
		{"with events", []*tracepb.Span_Event{
			{TimeUnixNano: 123, Name: "event1"},
		}, false},
		{"with attributes", []*tracepb.Span_Event{
			{
				TimeUnixNano: 123,
				Name:         "event1",
				Attributes: []*common.KeyValue{
					{Key: "key1", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "value1"}}},
				},
			},
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := eventsToJSON(tc.events)
			if tc.nilOut && result != nil {
				t.Errorf("eventsToJSON() = %v, want nil", result)
			}
			if !tc.nilOut && result == nil {
				t.Error("eventsToJSON() = nil, want non-nil")
			}
		})
	}
}

func TestLinksToJSON(t *testing.T) {
	tests := []struct {
		name   string
		links  []*tracepb.Span_Link
		nilOut bool
	}{
		{"nil links", nil, true},
		{"empty links", []*tracepb.Span_Link{}, true},
		{"with links", []*tracepb.Span_Link{
			{TraceId: []byte{0x01, 0x02}, SpanId: []byte{0x03, 0x04}},
		}, false},
		{"with trace state", []*tracepb.Span_Link{
			{TraceId: []byte{0x01}, SpanId: []byte{0x02}, TraceState: "vendor=value"},
		}, false},
		{"with attributes", []*tracepb.Span_Link{
			{
				TraceId: []byte{0x01, 0x02},
				SpanId:  []byte{0x03, 0x04},
				Attributes: []*common.KeyValue{
					{Key: "link.key", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "link.value"}}},
				},
			},
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := linksToJSON(tc.links)
			if tc.nilOut && result != nil {
				t.Errorf("linksToJSON() = %v, want nil", result)
			}
			if !tc.nilOut && result == nil {
				t.Error("linksToJSON() = nil, want non-nil")
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

func TestExemplarsToJSON(t *testing.T) {
	tests := []struct {
		name      string
		exemplars []*metricspb.Exemplar
		nilOut    bool
	}{
		{"nil exemplars", nil, true},
		{"empty exemplars", []*metricspb.Exemplar{}, true},
		{"with double exemplar", []*metricspb.Exemplar{
			{TimeUnixNano: 123, Value: &metricspb.Exemplar_AsDouble{AsDouble: 1.5}},
		}, false},
		{"with int exemplar", []*metricspb.Exemplar{
			{TimeUnixNano: 123, Value: &metricspb.Exemplar_AsInt{AsInt: 42}},
		}, false},
		{"with trace context", []*metricspb.Exemplar{
			{
				TimeUnixNano: 123,
				TraceId:      []byte{0x01, 0x02},
				SpanId:       []byte{0x03, 0x04},
				Value:        &metricspb.Exemplar_AsDouble{AsDouble: 1.0},
			},
		}, false},
		{"with attributes", []*metricspb.Exemplar{
			{
				TimeUnixNano: 123,
				Value:        &metricspb.Exemplar_AsDouble{AsDouble: 1.0},
				FilteredAttributes: []*common.KeyValue{
					{Key: "exemplar.key", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "exemplar.value"}}},
				},
			},
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := exemplarsToJSON(tc.exemplars)
			if tc.nilOut && result != nil {
				t.Errorf("exemplarsToJSON() = %v, want nil", result)
			}
			if !tc.nilOut && result == nil {
				t.Error("exemplarsToJSON() = nil, want non-nil")
			}
		})
	}
}

func TestExpHistBuckets(t *testing.T) {
	tests := []struct {
		name   string
		dp     *metricspb.ExponentialHistogramDataPoint
		nilOut bool
	}{
		{"nil positive", &metricspb.ExponentialHistogramDataPoint{}, true},
		{"empty bucket counts", &metricspb.ExponentialHistogramDataPoint{
			Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: []uint64{}},
		}, true},
		{"with buckets", &metricspb.ExponentialHistogramDataPoint{
			Scale:    0,
			Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: 0, BucketCounts: []uint64{1, 2, 3}},
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := expHistBuckets(tc.dp)
			if tc.nilOut && result != nil {
				t.Errorf("expHistBuckets() = %v, want nil", result)
			}
			if !tc.nilOut && result == nil {
				t.Error("expHistBuckets() = nil, want non-nil")
			}
		})
	}
}

func TestExpHistCounts(t *testing.T) {
	tests := []struct {
		name   string
		dp     *metricspb.ExponentialHistogramDataPoint
		nilOut bool
	}{
		{"nil positive", &metricspb.ExponentialHistogramDataPoint{}, true},
		{"with counts", &metricspb.ExponentialHistogramDataPoint{
			Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: []uint64{1, 2, 3}},
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := expHistCounts(tc.dp)
			if tc.nilOut && result != nil {
				t.Errorf("expHistCounts() = %v, want nil", result)
			}
			if !tc.nilOut && result == nil {
				t.Error("expHistCounts() = nil, want non-nil")
			}
		})
	}
}

func TestSummaryQuantiles(t *testing.T) {
	dp := &metricspb.SummaryDataPoint{
		QuantileValues: []*metricspb.SummaryDataPoint_ValueAtQuantile{
			{Quantile: 0.5, Value: 100.0},
			{Quantile: 0.99, Value: 500.0},
		},
	}

	result := summaryQuantiles(dp)
	if len(result) != 2 {
		t.Errorf("summaryQuantiles() len = %d, want 2", len(result))
	}
	if result[0] != 0.5 {
		t.Errorf("summaryQuantiles()[0] = %f, want 0.5", result[0])
	}
}

func TestSummaryValues(t *testing.T) {
	dp := &metricspb.SummaryDataPoint{
		QuantileValues: []*metricspb.SummaryDataPoint_ValueAtQuantile{
			{Quantile: 0.5, Value: 100.0},
			{Quantile: 0.99, Value: 500.0},
		},
	}

	result := summaryValues(dp)
	if len(result) != 2 {
		t.Errorf("summaryValues() len = %d, want 2", len(result))
	}
	if result[0] != 100.0 {
		t.Errorf("summaryValues()[0] = %f, want 100.0", result[0])
	}
}
