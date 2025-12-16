package render

import (
	"encoding/json"
	"testing"
)

func TestRegisteredComponents(t *testing.T) {
	types := Types()
	expected := []string{
		"badge", "bar", "chart", "diff", "grid", "heatmap",
		"histogram", "metric", "metric-compare", "panel",
		"slo", "sparkline", "stat-group", "table", "text",
		"threshold-bar", "timeline",
	}

	if len(types) != len(expected) {
		t.Errorf("expected %d components, got %d: %v", len(expected), len(types), types)
	}

	for _, e := range expected {
		c, ok := Get(e)
		if !ok {
			t.Errorf("component %q not registered", e)
			continue
		}
		if c.Type() != e {
			t.Errorf("component type mismatch: expected %q, got %q", e, c.Type())
		}
	}
}

func TestToolDescription(t *testing.T) {
	desc := ToolDescription()
	if desc == "" {
		t.Error("tool description is empty")
	}
	if len(desc) < 100 {
		t.Errorf("tool description too short: %d chars", len(desc))
	}
	t.Logf("Tool description (%d chars):\n%s", len(desc), desc)
}

func TestAllCSS(t *testing.T) {
	css := AllCSS()
	if css == "" {
		t.Error("CSS collection is empty")
	}
	if len(css) < 200 {
		t.Errorf("CSS too short: %d chars", len(css))
	}
	t.Logf("Collected CSS: %d chars", len(css))
}

func TestMetricRender(t *testing.T) {
	cfg := json.RawMessage(`{"label": "Requests", "value": "1.2k", "unit": "req/s", "trend": "up"}`)
	err := Validate("metric", cfg)
	if err != nil {
		t.Errorf("validation failed: %v", err)
	}

	out, err := RenderSection("metric", cfg, HTML)
	if err != nil {
		t.Errorf("render failed: %v", err)
	}
	if out.HTML == "" {
		t.Error("HTML output is empty")
	}
	t.Logf("HTML output: %s", out.HTML)
}

func TestBadgeRender(t *testing.T) {
	cfg := json.RawMessage(`{"label": "Healthy", "status": "healthy"}`)
	out, err := RenderSection("badge", cfg, HTML)
	if err != nil {
		t.Errorf("render failed: %v", err)
	}
	if out.HTML == "" {
		t.Error("HTML output is empty")
	}
	t.Logf("Badge HTML: %s", out.HTML)
}

func TestTableRender(t *testing.T) {
	cfg := json.RawMessage(`{"headers": ["Name", "Value"], "rows": [["foo", "123"], ["bar", "456"]]}`)
	out, err := RenderSection("table", cfg, Both)
	if err != nil {
		t.Errorf("render failed: %v", err)
	}
	if out.HTML == "" {
		t.Error("HTML output is empty")
	}
	if out.ASCII == "" {
		t.Error("ASCII output is empty")
	}
	t.Logf("ASCII:\n%s", out.ASCII)
}

func TestValidationError(t *testing.T) {
	// Missing required field
	cfg := json.RawMessage(`{"value": "1.2k"}`)
	err := Validate("metric", cfg)
	if err == nil {
		t.Error("expected validation error for missing label")
	}
	t.Logf("Expected error: %v", err)
}
