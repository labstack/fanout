package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/toolerror"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpUIExtension = "io.modelcontextprotocol/ui"

const ProtocolVersion = "2026-07-28"

var supportedProtocolVersions = []string{ProtocolVersion, "2025-11-25", "2025-06-18"}

const staticCatalogTTLMs = 5 * 60 * 1000

const serverInstructions = "Start with get_observability_overview for system health, use get_intelligence_snapshot for the latest precomputed anomalies and log patterns, get_service_topology for direct dependency edges (use this preset once for a service map; do not repeat the same map with query_telemetry), get_service_dependencies for bounded upstream or downstream reachability from a service, get_service_performance for latency, errors and slow operations across HTTP, gRPC and consumers, inspect_trace for one trace, and search_logs for application events. Treat schema, timestamps, provenance, and bounded time windows as authoritative. Dashboard tools are scoped to the authenticated user. For a custom chart in chat, use query_telemetry with one v1 panel; every chart view returns a dashboard fragment rendered by the shared panel renderer. Do not present empty results; state no matching data findings in the answer and prefer the signal a service actually has. Dashboards contain only panels that return data; text panels are exempt. To build a dashboard, read get_telemetry_schema, draft panels, run preview_panels until every non-text panel returns data, then create_dashboard. To change one, get_dashboard first and use edit_dashboard; replace only for a redesign the user asked for."

type Observability interface {
	Dependencies(context.Context, observability.Scope, observability.DependencyOptions) (observability.Result[observability.Dependencies], error)
	Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error)
}

type IntelligenceSnapshots interface {
	LatestSnapshot() *intelligence.Snapshot
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
	Limit     int        `json:"limit,omitempty" jsonschema:"Maximum operations to return, from 1 to 500"`
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
	Search    string     `json:"search,omitempty" jsonschema:"Optional case-insensitive text contained in the log body, at most 200 characters"`
	Limit     int        `json:"limit,omitempty" jsonschema:"Maximum log entries to return, from 1 to 500"`
}

// panelExecutor keeps execution injectable while production uses the pinned engine.
type panelExecutor interface {
	Run(context.Context, query.RunRequest) ([]query.Result, error)
	Validate(context.Context, *query.Dashboard) error
	Schema(context.Context, query.SchemaRequest) (*query.Schema, error)
	Exemplars(context.Context, query.ExemplarRequest) (query.ExemplarResponse, error)
	ResolveVariables(context.Context, query.ResolveRequest) (map[string][]query.Option, error)
	ResolveWindow(time.Time, time.Time) (time.Time, time.Time, error)
	ServiceGuidance(context.Context, time.Time, time.Time, string, string) (string, error)
	EmptyGuidance(context.Context, query.RunRequest) (string, string, error)
}

type Server struct {
	mcp          *mcp.Server
	queries      Observability
	intelligence IntelligenceSnapshots
	dashboards   *dashboard.Service
	panels       panelExecutor
	now          func() time.Time
	limitTools   map[string]bool
}

func NewWithIntelligence(queries Observability, dashboards *dashboard.Service, panels *query.Executor, snapshots IntelligenceSnapshots, version string) *Server {
	return newServer(queries, dashboards, panels, snapshots, version)
}

