package intelligence

import (
	"os"
	"strings"
	"testing"
)

// DuckDB's FLOAT is single precision, so the driver returns float32 and the
// `row["x"].(float64)` every caller here writes fails -- silently yielding 0.0.
// internal/engine now widens float32 on the way out, which disarms the trap, but
// a rate or a count has no business being single precision in the first place
// and the next reader should not have to know about the widening to trust it.
func TestNoFloatCastsInQueryStrings(t *testing.T) {
	source, err := os.ReadFile("detector.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(source), "\n") {
		if strings.Contains(strings.ToUpper(line), "::FLOAT") {
			t.Errorf("detector.go:%d casts to FLOAT (single precision): %q\nuse ::DOUBLE", i+1, strings.TrimSpace(line))
		}
	}
}
