package fanql

import (
	"strings"
	"testing"
)

func TestCompilePreservesNativeMeasureValidation(t *testing.T) {
	for _, tt := range []struct{ text, message string }{
		{"spans | avg(1)", "avg needs a field"},
		{"spans | count_distinct(7)", "count_distinct needs a field"},
		{"spans | count_if(`" + strings.Repeat("1=1 OR ", 64) + "1=1`)", "a measure is at most 256"},
		{"spans | count_if(`" + strings.Repeat(" ", 500) + "1=1`)", "filters are 1 to 500"},
	} {
		c, ps := Compile(tt.text, Options{Viz: "table"})
		if c != nil || len(ps) != 1 || ps[0].Span == nil || !strings.Contains(ps[0].Message, tt.message) {
			t.Errorf("native measure validation lost for %q: %+v %+v", tt.text, c, ps)
		}
	}

}

func TestCompileRepeatedConditionalMeasuresKeepTheirOwnSpans(t *testing.T) {
	text := "spans | count_if(service:$x) + count_if(service:$x) as total"
	_, ps := Compile(text, Options{Viz: "table"})
	if len(ps) != 1 || ps[0].Span == nil || ps[0].Span.Start != strings.Index(text, "$x") {
		t.Fatalf("first conditional span: %+v", ps)
	}
}
