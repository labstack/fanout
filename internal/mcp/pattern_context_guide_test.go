package mcp

import (
	"github.com/labstack/fanout/internal/panel"
	"reflect"
	"strings"
	"testing"
)

func TestPreviewPatternContextGuide(t *testing.T) {
	const rules = "Patterns group only by body_template. Severity is the most frequent severity, ties choose the higher severity. Service is the most frequent service, ties choose ascending service name. Both describe the scoped, redacted events, not extra grouping dimensions."
	for _, guide := range []string{baseSpecGuide, baseSpecGuide + " properties entities quantities utilities"} {
		if strings.Count(guide, rules) != 1 {
			t.Error("guide must state the exact pattern context rules once")
		}
		for _, retired := range []string{"ties go to the higher severity", "ties use service name ascending"} {
			if strings.Contains(guide, retired) {
				t.Errorf("guide retains duplicate phrasing %q", retired)
			}
		}
	}
	for _, term := range []string{"dominant severity", "top service", "same bounded query"} {
		if !strings.Contains(baseSpecGuide, term) {
			t.Errorf("guide missing %q", term)
		}
	}
	field, _ := reflect.TypeFor[panel.Panel]().FieldByName("Viz")
	if !strings.Contains(field.Tag.Get("jsonschema"), "dominant severity") {
		t.Error("spec description omits log pattern context")
	}
}
