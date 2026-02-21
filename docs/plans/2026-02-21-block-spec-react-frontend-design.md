# Block Spec + React Frontend

## Problem

The LLM currently generates raw HTML for chat responses. It writes `<table>`, `<div class="metric-value">`, and SVG wrappers with `data-*` JSON attributes. The server sanitizes this HTML via bluemonday before sending it to the client, which sets `innerHTML` and hopes for the best.

This is wrong. The LLM is a reasoning engine, not a template engine. The data comes from our tools and DuckDB queries. The LLM should analyze and explain — not format HTML.

## Design

### Block Spec

Every chat response is a JSON array of typed blocks. The server never sends HTML. The client owns all rendering.

```typescript
interface ChatEvent {
  type: "token" | "tool_call" | "tool_result" | "error" | "done";
  content?: string;
  name?: string;
  input?: object;
  error?: string;
  id?: string;
  blocks?: Block[];    // on "done" only
}

interface Block {
  type: BlockType;
  data: unknown;
}
```

Streaming: tokens stream in real-time for responsiveness. On `done`, the final `blocks` array replaces streamed text with structured content.

### Block Types

**Text** — LLM commentary rendered as markdown by the client.

```typescript
{ type: "text", data: { content: "The error rate correlates with the 14:30 deployment." } }
```

**Metrics** — key-value stats with status indicators.

```typescript
{ type: "metrics", data: {
  items: [
    { label: "Error Rate", value: 4.2, unit: "%", status: "danger" },
    { label: "P95 Latency", value: 340, unit: "ms", status: "warning" },
    { label: "Throughput", value: 1250, unit: "rpm", status: "ok" }
  ]
}}
```

**Table** — structured rows and columns.

```typescript
{ type: "table", data: {
  columns: [
    { key: "endpoint", label: "Endpoint" },
    { key: "errors", label: "Errors", align: "right" },
    { key: "p95", label: "P95", align: "right" }
  ],
  rows: [
    { endpoint: "/api/checkout", errors: 142, p95: 890 },
    { endpoint: "/api/cart", errors: 37, p95: 210 }
  ]
}}
```

**Charts** — standard chart types rendered by Recharts.

```typescript
// Timeseries
{ type: "timeseries", data: {
  title: "Error Rate",
  series: [{ label: "api-gateway", color: "#e55", values: [0.1, 0.1, 0.3, 4.2] }],
  labels: ["14:00", "14:15", "14:30", "14:45"],
  yLabel: "errors/min"
}}

// Bar
{ type: "bar", data: {
  title: "Requests by Service",
  bars: [{ label: "api-gw", value: 12500 }, { label: "auth", value: 8200 }],
  yLabel: "rpm",
  horizontal: false
}}

// Heatmap
{ type: "heatmap", data: {
  title: "Latency Distribution",
  buckets: [10, 50, 100, 500, 1000],
  times: ["14:00", "14:15", "14:30"],
  values: [[5, 20, 50, 10, 2], [3, 15, 45, 20, 5], [1, 8, 30, 40, 12]]
}}
```

**Domain viz** — observability-specific visualizations rendered by React + D3.

