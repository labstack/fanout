package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&histogramComponent{})
}

type histogramComponent struct{}

type histogramConfig struct {
	Label   string    `json:"label"`
	Buckets []float64 `json:"buckets"` // bucket boundaries
	Counts  []int     `json:"counts"`  // count per bucket
}

func (c *histogramComponent) Type() string { return "histogram" }

func (c *histogramComponent) Schema() *Schema {
	return &Schema{
		Description: "Distribution histogram chart",
		Properties: map[string]Property{
			"label":   {Type: "string", Description: "Histogram title"},
			"buckets": {Type: "array", Description: "Bucket boundaries", Items: &Property{Type: "number"}},
			"counts":  {Type: "array", Description: "Count per bucket", Items: &Property{Type: "integer"}},
		},
		Required: []string{"label", "buckets", "counts"},
	}
}

func (c *histogramComponent) CSS() string {
	return `.chart { width: 100%; min-height: 200px; }`
}

func (c *histogramComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg histogramConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	out.Title = cfg.Label

	if len(cfg.Buckets) == 0 || len(cfg.Counts) == 0 {
		if format == ASCII || format == Both {
			out.ASCII = cfg.Label + ": (no data)"
		}
		if format == HTML || format == Both {
			out.HTML = `<sl-card><div slot="header">` + escapeHTML(cfg.Label) + `</div><span>No data</span></sl-card>`
		}
		return out, nil
	}

	if format == ASCII || format == Both {
		maxCount := 0
		for _, cnt := range cfg.Counts {
			if cnt > maxCount {
				maxCount = cnt
			}
		}
		if maxCount == 0 {
			maxCount = 1
		}
		var sb strings.Builder
		sb.WriteString(cfg.Label + "\n")
		for i, cnt := range cfg.Counts {
			label := ""
			if i < len(cfg.Buckets) {
				label = formatFloat(cfg.Buckets[i])
			}
			width := cnt * 20 / maxCount
			bar := strings.Repeat("█", width) + strings.Repeat("░", 20-width)
			sb.WriteString(padRight(label, 8) + " " + bar + " " + itoa(cnt) + "\n")
		}
		out.ASCII = strings.TrimSuffix(sb.String(), "\n")
	}

	if format == HTML || format == Both {
		// Build Vega-Lite spec
		var values []string
		for i, cnt := range cfg.Counts {
			bucket := "?"
			if i < len(cfg.Buckets) {
				bucket = formatFloat(cfg.Buckets[i]) + "ms"
			}
			values = append(values, `{"bucket":"`+bucket+`","count":`+itoa(cnt)+`}`)
		}
		spec := `{"$schema":"https://vega.github.io/schema/vega-lite/v6.json",` +
			`"width":"container","height":180,` +
			`"data":{"values":[` + strings.Join(values, ",") + `]},` +
			`"mark":"bar",` +
			`"encoding":{` +
			`"x":{"field":"bucket","type":"ordinal","title":"Latency","sort":null},` +
			`"y":{"field":"count","type":"quantitative","title":"Count"}}}`
		escaped := strings.ReplaceAll(spec, `"`, `&quot;`)
		out.HTML = `<sl-card><div slot="header">` + escapeHTML(cfg.Label) + `</div>` +
			`<div class="chart" data-vega="` + escaped + `"></div></sl-card>`
	}
	return out, nil
}
