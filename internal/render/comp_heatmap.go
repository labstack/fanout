package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&heatmapComponent{})
}

type heatmapComponent struct{}

type heatmapConfig struct {
	Title   string      `json:"title"`
	XLabels []string    `json:"x_labels"` // column labels
	YLabels []string    `json:"y_labels"` // row labels
	Values  [][]float64 `json:"values"`   // [row][col] values
}

func (c *heatmapComponent) Type() string { return "heatmap" }

func (c *heatmapComponent) Schema() *Schema {
	return &Schema{
		Description: "2D heatmap visualization",
		Properties: map[string]Property{
			"title":    {Type: "string", Description: "Heatmap title"},
			"x_labels": {Type: "array", Description: "Column labels", Items: &Property{Type: "string"}},
			"y_labels": {Type: "array", Description: "Row labels", Items: &Property{Type: "string"}},
			"values":   {Type: "array", Description: "2D array of values [row][col]", Items: &Property{Type: "array"}},
		},
		Required: []string{"x_labels", "y_labels", "values"},
	}
}

func (c *heatmapComponent) CSS() string { return "" } // Uses chart class

func (c *heatmapComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg heatmapConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	out.Title = cfg.Title

	if format == ASCII || format == Both {
		chars := []rune{' ', '░', '▒', '▓', '█'}
		var sb strings.Builder
		if cfg.Title != "" {
			sb.WriteString(cfg.Title + "\n")
		}
		// Find max value
		max := 0.0
		for _, row := range cfg.Values {
			for _, v := range row {
				if v > max {
					max = v
				}
			}
		}
		if max == 0 {
			max = 1
		}
		// Header
		sb.WriteString("        ")
		for _, x := range cfg.XLabels {
			sb.WriteString(padRight(truncate(x, 4), 4))
		}
		sb.WriteString("\n")
		// Rows
		for i, row := range cfg.Values {
			label := ""
			if i < len(cfg.YLabels) {
				label = cfg.YLabels[i]
			}
			sb.WriteString(padRight(truncate(label, 8), 8))
			for _, v := range row {
				idx := int(v / max * float64(len(chars)-1))
				sb.WriteRune(chars[idx])
				sb.WriteString("   ")
			}
			sb.WriteString("\n")
		}
		out.ASCII = strings.TrimSuffix(sb.String(), "\n")
	}

	if format == HTML || format == Both {
		// Build Vega-Lite heatmap spec
		var values []string
		for i, row := range cfg.Values {
			yLabel := ""
			if i < len(cfg.YLabels) {
				yLabel = cfg.YLabels[i]
			}
			for j, v := range row {
				xLabel := ""
				if j < len(cfg.XLabels) {
					xLabel = cfg.XLabels[j]
				}
				values = append(values, `{"x":"`+xLabel+`","y":"`+yLabel+`","value":`+formatFloat(v)+`}`)
			}
		}
		spec := `{"$schema":"https://vega.github.io/schema/vega-lite/v6.json",` +
			`"width":"container","height":150,` +
			`"data":{"values":[` + strings.Join(values, ",") + `]},` +
			`"mark":"rect",` +
			`"encoding":{` +
			`"x":{"field":"x","type":"ordinal","title":null},` +
			`"y":{"field":"y","type":"ordinal","title":null},` +
			`"color":{"field":"value","type":"quantitative","scale":{"scheme":"blues"}}}}`
		escaped := strings.ReplaceAll(spec, `"`, `&quot;`)
		title := cfg.Title
		if title == "" {
			title = "Heatmap"
		}
		out.HTML = `<sl-card><div slot="header">` + escapeHTML(title) + `</div>` +
			`<div class="chart" data-vega="` + escaped + `"></div></sl-card>`
	}
	return out, nil
}
