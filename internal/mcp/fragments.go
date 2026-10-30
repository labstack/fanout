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
	"github.com/labstack/fanout/internal/fanql"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type QueryTelemetryInput struct {
	Panel     query.Panel            `json:"panel" jsonschema:"One v1 panel to display in the answer"`
	Text      string                 `json:"text,omitempty" jsonschema:"FanQL text that becomes panel.query; prefer panel.query (structured) or panel.sql; see fanout://fanql/reference"`
	Variables []query.Variable       `json:"variables,omitempty" jsonschema:"Optional v1 variable definitions used by this panel"`
	Time      *query.Time            `json:"time,omitempty" jsonschema:"Optional dashboard time override: exact from/to or a relative range"`
	Vars      map[string]query.Value `json:"vars,omitempty" jsonschema:"Resolved variable values: strings or lists; preserves $__all and empty lists"`
}

type fanqlToolFailureOutput struct {
	Error    fanql.Failure  `json:"error"`
	Problems query.Problems `json:"problems,omitempty"`
}

// QueryTelemetryOutput includes FanQL failures without changing preset fragments.
type QueryTelemetryOutput struct {
	*PanelFragment
	Notices  []fanql.Notice `json:"notices,omitempty"`
	Error    *fanql.Failure `json:"error,omitempty"`
	Problems query.Problems `json:"problems,omitempty"`
}
type FragmentView struct {
	Kind string `json:"kind" jsonschema:"View kind: preset or query"`
	Key  string `json:"key" jsonschema:"Stable SHA-256 hash of the view specification, captured window and variables"`
}
type PanelFragment struct {
	View      FragmentView                                     `json:"view"`
	Dashboard query.Dashboard                                  `json:"dashboard"`
	Results   []query.Result                                   `json:"results"`
	Vars      map[string]query.Value                           `json:"vars,omitempty"`
	Trace     *observability.Result[observability.TraceDetail] `json:"trace,omitempty"`
}

// The published schema deliberately has no subset selector. Raw input is
// checked by middleware before the SDK decodes this type.
type fragmentInput struct {
	Dashboard query.Dashboard        `json:"dashboard" jsonschema:"Complete v1 dashboard specification; every panel is executed"`
	Time      *query.Time            `json:"time,omitempty" jsonschema:"Optional dashboard time override: exact from/to or a relative range"`
	Vars      map[string]query.Value `json:"vars,omitempty" jsonschema:"Resolved variable values: strings or lists; preserves $__all and empty lists"`
	Widths    map[string]int         `json:"widths,omitempty" jsonschema:"Optional panel widths in pixels, keyed by panel ID"`
	Compare   *bool                  `json:"compare,omitempty" jsonschema:"Override previous-period comparison for the complete fragment"`
}
type variableOptions struct {
	Options map[string][]query.Option `json:"options"`
}

// Value's wire form is scalar/list JSON, not the fields of its Go struct.
// Supply the same override to input and output schema inference.
func fragmentTool[In, Out any](server *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	options := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[query.Value](): {AnyOf: []*jsonschema.Schema{{Type: "string"}, {Type: "array", Items: &jsonschema.Schema{Type: "string"}}}},
	}}
	input, err := jsonschema.For[In](options)
	if err != nil {
		panic(err)
	}
	output, err := jsonschema.For[Out](options)
	if err != nil {
		panic(err)
	}
	if reflect.TypeFor[Out]() == reflect.TypeFor[QueryTelemetryOutput]() {
		success, err := jsonschema.For[PanelFragment](options)
		if err != nil {
			panic(err)
		}
		success.Properties["notices"] = output.Properties["notices"]
		failure, err := jsonschema.For[fanqlToolFailureOutput](options)
		if err != nil {
			panic(err)
		}
		output = &jsonschema.Schema{Type: "object", AnyOf: []*jsonschema.Schema{success, failure}}
	}
	if reflect.TypeFor[Out]() == reflect.TypeFor[PanelFragment]() || reflect.TypeFor[Out]() == reflect.TypeFor[QueryTelemetryOutput]() {
		tool.Description += " All-empty results return no_data guidance instead of a view. State no matching data findings in the answer and prefer signals the service actually has."
	}
	tool.InputSchema = input
	tool.OutputSchema = output
	server.limitTools[tool.Name] = input.Properties["limit"] != nil
	mcp.AddTool(server.mcp, tool, handler)
}

