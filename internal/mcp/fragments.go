package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type QueryTelemetryInput struct {
	Panel     panel.Panel            `json:"panel" jsonschema:"One v1 panel to display in the answer"`
	Variables []panel.Variable       `json:"variables,omitempty"`
	Time      *panel.Time            `json:"time,omitempty"`
	Vars      map[string]panel.Value `json:"vars,omitempty"`
}
type PanelFragment struct {
	Dashboard panel.Dashboard                                  `json:"dashboard"`
	Results   []panel.Result                                   `json:"results"`
	Vars      map[string]panel.Value                           `json:"vars,omitempty"`
	Trace     *observability.Result[observability.TraceDetail] `json:"trace,omitempty"`
}

// The published schema deliberately has no subset selector. Raw input is
// checked by middleware before the SDK decodes this type.
type fragmentInput struct {
	Dashboard panel.Dashboard        `json:"dashboard"`
	Time      *panel.Time            `json:"time,omitempty"`
	Vars      map[string]panel.Value `json:"vars,omitempty"`
	Widths    map[string]int         `json:"widths,omitempty"`
	Compare   *bool                  `json:"compare,omitempty"`
}
type variableOptions struct {
	Options map[string][]panel.Option `json:"options"`
}

// Value's wire form is scalar/list JSON, not the fields of its Go struct.
// Supply the same override to input and output schema inference.
func fragmentTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	options := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[panel.Value](): {AnyOf: []*jsonschema.Schema{{Type: "string"}, {Type: "array", Items: &jsonschema.Schema{Type: "string"}}}},
	}}
	input, err := jsonschema.For[In](options)
	if err != nil {
		panic(err)
	}
	output, err := jsonschema.For[Out](options)
	if err != nil {
		panic(err)
	}
	tool.InputSchema = input
	tool.OutputSchema = output
	mcp.AddTool(server, tool, handler)
}

func (s *Server) runFragment(ctx context.Context, req panel.RunRequest) (*mcp.CallToolResult, PanelFragment, error) {
	out, err := s.executeFragment(ctx, req)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	out = boundFragment(out)
	return summary(fragmentSummary(out.Results)), out, nil
}

func (s *Server) executeFragment(ctx context.Context, req panel.RunRequest) (PanelFragment, error) {
	if s.panels == nil {
		return PanelFragment{}, errors.New("panels are unavailable")
	}
	// Detach the authored spec before normalization; executor/caller state must
	// never be changed by the transport adapter.
	raw, err := json.Marshal(req)
	if err != nil {
		return PanelFragment{}, err
	}
	var detached panel.RunRequest
	if err = json.Unmarshal(raw, &detached); err != nil {
		return PanelFragment{}, err
	}
	req = detached
	panel.Normalize(&req.Dashboard)
	if req.Time != nil {
		req.Dashboard.Time = *req.Time
		req.Time = nil
	}
	if req.Compare != nil {
		req.Dashboard.Time.Compare = ""
		if *req.Compare {
			req.Dashboard.Time.Compare = "previous_period"
		}
	}
	panel.Normalize(&req.Dashboard)
	// Reject oversized authored input before execution, rather than silently
	// changing query semantics to fit the persisted-response budget.
	authored, _ := json.Marshal(PanelFragment{Dashboard: req.Dashboard, Vars: req.Vars, Results: []panel.Result{}})
	if len(authored) > 128*1024 {
		return PanelFragment{}, errors.New("fragment spec and variables exceed the 128 KiB input budget; use fewer panels or shorter queries")
	}
	results, err := s.panels.Run(ctx, req)
	if err != nil {
		return PanelFragment{}, safePanelToolError(err)
	}
	return PanelFragment{Dashboard: req.Dashboard, Results: results, Vars: req.Vars}, nil
}
func (s *Server) queryTelemetry(ctx context.Context, _ *mcp.CallToolRequest, input QueryTelemetryInput) (*mcp.CallToolResult, PanelFragment, error) {
	d := panel.Dashboard{Name: input.Panel.Title, Variables: input.Variables, Panels: []panel.Panel{input.Panel}}
	return s.runFragment(ctx, panel.RunRequest{Dashboard: d, Time: input.Time, Vars: input.Vars})
}
func (s *Server) queryPanelFragment(ctx context.Context, _ *mcp.CallToolRequest, input panel.RunRequest) (*mcp.CallToolResult, PanelFragment, error) {
	if input.Panels != nil {
		return nil, PanelFragment{}, errors.New("panels is forbidden for query_panel_fragment")
	}
	return s.runFragment(ctx, input)
}
func (s *Server) panelExemplars(ctx context.Context, _ *mcp.CallToolRequest, input panel.ExemplarRequest) (*mcp.CallToolResult, panel.ExemplarResponse, error) {
	if s.panels == nil {
		return nil, panel.ExemplarResponse{}, errors.New("panels are unavailable")
	}
	out, err := s.panels.Exemplars(ctx, input)
	if err != nil {
		return nil, out, safePanelToolError(err)
	}
	return summary(fmt.Sprintf("%d exemplar traces.", len(out.Traces))), out, nil
}
func (s *Server) resolvePanelVariables(ctx context.Context, _ *mcp.CallToolRequest, input panel.ResolveRequest) (*mcp.CallToolResult, variableOptions, error) {
	if s.panels == nil {
		return nil, variableOptions{}, errors.New("panels are unavailable")
	}
	options, err := s.panels.ResolveVariables(ctx, input)
	if err != nil {
		return nil, variableOptions{}, safePanelToolError(err)
	}
	return summary("Panel variable options."), variableOptions{Options: options}, nil
}

