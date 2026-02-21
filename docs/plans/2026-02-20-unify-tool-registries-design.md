# Unify AI + MCP Tool Registries

Issue: #30

## Problem

`internal/ai/tools.go` hand-writes JSON schemas via `jsonSchema()` helper and `property` structs. The same 8 tools are duplicated between the AI registry and the MCP registry (`internal/mcp/`), with separate input structs, separate handler logic, and separate schema definitions. Changes to one registry don't propagate to the other.

## Decision

Use in-process MCP client (same pattern as `goal` project). The AI orchestrator connects to the MCP server via in-memory transport and dispatches shared tool calls through `session.CallTool()`. AI-only tools remain registered directly.

No new dependencies. The MCP SDK already uses `google/jsonschema-go` for schema generation from struct tags. The MCP input structs already have `jsonschema:"..."` tags.

## Architecture

```
main.go
  mcp.NewServer(svc, q, cfg)          <- always created (even if HTTP disabled)
  ai.NewToolRegistry(mcpServer, svc, cfg) <- connects via in-memory transport
  ai.NewOrchestrator(provider, tools, svc, cfg)

  if cfg.MCPEnabled { mcpServer.RegisterRoutes(e) }  <- HTTP route conditional
```

### Tool Dispatch

```
AI ToolRegistry.Execute(name, input)
  ├── name in AI-only handlers? -> call directly
  └── otherwise -> session.CallTool(name, args) -> extract text -> return
```

### Tool Ownership

| Tool | Owner | Notes |
|------|-------|-------|
| status, diagnose, find, trace, timeline, topology, compare, query, schema | MCP | Shared via in-memory client |
| metrics | AI-only | Not yet an MCP tool |
| tail | AI-only | Live streaming, not MCP concept |
| render | AI-only | Raw HTML passthrough (MCP render is different: section-based reports) |

## Changes

### `internal/mcp/server.go`

Add getter to expose inner MCP server:

```go
func (s *Server) MCP() *mcp.Server { return s.mcp }
```

### `internal/ai/tools.go`

Delete: `jsonSchema()`, `property` struct, all 11 handler registrations.

Replace `ToolRegistry`:

```go
type ToolRegistry struct {
    session  *mcpClient.Session
    defs     []ToolDef
    handlers map[string]ToolHandler  // AI-only overrides
}
```

`NewToolRegistry(mcpServer *mcp.Server, svc *service.Service, cfg config.Config)`:
1. Create in-memory transport pair
2. Connect MCP client to server
3. `session.ListTools()` -> convert to `[]ToolDef`
4. Register AI-only tools: tail, metrics, render

`Execute()`:
- Check `handlers` map first (AI-only tools)
- Fall through to `session.CallTool()` for MCP tools
- Extract text content from MCP result

Tool descriptions: use MCP descriptions as-is (single source of truth).

### `cmd/fanout/main.go`

- Create MCP server unconditionally (before AI setup)
- Pass `mcpServer.MCP()` to `ai.NewToolRegistry()`
- Keep `mcpServer.RegisterRoutes(e)` conditional on `cfg.MCPEnabled`

### Orchestrator

No changes to orchestrator logic. Special post-processing for `render` (sanitize HTML, send as card) and `tail` (extract TailConfig) stays in the orchestrator.

## What Gets Deleted

- `property` struct and `jsonSchema()` helper
- 8 inline anonymous input structs for shared tools
- 8 manual `json.Unmarshal` + default-value blocks
- ~200 lines of handler registration boilerplate

Net: ~120 line reduction, plus eliminated schema/handler drift.

## What Stays

- `ToolDef`, `ToolHandler` types (in provider.go)
- `marshal()` helper
- `ToolRegistry.Defs()` and `ToolRegistry.Execute()` interfaces (orchestrator depends on them)
- AI-only tool handlers (tail, metrics, render)