func (s *Server) runFragment(ctx context.Context, req query.RunRequest, kinds ...string) (*mcp.CallToolResult, PanelFragment, error) {
	out, err := s.executeFragment(ctx, req)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	if allResultsEmpty(out.Results) {
		return nil, PanelFragment{}, s.noDataError(ctx, out)
	}
	return finishFragment(ctx, out, kinds...)
}

func finishFragment(ctx context.Context, out PanelFragment, kinds ...string) (*mcp.CallToolResult, PanelFragment, error) {
	var err error
	kind := "query"
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	out.View = fragmentView(out, kind)
	out, err = boundFragment(ctx, out)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	return summary(fragmentSummary(out)), out, nil
}

func (s *Server) noDataError(ctx context.Context, out PanelFragment) error {
	code, guidance, err := s.panels.EmptyGuidance(ctx, query.RunRequest{Dashboard: out.Dashboard, Vars: out.Vars})
	if err != nil {
		return safePanelToolError(err)
	}
	if code == "unknown_service" {
		return guidanceError(code, query.PresentDiagnosis(guidance))
	}
	notes := []string{"No matching data.", query.PresentDiagnosis(guidance)}
	for i, r := range out.Results {
		title := r.ID
		if i < len(out.Dashboard.Panels) {
			title = out.Dashboard.Panels[i].Title
		}
		notes = append(notes, fmt.Sprintf("%s: %s", title, query.PresentDiagnosis(r.Diagnosis)))
	}
	return guidanceError("no_data", strings.Join(notes, " "))
}

