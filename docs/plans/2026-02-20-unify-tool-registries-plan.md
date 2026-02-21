# Unify AI + MCP Tool Registries — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace hand-written JSON schemas in `internal/ai/tools.go` with an in-process MCP client that delegates to the MCP server, eliminating duplicated tool definitions.

**Architecture:** The AI `ToolRegistry` connects to the MCP server via in-memory transport. MCP tools (status, diagnose, find, trace, timeline, topology, compare, query, schema) are dispatched through `session.CallTool()`. AI-only tools (tail, metrics, render) remain registered directly with hand-written schemas using `jsonSchema()` helper (kept for these 3 tools only).

**Tech Stack:** Go generics, MCP Go SDK (`github.com/modelcontextprotocol/go-sdk/mcp`), existing `google/jsonschema-go` (indirect dep via MCP SDK)

---

### Task 1: Add MCP() getter to expose inner server

**Files:**
- Modify: `internal/mcp/server.go:16-21`

**Step 1: Add the getter**

Add after the `Server` struct definition (line 21):

```go
// MCP returns the inner MCP server for in-process client connections.
func (s *Server) MCP() *mcp.Server { return s.mcp }
```

**Step 2: Verify it compiles**

Run: `go build ./internal/mcp/`
Expected: success

**Step 3: Commit**

```bash
git add internal/mcp/server.go
git commit -m "mcp: expose inner server via MCP() getter"
```

---

### Task 2: Rewire main.go — create MCP server unconditionally

**Files:**
- Modify: `cmd/fanout/main.go:179-225`

Currently the MCP server is created conditionally inside `if cfg.MCPEnabled` (line 220-225), and the AI tool registry is created inside `if cfg.AIAPIKey != ""` (line 197). We need to:

1. Create the MCP server unconditionally (before the AI block)
2. Pass it to `ai.NewToolRegistry` (changed signature — done in Task 3)
3. Keep `RegisterRoutes` conditional on `cfg.MCPEnabled`

**Step 1: Move MCP server creation before AI block**

Replace lines 177-225 with:

```go
	// Create shared service layer
	svc := service.New(q, cfg)

	// MCP server — always created (AI orchestrator connects via in-memory transport)
	mcpServer := mcp.NewServer(svc, q, cfg)
	go mcp.RunCleanup(ctx)

	// AI orchestrator (optional — needs API key)
	var orch *ai.Orchestrator
	var wsHandler *ai.WSHandler

	if cfg.AIAPIKey != "" {
		var provider ai.Provider
		switch cfg.AIProvider {
		case "openai":
			provider = ai.NewOpenAIProvider(cfg.AIAPIKey, cfg.AIModel, cfg.AIBaseURL)
			slog.Info("AI provider: OpenAI", "model", cfg.AIModel)
		case "anthropic", "":
			provider = ai.NewAnthropicProvider(cfg.AIAPIKey, cfg.AIModel, cfg.AIBaseURL)
			slog.Info("AI provider: Anthropic", "model", cfg.AIModel)
		default:
			slog.Error("unsupported AI_PROVIDER", "value", cfg.AIProvider, "supported", "anthropic, openai")
			os.Exit(1)
		}

		tools, err := ai.NewToolRegistry(ctx, mcpServer.MCP(), svc, cfg)
		if err != nil {
			slog.Error("AI tool registry init failed", "err", err)
			os.Exit(1)
		}
		orch = ai.NewOrchestrator(provider, tools, svc, cfg)
		wsHandler = ai.NewWSHandler(ctx, orch, svc)
		if cfg.APIToken == "" {
			slog.Warn("AI chat enabled without API_TOKEN — chat endpoint is unauthenticated")
		}
	} else {
		slog.Warn("AI_API_KEY not set — chat disabled, ingest + health active")
	}
```

And replace the old MCP block (lines 219-225) with:

```go
	// MCP HTTP endpoint (optional — MCP server always runs for AI, HTTP route is conditional)
	if cfg.MCPEnabled {
		mcpServer.RegisterRoutes(e)
		slog.Info("MCP server enabled", "path", "/mcp")
	}
```

**Step 2: This won't compile yet** — `ai.NewToolRegistry` signature changes in Task 3. That's expected. Move on to Task 3.

---

### Task 3: Rewrite tools.go — MCP client + AI-only tools

This is the core task. Replace the entire `NewToolRegistry` function body and delete the shared tool handlers.

