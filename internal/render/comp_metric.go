package render

import "encoding/json"

func init() {
	Register(&metricComponent{})
}

type metricComponent struct{}

type metricConfig struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit"`
	Trend string `json:"trend"` // up, down, stable
}

func (c *metricComponent) Type() string { return "metric" }

func (c *metricComponent) Schema() *Schema {
	return &Schema{
		Description: "Single metric display with optional trend indicator",
		Properties: map[string]Property{
			"label": {Type: "string", Description: "Metric name"},
			"value": {Type: "string", Description: "Metric value"},
			"unit":  {Type: "string", Description: "Unit suffix (e.g., 'ms', '%')"},
			"trend": {Type: "string", Description: "Trend direction", Enum: []string{"up", "down", "stable"}},
		},
		Required: []string{"label", "value"},
	}
}

func (c *metricComponent) CSS() string {
	return `
.metric-card {
	text-align: center;
	display: flex;
	flex-direction: column;
	align-items: center;
	justify-content: center;
	padding: 1rem;
}
.metric-card::part(body) {
	display: flex;
	flex-direction: column;
	align-items: center;
}
.metric-value {
	font-size: 2rem;
	font-weight: 700;
	color: var(--text-primary);
	line-height: 1;
	display: block;
}
.metric-label {
	font-size: 0.75rem;
	color: var(--text-muted);
	margin-top: 0.5rem;
	text-transform: uppercase;
	letter-spacing: 0.05em;
	display: block;
}`
}

func (c *metricComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg metricConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	if format == ASCII || format == Both {
		trend := ""
		switch cfg.Trend {
		case "up":
			trend = " ↑"
		case "down":
			trend = " ↓"
		}
		out.ASCII = cfg.Label + ": " + cfg.Value + cfg.Unit + trend
	}
	if format == HTML || format == Both {
		trend := ""
		switch cfg.Trend {
		case "up":
			trend = `<sl-icon name="arrow-up"></sl-icon>`
		case "down":
			trend = `<sl-icon name="arrow-down"></sl-icon>`
		}
		out.HTML = `<sl-card class="metric-card">` +
			`<span class="metric-value">` + escapeHTML(cfg.Value+cfg.Unit) + `</span>` +
			trend +
			`<span class="metric-label">` + escapeHTML(cfg.Label) + `</span>` +
			`</sl-card>`
	}
	return out, nil
}
