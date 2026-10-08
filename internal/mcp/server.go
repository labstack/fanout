package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpUIExtension = "io.modelcontextprotocol/ui"

const staticCatalogTTLMs = 5 * 60 * 1000

const serverInstructions = "Start with get_observability_overview for system health, use get_intelligence_snapshot for the latest precomputed anomalies and log patterns, get_service_topology for direct dependency edges, get_service_dependencies for bounded upstream or downstream reachability from a service, get_service_performance for latency and errors, inspect_trace for one trace, and search_logs for application events. Treat schema, timestamps, provenance, and bounded time windows as authoritative. Dashboard tools are scoped to the authenticated user. For a custom chart in chat, use query_telemetry with one v1 panel; every chart view returns a dashboard fragment rendered by the shared panel renderer. To build a dashboard, read get_telemetry_schema, draft panels, run preview_panels until every panel is ok or deliberately empty, then create_dashboard. To change one, get_dashboard first and use edit_dashboard; replace only for a redesign the user asked for."

type Observability interface {
	Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error)
	Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error)
	Dependencies(context.Context, observability.Scope, observability.DependencyOptions) (observability.Result[observability.Dependencies], error)
	Performance(context.Context, observability.Scope, observability.PerformanceOptions) (observability.Result[observability.Performance], error)
	Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error)
	Logs(context.Context, observability.Scope, string, string, string, int) (observability.Result[observability.Logs], error)
}

type IntelligenceSnapshots interface {
	LatestSnapshot() *intelligence.IntelligenceSnapshot
}

type QueryInput struct {
	From      *time.Time `json:"from,omitempty" jsonschema:"Absolute start, RFC3339Nano; requires to and excludes window"`
	To        *time.Time `json:"to,omitempty" jsonschema:"Absolute end, RFC3339Nano; requires from and excludes window"`
	Window    string     `json:"window,omitempty" jsonschema:"Time window such as 15m, 1h, 24h, 168h, or 720h; defaults to 1h"`
	Namespace string     `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; empty queries all namespaces"`
	Limit     int        `json:"limit,omitempty" jsonschema:"Maximum services or edges to return, from 1 to 500"`
}

type DependencyInput struct {
	Window    string `json:"window,omitempty" jsonschema:"Bounded telemetry window, defaults to 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Filter by namespace; traversal always keeps namespaces separate"`
	Service   string `json:"service" jsonschema:"Root service name"`
	Direction string `json:"direction,omitempty" jsonschema:"upstream or downstream; defaults to downstream"`
	MaxDepth  int    `json:"max_depth,omitempty" jsonschema:"Maximum hops from 1 to 32; defaults to 8"`
	MaxNodes  int    `json:"max_nodes,omitempty" jsonschema:"Maximum accumulated nodes from 1 to 500 including roots; defaults to 100"`
}

type PerformanceInput struct {
	From      *time.Time `json:"from,omitempty" jsonschema:"Absolute start, RFC3339Nano; requires to and excludes window"`
	To        *time.Time `json:"to,omitempty" jsonschema:"Absolute end, RFC3339Nano; requires from and excludes window"`
	Window    string     `json:"window,omitempty" jsonschema:"Time window such as 15m, 1h, 24h, 168h, or 720h; defaults to 1h"`
	Namespace string     `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; empty queries all namespaces"`
	Service   string     `json:"service,omitempty" jsonschema:"Optional exact OpenTelemetry service name; omit for the whole system"`
	Limit     int        `json:"limit,omitempty" jsonschema:"Maximum endpoints to return, from 1 to 500"`
}

type TraceInput struct {
	From      *time.Time `json:"from,omitempty" jsonschema:"Absolute start, RFC3339Nano; requires to and excludes window"`
	To        *time.Time `json:"to,omitempty" jsonschema:"Absolute end, RFC3339Nano; requires from and excludes window"`
	Window    string     `json:"window,omitempty" jsonschema:"Trace lookup window such as 1h, 24h, 168h, or 720h; defaults to 1h"`
	Namespace string     `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; empty queries all namespaces"`
	TraceID   string     `json:"trace_id,omitempty" jsonschema:"Exact trace ID; omit to inspect the most relevant recent error or slow trace"`
	Service   string     `json:"service,omitempty" jsonschema:"Optional service filter when choosing a recent trace"`
	Limit     int        `json:"limit,omitempty" jsonschema:"Maximum spans and correlated logs to return, from 1 to 500"`
}

