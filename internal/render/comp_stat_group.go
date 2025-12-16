package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&statGroupComponent{})
}

type statGroupComponent struct{}

type statGroupConfig struct {
	Title string           `json:"title"`
	Stats []statItemConfig `json:"stats"`
}

type statItemConfig struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func (c *statGroupComponent) Type() string { return "stat-group" }

func (c *statGroupComponent) Schema() *Schema {
	return &Schema{
		Description: "Group of related statistics in a card",
		Properties: map[string]Property{
			"title": {Type: "string", Description: "Card title"},
			"stats": {
				Type:        "array",
				Description: "Statistics to display",
				Items: &Property{
					Type: "object",
					Properties: map[string]Property{
						"label": {Type: "string", Description: "Stat label"},
						"value": {Type: "string", Description: "Stat value"},
					},
				},
			},
		},
		Required: []string{"stats"},
	}
}

func (c *statGroupComponent) CSS() string {
	return `
.stat-group .stat-grid {
	display: grid;
	grid-template-columns: repeat(auto-fit, minmax(80px, 1fr));
	gap: 1rem;
	text-align: center;
}
.stat-item {
	display: flex;
	flex-direction: column;
	align-items: center;
}
.stat-value {
	font-size: 1.25rem;
	font-weight: 700;
	color: var(--text-primary);
	display: block;
}
.stat-label {
	font-size: 0.75rem;
	color: var(--text-muted);
	text-transform: uppercase;
	display: block;
	margin-top: 0.25rem;
}`
}

func (c *statGroupComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg statGroupConfig
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
		for _, stat := range cfg.Stats {
			sb.WriteString("  " + stat.Label + ": " + stat.Value + "\n")
		}
		out.ASCII = strings.TrimSuffix(sb.String(), "\n")
	}

	if format == HTML || format == Both {
		var sb strings.Builder
		sb.WriteString(`<sl-card class="stat-group">`)
		if cfg.Title != "" {
			sb.WriteString(`<div slot="header">` + escapeHTML(cfg.Title) + `</div>`)
		}
		sb.WriteString(`<div class="stat-grid">`)
		for _, stat := range cfg.Stats {
			sb.WriteString(`<div class="stat-item">`)
			sb.WriteString(`<span class="stat-value">` + escapeHTML(stat.Value) + `</span>`)
			sb.WriteString(`<span class="stat-label">` + escapeHTML(stat.Label) + `</span>`)
			sb.WriteString(`</div>`)
		}
		sb.WriteString(`</div></sl-card>`)
		out.HTML = sb.String()
	}
	return out, nil
}
