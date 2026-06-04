package ingest

import (
	"encoding/json"
	"testing"

	common "go.opentelemetry.io/proto/otlp/common/v1"
)

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
			if got := spanDurationMs(c.start, c.end); got != c.want {
				t.Errorf("spanDurationMs(%d, %d) = %v, want %v", c.start, c.end, got, c.want)
			}
		})
	}
}

func TestAttrsJSON_FlatObject(t *testing.T) {
	attrs := []*common.KeyValue{
		kvStr("http.method", "GET"),
		kvInt("http.status_code", 200),
		kvBool("error", true),
	}
	got := attrsJSON(attrs)

	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("output is not a JSON object: %v (%s)", err, got)
	}
	if m["http.method"] != "GET" {
		t.Errorf("http.method = %v, want GET", m["http.method"])
	}
	if m["http.status_code"].(float64) != 200 {
		t.Errorf("http.status_code = %v, want 200", m["http.status_code"])
	}
	if m["error"] != true {
		t.Errorf("error = %v, want true", m["error"])
	}
}

func TestAttrsJSON_EmptyIsNil(t *testing.T) {
	if got := attrsJSON(nil); got != nil {
		t.Errorf("attrsJSON(nil) = %s, want nil", got)
	}
	if got := attrsJSON([]*common.KeyValue{}); got != nil {
		t.Errorf("attrsJSON([]) = %s, want nil", got)
	}
}

func TestAttrsJSON_NestedKvlist(t *testing.T) {
	attrs := []*common.KeyValue{
		{Key: "outer", Value: &common.AnyValue{Value: &common.AnyValue_KvlistValue{
			KvlistValue: &common.KeyValueList{Values: []*common.KeyValue{kvStr("inner", "v")}},
		}}},
	}
	var m map[string]any
	if err := json.Unmarshal(attrsJSON(attrs), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	outer, ok := m["outer"].(map[string]any)
	if !ok || outer["inner"] != "v" {
		t.Errorf("nested kvlist not preserved: %v", m["outer"])
	}
}
