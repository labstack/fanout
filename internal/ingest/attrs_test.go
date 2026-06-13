package ingest

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	common "go.opentelemetry.io/proto/otlp/common/v1"
)

func kvDouble(k string, v float64) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_DoubleValue{DoubleValue: v}}}
}

// The direct JSON-string encoder must be byte-for-byte identical to
// encoding/json.Marshal — including its default HTML escaping (< > &), control
// chars, U+2028/U+2029, and invalid UTF-8. Any divergence corrupts stored
// attributes_json.
func TestAppendJSONString_MatchesEncodingJSON(t *testing.T) {
	cases := []string{
		"", "simple", "with space", `quote " and \ backslash`,
		"newline\ntab\tcr\r", "bell\x07null\x00", "html < > & chars",
		"unicode ☃ 日本語 😀", "line sep para",
		"slash / ok", string([]byte{0xff, 0xfe}), "trailing\x1f",
	}
	for _, s := range cases {
		want, _ := json.Marshal(s)
		var buf bytes.Buffer
		appendJSONString(&buf, s)
		if got := buf.Bytes(); !bytes.Equal(got, want) {
			t.Errorf("appendJSONString(%q):\n got  %s\n want %s", s, got, want)
		}
	}
}

// The fast path and the reflection path must be semantically identical (key
// order may differ, so compare parsed values, not bytes) — across every scalar
// type, the nil value, and the nested fallback.
func TestAttrsJSON_MatchesReflect(t *testing.T) {
	cases := [][]*common.KeyValue{
		{kvStr("k", "v")},
		{kvStr("http.method", "GET"), kvInt("code", 200), kvBool("ok", true), kvDouble("ratio", 0.25)},
		{kvStr("esc", "a\"b\\c\nd<e>f&g")},
		{kvDouble("big", 1e20), kvDouble("small", -3.5), kvDouble("whole", 200)},
		{{Key: "nilval", Value: nil}},
		{{Key: "bytes", Value: &common.AnyValue{Value: &common.AnyValue_BytesValue{BytesValue: []byte{1, 2, 3}}}}},
		{ // nested → both must route through the reflect encoder
			{Key: "outer", Value: &common.AnyValue{Value: &common.AnyValue_KvlistValue{
				KvlistValue: &common.KeyValueList{Values: []*common.KeyValue{kvStr("inner", "v")}},
			}}},
			kvStr("flat", "x"),
		},
	}
	for i, attrs := range cases {
		fast := attrsJSON(attrs)
		slow := attrsJSONReflect(attrs)
		var mf, ms map[string]any
		if err := json.Unmarshal(fast, &mf); err != nil {
			t.Fatalf("case %d: fast output invalid JSON: %v (%s)", i, err, fast)
		}
		if err := json.Unmarshal(slow, &ms); err != nil {
			t.Fatalf("case %d: reflect output invalid JSON: %v", i, err)
		}
		if !reflect.DeepEqual(mf, ms) {
			t.Errorf("case %d mismatch:\n fast=%s\n slow=%s", i, fast, slow)
		}
	}
}

func BenchmarkAttrsJSON(b *testing.B) {
	attrs := []*common.KeyValue{
		kvStr("http.method", "POST"), kvStr("http.route", "/api/orders/:id"),
		kvInt("http.status_code", 200), kvStr("user.id", "u-48217"),
		kvBool("cache.hit", false), kvDouble("duration_ms", 12.7),
	}
	b.Run("fast", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = attrsJSON(attrs)
		}
	})
	b.Run("reflect", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = attrsJSONReflect(attrs)
		}
	})
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