func fragmentPreset(kind, service, namespace, severity, search string, limit int) panel.Dashboard {
	d := panel.Dashboard{Name: "Telemetry", Time: panel.Time{Range: "1h", Refresh: "off"}}
	filters := []string{}
	literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	if namespace != "" {
		filters = append(filters, "namespace = "+literal(namespace))
	}
	if service != "" {
		filters = append(filters, "service = "+literal(service))
	}
	add := func(id, title, viz, signal, measure, unit string, by []string, bucket string) {
		q := &panel.Query{From: signal, Where: append([]string(nil), filters...), By: by, Bucket: bucket}
		if measure != "" {
			q.Measures = []string{measure}
		}
		cap := 400
		if viz == "logs" {
			cap = 100
		}
		if viz == "traces" {
			cap = 50
		}
		q.Limit = cap
		if limit > 0 {
			q.Limit = min(cap, limit)
		}
		d.Panels = append(d.Panels, panel.Panel{ID: id, Title: title, Viz: viz, Unit: unit, Query: q})
	}
	switch kind {
	case "overview":
		add("health", "System health", "health", "spans", "", "", nil, "")
	case "topology":
		add("services", "Service dependencies", "service_map", "spans", "", "", nil, "")
	case "performance":
		add("latency", "p95 latency", "timeseries", "spans", "p95(duration_ms)", "ms", nil, "auto")
		add("errors", "Error rate", "timeseries", "spans", "error_rate()", "percent", nil, "auto")
		add("requests", "Request rate", "timeseries", "spans", "rate()", "per_second", nil, "auto")
		add("endpoints", "Slow endpoints", "table", "spans", "p95(duration_ms)", "ms", []string{"http_route"}, "")
		d.Panels[len(d.Panels)-1].Query.Where = append(d.Panels[len(d.Panels)-1].Query.Where, "http_route <> ''")
	case "logs":
		if severity != "" {
			filters = append(filters, "severity = "+literal(severity))
		}
		add("volume", "Log volume", "timeseries", "logs", "count()", "count", []string{"severity"}, "auto")
		add("events", "Log events", "logs", "logs", "", "", nil, "")
		if search != "" {
			d.Panels[len(d.Panels)-1].Options = &panel.Options{Highlight: search}
			d.Panels[0].Query.Where = append(d.Panels[0].Query.Where, "contains(lower(body),lower("+literal(search)+"))")
		}
	case "trace":
		add("traces", "Slow or erroring traces", "traces", "spans", "", "", nil, "")
	}
	for i := range d.Panels {
		if d.Panels[i].Query.From == "spans" && d.Panels[i].Viz != "health" && d.Panels[i].Viz != "service_map" {
			d.Panels[i].Drill = "traces"
		}
	}
	return d
}

func clip(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	end := max(0, budget-3)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}

// Allocate each panel a share first, so forty wide panels still report every
// status/count/diagnosis. Cells are samples of displayed values, never SQL.
func fragmentSummary(results []panel.Result) string {
	var out strings.Builder
	budget := min(1600, (16*1024)/max(1, len(results))-1)
	for _, r := range results {
		rows, truncated := 0, false
		if r.Frame != nil {
			rows = r.Frame.Rows
			truncated = r.Frame.Truncated
		}
		header := fmt.Sprintf("%s: status=%s rows=%d truncated=%t", clip(r.ID, 80), r.Status, rows, truncated)
		if r.Diagnosis != "" {
			header += " diagnosis=" + clip(r.Diagnosis, 100)
		}
		if r.Error != "" {
			header += " error=" + clip(r.Error, 100)
		}
		text := header
		if r.Frame != nil {
			for row := 0; row < min(5, rows); row++ {
				remaining := budget - len(text) - 8
				if remaining < 16 {
					break
				}
				sample := []string{}
				for c, col := range r.Frame.Columns {
					if c >= len(r.Frame.Values) || row >= len(r.Frame.Values[c]) {
						continue
					}
					cell := clip(fmt.Sprint(r.Frame.Values[c][row]), min(80, max(4, remaining/max(1, len(r.Frame.Columns))-16)))
					sample = append(sample, clip(col.Name, 24)+"="+cell)
				}
				text += "\n  " + clip(strings.Join(sample, ", "), remaining)
			}
		}
		out.WriteString(clip(text, budget))
		out.WriteByte('\n')
	}
	return out.String()
}

