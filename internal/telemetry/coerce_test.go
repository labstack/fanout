package telemetry

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/labstack/fanout/internal/telemetry/semconv"
)

var coercions = []struct {
	value any
	typ   semconv.Type
	want  any // nil: no conversion
}{
	{int64(200), semconv.Int, int64(200)},
	{"200", semconv.Int, int64(200)},
	{"+7", semconv.Int, int64(7)},
	{"-007", semconv.Int, int64(-7)},
	{" 200", semconv.Int, nil},
	{"200 ", semconv.Int, nil},
	{"2e2", semconv.Int, nil},
	{"1_000", semconv.Int, nil},
	{"0x10", semconv.Int, nil},
	{"", semconv.Int, nil},
	{"9223372036854775808", semconv.Int, nil},
	{"abc", semconv.Int, nil},
	{503.0, semconv.Int, int64(503)},
	{math.Copysign(0, -1), semconv.Int, int64(0)},
	{503.5, semconv.Int, nil},
	{-9223372036854775808.0, semconv.Int, int64(math.MinInt64)},
	{9223372036854775808.0, semconv.Int, nil},
	{math.NaN(), semconv.Int, nil},
	{math.Inf(1), semconv.Int, nil},
	{true, semconv.Int, nil},
	{[]byte("1"), semconv.Int, nil},
	{map[string]any{"a": int64(1)}, semconv.Int, nil},
	{[]any{int64(1)}, semconv.Int, nil},
	{"GET", semconv.String, "GET"},
	{"", semconv.String, ""},
	{int64(2), semconv.String, "2"},
	{int64(-9), semconv.String, "-9"},
	{true, semconv.String, "true"},
	{false, semconv.String, "false"},
	{503.0, semconv.String, "503"},
	{0.1, semconv.String, "0.1"},
	{1e21, semconv.String, "1e+21"},
	{math.Copysign(0, -1), semconv.String, "-0"},
	{math.Inf(-1), semconv.String, "-Inf"},
	{math.NaN(), semconv.String, "NaN"},
	{[]byte("x"), semconv.String, nil},
	{map[string]any{"a": "b"}, semconv.String, nil},
	{[]any{"a"}, semconv.String, nil},
}

func TestCoerce(t *testing.T) {
	for _, test := range coercions {
		got, ok := coerce(test.value, test.typ)
		if ok != (test.want != nil) || ok && got != test.want {
			t.Errorf("coerce(%#v, %d) = %#v, %v; want %#v", test.value, test.typ, got, ok, test.want)
		}
	}
}

// The verifier restates coerce in SQL: a file written from every case
// verifies, and its footer counts the values with no conversion.
func TestVerifierAgreesWithCoerce(t *testing.T) {
	status, _ := semconv.Lookup(semconv.Span, "http.response.status_code")
	method, _ := semconv.Lookup(semconv.Span, "http.request.method")
	var spans []Span
	var spills [64]uint64
	for i, test := range coercions {
		key := method
		if test.typ == semconv.Int {
			key = status
		}
		if test.want == nil {
			spills[key.Bit]++
		}
		span := completeTestSpan()
		span.SpanID = fmt.Sprintf("%016x", i)
		span.Attributes = map[string]any{key.Name: test.value}
		spans = append(spans, span)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "spans.parquet")
	if err := writeParquet(path, Spans, spans, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	counts, err := fileSpills(path, Spans)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []semconv.Key{status, method} {
		if counts[key.Bit] != spills[key.Bit] {
			t.Errorf("%s spills %d, want %d", key.Name, counts[key.Bit], spills[key.Bit])
		}
	}
	verifier, err := NewBatchVerifier(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	if err := verifier.values(t.Context(), path, Spans); err != nil {
		t.Fatal(err)
	}
}
