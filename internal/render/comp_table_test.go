package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTableComponent_Type(t *testing.T) {
	c := &tableComponent{}
	if c.Type() != "table" {
		t.Errorf("Type() = %q, want %q", c.Type(), "table")
	}
}

func TestTableComponent_Schema(t *testing.T) {
	c := &tableComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["headers"]; !ok {
		t.Error("Schema missing 'headers' property")
	}
	if _, ok := schema.Properties["rows"]; !ok {
		t.Error("Schema missing 'rows' property")
	}
}

func TestTableComponent_CSS(t *testing.T) {
	c := &tableComponent{}
	css := c.CSS()

	if css == "" {
		t.Error("CSS() returned empty string")
	}
	if !strings.Contains(css, ".table") {
		t.Error("CSS missing .table class")
	}
}

func TestTableComponent_Render(t *testing.T) {
	c := &tableComponent{}

	tests := []struct {
		name            string
		config          tableConfig
		format          Format
		wantASCII       string
		wantHTMLContain string
	}{
		{
			name: "basic table ASCII",
			config: tableConfig{
				Headers: []string{"Name", "Value"},
				Rows:    [][]string{{"foo", "100"}, {"bar", "200"}},
			},
			format:    ASCII,
			wantASCII: "│ Name │",
		},
		{
			name: "table with title",
			config: tableConfig{
				Title:   "Test Table",
				Headers: []string{"Col1"},
				Rows:    [][]string{{"data"}},
			},
			format:    ASCII,
			wantASCII: "Test Table",
		},
		{
			name: "empty table",
			config: tableConfig{
				Headers: []string{},
				Rows:    [][]string{},
			},
			format:    ASCII,
			wantASCII: "",
		},
		{
			name: "HTML table",
			config: tableConfig{
				Headers: []string{"Name", "Value"},
				Rows:    [][]string{{"test", "123"}},
			},
			format:          HTML,
			wantHTMLContain: "<table",
		},
		{
			name: "HTML table with title",
			config: tableConfig{
				Title:   "Report",
				Headers: []string{"A"},
				Rows:    [][]string{{"1"}},
			},
			format:          HTML,
			wantHTMLContain: "<caption>Report</caption>",
		},
		{
			name: "truncation",
			config: tableConfig{
				Headers:  []string{"Long Header"},
				Rows:     [][]string{{"This is a very long cell value that should be truncated"}},
				MaxWidth: 10,
			},
			format:    ASCII,
			wantASCII: "│",
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

			if tc.wantHTMLContain != "" && !strings.Contains(out.HTML, tc.wantHTMLContain) {
				t.Errorf("Render() HTML = %q, want to contain %q", out.HTML, tc.wantHTMLContain)
			}
		})
	}
}

func TestTableComponent_Render_InvalidJSON(t *testing.T) {
	c := &tableComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}

func TestTableComponent_RenderBoth(t *testing.T) {
	c := &tableComponent{}
	cfg := tableConfig{
		Headers: []string{"A", "B"},
		Rows:    [][]string{{"1", "2"}},
	}
	cfgJSON, _ := json.Marshal(cfg)
	out, err := c.Render(cfgJSON, Both)
	if err != nil {
		t.Errorf("Render() error = %v", err)
		return
	}

	if out.ASCII == "" {
		t.Error("Both format should produce ASCII output")
	}
	if out.HTML == "" {
		t.Error("Both format should produce HTML output")
	}
}