func newServer(queries Observability, dashboards *dashboard.Service, panels *query.Executor, snapshots IntelligenceSnapshots, version string) *Server {
	s := &Server{
		mcp: mcp.NewServer(&mcp.Implementation{
			Name:    "fanout",
			Title:   "Fanout Observability",
			Version: version,
		}, &mcp.ServerOptions{SupportedProtocolVersions: supportedProtocolVersions, Instructions: serverInstructions, Capabilities: &mcp.ServerCapabilities{
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
		now:          time.Now,
		limitTools:   make(map[string]bool),
	}
	if panels != nil {
		s.panels = panels
	}
	s.registerTools()
	s.registerPanelTools()
	s.registerDashboardTools()
	s.registerAppResources()
	s.registerFanQLResource()
	s.mcp.AddReceivingMiddleware(limitLegacySessions(s.mcp), normalizeToolErrors, recoverToolPanic, addStaticCacheHints, filterMCPAppToolMetadata, s.validateToolArguments)
	return s
}

// Tool handlers run in the MCP server's request goroutine, outside the agent's
// execution stack. Recover there so local and remote calls keep a tool error.
func recoverToolPanic(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
		if method == "tools/call" {
			ctx = dashboard.TrackSave(ctx)
			name := ""
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				name = call.Params.Name
			}
			defer func() {
				if value := recover(); value != nil {
					slog.Error("MCP tool panicked", "tool", name, "panic", value, "stack", string(debug.Stack()))
					failure := summary("tool execution failed")
					failure.IsError = true
					if dashboard.SaveCommitted(ctx) {
						failure.Content = []mcp.Content{&mcp.TextContent{Text: toolerror.JSON("interrupted", "Tool execution was interrupted. The save may have completed.")}}
						failure.StructuredContent = toolerror.New("interrupted", "Tool execution was interrupted. The save may have completed.")
					}
					result, err = failure, nil
				}
			}()
		}
		return next(ctx, method, req)
	}
}

// Normalize only product tool failures; JSON-RPC errors and panel frames keep
// their protocol contracts. Both content paths carry the same error object.
func normalizeToolErrors(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		failure, ok := result.(*mcp.CallToolResult)
		if err != nil || method != "tools/call" || !ok || !failure.IsError {
			return result, err
		}
		// A tool's output schema rarely declares an error field, so a failure
		// keeps structured content only when the tool supplied it.
		structured := failure.StructuredContent != nil
		content := ""
		if structured {
			if raw, marshalErr := json.Marshal(failure.StructuredContent); marshalErr == nil {
				content = string(raw)
			}
		} else {
			for _, block := range failure.Content {
				if value, ok := block.(*mcp.TextContent); ok {
					content += value.Text
				}
			}
		}
		payload := toolerror.Normalize(content)
		// Product guidance is already presented and may contain observed routes.
		alreadyPresented := []string{"no_data", "unknown_service", "invalid_spec"}
		switch failure := payload["error"].(type) {
		case *toolerror.Error:
			if !slices.Contains(alreadyPresented, failure.Code) {
				failure.Message = query.PresentMessage(failure.Message)
			}
		case map[string]any:
			code, _ := failure["code"].(string)
			if message, ok := failure["message"].(string); ok && !slices.Contains(alreadyPresented, code) {
				failure["message"] = query.PresentMessage(message)
			}
		}
		raw, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if structured {
			failure.StructuredContent = payload
		}
		failure.Content = []mcp.Content{&mcp.TextContent{Text: string(raw)}}
		return failure, nil
	}
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
	request, ok := req.(interface {
		ClientCapabilities() *mcp.ClientCapabilities
		ProtocolVersion() string
	})
	if !ok {
		return false
	}
	capabilities := request.ClientCapabilities()
	legacy := request.ProtocolVersion() < ProtocolVersion
	if extra := req.GetExtra(); extra != nil && extra.Header != nil {
		legacy = extra.Header.Get("MCP-Protocol-Version") < ProtocolVersion
	}
	// The SDK prefers per-request capabilities even on legacy sessions; their
	// declaration belongs to initialize, including when its version negotiated down.
	if legacy {
		if session, ok := req.GetSession().(*mcp.ServerSession); ok && session != nil {
			if initialized := session.InitializeParams(); initialized != nil {
				capabilities = initialized.Capabilities
			}
		}
	}
	if capabilities == nil {
		return false
	}
	settings, ok := capabilities.Extensions[mcpUIExtension].(map[string]any)
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
	modern := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcp },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			PropagateRequestCancellation: true,
			DisableLocalhostProtection:   true,
		},
	)
	legacy := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcp },
		&mcp.StreamableHTTPOptions{
			SessionTimeout:             legacySessionTimeout,
			DisableLocalhostProtection: true,
		},
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// The SDK defaults headerless requests to 2025-03-26, which allows
			// batches. Decode one message before entering that compatibility path.
			message, err := ReadHTTPMessage(r)
			if err != nil {
				WriteHTTPMessageError(w, err)
				return
			}
			if request, ok := message.(*jsonrpc.Request); ok {
				if request.Method == "initialize" {
					if r.Header.Get("MCP-Protocol-Version") >= ProtocolVersion {
						// Initialize selects legacy semantics; the SDK otherwise
						// rejects this header before negotiating the body version.
						r = r.Clone(r.Context())
						r.Header.Del("MCP-Protocol-Version")
					}
					legacy.ServeHTTP(w, r)
					return
				}
				var params map[string]json.RawMessage
				var meta mcp.Meta
				if json.Unmarshal(request.Params, &params) == nil && json.Unmarshal(params["_meta"], &meta) == nil && meta[mcp.MetaKeyProtocolVersion] != nil {
					modern.ServeHTTP(w, r)
					return
				}
			}
		}
		if r.Header.Get("MCP-Protocol-Version") >= ProtocolVersion {
			modern.ServeHTTP(w, r)
			return
		}
		legacy.ServeHTTP(w, r)
	})
}