**Files:**
- Rewrite: `internal/ai/tools.go` (complete replacement)

**Step 1: Write the new tools.go**

The new file keeps `ToolHandler`, `ToolRegistry`, `marshal()`, and the AI-only tools (tail, metrics, render). It replaces `NewToolRegistry` to connect via in-memory MCP transport.

```go
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandler executes a tool and returns JSON result.
type ToolHandler func(ctx context.Context, input json.RawMessage) (string, error)

// ToolRegistry maps tool names to definitions and handlers.
// MCP-backed tools are dispatched via an in-process MCP client session.
// AI-only tools (tail, metrics, render) are handled directly.
type ToolRegistry struct {
	session  *mcp.ClientSession
	defs     []ToolDef
	handlers map[string]ToolHandler // AI-only tool overrides
}

// NewToolRegistry connects to the MCP server via in-memory transport,
// loads MCP tool definitions, and registers AI-only tools.
func NewToolRegistry(ctx context.Context, mcpServer *mcp.Server, svc *service.Service, cfg config.Config) (*ToolRegistry, error) {
	// Connect to MCP server in-process (same pattern as goal project)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	go func() {
		if _, err := mcpServer.Connect(ctx, serverTransport, nil); err != nil {
			slog.Error("MCP server in-process connection failed", "err", err)
		}
	}()

	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "ai-orchestrator",
		Version: "1.0.0",
	}, nil)

	session, err := mcpClient.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("connecting in-process MCP client: %w", err)
	}

	// Load tool definitions from MCP server
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("listing MCP tools: %w", err)
	}

	r := &ToolRegistry{
		session:  session,
		handlers: make(map[string]ToolHandler),
	}

	// Convert MCP tool definitions to AI ToolDef format.
	// Skip MCP's "render" — AI has its own render tool (raw HTML passthrough).
	for _, t := range toolsResult.Tools {
		if t.Name == "render" {
			continue
		}
		r.defs = append(r.defs, ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	// Register AI-only tools
	r.registerAITools(svc, cfg)

	slog.Info("AI tool registry initialized",
		"mcp_tools", len(toolsResult.Tools)-1, // -1 for skipped MCP render
		"ai_only_tools", len(r.handlers),
		"total", len(r.defs))

	return r, nil
}

// Defs returns all tool definitions for the LLM.
func (r *ToolRegistry) Defs() []ToolDef {
	return r.defs
}

// Execute runs a tool by name and returns the result.
func (r *ToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	// AI-only tools take priority
	if h, ok := r.handlers[name]; ok {
		result, err := h(ctx, input)
		if err != nil {
			slog.Warn("tool execution failed", "tool", name, "err", err)
			return "", err
		}
		return result, nil
	}

	// Dispatch to MCP server
	var args map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid tool input: %w", err)
		}
	}

	result, err := r.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		slog.Warn("MCP tool execution failed", "tool", name, "err", err)
		return "", err
	}

	if result.IsError {
		text := extractMCPText(result)
		slog.Warn("MCP tool returned error", "tool", name, "text", text)
		return "", fmt.Errorf("tool %s: %s", name, text)
	}

	return extractMCPText(result), nil
}

// extractMCPText pulls text content from an MCP CallToolResult.
func extractMCPText(result *mcp.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
			continue
		}
		// Fallback: marshal and extract text field
		data, err := c.MarshalJSON()
		if err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if text, ok := m["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
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

// registerAITools adds tools that are AI-only (not in MCP).
func (r *ToolRegistry) registerAITools(svc *service.Service, cfg config.Config) {
	// metrics — AI-only (not yet an MCP tool)
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

	// tail — AI-only (live log streaming, not an MCP concept)
	r.register(ToolDef{
		Name:        "tail",
		Description: "Start live log tailing. Returns recent logs and begins streaming new entries in real-time. User sees a terminal-style log viewer. Use when user asks to tail, watch, or follow logs.",
		InputSchema: jsonSchema(map[string]property{
			"service":   {Type: "string", Desc: "Service name to tail (required)", Required: true},
			"pattern":   {Type: "string", Desc: "Filter log body by text pattern (optional)"},
			"severity":  {Type: "string", Desc: "Minimum severity: ERROR, WARN, INFO, DEBUG (optional)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Service   string `json:"service"`
			Pattern   string `json:"pattern"`
			Severity  string `json:"severity"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Service == "" {
			return "", fmt.Errorf("service is required")
		}

		var severities []string
		if p.Severity != "" {
			severities = []string{p.Severity}
		}

		res, err := svc.Find(ctx, service.FindParams{
			Query:     p.Pattern,
			Service:   p.Service,
			Severity:  severities,
			Type:      "logs",
			Window:    5,
			Limit:     20,
			Namespace: p.Namespace,
		})
		if err != nil {
			return "", err
		}

		type tailResult struct {
			Logs []service.LogResult `json:"logs"`
			Tail struct {
				Service   string    `json:"service"`
				Pattern   string    `json:"pattern,omitempty"`
				Severity  string    `json:"severity,omitempty"`
				Namespace string    `json:"namespace,omitempty"`
				Since     time.Time `json:"since,omitempty"`
			} `json:"tail"`
		}
		out := tailResult{Logs: res.Logs}
		out.Tail.Service = p.Service
		out.Tail.Pattern = p.Pattern
		out.Tail.Severity = p.Severity
		out.Tail.Namespace = svc.ResolveNamespace(p.Namespace)
		if len(res.Logs) > 0 {
			if t, err := time.Parse("2006-01-02T15:04:05Z", res.Logs[0].Time); err == nil {
				out.Tail.Since = t
			} else {
				slog.Warn("tail tool: failed to parse log time for Since cursor",
					"time", res.Logs[0].Time, "err", err)
			}
		}
		return marshal(out)
	})

	// render — AI-only (raw HTML passthrough; MCP render is section-based reports)
	// SECURITY: The raw HTML returned here is sanitized by the orchestrator
	// (via bluemonday) before being sent to the browser. Never bypass sanitization.
	r.register(ToolDef{
		Name: "render",
		Description: `Render HTML card inline in chat. CSS vars: --text-primary/secondary/muted, --bg-primary/secondary/tertiary, --border-color, --success, --warning, --danger, --signal-trace/log/metric/error, --font-sans/mono, --radius. Shoelace: <sl-card>, <sl-badge>, <sl-tag>, <sl-icon>, <sl-progress-bar>, <sl-tooltip>.

Metric grid (MUST wrap each metric in a div): <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:1rem"><div><div class="metric-value">99.9%</div><div class="metric-label">Label</div></div>...</div>. Table: <table class="table"><thead>...</thead><tbody>...</tbody></table> — put status/icon in col 1, name in col 2, numeric cols 3+ (auto right-aligned).

SVG viz (class + data-attr JSON): trace-waterfall(data-spans:[{id,parent,service,op,start,dur,status}]), topology-graph(data-graph:{nodes:[{id,status,rpm,p95,errors}],edges:[{source,target,rpm,errorRate}]}), flow-sankey(data-flow:{nodes:[{id,label,rpm,status?}],links:[{source,target,value}]}), flame-graph(data-frames:[{name,depth,x,w,self,total,samples,service}]), latency-heatmap(data-heatmap:{buckets:[],times:[],values:[[]]}), dep-matrix(data-matrix:{services:[],cells:[{from,to,errorRate,rpm,p95}]}), endpoint-breakdown(data-endpoints:{endpoints:[{method,path,rpm,p50,p95,p99,errorRate,status,trend:[]}]}), correlation-view(data-correlation:{times:[],panels:[{label,color,values:[],baseline?,markers?:[{t,label,severity}]}]}), timeseries-chart(data-timeseries:{series:[{label,color,values:[],type}],labels:[],yLabel}), bar-chart(data-barchart:{bars:[{label,value,color?}],yLabel?,horizontal?}).

Wrap viz: <div class="viz-card"><div class="viz-card-header"><div class="viz-card-title"><span class="signal-dot" style="background:var(--signal-trace)"></span>Title</div><div class="viz-card-actions"><button class="btn-icon btn-viz-expand" title="Expand"><svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 2H2v4M10 2h4v4M6 14H2v-4M10 14h4v-4"/></svg></button></div></div><div class="viz-card-body"><div class="CLASS" data-ATTR='JSON'></div></div></div>`,
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
}

// JSON Schema helpers — used only by AI-only tools above.

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
```

**Step 2: Remove unused imports**

The new file no longer imports `"github.com/labstack/fanout/internal/query"`. The `config` import replaces the old `lakeDir string` param. Verify imports match the code above.

**Step 3: Build**

Run: `go build ./...`
Expected: success (main.go was updated in Task 2 to match new signature)

**Step 4: Commit**

```bash
git add internal/ai/tools.go cmd/fanout/main.go
git commit -m "ai: unify tool registry with in-process MCP client

Replaces hand-written JSON schemas and duplicated tool handlers with an
in-process MCP client connection. Shared tools (status, diagnose, find,
trace, timeline, topology, compare, query, schema) dispatch through
session.CallTool(). AI-only tools (tail, metrics, render) remain
registered directly.

Closes #30"
```

---

### Task 4: Fix orchestrator_test.go — update ToolRegistry construction

**Files:**
- Modify: `internal/ai/orchestrator_test.go:174`

The test creates a bare `&ToolRegistry{}`. With the new struct fields (`session`, `handlers`), this still compiles fine (zero values). But verify.

**Step 1: Verify tests compile**

Run: `go test ./internal/ai/ -count=1 -run .`
Expected: all tests pass (the `&ToolRegistry{}` literal uses zero values which are valid)

**Step 2: If tests fail**, update the construction to:

```go
NewOrchestrator(nil, &ToolRegistry{handlers: make(map[string]ToolHandler)}, nil, config.Config{})
```

**Step 3: Commit if changes were needed**

```bash
git add internal/ai/orchestrator_test.go
git commit -m "test: fix ToolRegistry construction in orchestrator test"
```

---

### Task 5: Write integration test for MCP-backed tool registry

**Files:**
- Modify: `internal/ai/orchestrator_test.go` (add new test)

**Step 1: Write test for NewToolRegistry + Execute**

This test creates a real MCP server with a single test tool, connects the AI registry to it, and verifies tool listing and execution.

Add to `internal/ai/orchestrator_test.go`:

```go
func TestToolRegistryMCPIntegration(t *testing.T) {
	ctx := context.Background()

	// Create an MCP server with a test tool
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	type testInput struct {
		Name string `json:"name" jsonschema:"The name to greet"`
	}
	type testOutput struct {
		Greeting string `json:"greeting"`
	}

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "greet",
		Description: "Say hello",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testInput) (*mcp.CallToolResult, testOutput, error) {
		return nil, testOutput{Greeting: "hello " + in.Name}, nil
	})

	// Connect AI registry (nil svc/cfg — AI-only tools will panic but we won't call them)
	registry, err := NewToolRegistry(ctx, mcpServer, nil, config.Config{})
	if err != nil {
		t.Fatal(err)
	}

	// Verify tool definitions include the MCP tool
	found := false
	for _, d := range registry.Defs() {
		if d.Name == "greet" {
			found = true
			if d.Description != "Say hello" {
				t.Errorf("description = %q, want %q", d.Description, "Say hello")
			}
		}
	}
	if !found {
		t.Error("greet tool not found in Defs()")
	}

	// Execute the MCP tool
	result, err := registry.Execute(ctx, "greet", json.RawMessage(`{"name":"world"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("result = %q, want containing 'hello world'", result)
	}

	// Execute unknown tool
	_, err = registry.Execute(ctx, "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}
```

Note: This test creates a minimal MCP server without the real service layer. The `NewToolRegistry` call will still try to register AI-only tools (metrics, tail, render) which reference `svc`. Since `svc` is nil and these tools aren't called, the registry will initialize fine — the tool handlers just capture the nil `svc` in closures but don't call it during registration.

Actually — `registerAITools` needs `svc` to be non-nil at handler call time only, not at registration time. The closures capture `svc` but don't invoke it. So passing nil is safe for this test.

**Step 2: Run test**

Run: `go test ./internal/ai/ -count=1 -run TestToolRegistryMCPIntegration -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/ai/orchestrator_test.go
git commit -m "test: add integration test for MCP-backed tool registry"
```

---

### Task 6: Full build + test pass verification

**Files:** none (verification only)

**Step 1: Run full build**

Run: `just build`
Expected: success

**Step 2: Run all tests**

Run: `go test ./... -count=1`
Expected: all pass

**Step 3: Verify no import cycle issues**

Run: `go vet ./...`
Expected: no errors

The `internal/ai` package now imports `github.com/modelcontextprotocol/go-sdk/mcp` for the client session. The `internal/mcp` package also imports it. There's no circular dependency because `internal/ai` doesn't import `internal/mcp` — it receives a `*mcp.Server` (from the SDK, not from the internal package) via `main.go`.