const fragmentPayloadLimit = 256 * 1024
const payloadLimitNote = "Rows truncated to stay within the 256 KiB payload limit."

func fragmentBytes(value any) int { raw, _ := json.Marshal(value); return len(raw) }

// Clone first, then drop tails from the largest serialized collection. Stable
// ordering avoids concurrency-dependent chart changes and keeps trends aligned.
func boundFragment(fragment PanelFragment) PanelFragment {
	raw, _ := json.Marshal(fragment)
	var out PanelFragment
	_ = json.Unmarshal(raw, &out)
	for i := range out.Results {
		r := &out.Results[i]
		r.SQL = clip(r.SQL, 1024)
		r.Diagnosis = clip(r.Diagnosis, 512)
		r.Error = clip(r.Error, 512)
		r.AnnotationError = clip(r.AnnotationError, 256)
		for _, f := range []*panel.Frame{r.Frame, r.Previous} {
			if f == nil {
				continue
			}
			f.Note = clip(f.Note, 512)
		}
	}
	if out.Trace != nil {
		out.Trace.Summary = clip(out.Trace.Summary, 512)
	}
	type collection struct {
		key  string
		size int
		cut  func()
	}
	for fragmentBytes(out) > fragmentPayloadLimit {
		var choices []collection
		for i := range out.Results {
			r := &out.Results[i]
			if scope := r.AnnotationScope; scope != nil && len(scope.Services) > 0 {
				choices = append(choices, collection{r.ID + "/annotation_scope", fragmentBytes(scope.Services), func() {
					scope.Services = scope.Services[:len(scope.Services)/2]
					scope.Limited = true
					r.AnnotationError = "Annotation scope truncated to stay within the payload limit."
				}})
			}
			for j, f := range []*panel.Frame{r.Frame, r.Previous} {
				if f == nil {
					continue
				}
				if f.Rows > 0 {
					choices = append(choices, collection{fmt.Sprintf("%s/%d", r.ID, j), fragmentBytes(f), func() {
						rows := f.Rows / 2
						for c := range f.Values {
							f.Values[c] = f.Values[c][:min(rows, len(f.Values[c]))]
						}
						for name, trend := range f.Trends {
							f.Trends[name] = trend[:min(rows, len(trend))]
						}
						f.Rows = rows
						f.Truncated = true
						if !strings.Contains(f.Note, payloadLimitNote) {
							f.Note += " " + payloadLimitNote
						}
					}})
				} else if len(f.Totals) > 0 || len(f.Trends) > 0 || f.Health != nil && len(f.Health.ErrorTrend) > 0 {
					choices = append(choices, collection{fmt.Sprintf("%s/%d/aux", r.ID, j), fragmentBytes(f), func() {
						f.Totals = nil
						f.Trends = nil
						if f.Health != nil {
							f.Health.ErrorTrend = nil
						}
						f.Truncated = true
						f.Note = payloadLimitNote
					}})
				} else if len(f.Columns) > 0 || len(f.Periods) > 0 {
					// Once every row is gone, an adversarially wide column schema
					// can still exceed the cap. Retain the spec and visible note;
					// remove the empty projection as a unit, never mismatched columns.
					choices = append(choices, collection{fmt.Sprintf("%s/%d/metadata", r.ID, j), fragmentBytes(f), func() {
						f.Columns = []panel.Column{}
						f.Values = [][]any{}
						f.Periods = nil
						f.Truncated = true
						f.Note = payloadLimitNote
					}})
				}
			}
		}
		if out.Trace != nil {
			d := &out.Trace.Data
			mark := func() {
				d.Truncated = true
				out.Trace.Provenance.Complete = false
				if !strings.Contains(out.Trace.Summary, payloadLimitNote) {
					out.Trace.Summary += " " + payloadLimitNote
				}
			}
			if len(d.Spans) > 0 {
				choices = append(choices, collection{"~trace/spans", fragmentBytes(d.Spans), func() { d.Spans = d.Spans[:len(d.Spans)/2]; mark() }})
			}
			if len(d.Logs) > 0 {
				choices = append(choices, collection{"~trace/logs", fragmentBytes(d.Logs), func() { d.Logs = d.Logs[:len(d.Logs)/2]; mark() }})
			}
			if len(d.Services) > 0 {
				choices = append(choices, collection{"~trace/services", fragmentBytes(d.Services), func() { d.Services = d.Services[:len(d.Services)/2]; mark() }})
			}
		}
		if len(choices) == 0 {
			break
		} // fixed input is bounded before execution
		sort.Slice(choices, func(i, j int) bool {
			if choices[i].size == choices[j].size {
				return choices[i].key < choices[j].key
			}
			return choices[i].size > choices[j].size
		})
		choices[0].cut()
	}
	return out
}
