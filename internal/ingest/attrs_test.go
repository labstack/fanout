package ingest

import (
	common "go.opentelemetry.io/proto/otlp/common/v1"
	"math"
	"reflect"
	"testing"
)

func TestAttributesPreserveTypesAndOwnBytes(t *testing.T) {
	bytes := []byte{0, 255, 7}
	input := []*common.KeyValue{
		kvStr("http.method", "GET"), kvInt("big", math.MaxInt64), kvBool("false", false), kvInt("zero", 0),
		{Key: "null"},
		{Key: "bytes", Value: &common.AnyValue{Value: &common.AnyValue_BytesValue{BytesValue: bytes}}},
		{Key: "array", Value: &common.AnyValue{Value: &common.AnyValue_ArrayValue{ArrayValue: &common.ArrayValue{Values: []*common.AnyValue{nil, kvInt("", 9).Value}}}}},
		{Key: "object", Value: &common.AnyValue{Value: &common.AnyValue_KvlistValue{KvlistValue: &common.KeyValueList{Values: []*common.KeyValue{kvBool("ok", true)}}}}},
		{Key: "nan", Value: &common.AnyValue{Value: &common.AnyValue_DoubleValue{DoubleValue: math.NaN()}}},
	}
	got, err := attributes(input)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"http.method": "GET", "big": int64(math.MaxInt64), "false": false, "zero": int64(0), "null": nil, "bytes": []byte{0, 255, 7}, "array": []any{nil, int64(9)}, "object": map[string]any{"ok": true}}
	if !math.IsNaN(got["nan"].(float64)) {
		t.Fatal("nonfinite double lost")
	}
	delete(got, "nan")
	bytes[0] = 42
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typed attributes: %#v", got)
	}
	for _, input := range [][]*common.KeyValue{nil, {}, {nil, {Key: ""}}} {
		if got, err := attributes(input); got != nil || err != nil {
			t.Fatal("empty attributes should be nil")
		}
	}
}

func kvStr(k, v string) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
}
func kvInt(k string, v int64) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: v}}}
}
func kvBool(k string, v bool) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_BoolValue{BoolValue: v}}}
}

func TestSpanDurationMs(t *testing.T) {
	cases := []struct {
		name       string
		start, end uint64
		want       float64
	}{
		{"normal", 0, 1_000_000, 1.0},
		{"sub-ms", 0, 500_000, 0.5},
		{"zero-length", 100, 100, 0},
		{"underflow clamped", 10, 5, 0}, // end < start must NOT wrap to a huge value
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := spanDurationMS(c.start, c.end); got != c.want {
				t.Errorf("spanDurationMS(%d, %d) = %v, want %v", c.start, c.end, got, c.want)
			}
		})
	}
}
