package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SchemaInput struct {
	Window    string `json:"window,omitempty" jsonschema:"5m, 15m, 1h, 3h, 6h, 12h or 24h; default 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Limit discovery to one service namespace"`
}

type PreviewInput struct {
	Panels    []panel.Panel     `json:"panels" jsonschema:"Panels to check and run, in the same format as dashboard panels"`
	Variables []panel.Variable  `json:"variables,omitempty" jsonschema:"Variables the panels reference"`
	Time      *panel.Time       `json:"time,omitempty" jsonschema:"Time range; default the last hour"`
	Vars      map[string]string `json:"vars,omitempty" jsonschema:"Variable values to preview with; $__all selects All"`
}

type PanelPreview struct {
	ID        string          `json:"id"`
	Status    string          `json:"status" jsonschema:"ok, empty, error, invalid or not_run"`
	Rows      int             `json:"rows,omitempty"`
	Columns   []string        `json:"columns,omitempty"`
	Sample    [][]any         `json:"sample,omitempty"`
	Interval  string          `json:"interval,omitempty"`
	Diagnosis string          `json:"diagnosis,omitempty"`
	Error     string          `json:"error,omitempty"`
	Problems  []panel.Problem `json:"problems,omitempty"`
}

type PreviewOutput struct {
	Panels   []PanelPreview  `json:"panels"`
	Problems []panel.Problem `json:"problems,omitempty"`
}

const baseSpecGuide = `A dashboard is {name, description, time:{range}, variables, panels}. Each panel: {id (lowercase), title, viz, width 1-12, query or sql}. viz: stat or gauge (one measure; gauge needs min and max), timeseries (bucket auto, at most one by), bar (one or two by), table (up to three by), text (content in Markdown), heatmap (bucket auto, no by), histogram (at most one by), scatter (exactly two measures in x then y order, one item dimension and an optional colour dimension), state_timeline (one measure, one item dimension, bucket auto and thresholds required). Rollup panel types are service_map (dependency graph) and health (overview metrics, error trend and health distribution). They require query.from=spans, no measures, by, bucket or sort, and only namespace/service equality filters with a literal or single-value variable; explicit casts and values with surrounding whitespace are rejected. Reads default to 400 services/edges and are capped at 400 even when query.limit is larger. Service scope applies before the limit; map nodes carry p95_ms and edges carry average_ms; error_rate is percent. Row panel types are logs, log_patterns and traces. Logs and traces take no measures, by or bucket. Patterns use count() by body_template. Redaction applies before filtering, grouping, display and drill-down. Row panels cannot use SQL. "erroring" means slowest erroring traces: filter to erroring traces, then rank by root duration. Trace candidate ranking is approximate when filters exclude the root span, as with exemplars. Categorical charts use fixed-order colour slots. options.top defaults to 6 and must be at most 6; remaining series become muted Other. Scatter supports options.x_scale and options.y_scale (log or linear), with distinct x_unit for x and unit for y, each inferred independently when omitted. Rows returned use the count unit. State timelines preserve null and absent buckets as unknown and keep the top items without merging them into Other. Item panels require structured queries. SQL panels cannot use scatter or state_timeline. Items rank by volume (row count) for top N. Previous-period comparison is not drawn for scatter or state_timeline. Distribution panels use a structured query with one count() measure and histogram: {field: duration_ms, buckets: log2} for spans or {field: value, buckets: explicit, temporality: cumulative|delta} for metrics. Negative span durations clamp to [0,1); +Inf goes to overflow; NaN is excluded because it is not a measurement. Metric histograms default to cumulative: last minus first per bound, clamped at zero; a series whose counter resets mid-window contributes 0 for that window. Delta histograms sum observation weights. query: {from: spans|logs|metrics, where: [filter expressions], measures: [fn(field) as alias], by: [fields], bucket, sort, limit}. Measures: count(), rate() per second, error_rate() percent, share() percent, avg/min/max/sum(field), last(value) for metrics, p50/p75/p90/p95/p99(field), quantile(field, 0.999), count_distinct(field). Fields are columns from get_telemetry_schema or attributes['key'] / resource['key'] with the key written literally. Filters are SQL boolean expressions: service = $service, kind = 'SPAN_KIND_SERVER', status = 'STATUS_CODE_ERROR', http_route IN $routes, attributes['http.response.status_code']::INTEGER >= 500. Variables: {name, kind: query|custom|constant|text, from, field, default, include_all}; $__all selects All and removes filters that use the variable. sql: one SELECT over spans, logs, metrics, service_rollup or edge_rollup that filters time with $__window(column). Use values exactly as get_telemetry_schema lists them. Example panel: {"id":"latency","title":"Checkout latency","viz":"timeseries","width":8,"query":{"from":"spans","where":["service = 'checkout'","kind = 'SPAN_KIND_SERVER'"],"measures":["p50(duration_ms)","p95(duration_ms)","p99(duration_ms)"],"bucket":"auto"},"unit":"ms","thresholds":[{"value":1500,"status":"bad"}]}.`