func (s *Server) registerTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "get_service_dependencies", Title: "Service dependency traversal",
		Description: "Find upstream or downstream dependencies of one service with minimum hop counts. Uses the full scoped edge rollup, handles cycles, and reports truncation from depth or node limits.",
		Annotations: readOnly,
	}, s.dependencies)
	fragmentTool(s, &mcp.Tool{
		Name:        "get_observability_overview",
		Title:       "System health overview",
		Description: "Summarize service health for a bounded telemetry window. Start here for incident triage.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.overview)
	fragmentTool(s, &mcp.Tool{
		Name:        "get_service_topology",
		Title:       "Service dependency topology",
		Description: "Return services and observed dependency edges with health, traffic, latency, and error data.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.topology)
	fragmentTool(s, &mcp.Tool{
		Name:        "get_service_performance",
		Title:       "Service performance explorer",
		Description: "Display separate latency, error rate and request rate panels plus a Slow operations table showing p95 duration grouped by operation over server and consumer spans, covering HTTP, gRPC and message consumers, for one service or the system.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.performance)
	fragmentTool(s, &mcp.Tool{
		Name:        "inspect_trace",
		Title:       "Trace detail",
		Description: "Inspect an exact trace, or select the most relevant recent error or slow trace, with spans, waterfall, flame graph, and correlated logs.",
		Annotations: readOnly,
		Meta:        appToolMeta(panelsAppURI),
	}, s.trace)
	fragmentTool(s, &mcp.Tool{
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
	if len(output.Data.Nodes) == 0 {
		if err := s.checkService(ctx, scope, strings.TrimSpace(input.Service)); err != nil {
			return nil, observability.Result[observability.Dependencies]{}, err
		}
	}
	return summary(output.Summary), output, nil
}

func (s *Server) intelligenceSnapshot(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, intelligence.Snapshot, error) {
	snapshot := s.intelligence.LatestSnapshot()
	if snapshot == nil {
		return nil, intelligence.Snapshot{}, fmt.Errorf("intelligence snapshot is not ready")
	}
	return summary(snapshot.Summary), *snapshot, nil
}

func (s *Server) presetFragment(ctx context.Context, input QueryInput, kind, service, severity, search string) (*mcp.CallToolResult, PanelFragment, error) {
	scope, err := s.scope(input)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	d := fragmentPreset(kind, service, input.Namespace, severity, search, input.Limit)
	d.Time = query.Time{From: &scope.Start, To: &scope.End, Refresh: "off"}
	out, err := s.executeFragment(ctx, query.RunRequest{Dashboard: d})
	if err != nil {
		return nil, PanelFragment{}, err
	}
	if allResultsEmpty(out.Results) {
		return nil, PanelFragment{}, s.noDataError(ctx, out)
	}
	return finishFragment(ctx, out, "preset")
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
	if utf8.RuneCountInString(input.Search) > 200 {
		return nil, PanelFragment{}, errors.New("search must be at most 200 characters")
	}
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
	d.Time = query.Time{From: &scope.Start, To: &scope.End, Refresh: "off"}
	// A representative lookup is performed once. The list must represent that
	// selected trace, even when no match exists (empty id cannot show others).
	id := detail.Data.TraceID
	d.Panels[0].Query.Where = append(d.Panels[0].Query.Where, "trace_id = '"+strings.ReplaceAll(id, "'", "''")+"'")
	out, err := s.executeFragment(ctx, query.RunRequest{Dashboard: d})
	if err != nil {
		return nil, PanelFragment{}, err
	}
	if allResultsEmpty(out.Results) && len(detail.Data.Spans) == 0 && len(detail.Data.Logs) == 0 {
		return nil, PanelFragment{}, s.noDataError(ctx, out)
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
	out.View = fragmentView(out, "preset")
	out, err = boundFragment(ctx, out)
	if err != nil {
		return nil, PanelFragment{}, err
	}
	return summary(fragmentSummary(out)), out, nil
}

func allResultsEmpty(results []query.Result) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result.Status != query.StatusEmpty {
			return false
		}
	}
	return true
}

// Called only after a service-scoped tool succeeds with entirely empty results.
// Unknown names return a product tool error without a typed success payload.
func (s *Server) checkService(ctx context.Context, scope observability.Scope, service string) error {
	if service == "" || s.panels == nil {
		return nil
	}
	message, err := s.panels.ServiceGuidance(ctx, scope.Start, scope.End, scope.Namespace, service)
	if err != nil {
		return safePanelToolError(err)
	}
	if message == "" {
		return nil
	}
	return guidanceError("unknown_service", message)
}

func guidanceError(code, message string) error {
	// JSON escaping expands a byte by at most six bytes. Reserve the envelope
	// and bound before encoding so the agent's 16 KiB app cap cannot cut JSON.
	const maxMessageBytes = (16*1024 - 128) / 6
	if len(message) > maxMessageBytes {
		message = message[:maxMessageBytes-len("…")]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		message += "…"
	}
	return errors.New(toolerror.JSON(code, message))
}

func (s *Server) scope(input QueryInput) (observability.Scope, error) {
	if input.Limit < 0 || input.Limit > 500 {
		return observability.Scope{}, errors.New("limit must be from 1 to 500")
	}
	if (input.From == nil) != (input.To == nil) {
		return observability.Scope{}, errors.New("from and to must be supplied together")
	}
	var start, end time.Time
	if input.From != nil {
		if strings.TrimSpace(input.Window) != "" {
			return observability.Scope{}, errors.New("from/to cannot coexist with window")
		}
		start, end = input.From.UTC(), input.To.UTC()
		if !end.After(start) {
			return observability.Scope{}, errors.New("to must be after from")
		}
	} else {
		window := time.Hour
		if strings.TrimSpace(input.Window) != "" {
			parsed, err := time.ParseDuration(input.Window)
			if err != nil || parsed <= 0 {
				return observability.Scope{}, fmt.Errorf("window must be a positive duration such as 15m or 1h")
			}
			window = parsed
		}
		end = s.now().UTC()
		start = end.Add(-window)
	}
	if s.panels != nil {
		var err error
		start, end, err = s.panels.ResolveWindow(start, end)
		if err != nil {
			return observability.Scope{}, safePanelToolError(err)
		}
	}
	return observability.Scope{Namespace: input.Namespace, Start: start, End: end}, nil
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
func (s *Server) validateToolArguments(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/call" {
			if call, ok := req.(*mcp.CallToolRequest); ok {
				if len(call.Params.Arguments) == 0 || strings.TrimSpace(string(call.Params.Arguments)) == "null" {
					call.Params.Arguments = json.RawMessage(`{}`)
				}
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(call.Params.Arguments, &raw); err != nil || raw == nil {
					return summaryToolError("arguments must be an object"), nil
				}
				if _, exists := raw["panels"]; exists && call.Params.Name == "query_panel_fragment" {
					return summaryToolError("panels is forbidden for query_panel_fragment"), nil
				}
				// Registered limit-bearing inputs use this one wire check: absent is the
				// typed default; explicit zero/null is invalid. Scope checks
				// typed calls too, where absence and zero cannot be separated.
				if value, present := raw["limit"]; present && s.limitTools[call.Params.Name] {
					var limit int
					if err := json.Unmarshal(value, &limit); err != nil || limit < 1 || limit > 500 {
						return summaryToolError("limit must be from 1 to 500"), nil
					}
				}
			}
		}
		return next(ctx, method, req)
	}
}

func summaryToolError(text string) *mcp.CallToolResult {
	result := summary(text)
	result.IsError = true
	return result
}

// Shared with the in-process agent catalog; protocol visibility is identical.
func AppOnly(meta mcp.Meta) bool { return appOnly(meta) }

const UIExtension = mcpUIExtension
const AppMIME = mcpAppMIME