func (s *Server) executeFragment(ctx context.Context, req query.RunRequest) (PanelFragment, error) {
	if s.panels == nil {
		return PanelFragment{}, errors.New("panels are unavailable")
	}
	// Detach the authored spec before normalization; executor/caller state must
	// never be changed by the transport adapter.
	raw, err := json.Marshal(req)
	if err != nil {
		return PanelFragment{}, err
	}
	var detached query.RunRequest
	if err = json.Unmarshal(raw, &detached); err != nil {
		return PanelFragment{}, err
	}
	req = detached
	query.Normalize(&req.Dashboard)
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
	query.Normalize(&req.Dashboard)
	// Reject oversized authored input before execution, rather than silently
	// changing query semantics to fit the persisted-response budget.
	authored, err := json.Marshal(PanelFragment{Dashboard: req.Dashboard, Vars: req.Vars, Results: []query.Result{}})
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
func (s *Server) queryTelemetry(ctx context.Context, _ *mcp.CallToolRequest, input QueryTelemetryInput) (*mcp.CallToolResult, QueryTelemetryOutput, error) {
	if input.Text != "" && (input.Panel.Query != nil || input.Panel.SQL != "") {
		return nil, QueryTelemetryOutput{}, guidanceError("invalid_input", "text cannot be combined with panel.query or panel.sql")
	}
	var notices []fanql.Notice
	if input.Text != "" {
		compiled, problems := fanql.Compile(input.Text, fanql.Options{Viz: input.Panel.Viz, Variables: input.Variables})
		if len(problems) != 0 {
			return fanqlToolFailure(fanql.Failure{Code: "invalid_fanql", Message: "The query has problems."}, query.PresentProblems(problems))
		}
		input.Panel.Query = &compiled.Query
		notices = append(notices, compiled.Notices...)
	}
	d := query.Dashboard{Name: input.Panel.Title, Variables: input.Variables, Panels: []query.Panel{input.Panel}}
	result, out, err := s.runFragment(ctx, query.RunRequest{Dashboard: d, Time: input.Time, Vars: input.Vars})
	response := QueryTelemetryOutput{PanelFragment: &out}
	if err == nil && input.Text != "" {
		response.Notices = notices
	}
	return result, response, err
}
func (s *Server) queryPanelFragment(ctx context.Context, _ *mcp.CallToolRequest, input query.RunRequest) (*mcp.CallToolResult, PanelFragment, error) {
	if input.Panels != nil {
		return nil, PanelFragment{}, errors.New("panels is forbidden for query_panel_fragment")
	}
	return s.runFragment(ctx, input)
}
func (s *Server) panelExemplars(ctx context.Context, _ *mcp.CallToolRequest, input query.ExemplarRequest) (*mcp.CallToolResult, query.ExemplarResponse, error) {
	if s.panels == nil {
		return nil, query.ExemplarResponse{}, errors.New("panels are unavailable")
	}
	out, err := s.panels.Exemplars(ctx, input)
	if err != nil {
		return nil, out, safePanelToolError(err)
	}
	return summary(fmt.Sprintf("%d exemplar traces.", len(out.Traces))), out, nil
}
func (s *Server) resolvePanelVariables(ctx context.Context, _ *mcp.CallToolRequest, input query.ResolveRequest) (*mcp.CallToolResult, variableOptions, error) {
	if s.panels == nil {
		return nil, variableOptions{}, errors.New("panels are unavailable")
	}
	options, err := s.panels.ResolveVariables(ctx, input)
	if err != nil {
		return nil, variableOptions{}, safePanelToolError(err)
	}
	return summary("Panel variable options."), variableOptions{Options: options}, nil
}

func fragmentPreset(kind, service, namespace, severity, search string, limit int) query.Dashboard {
	d := query.Dashboard{Name: map[string]string{"overview": "System health", "topology": "Service dependencies", "performance": "Service performance", "logs": "Log explorer", "trace": "Trace detail"}[kind], Time: query.Time{Range: "1h", Refresh: "off"}}
	filters := []string{}
	literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	if namespace != "" {
		filters = append(filters, "namespace = "+literal(namespace))
	}
	if service != "" {
		filters = append(filters, "service = "+literal(service))
	}
	add := func(id, title, viz, signal, measure, unit string, by []string, bucket string) {
		q := &query.Spec{From: signal, Where: append([]string(nil), filters...), By: by, Bucket: bucket}
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
		d.Panels = append(d.Panels, query.Panel{ID: id, Title: title, Viz: viz, Unit: unit, Query: q})
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
		add("operations", "Slow operations", "table", "spans", "p95(duration_ms)", "ms", []string{"operation"}, "")
		d.Panels[len(d.Panels)-1].Query.Where = append(d.Panels[len(d.Panels)-1].Query.Where, "kind IN ('SPAN_KIND_SERVER','SPAN_KIND_CONSUMER')")
	case "logs":
		if severity != "" {
			filters = append(filters, "severity = "+literal(severity))
		}
		add("volume", "Log volume", "timeseries", "logs", "count()", "count", []string{"severity"}, "auto")
		add("events", "Log events", "logs", "logs", "", "", nil, "")
		if search != "" {
			d.Panels[len(d.Panels)-1].Options = &query.Options{Highlight: search}
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
				if kind == "erroring" && span.Status != "STATUS_CODE_ERROR" {
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
func summaryCell(col query.Column, value any) string {
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

// Reduce the largest serialized collection first, retaining the maximal prefix.
// Metadata is discarded only after every reducible collection is exhausted.
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
		for _, f := range []*query.Frame{r.Frame, r.Previous} {
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
		key   string
		count int
		value func() any
		cut   func(int)
		owner func() any
	}
	choices := []collection{}
	minimum := map[string]int{}
	for i := range out.Results {
		r := &out.Results[i]
		if scope := r.AnnotationScope; scope != nil && len(scope.Services) > 0 {
			original := scope.Services
			choices = append(choices, collection{r.ID + "/scope", len(original), func() any { return scope.Services }, func(n int) { scope.Services = original[:n]; scope.Limited = true; r.AnnotationError = payloadLimitNote }, func() any { return r }})
		}
		for j, f := range []*query.Frame{r.Frame, r.Previous} {
			if f == nil || f.Rows == 0 {
				continue
			}
			values, trends := f.Values, f.Trends
			choices = append(choices, collection{fmt.Sprintf("%s/%d", r.ID, j), f.Rows, func() any { return f }, func(n int) {
				f.Values = make([][]any, len(values))
				for c := range values {
					f.Values[c] = values[c][:min(n, len(values[c]))]
				}
				if trends != nil {
					f.Trends = make(map[string][][]any, len(trends))
					for name, trend := range trends {
						f.Trends[name] = trend[:min(n, len(trend))]
					}
				}
				f.Rows = n
				f.Totals = nil
				f.Truncated = true
				f.Note = payloadLimitNote
				if f.Health != nil {
					f.Health.ErrorTrend = nil
				}
			}, func() any { return f }})
		}
	}
	if out.Trace != nil {
		d := &out.Trace.Data
		mark := func() {
			d.Truncated = true
			out.Trace.Provenance.Complete = false
			out.Trace.Summary = payloadLimitNote
		}
		originalSpans := d.Spans
		indices := make([]int, len(originalSpans))
		for i := range indices {
			indices[i] = i
		}
		parents := map[string]bool{}
		for _, span := range originalSpans {
			parents[span.SpanID] = true
		}
		priority := func(span observability.TraceSpan) int {
			if span.ParentSpanID == "" || !parents[span.ParentSpanID] {
				return 0
			}
			if span.Status == "STATUS_CODE_ERROR" {
				return 1
			}
			return 2
		}
		sort.SliceStable(indices, func(i, j int) bool {
			left, right := originalSpans[indices[i]], originalSpans[indices[j]]
			a, b := priority(left), priority(right)
			if a != b {
				return a < b
			}
			return left.DurationMS > right.DurationMS
		})
		protected := 0
		for _, i := range indices {
			if priority(originalSpans[i]) < 2 {
				protected++
			}
		}
		floor := min(len(indices), protected+1)
		protectedSpans := make([]observability.TraceSpan, floor)
		for i, index := range indices[:floor] {
			protectedSpans[i] = originalSpans[index]
		}
		protectedBytes, e := jsonBytes(protectedSpans)
		if e != nil {
			return PanelFragment{}, e
		}
		if protectedBytes > fragmentPayloadLimit {
			floor = min(1, len(indices))
		}
		minimum["~trace/spans"] = floor
		logs, services := d.Logs, d.Services
		choices = append(choices,
			collection{"~trace/spans", len(indices), func() any { return d.Spans }, func(n int) {
				selected := make([]bool, len(indices))
				for _, index := range indices[:n] {
					selected[index] = true
				}
				d.Spans = []observability.TraceSpan{}
				for index, span := range originalSpans {
					if selected[index] {
						d.Spans = append(d.Spans, span)
					}
				}
				mark()
			}, func() any { return out.Trace }},
			collection{"~trace/logs", len(logs), func() any { return d.Logs }, func(n int) { d.Logs = logs[:n]; mark() }, func() any { return out.Trace }},
			collection{"~trace/services", len(services), func() any { return d.Services }, func(n int) { d.Services = services[:n]; mark() }, func() any { return out.Trace }},
		)
	}
	sizes := make([]int, len(choices))
	for i, choice := range choices {
		if err := ctx.Err(); err != nil {
			return PanelFragment{}, err
		}
		sizes[i], err = jsonBytes(choice.value())
		if err != nil {
			return PanelFragment{}, err
		}
	}
	for size > fragmentPayloadLimit {
		if err := ctx.Err(); err != nil {
			return PanelFragment{}, err
		}
		largest, largestBytes := -1, 0
		for i, choice := range choices {
			if choice.count <= minimum[choice.key] {
				continue
			}
			n := sizes[i]
			if n > largestBytes || n == largestBytes && (largest < 0 || choice.key < choices[largest].key) {
				largest, largestBytes = i, n
			}
		}
		if largest < 0 {
			// Priority spans are only sacrificed when all ordinary rows have
			// been exhausted and their protected detail alone cannot fit.
			if len(minimum) > 0 {
				clear(minimum)
				continue
			}
			break
		}
		choice := &choices[largest]
		// Test the empty collection, then search the exact serialized payload
		// for the maximal retained prefix. No average row size is assumed.
		ownerBytes, e := jsonBytes(choice.owner())
		if e != nil {
			return PanelFragment{}, e
		}
		otherBytes := size - ownerBytes
		measure := func() (int, error) { n, e := jsonBytes(choice.owner()); return otherBytes + n, e }
		floor := minimum[choice.key]
		choice.cut(floor)
		size, err = measure()
		if err != nil {
			return PanelFragment{}, err
		}
		if size <= fragmentPayloadLimit {
			lo, hi := floor, choice.count-1
			for lo < hi {
				if err := ctx.Err(); err != nil {
					return PanelFragment{}, err
				}
				mid := (lo + hi + 1) / 2
				choice.cut(mid)
				n, e := measure()
				if e != nil {
					return PanelFragment{}, e
				}
				if n <= fragmentPayloadLimit {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			choice.cut(lo)
			choice.count = lo
			size, err = measure()
			if err != nil {
				return PanelFragment{}, err
			}
		} else {
			choice.count = floor
		}
		sizes[largest], err = jsonBytes(choice.value())
		if err != nil {
			return PanelFragment{}, err
		}
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
			r.Frame = &query.Frame{Columns: []query.Column{}, Values: [][]any{}, Truncated: true, Note: payloadLimitNote}
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

func fanqlToolFailure(problem fanql.Failure, problems query.Problems) (*mcp.CallToolResult, QueryTelemetryOutput, error) {
	result := summary(problem.Message)
	result.IsError = true
	return result, QueryTelemetryOutput{Error: &problem, Problems: problems}, nil
}
