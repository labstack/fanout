package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&diffComponent{})
}

type diffComponent struct{}

type diffConfig struct {
	Title string          `json:"title"`
	Rows  []diffRowConfig `json:"rows"`
}

type diffRowConfig struct {
	Metric string `json:"metric"`
	Before string `json:"before"`
	After  string `json:"after"`
}

func (c *diffComponent) Type() string { return "diff" }

func (c *diffComponent) Schema() *Schema {
	return &Schema{
		Description: "Before/after comparison table",
		Properties: map[string]Property{
			"title": {Type: "string", Description: "Comparison title"},
			"rows": {
				Type:        "array",
				Description: "Comparison rows",
				Items: &Property{
					Type: "object",
					Properties: map[string]Property{
						"metric": {Type: "string", Description: "Metric name"},
						"before": {Type: "string", Description: "Previous value"},
						"after":  {Type: "string", Description: "Current value"},
					},
				},
			},
		},
		Required: []string{"rows"},
	}
}

func (c *diffComponent) CSS() string {
	return `
.diff-table .diff-before { color: var(--text-muted); }
.diff-table .diff-after { font-weight: 600; }
.diff-table .text-success { color: var(--success); }
.diff-table .text-danger { color: var(--danger); }
.diff-improved td { background: rgba(34, 197, 94, 0.1); }
.diff-regressed td { background: rgba(239, 68, 68, 0.1); }`
}

func (c *diffComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg diffConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	out.Title = cfg.Title

	if format == ASCII || format == Both {
		var sb strings.Builder
		if cfg.Title != "" {
			sb.WriteString(cfg.Title + "\n")
		}
		sb.WriteString("Metric          Before      After       Change\n")
		sb.WriteString("─────────────────────────────────────────────\n")
		for _, r := range cfg.Rows {
			sb.WriteString(padRight(truncate(r.Metric, 15), 15) + " ")
			sb.WriteString(padRight(truncate(r.Before, 11), 11) + " ")
			sb.WriteString(padRight(truncate(r.After, 11), 11) + "\n")
		}
		out.ASCII = strings.TrimSuffix(sb.String(), "\n")
	}

	if format == HTML || format == Both {
		var sb strings.Builder
		sb.WriteString(`<sl-card>`)
		if cfg.Title != "" {
			sb.WriteString(`<div slot="header">` + escapeHTML(cfg.Title) + `</div>`)
		}
		sb.WriteString(`<table class="table diff-table"><thead><tr>`)
		sb.WriteString(`<th>Metric</th><th>Before</th><th>After</th><th>Change</th>`)
		sb.WriteString(`</tr></thead><tbody>`)
		for _, r := range cfg.Rows {
			changeClass := ""
			changeIcon := ""

			// Parse numeric values for proper comparison
			beforeVal, beforeOk := parseNumericValue(r.Before)
			afterVal, afterOk := parseNumericValue(r.After)
			if beforeOk && afterOk && beforeVal != afterVal {
				// For metrics like latency/errors, lower is better
				if afterVal < beforeVal {
					changeClass = "diff-improved"
					changeIcon = `<sl-icon name="arrow-down" class="text-success"></sl-icon>`
				} else {
					changeClass = "diff-regressed"
					changeIcon = `<sl-icon name="arrow-up" class="text-danger"></sl-icon>`
				}
			}

			sb.WriteString(`<tr class="` + changeClass + `">`)
			sb.WriteString(`<td>` + escapeHTML(r.Metric) + `</td>`)
			sb.WriteString(`<td class="diff-before">` + escapeHTML(r.Before) + `</td>`)
			sb.WriteString(`<td class="diff-after">` + escapeHTML(r.After) + `</td>`)
			sb.WriteString(`<td>` + changeIcon + `</td>`)
			sb.WriteString(`</tr>`)
		}
		sb.WriteString(`</tbody></table></sl-card>`)
		out.HTML = sb.String()
	}
	return out, nil
}