func (s *Server) registerPanelTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "get_telemetry_schema", Title: "Telemetry schema",
		Description: "List the signals, columns with their common values, attribute keys seen per service, metric names, services, measure functions and units. Read this before drafting panels so filters use values that exist. See create_dashboard for the spec guide.",
		Annotations: readOnly,
	}, s.telemetrySchema)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "preview_panels", Title: "Preview panels",
		Description: "Check and run panels without saving them. Each panel reports ok with rows and a sample, empty with the reason, error, or invalid with the exact field and a suggestion. Fix every invalid panel and replace or explain every empty one before saving. Spec guide: see create_dashboard.",
		Annotations: readOnly,
	}, s.previewPanels)
}

func (s *Server) telemetrySchema(ctx context.Context, _ *mcp.CallToolRequest, input SchemaInput) (*mcp.CallToolResult, *panel.Schema, error) {
	if s.panels == nil {
		return nil, nil, errors.New("panels are unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	schema, err := s.panels.Schema(ctx, panel.SchemaRequest{Window: input.Window, Namespace: input.Namespace})
	if err != nil {
		return nil, nil, safePanelToolError(err)
	}
	attributes := 0
	for _, sig := range schema.Signals {
		attributes += len(sig.Attributes)
	}
	return summary(fmt.Sprintf("%d services, %d attribute keys and %d metric names in the last %s.", len(schema.Services), attributes, len(schema.Signals["metrics"].Metrics), schema.Window)), schema, nil
}

var panelPath = regexp.MustCompile(`^panels\[(\d+)\]`)

func (s *Server) previewPanels(ctx context.Context, _ *mcp.CallToolRequest, input PreviewInput) (*mcp.CallToolResult, PreviewOutput, error) {
	if s.panels == nil {
		return nil, PreviewOutput{}, errors.New("panels are unavailable")
	}
	d := panel.Dashboard{Name: "Preview", Variables: input.Variables, Panels: input.Panels}
	if input.Time != nil {
		d.Time = *input.Time
	}
	vars := map[string]panel.Value{}
	for name, value := range input.Vars {
		if value == panel.AllValue {
			vars[name] = panel.Value{All: true}
		} else {
			vars[name] = panel.Value{Values: []string{value}}
		}
	}
	out := PreviewOutput{Panels: make([]PanelPreview, len(input.Panels))}
	for i, p := range input.Panels {
		out.Panels[i] = PanelPreview{ID: p.ID, Status: "not_run"}
	}
	results, err := s.panels.Run(ctx, panel.RunRequest{Dashboard: d, Vars: vars})
	var problems panel.Problems
	if errors.As(err, &problems) {
		for _, problem := range problems {
			if m := panelPath.FindStringSubmatch(problem.Path); m != nil {
				i, _ := strconv.Atoi(m[1])
				if i < len(out.Panels) {
					out.Panels[i].Status = "invalid"
					out.Panels[i].Problems = append(out.Panels[i].Problems, problem)
					continue
				}
			}
			out.Problems = append(out.Problems, problem)
		}
		return summary(previewSummary(out)), out, nil
	}
	if err != nil {
		return nil, PreviewOutput{}, safePanelToolError(err)
	}
	for i, r := range results {
		preview := PanelPreview{ID: r.ID, Status: r.Status, Interval: r.Interval, Diagnosis: r.Diagnosis, Error: r.Error}
		if r.Frame != nil {
			preview.Rows = r.Frame.Rows
			for _, c := range r.Frame.Columns {
				preview.Columns = append(preview.Columns, c.Name)
			}
			for row := 0; row < min(3, r.Frame.Rows); row++ {
				sample := make([]any, len(r.Frame.Columns))
				for c := range r.Frame.Columns {
					sample[c] = r.Frame.Values[c][row]
				}
				preview.Sample = append(preview.Sample, sample)
			}
		}
		out.Panels[i] = preview
	}
	return summary(previewSummary(out)), out, nil
}

func previewSummary(out PreviewOutput) string {
	counts := map[string]int{}
	var notes []string
	for _, p := range out.Panels {
		counts[p.Status]++
		switch p.Status {
		case "invalid":
			for _, problem := range p.Problems {
				notes = append(notes, fmt.Sprintf("%s: %s", p.ID, strings.TrimSpace(problem.Message+" "+problem.Hint)))
			}
		case "empty":
			notes = append(notes, fmt.Sprintf("%s is empty: %s", p.ID, p.Diagnosis))
		case "error":
			notes = append(notes, fmt.Sprintf("%s failed: %s", p.ID, p.Error))
		}
	}
	for _, problem := range out.Problems {
		notes = append(notes, problem.Path+": "+problem.Message)
	}
	text := fmt.Sprintf("%d ok, %d empty, %d error, %d invalid, %d not run.", counts["ok"], counts["empty"], counts["error"], counts["invalid"], counts["not_run"])
	if len(notes) > 0 {
		text += " " + strings.Join(notes, " ")
	}
	return text
}

// Preserve cancellation identity while keeping wrapped engine paths private.
type redactedToolError struct {
	cause   error
	message string
}

func (e redactedToolError) Error() string { return e.message }
func (e redactedToolError) Unwrap() error { return e.cause }
func safePanelToolError(err error) error {
	return redactedToolError{cause: err, message: panel.SafeError(err)}
}