```typescript
// Trace waterfall
{ type: "trace_waterfall", data: {
  spans: [{ id: "a", parent: null, service: "api-gw", operation: "GET /checkout",
             start: 0, duration: 340, status: "error" }]
}}

// Topology
{ type: "topology", data: {
  nodes: [{ id: "api-gw", status: "degraded", rpm: 1250, p95: 340, errors: 53 }],
  edges: [{ source: "api-gw", target: "auth", rpm: 800, errorRate: 0.02 }]
}}

// Flame graph
{ type: "flame_graph", data: {
  frames: [{ name: "handleRequest", depth: 0, x: 0, w: 1.0,
              self: 12, total: 340, service: "api-gw" }]
}}

// Sankey
{ type: "sankey", data: {
  nodes: [{ id: "api-gw", label: "API Gateway", rpm: 1250, status: "ok" }],
  links: [{ source: "api-gw", target: "auth", value: 800 }]
}}

// Dependency matrix
{ type: "dep_matrix", data: {
  services: ["api-gw", "auth", "db"],
  cells: [{ from: "api-gw", to: "auth", errorRate: 0.02, rpm: 800, p95: 45 }]
}}

// Endpoint breakdown
{ type: "endpoints", data: {
  endpoints: [{ method: "POST", path: "/checkout", rpm: 450, p50: 120,
                 p95: 340, p99: 890, errorRate: 0.04, status: "degraded" }]
}}

// Correlation
{ type: "correlation", data: {
  times: ["14:00", "14:15", "14:30"],
  panels: [{ label: "Error Rate", color: "#e55", values: [0.1, 0.3, 4.2],
              markers: [{ t: "14:30", label: "deploy", severity: "warning" }] }]
}}
```

**Log tail** — live log streaming (unchanged protocol, structured entries).

```typescript
{ type: "tail", data: {
  entries: [{ time: 1708500000, severity: "error", service: "api-gw",
               body: "connection refused", traceId: "abc123" }]
}}
```

### Block Rendering

```tsx
function BlockRenderer({ block }: { block: Block }) {
  switch (block.type) {
    case "text":             return <TextBlock data={block.data} />;
    case "metrics":          return <MetricsBlock data={block.data} />;
    case "table":            return <TableBlock data={block.data} />;
    case "timeseries":       return <TimeseriesBlock data={block.data} />;
    case "bar":              return <BarBlock data={block.data} />;
    case "heatmap":          return <HeatmapBlock data={block.data} />;
    case "trace_waterfall":  return <TraceWaterfallBlock data={block.data} />;
    case "topology":         return <TopologyBlock data={block.data} />;
    case "flame_graph":      return <FlameGraphBlock data={block.data} />;
    case "sankey":           return <SankeyBlock data={block.data} />;
    case "dep_matrix":       return <DepMatrixBlock data={block.data} />;
    case "endpoints":        return <EndpointsBlock data={block.data} />;
    case "correlation":      return <CorrelationBlock data={block.data} />;
    case "tail":             return <TailBlock data={block.data} />;
    default:                 return <GenericBlock data={block.data} />;
  }
}
```

GenericBlock renders unknown types as formatted key-value pairs — graceful degradation for new block types before a dedicated renderer exists.

### Tool to Block Mapping

Tools emit blocks directly on the server. The LLM never touches presentation.

```go
// Each tool returns structured results that map to blocks.
// A single tool can emit multiple blocks.

// diagnose("api-gateway") emits:
//   Block{Type: "metrics", Data: ...}   — P50/P95/P99, error rate
//   Block{Type: "table", Data: ...}     — top failing endpoints
//   Block{Type: "timeseries", Data: ...} — trend (if interesting)

// find("error service:api-gw") emits:
//   Block{Type: "table", Data: ...}     — matching spans/logs

// trace("abc123") emits:
//   Block{Type: "trace_waterfall", Data: ...}

// topology() emits:
//   Block{Type: "topology", Data: ...}
```

The LLM's system prompt changes from "Reply in HTML" to just providing analysis in plain text. Text blocks contain the LLM's commentary. Data blocks come from tools.

## What Goes Away

- `render` tool — deleted
- HTML sanitizer (bluemonday) — no HTML crosses the wire
- "Reply in HTML, not Markdown" system prompt — LLM writes plain text
- Shoelace CDN — replaced by shadcn/ui
- `internal/web/*.templ` — replaced by React app
- `internal/web/static/renderers/*.js` — ported to React + D3 components
- `FanoutViz` JS registry — replaced by BlockRenderer
- Custom CSS for tables, metrics, viz — replaced by Tailwind + shadcn/ui

