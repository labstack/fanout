package mcp

import (
	"strings"
	"testing"
)

func TestDistributionSpecGuide(t *testing.T) {
	for _, phrase := range []string{"NaN is excluded", "last minus first", "counter resets mid-window contributes 0"} {
		if !strings.Contains(specGuide, phrase) {
			t.Errorf("spec guide must include %q", phrase)
		}
	}
}
