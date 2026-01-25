package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMetricComponent_Type(t *testing.T) {
	c := &metricComponent{}
	if c.Type() != "metric" {
		t.Errorf("Type() = %q, want %q", c.Type(), "metric")
	}
}

func TestMetricComponent_Schema(t *testing.T) {
	c := &metricComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if schema.Description == "" {
		t.Error("Schema has no description")
	}
	if _, ok := schema.Properties["label"]; !ok {
		t.Error("Schema missing 'label' property")
	}
	if _, ok := schema.Properties["value"]; !ok {
		t.Error("Schema missing 'value' property")
	}
}

func TestMetricComponent_CSS(t *testing.T) {
	c := &metricComponent{}
	css := c.CSS()

	if css == "" {
		t.Error("CSS() returned empty string")
	}
	if !strings.Contains(css, ".metric-card") {
		t.Error("CSS missing .metric-card class")
	}
}

func TestMetricComponent_Render(t *testing.T) {
	c := &metricComponent{}

	tests := []struct {
		name       string
		config     metricConfig
		format     Format
		wantASCII  string
		wantHTML   string
		wantErr    bool
	}{
		{
			name:      "basic metric ASCII",
			config:    metricConfig{Label: "Requests", Value: "1234"},
			format:    ASCII,
			wantASCII: "Requests: 1234",
		},
		{
			name:      "metric with unit",
			config:    metricConfig{Label: "Latency", Value: "42", Unit: "ms"},
			format:    ASCII,
			wantASCII: "Latency: 42ms",
		},
		{
			name:      "metric with trend up",
			config:    metricConfig{Label: "Score", Value: "95", Trend: "up"},
			format:    ASCII,
			wantASCII: "Score: 95 ↑",
		},
		{
			name:      "metric with trend down",
			config:    metricConfig{Label: "Errors", Value: "5", Trend: "down"},
			format:    ASCII,
			wantASCII: "Errors: 5 ↓",
		},
		{
			name:     "HTML format",
			config:   metricConfig{Label: "Test", Value: "100"},
			format:   HTML,
			wantHTML: "sl-card",
		},
		{
			name:      "Both format",
			config:    metricConfig{Label: "Test", Value: "100"},
			format:    Both,
			wantASCII: "Test: 100",
			wantHTML:  "sl-card",
		},
		{
			name:     "HTML with trend up",
			config:   metricConfig{Label: "CPU", Value: "80", Unit: "%", Trend: "up"},
			format:   HTML,
			wantHTML: "arrow-up",
		},
		{
			name:     "HTML with trend down",
			config:   metricConfig{Label: "Memory", Value: "40", Unit: "%", Trend: "down"},
			format:   HTML,
			wantHTML: "arrow-down",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgJSON, _ := json.Marshal(tc.config)
			out, err := c.Render(cfgJSON, tc.format)

			if (err != nil) != tc.wantErr {
				t.Errorf("Render() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if tc.wantASCII != "" && out.ASCII != tc.wantASCII {
				t.Errorf("Render() ASCII = %q, want %q", out.ASCII, tc.wantASCII)
			}

			if tc.wantHTML != "" && !strings.Contains(out.HTML, tc.wantHTML) {
				t.Errorf("Render() HTML = %q, want to contain %q", out.HTML, tc.wantHTML)
			}
		})
	}
}

func TestMetricComponent_Render_InvalidJSON(t *testing.T) {
	c := &metricComponent{}
	_, err := c.Render([]byte(`{invalid json`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
