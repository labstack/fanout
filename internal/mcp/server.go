package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/observability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Observability interface {
	Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error)
	Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error)
	Performance(context.Context, observability.Scope, string, int) (observability.Result[observability.Performance], error)
	Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error)
	Logs(context.Context, observability.Scope, string, string, string, int) (observability.Result[observability.Logs], error)
}

type QueryInput struct {
	Window    string `json:"window,omitempty" jsonschema:"Time window such as 15m, 1h, or 24h; defaults to 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; defaults to the server namespace"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum services or edges to return, from 1 to 500"`
}

type PerformanceInput struct {
	Window    string `json:"window,omitempty" jsonschema:"Time window such as 15m, 1h, or 24h; defaults to 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; defaults to the server namespace"`
	Service   string `json:"service,omitempty" jsonschema:"Optional exact OpenTelemetry service name; omit for the whole system"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum endpoints to return, from 1 to 500"`
}

type TraceInput struct {
	Window    string `json:"window,omitempty" jsonschema:"Trace lookup window such as 1h or 24h; defaults to 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; defaults to the server namespace"`
	TraceID   string `json:"trace_id,omitempty" jsonschema:"Exact trace ID; omit to inspect the most relevant recent error or slow trace"`
	Service   string `json:"service,omitempty" jsonschema:"Optional service filter when choosing a recent trace"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum spans and correlated logs to return, from 1 to 500"`
}

type LogsInput struct {
	Window    string `json:"window,omitempty" jsonschema:"Time window such as 15m, 1h, or 24h; defaults to 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"OpenTelemetry service namespace; defaults to the server namespace"`
	Service   string `json:"service,omitempty" jsonschema:"Optional exact OpenTelemetry service name"`
	Severity  string `json:"severity,omitempty" jsonschema:"Optional exact severity such as ERROR, WARN, or INFO"`
	Search    string `json:"search,omitempty" jsonschema:"Optional case-insensitive text contained in the log body"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum log entries to return, from 1 to 500"`
}

type Server struct {
	mcp     *mcp.Server
	queries Observability
	now     func() time.Time
}

func New(queries Observability, version string) *Server {
	s := &Server{
		mcp: mcp.NewServer(&mcp.Implementation{
			Name:    "fanout",
			Title:   "Fanout Observability",
			Version: version,
		}, nil),
		queries: queries,
		now:     time.Now,
	}
	s.registerTools()
	s.registerAppResources()
	return s
}

func (s *Server) MCP() *mcp.Server { return s.mcp }

func (s *Server) HTTPHandler() http.Handler {
	// The MCP OAuth bearer middleware runs before this handler. Disable the SDK's
	// localhost Host heuristic because a legitimate local reverse proxy connects
	// over loopback while preserving the public Host (for example,
	// demo.fanout.test); the heuristic otherwise rejects every browser MCP App.
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
		Name:        "observability_overview",
		Title:       "System health overview",
		Description: "Summarize service health for a bounded telemetry window. Start here for incident triage.",
		Annotations: readOnly,
		Meta:        appToolMeta(overviewAppURI),
	}, s.overview)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "service_topology",
		Title:       "Service dependency topology",
		Description: "Return services and observed dependency edges with health, traffic, latency, and error data.",
		Annotations: readOnly,
		Meta:        appToolMeta(topologyAppURI),
	}, s.topology)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "service_performance",
		Title:       "Service performance explorer",
		Description: "Inspect activity, errors, latency, endpoints, cross-signal correlation, and change over time for one service or the system.",
		Annotations: readOnly,
		Meta:        appToolMeta(performanceAppURI),
	}, s.performance)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "trace_detail",
		Title:       "Trace detail",
		Description: "Inspect an exact trace, or select the most relevant recent error or slow trace, with spans, waterfall, flame graph, and correlated logs.",
		Annotations: readOnly,
		Meta:        appToolMeta(traceAppURI),
	}, s.trace)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "search_logs",
		Title:       "Log explorer",
		Description: "Search and filter logs with a severity timeline and links back to correlated traces.",
		Annotations: readOnly,
		Meta:        appToolMeta(logsAppURI),
	}, s.logs)
}

func (s *Server) overview(ctx context.Context, _ *mcp.CallToolRequest, input QueryInput) (*mcp.CallToolResult, observability.Result[observability.Overview], error) {
	scope, err := s.scope(input)
	if err != nil {
		return nil, observability.Result[observability.Overview]{}, err
	}
	output, err := s.queries.Overview(ctx, scope, input.Limit)
	if err != nil {
		return nil, output, err
	}
	return summary(output.Summary), output, nil
}

func (s *Server) topology(ctx context.Context, _ *mcp.CallToolRequest, input QueryInput) (*mcp.CallToolResult, observability.Result[observability.Topology], error) {
	scope, err := s.scope(input)
	if err != nil {
		return nil, observability.Result[observability.Topology]{}, err
	}
	output, err := s.queries.Topology(ctx, scope, input.Limit)
	if err != nil {
		return nil, output, err
	}
	return summary(output.Summary), output, nil
}

func (s *Server) performance(ctx context.Context, _ *mcp.CallToolRequest, input PerformanceInput) (*mcp.CallToolResult, observability.Result[observability.Performance], error) {
	scope, err := s.scope(QueryInput{Window: input.Window, Namespace: input.Namespace, Limit: input.Limit})
	if err != nil {
		return nil, observability.Result[observability.Performance]{}, err
	}
	output, err := s.queries.Performance(ctx, scope, input.Service, input.Limit)
	if err != nil {
		return nil, output, err
	}
	return summary(output.Summary), output, nil
}

func (s *Server) trace(ctx context.Context, _ *mcp.CallToolRequest, input TraceInput) (*mcp.CallToolResult, observability.Result[observability.TraceDetail], error) {
	scope, err := s.scope(QueryInput{Window: input.Window, Namespace: input.Namespace, Limit: input.Limit})
	if err != nil {
		return nil, observability.Result[observability.TraceDetail]{}, err
	}
	output, err := s.queries.Trace(ctx, scope, input.TraceID, input.Service, input.Limit)
	if err != nil {
		return nil, output, err
	}
	return summary(output.Summary), output, nil
}

func (s *Server) logs(ctx context.Context, _ *mcp.CallToolRequest, input LogsInput) (*mcp.CallToolResult, observability.Result[observability.Logs], error) {
	scope, err := s.scope(QueryInput{Window: input.Window, Namespace: input.Namespace, Limit: input.Limit})
	if err != nil {
		return nil, observability.Result[observability.Logs]{}, err
	}
	output, err := s.queries.Logs(ctx, scope, input.Service, input.Severity, input.Search, input.Limit)
	if err != nil {
		return nil, output, err
	}
	return summary(output.Summary), output, nil
}

func (s *Server) scope(input QueryInput) (observability.Scope, error) {
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
		// Keep the deprecated flat form for older MCP Apps hosts. The nested
		// value above is authoritative under the current extension contract.
		"ui/resourceUri": resourceURI,
	}
}
