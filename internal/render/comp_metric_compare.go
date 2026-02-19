package render

import "encoding/json"

func init() {
	Register(&metricCompareComponent{})
}

type metricCompareComponent struct{}

type metricCompareConfig struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Compare string `json:"compare"` // previous period value
	Period  string `json:"period"`  // e.g., "vs last week"
}

func (c *metricCompareComponent) Type() string { return "metric-compare" }

func (c *metricCompareComponent) Schema() *Schema {
	return &Schema{
		Description: "Metric with comparison to previous period",
		Properties: map[string]Property{
			"label":   {Type: "string", Description: "Metric name"},
			"value":   {Type: "string", Description: "Current value"},
			"compare": {Type: "string", Description: "Previous period value"},
			"period":  {Type: "string", Description: "Comparison period (e.g., 'vs last week')"},
		},
		Required: []string{"label", "value", "compare"},
	}
}

func (c *metricCompareComponent) CSS() string {
	return `
.metric-compare {
	font-size: 0.75rem;
	margin-top: 0.25rem;
	display: block;
}
.metric-compare.positive { color: var(--success); }
.metric-compare.negative { color: var(--danger); }
.metric-compare.neutral { color: var(--text-muted); }`
}

func (c *metricCompareComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg metricCompareConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	if format == ASCII || format == Both {
		out.ASCII = cfg.Label + ": " + cfg.Value + " (" + cfg.Compare + " " + cfg.Period + ")"
	}
	if format == HTML || format == Both {
		// Parse numeric values for proper comparison
		changeClass := "neutral"
		icon := ""
		curVal, curOk := parseNumericValue(cfg.Value)
		prevVal, prevOk := parseNumericValue(cfg.Compare)
		if curOk && prevOk {
			if curVal > prevVal {
				changeClass = "positive"
				icon = "▲"
			} else if curVal < prevVal {
				changeClass = "negative"
				icon = "▼"
			}
		}
		out.HTML = `<sl-card class="metric-card">` +
			`<span class="metric-value">` + escapeHTML(cfg.Value) + `</span>` +
			`<span class="metric-compare ` + changeClass + `">` + icon + ` ` + escapeHTML(cfg.Compare) + ` ` + escapeHTML(cfg.Period) + `</span>` +
			`<span class="metric-label">` + escapeHTML(cfg.Label) + `</span>` +
			`</sl-card>`
	}
	return out, nil
}
