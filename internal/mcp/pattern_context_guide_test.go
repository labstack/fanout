package mcp

import (
	"github.com/labstack/fanout/internal/panel"
	"reflect"
	"strings"
	"testing"
)

func TestPreviewPatternContextGuide(t *testing.T) {
	for _, term := range []string{"dominant severity", "ties go to the higher severity", "top service", "same bounded query"} {
		if !strings.Contains(baseSpecGuide, term) {
			t.Errorf("guide missing %q", term)
		}
	}
	field, _ := reflect.TypeFor[panel.Panel]().FieldByName("Viz")
	if !strings.Contains(field.Tag.Get("jsonschema"), "dominant severity") {
		t.Error("spec description omits log pattern context")
	}
}
