package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
)

// ToolHandler executes a tool and returns JSON result.
type ToolHandler func(ctx context.Context, input json.RawMessage) (string, error)

// ToolRegistry maps tool names to definitions and handlers.
type ToolRegistry struct {
	defs     []ToolDef
	handlers map[string]ToolHandler
}

// NewToolRegistry creates the registry with all 10 tools (9 data + render).
func NewToolRegistry(svc *service.Service, duck *query.Duck, lakeDir string) *ToolRegistry {
	r := &ToolRegistry{handlers: make(map[string]ToolHandler)}

	r.register(ToolDef{
		Name:        "status",
		Description: "System health overview: service counts, top issues, throughput, P95 latency, error rate. Start here.",
		InputSchema: jsonSchema(map[string]property{
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Window    int    `json:"window"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		res, err := svc.Status(ctx, p.Window, p.Namespace, "")
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "diagnose",
		Description: "Deep-dive into a specific service: P50/P95/P99 latency, error rate, top errors with example trace IDs, slow operations, downstream dependencies.",
		InputSchema: jsonSchema(map[string]property{
			"service":   {Type: "string", Desc: "Service name (required)", Required: true},
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Service   string `json:"service"`
			Window    int    `json:"window"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		res, err := svc.Diagnose(ctx, p.Service, p.Window, p.Namespace, "")
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "find",
		Description: "Search spans and logs by pattern, service, status (error/slow), or severity. Returns matching items with trace IDs for drill-down.",
		InputSchema: jsonSchema(map[string]property{
			"pattern":   {Type: "string", Desc: "Search pattern (text search across names/bodies)"},
			"service":   {Type: "string", Desc: "Filter by service name"},
			"status":    {Type: "string", Desc: "Filter by status: error, slow"},
			"severity":  {Type: "string", Desc: "Filter log severity: ERROR, WARN, INFO, DEBUG"},
			"type":      {Type: "string", Desc: "Search type: spans, logs, both (default both)"},
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"limit":     {Type: "integer", Desc: "Max results (default 20)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Pattern   string `json:"pattern"`
			Service   string `json:"service"`
			Status    string `json:"status"`
			Severity  string `json:"severity"`
			Type      string `json:"type"`
			Window    int    `json:"window"`
			Limit     int    `json:"limit"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		if p.Limit == 0 {
			p.Limit = 20
		}

		var severities []string
		if p.Severity != "" {
			severities = []string{p.Severity}
		}

		res, err := svc.Find(ctx, service.FindParams{
			Query:     p.Pattern,
			Service:   p.Service,
			Status:    p.Status,
			Severity:  severities,
			Type:      p.Type,
			Window:    p.Window,
			Limit:     p.Limit,
			Namespace: p.Namespace,
		})
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "trace",
		Description: "Retrieve a complete distributed trace by trace ID. Shows full span tree, correlated logs, root-cause analysis, and critical path.",
		InputSchema: jsonSchema(map[string]property{
			"trace_id": {Type: "string", Desc: "Trace ID (required)", Required: true},
			"window":   {Type: "integer", Desc: "Time window in minutes (default 1440)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			TraceID string `json:"trace_id"`
			Window  int    `json:"window"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 1440
		}
		res, err := svc.Trace(ctx, p.TraceID, true, p.Window)
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "timeline",
		Description: "Time-bucketed metrics for a service (or all services) with anomaly detection. Returns request counts, error rates, P95 latency per bucket, and detected anomalies.",
		InputSchema: jsonSchema(map[string]property{
			"service":   {Type: "string", Desc: "Service name (empty for all services)"},
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"buckets":   {Type: "integer", Desc: "Number of time buckets (default 5)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Service   string `json:"service"`
			Window    int    `json:"window"`
			Buckets   int    `json:"buckets"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		if p.Buckets == 0 {
			p.Buckets = 5
		}
		res, err := svc.Timeline(ctx, p.Service, p.Window, p.Buckets, p.Namespace, "")
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "topology",
		Description: "Service dependency map with health status for each node. Shows call relationships, request counts, error rates, and latency.",
		InputSchema: jsonSchema(map[string]property{
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Window    int    `json:"window"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		res, err := svc.Topology(ctx, p.Window, p.Namespace, "")
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "compare",
		Description: "Side-by-side comparison of 2-4 services: requests, error rate, P50/P95 latency, and overall winner.",
		InputSchema: jsonSchema(map[string]property{
			"services":  {Type: "array", Desc: "Service names to compare (2-4 required)", Required: true, Items: &property{Type: "string"}},
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Services  []string `json:"services"`
			Window    int      `json:"window"`
			Namespace string   `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		res, err := svc.Compare(ctx, p.Services, p.Window, p.Namespace, "")
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "metrics",
		Description: "Explore available metrics: search by name, filter by service/type. Returns aggregated summaries with sparkline data.",
		InputSchema: jsonSchema(map[string]property{
			"name":      {Type: "string", Desc: "Metric name or pattern (supports wildcards like http.*)"},
			"service":   {Type: "string", Desc: "Filter by service name"},
			"type":      {Type: "string", Desc: "Filter by metric type: gauge, counter, histogram"},
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Name      string `json:"name"`
			Service   string `json:"service"`
			Type      string `json:"type"`
			Window    int    `json:"window"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		var names []string
		if p.Name != "" {
			names = []string{p.Name}
		}
		var services []string
		if p.Service != "" {
			services = []string{p.Service}
		}
		var types []string
		if p.Type != "" {
			types = []string{p.Type}
		}
		res, err := svc.Metrics(ctx, service.MetricsParams{
			Names:     names,
			Services:  services,
			Types:     types,
			Window:    p.Window,
			Namespace: p.Namespace,
		})
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "query",
		Description: "Execute raw SQL against DuckDB. Use for custom analysis when built-in tools aren't sufficient. Tables: service_rollup, read_parquet for spans/logs/metrics parquet files.",
		InputSchema: jsonSchema(map[string]property{
			"sql":      {Type: "string", Desc: "SQL query (SELECT only, 30s timeout)", Required: true},
			"max_rows": {Type: "integer", Desc: "Maximum rows to return (default 200, max 1000)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			SQL     string `json:"sql"`
			MaxRows int    `json:"max_rows"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.MaxRows == 0 {
			p.MaxRows = 200
		}
		res := duck.ExecuteSQL(ctx, query.SQLRequest{
			Query:   p.SQL,
			MaxRows: p.MaxRows,
		})
		return marshal(res)
	})

	// The render tool — LLM generates HTML, we sanitize and pass through.
	// SECURITY: The raw HTML returned here is sanitized by the orchestrator
	// (via bluemonday) before being sent to the browser. Never bypass sanitization.
	r.register(ToolDef{
		Name:        "render",
		Description: `Display rich HTML visualization inline in chat. The HTML will be shown as a card in the conversation.

CSS vars (light+dark): --text-primary, --text-secondary, --text-muted, --bg-primary, --bg-secondary, --bg-tertiary, --border-color, --success (#22c55e), --warning (#f59e0b), --danger (#ef4444), --signal-trace (blue), --signal-log (amber), --signal-metric (green), --signal-error (red), --font-sans, --font-mono, --radius (0.5rem).

Shoelace: <sl-card>, <sl-badge>, <sl-tag>, <sl-icon>, <sl-progress-bar>, <sl-tooltip>.

Layout: Grid: <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:1rem">. Metric card: <sl-card><div style="font-size:1.5rem;font-weight:700">99.9%</div><div style="font-size:0.7rem;color:var(--text-muted);text-transform:uppercase">Label</div></sl-card>. Table: <table class="table"><thead>...</thead><tbody>...</tbody></table>.

SVG Viz Renderers (auto-initialized, no inline event handlers):
- trace-waterfall data-spans: [{id, parent, service, op, start, dur, status}]
- topology-graph data-graph: {nodes:[{id,status,rpm,p95,errors}], edges:[{source,target,rpm,errorRate}]}
- flow-sankey data-flow: {nodes:[{id,label,rpm,status?}], links:[{source,target,value}]}
- flame-graph data-frames: [{name,depth,x,w,self,total,samples,service}]
- latency-heatmap data-heatmap: {buckets:[], times:[], values:[[]]}
- dep-matrix data-matrix: {services:[], cells:[{from,to,errorRate,rpm,p95}]}
- endpoint-breakdown data-endpoints: {endpoints:[{method,path,rpm,p50,p95,p99,errorRate,status,trend:[]}]}
- correlation-view data-correlation: {times:[], panels:[{label,color,values:[],baseline?,markers?:[{t,label,severity}]}]}
- timeseries-chart data-timeseries: {series:[{label,color,values:[],type:"line"|"area"}], labels:[], yLabel}
- bar-chart data-barchart: {bars:[{label,value,color?}], yLabel?, horizontal?:bool}

Wrap viz in: <div class="viz-card"><div class="viz-card-header"><div class="viz-card-title"><span class="signal-dot" style="background:var(--signal-trace)"></span> Title</div><div class="viz-card-actions"><button class="btn-icon btn-viz-expand" title="Expand"><svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 2H2v4M10 2h4v4M6 14H2v-4M10 14h4v-4"/></svg></button></div></div><div class="viz-card-body"><div class="CLASS" data-ATTR='JSON'></div></div></div>`,
		InputSchema: jsonSchema(map[string]property{
			"html": {Type: "string", Desc: "HTML content to render (Shoelace components, CSS vars, Vega-Lite supported)", Required: true},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			HTML string `json:"html"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if strings.TrimSpace(p.HTML) == "" {
			return "", fmt.Errorf("render tool requires non-empty html")
		}
		return p.HTML, nil
	})

	return r
}

// Defs returns all tool definitions for the LLM.
func (r *ToolRegistry) Defs() []ToolDef {
	return r.defs
}

// Execute runs a tool by name and returns the result.
// Tool execution errors are returned as errors (not swallowed).
func (r *ToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	h, ok := r.handlers[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	result, err := h(ctx, input)
	if err != nil {
		slog.Warn("tool execution failed", "tool", name, "err", err)
		return "", err
	}
	return result, nil
}

func (r *ToolRegistry) register(def ToolDef, handler ToolHandler) {
	if _, exists := r.handlers[def.Name]; exists {
		panic("ai: duplicate tool registration: " + def.Name)
	}
	r.defs = append(r.defs, def)
	r.handlers[def.Name] = handler
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// JSON Schema helpers

type property struct {
	Type     string    `json:"type"`
	Desc     string    `json:"description"`
	Required bool      `json:"-"`
	Items    *property `json:"items,omitempty"`
}

func jsonSchema(props map[string]property) map[string]any {
	properties := make(map[string]any)
	var required []string

	for name, p := range props {
		prop := map[string]any{
			"type":        p.Type,
			"description": p.Desc,
		}
		if p.Items != nil {
			prop["items"] = map[string]any{"type": p.Items.Type}
		}
		properties[name] = prop
		if p.Required {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
