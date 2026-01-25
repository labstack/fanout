package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSLOComponent_Type(t *testing.T) {
	c := &sloComponent{}
	if c.Type() != "slo" {
		t.Errorf("Type() = %q, want %q", c.Type(), "slo")
	}
}

func TestSLOComponent_Schema(t *testing.T) {
	c := &sloComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["name"]; !ok {
		t.Error("Schema missing 'name' property")
	}
	if _, ok := schema.Properties["current"]; !ok {
		t.Error("Schema missing 'current' property")
	}
	if _, ok := schema.Properties["target"]; !ok {
		t.Error("Schema missing 'target' property")
	}
}

func TestSLOComponent_CSS(t *testing.T) {
	c := &sloComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() returned empty string")
	}
	if !strings.Contains(css, "slo-card") {
		t.Error("CSS() should contain slo-card class")
	}
}

func TestSLOComponent_Render(t *testing.T) {
	c := &sloComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "meeting SLO",
			config: map[string]any{
				"name":             "API Availability",
				"current":          99.95,
				"target":           99.9,
				"budget_remaining": 75.0,
				"burn_rate":        0.5,
			},
			format:    Both,
			wantASCII: "MEETING",
			wantHTML:  "success",
		},
		{
			name: "breached SLO",
			config: map[string]any{
				"name":             "API Availability",
				"current":          99.5,
				"target":           99.9,
				"budget_remaining": -50.0,
				"burn_rate":        3.0,
			},
			format:    Both,
			wantASCII: "BREACHED",
			wantHTML:  "danger",
		},
		{
			name: "low budget warning",
			config: map[string]any{
				"name":             "Latency P99",
				"current":          99.92,
				"target":           99.9,
				"budget_remaining": 20.0,
				"burn_rate":        1.5,
			},
			format:   HTML,
			wantHTML: "warning",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgJSON, _ := json.Marshal(tc.config)
			out, err := c.Render(cfgJSON, tc.format)
			if err != nil {
				t.Errorf("Render() error = %v", err)
				return
			}

			if tc.wantASCII != "" && !strings.Contains(out.ASCII, tc.wantASCII) {
				t.Errorf("Render() ASCII = %q, want to contain %q", out.ASCII, tc.wantASCII)
			}

			if tc.wantHTML != "" && !strings.Contains(out.HTML, tc.wantHTML) {
				t.Errorf("Render() HTML = %q, want to contain %q", out.HTML, tc.wantHTML)
			}
		})
	}
}

func TestSLOComponent_Render_InvalidJSON(t *testing.T) {
	c := &sloComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