type LogsInput struct {
	From      *time.Time `json:"from,omitempty" jsonschema:"Absolute start, RFC3339Nano; requires to and excludes window"`
	To        *time.Time `json:"to,omitempty" jsonschema:"Absolute end, RFC3339Nano; requires from and excludes window"`
	Window    string     `json:"window,omitempty" jsonschema:"Time window such as 15m, 1h, 24h, 168h, or 720h; defaults to 1h"`
	Namespace string     `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; empty queries all namespaces"`
	Service   string     `json:"service,omitempty" jsonschema:"Optional exact OpenTelemetry service name"`
	Severity  string     `json:"severity,omitempty" jsonschema:"Optional exact severity such as ERROR, WARN, or INFO"`
	Search    string     `json:"search,omitempty" jsonschema:"Optional case-insensitive text contained in the log body"`
	Limit     int        `json:"limit,omitempty" jsonschema:"Maximum log entries to return, from 1 to 500"`
}

type Server struct {
	mcp          *mcp.Server
	queries      Observability
	intelligence IntelligenceSnapshots
	dashboards   *dashboard.Service
	panels       *panel.Executor
	now          func() time.Time
}

func New(queries Observability, dashboards *dashboard.Service, panels *panel.Executor, version string) *Server {
	return newServer(queries, dashboards, panels, nil, version)
}

func NewWithIntelligence(queries Observability, dashboards *dashboard.Service, panels *panel.Executor, snapshots IntelligenceSnapshots, version string) *Server {
	return newServer(queries, dashboards, panels, snapshots, version)
}

func newServer(queries Observability, dashboards *dashboard.Service, panels *panel.Executor, snapshots IntelligenceSnapshots, version string) *Server {
	s := &Server{
		mcp: mcp.NewServer(&mcp.Implementation{
			Name:    "fanout",
			Title:   "Fanout Observability",
			Version: version,
		}, &mcp.ServerOptions{Instructions: serverInstructions, Capabilities: &mcp.ServerCapabilities{
			Extensions: map[string]any{
				mcpUIExtension: map[string]any{"mimeTypes": []string{mcpAppMIME}},
			},
			// Fanout's tool and resource sets are fixed for the process lifetime.
			Tools:     &mcp.ToolCapabilities{},
			Resources: &mcp.ResourceCapabilities{},
		}}),
		queries:      queries,
		intelligence: snapshots,
		dashboards:   dashboards,
		panels:       panels,
		now:          time.Now,
	}
	s.registerTools()
	s.registerPanelTools()
	s.registerDashboardTools()
	s.registerAppResources()
	s.mcp.AddReceivingMiddleware(addStaticCacheHints, filterMCPAppToolMetadata, rejectFragmentSubset)
	return s
}

// addStaticCacheHints lets clients reuse catalogs and embedded MCP Apps between
// reconnects. They are identical for every principal and cannot change until
// the process is replaced, so a short public TTL is both safe and bounded by a
// deployment rather than runtime state.
func addStaticCacheHints(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil {
			return result, err
		}
		switch value := result.(type) {
		case *mcp.ListToolsResult:
			value.TTLMs = staticCatalogTTLMs
		case *mcp.ListResourcesResult:
			value.TTLMs = staticCatalogTTLMs
		case *mcp.ListResourceTemplatesResult:
			value.TTLMs = staticCatalogTTLMs
		case *mcp.ReadResourceResult:
			value.TTLMs = staticCatalogTTLMs
		}
		return result, nil
	}
}

// filterMCPAppToolMetadata preserves the same tools and text/structured
// fallbacks for every MCP client, but advertises their optional UI only to
// clients that negotiated support for the MCP Apps HTML profile.
func filterMCPAppToolMetadata(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil || method != "tools/list" || clientSupportsMCPApps(req) {
			return result, err
		}
		listed, ok := result.(*mcp.ListToolsResult)
		if !ok {
			return result, err
		}
		filtered := *listed
		filtered.Tools = make([]*mcp.Tool, 0, len(listed.Tools))
		for _, tool := range listed.Tools {
			if appOnly(tool.Meta) {
				continue
			}
			cloned := *tool
			cloned.Meta = maps.Clone(tool.Meta)
			delete(cloned.Meta, "ui")
			filtered.Tools = append(filtered.Tools, &cloned)
		}
		return &filtered, nil
	}
}

func clientSupportsMCPApps(req mcp.Request) bool {
	session, ok := req.GetSession().(*mcp.ServerSession)
	if !ok {
		return false
	}
	params := session.InitializeParams()
	if params == nil || params.Capabilities == nil {
		return false
	}
	settings, ok := params.Capabilities.Extensions[mcpUIExtension].(map[string]any)
	if !ok {
		return false
	}
	switch mimeTypes := settings["mimeTypes"].(type) {
	case []any:
		for _, value := range mimeTypes {
			if value == mcpAppMIME {
				return true
			}
		}
	case []string:
		for _, value := range mimeTypes {
			if value == mcpAppMIME {
				return true
			}
		}
	}
	return false
}

func (s *Server) MCP() *mcp.Server { return s.mcp }

