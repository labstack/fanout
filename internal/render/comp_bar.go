package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&barComponent{})
}

type barComponent struct{}

type barConfig struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Max   float64 `json:"max"`
	Unit  string  `json:"unit"`
}

func (c *barComponent) Type() string { return "bar" }

func (c *barComponent) Schema() *Schema {
	return &Schema{
		Description: "Progress bar with value display",
		Properties: map[string]Property{
			"label": {Type: "string", Description: "Bar label"},
			"value": {Type: "number", Description: "Current value"},
			"max":   {Type: "number", Description: "Maximum value"},
			"unit":  {Type: "string", Description: "Unit suffix"},
		},
		Required: []string{"label", "value", "max"},
	}
}

func (c *barComponent) CSS() string {
	return `
.bar-container {
	display: flex;
	align-items: center;
	gap: 0.75rem;
}
.bar-label {
	min-width: 80px;
	font-size: 0.875rem;
	flex-shrink: 0;
}
.bar-container sl-progress-bar {
	flex: 1;
}
.bar-value {
	min-width: 60px;
	font-size: 0.875rem;
	text-align: right;
	flex-shrink: 0;
}`
}

func (c *barComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg barConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	// Normalize
	if cfg.Max <= 0 {
		cfg.Max = 100
	}
	pct := clamp(cfg.Value/cfg.Max, 0, 1)

	var out Output
	if format == ASCII || format == Both {
		width := 20
		filled := int(pct * float64(width))
		bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
		out.ASCII = cfg.Label + " " + bar + " " + formatFloat(cfg.Value) + cfg.Unit
	}
	if format == HTML || format == Both {
		pctInt := int(pct * 100)
		out.HTML = `<div class="bar-container">` +
			`<span class="bar-label">` + escapeHTML(cfg.Label) + `</span>` +
			`<sl-progress-bar value="` + itoa(pctInt) + `"></sl-progress-bar>` +
			`<span class="bar-value">` + formatFloat(cfg.Value) + escapeHTML(cfg.Unit) + `</span>` +
			`</div>`
	}
	return out, nil
}