## What Stays

- WebSocket transport (same event types, `html` field becomes `blocks`)
- Go HTTP API serving JSON
- Single binary with `go:embed`
- Tool execution and DuckDB queries in Go
- MCP server (unchanged)
- OTLP ingest (unchanged)

## Frontend Stack

| Layer | Choice | Why |
|-------|--------|-----|
| Build | Bun | Fast, already used in Goal |
| Framework | React | Interactive UI, block rendering, shared patterns with Goal |
| Routing | React Router | Standard SPA routing |
| Components | shadcn/ui + Radix UI | Accessible, owned (copy-paste), Tailwind-native |
| Styling | Tailwind CSS | Utility-first, consistent with other projects |
| Tables | TanStack Table | Headless, styled with shadcn/ui |
| Standard charts | Recharts | Timeseries, bar, area, pie, heatmap. Built on D3. |
| Domain viz | React + D3 | Waterfall, topology, flame graph, dep matrix, sankey |
| LLM text | react-markdown | Render markdown from text blocks |
| State | Zustand | Lightweight, same as Goal |
| Icons | Lucide React | Tree-shakeable, clean |
| Embedding | Go `embed.FS` | Single binary deployment |

## Build

```bash
# Development
bun run dev          # React dev server with HMR
go run ./cmd/fanout  # Go server proxies to dev server

# Production
just build
# 1. cd client && bun run build   → client/dist/
# 2. templ generate               → (if any remaining templ)
# 3. go build -o fanout ./cmd/fanout  (embeds client/dist/)
```

Single binary. No Node/Bun at runtime.

## Mobile (Future)

The block spec is the contract. Any client that understands the JSON can render it.

- **Web** — React + shadcn/ui + Recharts + D3
- **Mobile** — Expo + React Native (shared types, Zustand stores, API client)

Shared across web and mobile: TypeScript block types, Zustand state, WebSocket client, API layer. Platform-specific: rendering components, styling.

## Project Structure

```
fanout/
├── client/                 # React app (Bun)
│   ├── src/
│   │   ├── components/
│   │   │   ├── blocks/     # BlockRenderer, all block components
│   │   │   ├── chat/       # Chat page, message list, input
│   │   │   ├── layout/     # Nav, sidebar, header
│   │   │   └── ui/         # shadcn/ui components
│   │   ├── lib/
│   │   │   ├── types.ts    # Block types, API types
│   │   │   ├── api.ts      # HTTP client
│   │   │   └── ws.ts       # WebSocket client
│   │   ├── stores/         # Zustand stores
│   │   ├── pages/          # Services, traces, logs, metrics, chat
│   │   └── main.tsx
│   ├── index.html
│   ├── tailwind.config.ts
│   ├── tsconfig.json
│   ├── bun.lock
│   └── package.json
├── cmd/fanout/             # Go binary
├── internal/               # Go server (unchanged)
└── client.go               # //go:embed all:client/dist
```

Go serves `client/dist/` via `embed.FS` for all non-API routes. API routes (`/api/*`, `/ws/*`, `/mcp`) stay as-is. SPA routing — unmatched paths serve `index.html`, React Router handles the rest.

## Migration Path

1. Scaffold React app in `client/`, set up Bun + Tailwind + shadcn/ui
2. Implement BlockRenderer + all block components
3. Rewrite chat page as React (WebSocket + streaming + blocks)
4. Rewrite data pages (services, traces, logs, metrics) as React
5. Update Go tools to return typed blocks instead of text
6. Update system prompt — remove HTML instructions, add plain text guidance
7. Delete Templ templates, Shoelace, bluemonday, render tool
8. Wire `embed.FS` to serve React build

## Prior Art

Goal already implements this pattern: JSON blocks, BlockRenderer, GenericBlock fallback, React + Zustand, single Go binary. This design extends the same pattern with observability-specific block types and D3-based domain visualizations.
