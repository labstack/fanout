package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&sparklineComponent{})
}

type sparklineComponent struct{}

type sparklineConfig struct {
	Label  string    `json:"label"`
	Values []float64 `json:"values"`
}

func (c *sparklineComponent) Type() string { return "sparkline" }

func (c *sparklineComponent) Schema() *Schema {
	return &Schema{
		Description: "Mini inline chart showing trend",
		Properties: map[string]Property{
			"label":  {Type: "string", Description: "Sparkline label"},
			"values": {Type: "array", Description: "Data points", Items: &Property{Type: "number"}},
		},
		Required: []string{"label", "values"},
	}
}

func (c *sparklineComponent) CSS() string {
	return `
.sparkline {
	display: flex;
	align-items: center;
	gap: 0.5rem;
}
.sparkline svg {
	vertical-align: middle;
}`
}

func (c *sparklineComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg sparklineConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	out.Title = cfg.Label

	if len(cfg.Values) == 0 {
		if format == ASCII || format == Both {
			out.ASCII = cfg.Label + " ─"
		}
		if format == HTML || format == Both {
			out.HTML = `<span>` + escapeHTML(cfg.Label) + `</span>`
		}
		return out, nil
	}

	// Find min/max
	min, max := cfg.Values[0], cfg.Values[0]
	for _, v := range cfg.Values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	rng := max - min
	if rng == 0 {
		rng = 1
	}

	if format == ASCII || format == Both {
		chars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
		var sb strings.Builder
		sb.WriteString(cfg.Label + " ")
		for _, v := range cfg.Values {
			idx := int((v - min) / rng * float64(len(chars)-1))
			sb.WriteRune(chars[idx])
		}
		out.ASCII = sb.String()
	}

	if format == HTML || format == Both {
		width := 100
		height := 20
		var points strings.Builder
		for i, v := range cfg.Values {
			x := float64(i) / float64(len(cfg.Values)-1) * float64(width)
			y := float64(height) - (v-min)/rng*float64(height)
			if i > 0 {
				points.WriteString(" ")
			}
			points.WriteString(formatFloat(x) + "," + formatFloat(y))
		}
		out.HTML = `<div class="sparkline">` +
			`<span>` + escapeHTML(cfg.Label) + `</span>` +
			`<svg width="` + itoa(width) + `" height="` + itoa(height) + `">` +
			`<polyline points="` + points.String() + `" fill="none" stroke="var(--accent)" stroke-width="1.5"/>` +
			`</svg></div>`
	}
	return out, nil
}