func (s *Server) HTTPHandler() http.Handler {
	// The MCP OAuth bearer middleware runs before this handler. Disable the SDK's
	// localhost Host heuristic because a legitimate local reverse proxy connects
	// over loopback while preserving the public Host (for example,
	// fanout.example.com); the heuristic otherwise rejects every browser MCP App.
	// Keep browser connections stateful so MCP Apps can open the protocol's GET
	// event stream instead of producing an expected-but-noisy 405 fallback in
	// the browser console. Expire abandoned iframe sessions promptly.
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcp },
		&mcp.StreamableHTTPOptions{
			SessionTimeout:             10 * time.Minute,
			DisableLocalhostProtection: true,
		},
	)
}

func (s *Server) registerTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "get_service_dependencies", Title: "Service dependency traversal",
		Description: "Find upstream or downstream dependencies of one service with minimum hop counts. Uses the full scoped edge rollup, handles cycles, and reports truncation from depth or node limits.",
		Annotations: readOnly,
	}, s.dependencies)
	fragmentTool(s.mcp, &mcp.Tool{
		Name:        "get_observability_overview",
		Title:       "System health overview",
		Description: "Summarize service health for a bounded telemetry window. Start here for incident triage.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.overview)
	fragmentTool(s.mcp, &mcp.Tool{
		Name:        "get_service_topology",
		Title:       "Service dependency topology",
		Description: "Return services and observed dependency edges with health, traffic, latency, and error data.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.topology)
	fragmentTool(s.mcp, &mcp.Tool{
		Name:        "get_service_performance",
		Title:       "Service performance explorer",
		Description: "Display separate latency, error rate and request rate panels plus slow endpoints for one service or the system.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.performance)
	fragmentTool(s.mcp, &mcp.Tool{
		Name:        "inspect_trace",
		Title:       "Trace detail",
		Description: "Inspect an exact trace, or select the most relevant recent error or slow trace, with spans, waterfall, flame graph, and correlated logs.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.trace)
	fragmentTool(s.mcp, &mcp.Tool{
		Name:        "search_logs",
		Title:       "Log explorer",
		Description: "Search and filter logs with a severity timeline and links back to correlated traces.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.logs)
	if s.intelligence != nil {
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "get_intelligence_snapshot",
			Title:       "Latest detected anomalies",
			Description: "Return the latest precomputed health score, anomalies, insights, and recurring warning or error log patterns.",
			Annotations: readOnly,
		}, s.intelligenceSnapshot)
	}
}

func (s *Server) dependencies(ctx context.Context, _ *mcp.CallToolRequest, input DependencyInput) (*mcp.CallToolResult, observability.Result[observability.Dependencies], error) {
	scope, err := s.scope(QueryInput{Window: input.Window, Namespace: input.Namespace})
	if err != nil {
		return nil, observability.Result[observability.Dependencies]{}, err
	}
	output, err := s.queries.Dependencies(ctx, scope, observability.DependencyOptions{Service: input.Service, Direction: input.Direction, MaxDepth: input.MaxDepth, MaxNodes: input.MaxNodes})
	if err != nil {
		return nil, output, safePanelToolError(err)
	}
	return summary(output.Summary), output, nil
}

func (s *Server) intelligenceSnapshot(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, intelligence.IntelligenceSnapshot, error) {
	snapshot := s.intelligence.LatestSnapshot()
	if snapshot == nil {
		return nil, intelligence.IntelligenceSnapshot{}, fmt.Errorf("intelligence snapshot is not ready")
	}
	return summary(snapshot.Summary), *snapshot, nil
}

func (s *Server) presetFragment(ctx context.Context, input QueryInput, kind, service, severity, search string) (*mcp.CallToolResult, PanelFragment, error) {
	scope, err := s.scope(input)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	d := fragmentPreset(kind, service, input.Namespace, severity, search, input.Limit)
	d.Time = panel.Time{From: &scope.Start, To: &scope.End, Refresh: "off"}
	return s.runFragment(ctx, panel.RunRequest{Dashboard: d})
}
func (s *Server) overview(ctx context.Context, _ *mcp.CallToolRequest, input QueryInput) (*mcp.CallToolResult, PanelFragment, error) {
	return s.presetFragment(ctx, input, "overview", "", "", "")
}
func (s *Server) topology(ctx context.Context, _ *mcp.CallToolRequest, input QueryInput) (*mcp.CallToolResult, PanelFragment, error) {
	return s.presetFragment(ctx, input, "topology", "", "", "")
}
func (s *Server) performance(ctx context.Context, _ *mcp.CallToolRequest, input PerformanceInput) (*mcp.CallToolResult, PanelFragment, error) {
	return s.presetFragment(ctx, QueryInput{Window: input.Window, Namespace: input.Namespace, Limit: input.Limit, From: input.From, To: input.To}, "performance", input.Service, "", "")
}
func (s *Server) logs(ctx context.Context, _ *mcp.CallToolRequest, input LogsInput) (*mcp.CallToolResult, PanelFragment, error) {
	return s.presetFragment(ctx, QueryInput{Window: input.Window, Namespace: input.Namespace, Limit: input.Limit, From: input.From, To: input.To}, "logs", input.Service, input.Severity, input.Search)
}
func (s *Server) trace(ctx context.Context, _ *mcp.CallToolRequest, input TraceInput) (*mcp.CallToolResult, PanelFragment, error) {
	scope, err := s.scope(QueryInput{Window: input.Window, Namespace: input.Namespace, Limit: input.Limit, From: input.From, To: input.To})
	if err != nil {
		return nil, PanelFragment{}, err
	}
	detail, err := s.queries.Trace(ctx, scope, input.TraceID, input.Service, input.Limit)
	if err != nil {
		return nil, PanelFragment{}, safePanelToolError(err)
	}
	d := fragmentPreset("trace", input.Service, input.Namespace, "", "", input.Limit)
	d.Time = panel.Time{From: &scope.Start, To: &scope.End, Refresh: "off"}
	// A representative lookup is performed once. The list must represent that
	// selected trace, even when no match exists (empty id cannot show others).
	id := detail.Data.TraceID
	d.Panels[0].Query.Where = append(d.Panels[0].Query.Where, "trace_id = '"+strings.ReplaceAll(id, "'", "''")+"'")
	out, err := s.executeFragment(ctx, panel.RunRequest{Dashboard: d})
	if err != nil {
		return nil, PanelFragment{}, err
	}
	if detail.Data.Spans == nil {
		detail.Data.Spans = []observability.TraceSpan{}
	}
	if detail.Data.Logs == nil {
		detail.Data.Logs = []observability.LogEntry{}
	}
	if detail.Data.Services == nil {
		detail.Data.Services = []string{}
	}
	out.Trace = &detail
	out = boundFragment(out)
	return summary(fragmentSummary(out.Results)), out, nil
}

func (s *Server) scope(input QueryInput) (observability.Scope, error) {
	if input.Limit < 0 || input.Limit > 500 {
		return observability.Scope{}, errors.New("limit must be from 1 to 500")
	}
	if (input.From == nil) != (input.To == nil) {
		return observability.Scope{}, errors.New("from and to must be supplied together")
	}
	if input.From != nil {
		if strings.TrimSpace(input.Window) != "" {
			return observability.Scope{}, errors.New("from/to cannot coexist with window")
		}
		start, end := input.From.UTC(), input.To.UTC()
		if !end.After(start) {
			return observability.Scope{}, errors.New("to must be after from")
		}
		return observability.Scope{Namespace: input.Namespace, Start: start, End: end}, nil
	}
	window := time.Hour
	if strings.TrimSpace(input.Window) != "" {
		parsed, err := time.ParseDuration(input.Window)
		if err != nil || parsed <= 0 {
			return observability.Scope{}, fmt.Errorf("window must be a positive duration such as 15m or 1h")
		}
		window = parsed
	}
	end := s.now().UTC()
	return observability.Scope{Namespace: input.Namespace, Start: end.Add(-window), End: end}, nil
}

func summary(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func boolPtr(value bool) *bool { return &value }

func appToolMeta(resourceURI string) mcp.Meta {
	return mcp.Meta{
		"ui": map[string]any{
			"resourceUri": resourceURI,
			"visibility":  []string{"model", "app"},
		},
	}
}

func appVisibility(meta mcp.Meta) []string {
	ui, _ := meta["ui"].(map[string]any)
	var out []string
	switch values := ui["visibility"].(type) {
	case []string:
		out = values
	case []any:
		for _, v := range values {
			if name, ok := v.(string); ok {
				out = append(out, name)
			}
		}
	}
	return out
}
func appOnly(meta mcp.Meta) bool { v := appVisibility(meta); return len(v) == 1 && v[0] == "app" }
func appHelperMeta() mcp.Meta    { return mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}} }
func rejectFragmentSubset(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/call" {
			if call, ok := req.(*mcp.CallToolRequest); ok {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(call.Params.Arguments, &raw); err != nil {
					return nil, err
				}
				if _, exists := raw["panels"]; exists && call.Params.Name == "query_panel_fragment" {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "panels is forbidden for query_panel_fragment"}}}, nil
				}
				switch call.Params.Name {
				case "get_observability_overview", "get_service_topology", "get_service_performance", "inspect_trace", "search_logs":
					if value, present := raw["limit"]; present {
						var limit int
						if err := json.Unmarshal(value, &limit); err != nil || limit < 1 || limit > 500 {
							return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "limit must be from 1 to 500"}}}, nil
						}
					}
				}
			}
		}
		return next(ctx, method, req)
	}
}
