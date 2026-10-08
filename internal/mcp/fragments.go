package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type QueryTelemetryInput struct {
	Panel     panel.Panel            `json:"panel" jsonschema:"One v1 panel to display in the answer"`
	Variables []panel.Variable       `json:"variables,omitempty" jsonschema:"Optional v1 variable definitions used by this panel"`
	Time      *panel.Time            `json:"time,omitempty" jsonschema:"Optional dashboard time override: exact from/to or a relative range"`
	Vars      map[string]panel.Value `json:"vars,omitempty" jsonschema:"Resolved variable values: strings or lists; preserves $__all and empty lists"`
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
	Dashboard panel.Dashboard        `json:"dashboard" jsonschema:"Complete v1 dashboard specification; every panel is executed"`
	Time      *panel.Time            `json:"time,omitempty" jsonschema:"Optional dashboard time override: exact from/to or a relative range"`
	Vars      map[string]panel.Value `json:"vars,omitempty" jsonschema:"Resolved variable values: strings or lists; preserves $__all and empty lists"`
	Widths    map[string]int         `json:"widths,omitempty" jsonschema:"Optional panel widths in pixels, keyed by panel ID"`
	Compare   *bool                  `json:"compare,omitempty" jsonschema:"Override previous-period comparison for the complete fragment"`
}
type variableOptions struct {
	Options map[string][]panel.Option `json:"options"`
}

// Value's wire form is scalar/list JSON, not the fields of its Go struct.
// Supply the same override to input and output schema inference.
func fragmentTool[In, Out any](server *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
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
	server.limitTools[tool.Name] = input.Properties["limit"] != nil
	mcp.AddTool(server.mcp, tool, handler)
}

