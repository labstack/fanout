package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestThresholdBarComponent_Type(t *testing.T) {
	c := &thresholdBarComponent{}
	if c.Type() != "threshold-bar" {
		t.Errorf("Type() = %q, want %q", c.Type(), "threshold-bar")
	}
}

func TestThresholdBarComponent_Schema(t *testing.T) {
	c := &thresholdBarComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	required := []string{"label", "value", "max", "warn", "crit"}
	for _, prop := range required {
		if _, ok := schema.Properties[prop]; !ok {
			t.Errorf("Schema missing '%s' property", prop)
		}
	}
}

func TestThresholdBarComponent_CSS(t *testing.T) {
	c := &thresholdBarComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() returned empty string")
	}
	if !strings.Contains(css, "threshold-bar") {
		t.Error("CSS() should contain threshold-bar class")
	}
}

func TestThresholdBarComponent_Render(t *testing.T) {
	c := &thresholdBarComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "normal value",
			config: map[string]any{
				"label": "CPU Usage",
				"value": 45.0,
				"max":   100.0,
				"warn":  70.0,
				"crit":  90.0,
				"unit":  "%",
			},
			format:    Both,
			wantASCII: "CPU Usage",
			wantHTML:  "primary",
		},
		{
			name: "warning value",
			config: map[string]any{
				"label": "Memory",
				"value": 75.0,
				"max":   100.0,
				"warn":  70.0,
				"crit":  90.0,
				"unit":  "%",
			},
			format:    Both,
			wantASCII: "[WARNING]",
			wantHTML:  "warning",
		},
		{
			name: "critical value",
			config: map[string]any{
				"label": "Disk",
				"value": 95.0,
				"max":   100.0,
				"warn":  70.0,
				"crit":  90.0,
				"unit":  "%",
			},
			format:    Both,
			wantASCII: "[CRITICAL]",
			wantHTML:  "danger",
		},
		{
			name: "zero max defaults to 100",
			config: map[string]any{
				"label": "Test",
				"value": 50.0,
				"max":   0,
				"warn":  70.0,
				"crit":  90.0,
			},
			format:    ASCII,
			wantASCII: "Test",
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

func TestThresholdBarComponent_Render_InvalidJSON(t *testing.T) {
	c := &thresholdBarComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
