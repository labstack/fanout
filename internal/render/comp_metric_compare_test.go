package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMetricCompareComponent_Type(t *testing.T) {
	c := &metricCompareComponent{}
	if c.Type() != "metric-compare" {
		t.Errorf("Type() = %q, want %q", c.Type(), "metric-compare")
	}
}

func TestMetricCompareComponent_Schema(t *testing.T) {
	c := &metricCompareComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["label"]; !ok {
		t.Error("Schema missing 'label' property")
	}
	if _, ok := schema.Properties["value"]; !ok {
		t.Error("Schema missing 'value' property")
	}
	if _, ok := schema.Properties["compare"]; !ok {
		t.Error("Schema missing 'compare' property")
	}
}

func TestMetricCompareComponent_Render(t *testing.T) {
	c := &metricCompareComponent{}

	tests := []struct {
		name      string
		config    map[string]string
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name:      "basic ASCII",
			config:    map[string]string{"label": "Requests", "value": "1.2k", "compare": "1.0k", "period": "vs last week"},
			format:    ASCII,
			wantASCII: "Requests: 1.2k (1.0k vs last week)",
		},
		{
			name:     "basic HTML",
			config:   map[string]string{"label": "Requests", "value": "1.2k", "compare": "1.0k", "period": "vs last week"},
			format:   HTML,
			wantHTML: "metric-card",
		},
		{
			name:     "increase shows positive",
			config:   map[string]string{"label": "Sales", "value": "150", "compare": "100", "period": "vs yesterday"},
			format:   HTML,
			wantHTML: "positive",
		},
		{
			name:     "decrease shows negative",
			config:   map[string]string{"label": "Sales", "value": "50", "compare": "100", "period": "vs yesterday"},
			format:   HTML,
			wantHTML: "negative",
		},
		{
			name:     "equal shows neutral",
			config:   map[string]string{"label": "Sales", "value": "100", "compare": "100", "period": "vs yesterday"},
			format:   HTML,
			wantHTML: "neutral",
		},
		{
			name:     "non-numeric shows neutral",
			config:   map[string]string{"label": "Status", "value": "OK", "compare": "WARN", "period": "vs yesterday"},
			format:   HTML,
			wantHTML: "neutral",
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

			if tc.wantASCII != "" && out.ASCII != tc.wantASCII {
				t.Errorf("Render() ASCII = %q, want %q", out.ASCII, tc.wantASCII)
			}

			if tc.wantHTML != "" && !strings.Contains(out.HTML, tc.wantHTML) {
				t.Errorf("Render() HTML = %q, want to contain %q", out.HTML, tc.wantHTML)
			}
		})
	}
}

func TestMetricCompareComponent_Render_InvalidJSON(t *testing.T) {
	c := &metricCompareComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}

func TestMetricCompareComponent_CSS(t *testing.T) {
	c := &metricCompareComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() should not be empty")
	}
	if !strings.Contains(css, ".metric-compare") {
		t.Error("CSS() should contain .metric-compare class")
	}
}