func (s *Server) runFragment(ctx context.Context, req panel.RunRequest) (*mcp.CallToolResult, PanelFragment, error) {
	out, err := s.executeFragment(ctx, req)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	out, err = boundFragment(ctx, out)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	return summary(fragmentSummary(out)), out, nil
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
	authored, err := json.Marshal(PanelFragment{Dashboard: req.Dashboard, Vars: req.Vars, Results: []panel.Result{}})
	if err != nil {
		return PanelFragment{}, fmt.Errorf("encode authored fragment: %w", err)
	}
	if len(authored) > 128*1024 {
		return PanelFragment{}, fmt.Errorf("fragment spec and variables exceed the 128 KiB input budget (limit %d bytes, actual %d bytes); use fewer panels or shorter queries", 128*1024, len(authored))
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
	d := panel.Dashboard{Name: map[string]string{"overview": "System health", "topology": "Service dependencies", "performance": "Service performance", "logs": "Log explorer", "trace": "Trace detail"}[kind], Time: panel.Time{Range: "1h", Refresh: "off"}}
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
	if budget < 3 {
		end := max(0, budget)
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		return text[:end]
	}
	end := max(0, budget-3)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}

// Summaries describe the rendered fragment, including rollup and trace metadata.
// Give each panel a share so every status survives even on a wide dashboard.
func fragmentSummary(fragment PanelFragment) string {
	var out strings.Builder
	if fragment.Trace != nil {
		d := fragment.Trace.Data
		root := "unknown"
		for _, span := range d.Spans {
			if span.ParentSpanID == "" {
				root = span.Operation
				break
			}
		}
		status := "OK"
		if d.HasError {
			status = "Error"
		}
		fmt.Fprintf(&out, "Trace %s: root=%s duration=%s ms status=%s spans=%d truncated=%t\n", clip(d.TraceID, 80), clip(root, 160), number(d.DurationMS), status, d.SpanCount, d.Truncated)
		spans := append([]observability.TraceSpan(nil), d.Spans...)
		sort.SliceStable(spans, func(i, j int) bool { return spans[i].DurationMS > spans[j].DurationMS })
		for _, kind := range []string{"slowest", "erroring"} {
			count := 0
			for _, span := range spans {
				if kind == "erroring" && !strings.Contains(strings.ToUpper(span.Status), "ERROR") {
					continue
				}
				fmt.Fprintf(&out, "  %s: %s / %s duration=%s ms status=%s start=%s\n", kind, clip(span.Service, 80), clip(span.Operation, 160), number(span.DurationMS), clip(span.Status, 80), span.Start.UTC().Format(time.RFC3339Nano))
				count++
				if count == 5 {
					break
				}
			}
		}
	}
	budget := min(2400, (16*1024-out.Len())/max(1, len(fragment.Results))-1)
	for _, r := range fragment.Results {
		rows, truncated := 0, false
		if r.Frame != nil {
			rows = r.Frame.Rows
			truncated = r.Frame.Truncated
		}
		text := fmt.Sprintf("%s: status=%s rows=%d truncated=%t", clip(r.ID, 80), r.Status, rows, truncated)
		if r.Diagnosis != "" {
			text += " diagnosis=" + clip(r.Diagnosis, 100)
		}
		if r.Error != "" {
			text += " error=" + clip(r.Error, 100)
		}
		if f := r.Frame; f != nil {
			if h := f.Health; h != nil {
				text += fmt.Sprintf("\n  health=%s healthy=%d degraded=%d unhealthy=%d services=%d spans=%d error_rate=%s%%", h.Health, h.Counts.Healthy, h.Counts.Degraded, h.Counts.Unhealthy, h.ServiceCount, h.TotalSpans, number(h.ErrorRate))
			}
			indices := make([]int, rows)
			for row := range rows {
				indices[row] = row
			}
			cell := func(row int, name string) any {
				for c, col := range f.Columns {
					if col.Name == name && c < len(f.Values) && row < len(f.Values[c]) {
						return f.Values[c][row]
					}
				}
				return nil
			}
			limit := 5
			// Edges must not disappear behind a node-heavy projection.
			topology := false
			for _, p := range fragment.Dashboard.Panels {
				if p.ID == r.ID && p.Viz == "service_map" {
					topology = true
				}
			}
			if topology {
				indices = indices[:0]
				for row := range rows {
					if cell(row, "kind") == "edge" {
						indices = append(indices, row)
					}
				}
				limit = 10
				sort.SliceStable(indices, func(i, j int) bool {
					a, b := indices[i], indices[j]
					ae, be := numeric(cell(a, "error_rate")), numeric(cell(b, "error_rate"))
					if ae != be {
						return ae > be
					}
					return numeric(cell(a, "calls")) > numeric(cell(b, "calls"))
				})
			} else if f.Health != nil {
				rank := func(row int) int {
					switch cell(row, "health") {
					case "unhealthy":
						return 2
					case "degraded":
						return 1
					}
					return 0
				}
				sort.SliceStable(indices, func(i, j int) bool {
					a, b := indices[i], indices[j]
					if rank(a) != rank(b) {
						return rank(a) > rank(b)
					}
					return numeric(cell(a, "error_rate")) > numeric(cell(b, "error_rate"))
				})
			}
			for _, row := range indices[:min(limit, len(indices))] {
				sample := []string{}
				for c, col := range f.Columns {
					if c >= len(f.Values) || row >= len(f.Values[c]) || f.Values[c][row] == nil || f.Values[c][row] == "" {
						continue
					}
					sample = append(sample, clip(col.Name, 24)+"="+clip(summaryCell(col, f.Values[c][row]), 160))
				}
				text += "\n  " + strings.Join(sample, ", ")
			}
		}
		out.WriteString(clip(text, max(0, budget)))
		out.WriteByte('\n')
	}
	return clip(out.String(), 16*1024)
}
func number(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
func numeric(value any) float64 {
	switch v := value.(type) {
	case json.Number:
		n, _ := v.Float64()
		return n
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}
func summaryCell(col panel.Column, value any) string {
	if col.Type == "time" {
		switch v := value.(type) {
		case time.Time:
			return v.UTC().Format(time.RFC3339Nano)
		case string:
			if at, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return at.UTC().Format(time.RFC3339Nano)
			}
		}
		return time.UnixMilli(int64(numeric(value))).UTC().Format(time.RFC3339Nano)
	}
	text := fmt.Sprint(value)
	if col.Type == "number" {
		text = number(numeric(value))
		if v, ok := value.(json.Number); ok {
			text = v.String()
		}
		if v, ok := value.(int64); ok {
			text = strconv.FormatInt(v, 10)
		}
	}
	switch col.Unit {
	case "percent":
		text += "%"
	case "per_second":
		text += "/s"
	case "per_minute":
		text += "/min"
	case "ratio":
		text += "×"
	case "ms", "s", "ns", "bytes":
		text += " " + col.Unit
	case "count":
		text += " count"
	}
	return text
}

const fragmentPayloadLimit = 256 * 1024
const payloadLimitNote = "Rows truncated to stay within the 256 KiB payload limit."

// Serialized string budgets include quotes and JSON escaping, unlike raw bytes.
func clipJSON(text string, budget int) string {
	raw, _ := json.Marshal(text)
	if len(raw) <= budget {
		return text
	}
	lo, hi := 0, len(text)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		end := mid
		for end > 0 && end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		raw, _ = json.Marshal(text[:end] + "…")
		if len(raw) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	for lo > 0 && lo < len(text) && !utf8.RuneStart(text[lo]) {
		lo--
	}
	if budget < 5 {
		return ""
	}
	return text[:lo] + "…"
}
func jsonBytes(value any) (int, error) { raw, err := json.Marshal(value); return len(raw), err }

// Measure each collection once and proportionally drop the excess from the
// largest first, leaving small panels intact. Remeasure the fragment once after
// cutting; a final hard check handles oversized metadata.
func boundFragment(ctx context.Context, fragment PanelFragment) (PanelFragment, error) {
	if err := ctx.Err(); err != nil {
		return PanelFragment{}, err
	}
	raw, err := json.Marshal(fragment)
	if err != nil {
		return PanelFragment{}, fmt.Errorf("encode panel fragment: %w", err)
	}
	var out PanelFragment
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&out); err != nil {
		return PanelFragment{}, fmt.Errorf("clone panel fragment: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return PanelFragment{}, err
	}
	for i := range out.Results {
		r := &out.Results[i]
		r.SQL = clipJSON(r.SQL, 1024)
		r.Diagnosis = clipJSON(r.Diagnosis, 512)
		r.Error = clipJSON(r.Error, 512)
		r.AnnotationError = clipJSON(r.AnnotationError, 256)
		for _, f := range []*panel.Frame{r.Frame, r.Previous} {
			if f != nil {
				f.Note = clipJSON(f.Note, 512)
			}
		}
	}
	if out.Trace != nil {
		out.Trace.Summary = clipJSON(out.Trace.Summary, 512)
	}
	size, err := jsonBytes(out)
	if err != nil {
		return PanelFragment{}, err
	}
	if size <= fragmentPayloadLimit {
		return out, nil
	}
	type collection struct {
		key  string
		size int
		cut  func(float64)
	}
	choices := []collection{}
	add := func(key string, value any, cut func(float64)) error {
		n, err := jsonBytes(value)
		if err != nil {
			return err
		}
		choices = append(choices, collection{key, n, cut})
		return nil
	}
	for i := range out.Results {
		if err := ctx.Err(); err != nil {
			return PanelFragment{}, err
		}
		r := &out.Results[i]
		if scope := r.AnnotationScope; scope != nil && len(scope.Services) > 0 {
			if err := add(r.ID+"/scope", scope.Services, func(fraction float64) {
				scope.Services = scope.Services[:int(float64(len(scope.Services))*fraction)]
				scope.Limited = true
				r.AnnotationError = payloadLimitNote
			}); err != nil {
				return PanelFragment{}, err
			}
		}
		for j, f := range []*panel.Frame{r.Frame, r.Previous} {
			if f == nil {
				continue
			}
			if err := add(fmt.Sprintf("%s/%d", r.ID, j), f, func(fraction float64) {
				rows := int(float64(f.Rows) * fraction)
				for c := range f.Values {
					f.Values[c] = f.Values[c][:min(rows, len(f.Values[c]))]
				}
				for name, trend := range f.Trends {
					f.Trends[name] = trend[:min(rows, len(trend))]
				}
				f.Rows = rows
				f.Totals = nil
				f.Truncated = true
				f.Note = payloadLimitNote
				if f.Health != nil {
					f.Health.ErrorTrend = nil
				}
			}); err != nil {
				return PanelFragment{}, err
			}
		}
	}
	if out.Trace != nil {
		d := &out.Trace.Data
		mark := func() {
			d.Truncated = true
			out.Trace.Provenance.Complete = false
			out.Trace.Summary = payloadLimitNote
		}
		for _, entry := range []struct {
			key   string
			value any
			cut   func(float64)
		}{
			{"~trace/spans", d.Spans, func(f float64) { d.Spans = d.Spans[:int(float64(len(d.Spans))*f)]; mark() }},
			{"~trace/logs", d.Logs, func(f float64) { d.Logs = d.Logs[:int(float64(len(d.Logs))*f)]; mark() }},
			{"~trace/services", d.Services, func(f float64) { d.Services = d.Services[:int(float64(len(d.Services))*f)]; mark() }},
		} {
			if err := add(entry.key, entry.value, entry.cut); err != nil {
				return PanelFragment{}, err
			}
		}
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].size == choices[j].size {
			return choices[i].key < choices[j].key
		}
		return choices[i].size > choices[j].size
	})
	remaining := size - fragmentPayloadLimit + 4096
	for _, choice := range choices {
		if err := ctx.Err(); err != nil {
			return PanelFragment{}, err
		}
		if remaining <= 0 {
			break
		}
		removed := min(remaining, choice.size)
		fraction := max(0, 1-float64(removed)/float64(max(1, choice.size)))
		choice.cut(fraction)
		remaining -= removed
	}
	size, err = jsonBytes(out)
	if err != nil {
		return PanelFragment{}, err
	}
	if size > fragmentPayloadLimit {
		// Metadata can outweigh rows. Keep the authored spec and every status, with
		// explicit empty/truncated frames rather than silently exceed the contract.
		for i := range out.Results {
			r := &out.Results[i]
			r.Frame = &panel.Frame{Columns: []panel.Column{}, Values: [][]any{}, Truncated: true, Note: payloadLimitNote}
			r.Previous = nil
			r.AnnotationScope = nil
		}
		out.Trace = nil
		size, err = jsonBytes(out)
		if err != nil {
			return PanelFragment{}, err
		}
	}
	if size > fragmentPayloadLimit {
		return PanelFragment{}, fmt.Errorf("panel fragment metadata exceeds the 256 KiB payload limit: %d bytes", size)
	}
	if err := ctx.Err(); err != nil {
		return PanelFragment{}, err
	}
	return out, nil
}
