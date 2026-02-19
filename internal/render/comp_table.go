package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&tableComponent{})
}

type tableComponent struct{}

type tableConfig struct {
	Title    string     `json:"title"`
	Headers  []string   `json:"headers"`
	Rows     [][]string `json:"rows"`
	MaxWidth int        `json:"max_width"`
}

func (c *tableComponent) Type() string { return "table" }

func (c *tableComponent) Schema() *Schema {
	return &Schema{
		Description: "Data table with headers and rows",
		Properties: map[string]Property{
			"title":     {Type: "string", Description: "Table title"},
			"headers":   {Type: "array", Description: "Column headers", Items: &Property{Type: "string"}},
			"rows":      {Type: "array", Description: "Table rows (array of arrays)", Items: &Property{Type: "array"}},
			"max_width": {Type: "integer", Description: "Maximum column width for truncation"},
		},
		Required: []string{"headers", "rows"},
	}
}

func (c *tableComponent) CSS() string {
	return `
.table {
	width: 100%;
	border-collapse: collapse;
	font-size: 0.875rem;
}
.table th, .table td {
	padding: 0.75rem;
	text-align: left;
	border-bottom: 1px solid var(--border-color);
	max-width: 300px;
	overflow: hidden;
	text-overflow: ellipsis;
	white-space: nowrap;
}
.table th {
	font-weight: 600;
	color: var(--text-secondary);
	font-size: 0.75rem;
	text-transform: uppercase;
}
.table tr:hover td { background: var(--bg-tertiary); }
.table a { color: var(--accent); text-decoration: none; }
.table a:hover { text-decoration: underline; }`
}

func (c *tableComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg tableConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	if cfg.MaxWidth == 0 {
		cfg.MaxWidth = 40
	}

	var out Output
	out.Title = cfg.Title

	if format == ASCII || format == Both {
		out.ASCII = c.renderASCII(cfg)
	}
	if format == HTML || format == Both {
		out.HTML = c.renderHTML(cfg)
	}
	return out, nil
}

func (c *tableComponent) renderASCII(cfg tableConfig) string {
	if len(cfg.Headers) == 0 && len(cfg.Rows) == 0 {
		return ""
	}

	// Calculate column widths
	widths := make([]int, len(cfg.Headers))
	for i, h := range cfg.Headers {
		widths[i] = len(h)
	}
	for _, row := range cfg.Rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Apply max width
	for i := range widths {
		if widths[i] > cfg.MaxWidth {
			widths[i] = cfg.MaxWidth
		}
	}

	var sb strings.Builder
	if cfg.Title != "" {
		sb.WriteString(cfg.Title + "\n")
	}

	// Top border
	sb.WriteString("┌")
	for i, w := range widths {
		sb.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			sb.WriteString("┬")
		}
	}
	sb.WriteString("┐\n")

	// Headers
	sb.WriteString("│")
	for i, h := range cfg.Headers {
		sb.WriteString(" " + padRight(truncate(h, widths[i]), widths[i]) + " │")
	}
	sb.WriteString("\n")

	// Header separator
	sb.WriteString("├")
	for i, w := range widths {
		sb.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			sb.WriteString("┼")
		}
	}
	sb.WriteString("┤\n")

	// Rows
	for _, row := range cfg.Rows {
		sb.WriteString("│")
		for i, cell := range row {
			if i < len(widths) {
				sb.WriteString(" " + padRight(truncate(cell, widths[i]), widths[i]) + " │")
			}
		}
		sb.WriteString("\n")
	}

	// Bottom border
	sb.WriteString("└")
	for i, w := range widths {
		sb.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			sb.WriteString("┴")
		}
	}
	sb.WriteString("┘")

	return sb.String()
}

func (c *tableComponent) renderHTML(cfg tableConfig) string {
	var sb strings.Builder
	sb.WriteString(`<table class="table">`)
	if cfg.Title != "" {
		sb.WriteString(`<caption>` + escapeHTML(cfg.Title) + `</caption>`)
	}
	sb.WriteString(`<thead><tr>`)
	for _, h := range cfg.Headers {
		sb.WriteString(`<th>` + escapeHTML(h) + `</th>`)
	}
	sb.WriteString(`</tr></thead><tbody>`)
	for _, row := range cfg.Rows {
		sb.WriteString(`<tr>`)
		for _, cell := range row {
			sb.WriteString(`<td>` + escapeHTML(cell) + `</td>`)
		}
		sb.WriteString(`</tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}
