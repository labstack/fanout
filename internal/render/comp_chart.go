package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&chartComponent{})
}

type chartComponent struct{}

type chartConfig struct {
	Title    string          `json:"title"`
	Spec     json.RawMessage `json:"spec,omitempty"`     // Full Vega-Lite spec
	Mark     string          `json:"mark,omitempty"`     // line, bar, point, area
	Data     json.RawMessage `json:"data,omitempty"`     // {values: [...]}
	Encoding json.RawMessage `json:"encoding,omitempty"` // {x: {...}, y: {...}}
	Width    int             `json:"width,omitempty"`
	Height   int             `json:"height,omitempty"`
}

func (c *chartComponent) Type() string { return "chart" }

func (c *chartComponent) Schema() *Schema {
	return &Schema{
		Description: "Vega-Lite chart visualization",
		Properties: map[string]Property{
			"title":    {Type: "string", Description: "Chart title"},
			"spec":     {Type: "object", Description: "Full Vega-Lite specification"},
			"mark":     {Type: "string", Description: "Chart type", Enum: []string{"line", "bar", "point", "area"}},
			"data":     {Type: "object", Description: "Data object with values array"},
			"encoding": {Type: "object", Description: "Vega-Lite encoding specification"},
			"width":    {Type: "integer", Description: "Chart width in pixels"},
			"height":   {Type: "integer", Description: "Chart height in pixels"},
		},
	}
}

func (c *chartComponent) CSS() string { return "" } // chart class defined in histogram

func (c *chartComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg chartConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	out.Title = cfg.Title

	if format == ASCII || format == Both {
		// ASCII representation is limited for charts
		out.ASCII = "[Chart: " + cfg.Title + "]"
		if cfg.Mark != "" {
			out.ASCII = "[" + cfg.Mark + " chart: " + cfg.Title + "]"
		}
	}

	if format == HTML || format == Both {
		var spec json.RawMessage

		if cfg.Spec != nil {
			spec = cfg.Spec
		} else {
			// Build spec from parts
			height := cfg.Height
			if height == 0 {
				height = 200
			}

			built := map[string]any{
				"$schema": "https://vega.github.io/schema/vega-lite/v5.json",
				"width":   "container",
				"height":  height,
			}
			if cfg.Mark != "" {
				built["mark"] = cfg.Mark
			}
			if cfg.Data != nil {
				var data any
				_ = json.Unmarshal(cfg.Data, &data)
				built["data"] = data
			}
			if cfg.Encoding != nil {
				var enc any
				_ = json.Unmarshal(cfg.Encoding, &enc)
				built["encoding"] = enc
			}
			spec, _ = json.Marshal(built)
		}

		escaped := strings.ReplaceAll(string(spec), `"`, `&quot;`)

		html := `<sl-card>`
		if cfg.Title != "" {
			html += `<div slot="header">` + escapeHTML(cfg.Title) + `</div>`
		}
		html += `<div class="chart" data-vega="` + escaped + `"></div></sl-card>`
		out.HTML = html
	}
	return out, nil
}
