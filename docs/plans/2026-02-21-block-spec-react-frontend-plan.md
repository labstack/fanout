# Block Spec + React Frontend Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace LLM-generated HTML chat responses with a typed JSON block spec, and rewrite the frontend as a React SPA embedded in the Go binary.

**Architecture:** Tools emit typed blocks (Go structs) instead of text. The orchestrator collects blocks from tool results and LLM text, sends them as JSON over WebSocket. The React client renders each block type with a dedicated component. The Go binary embeds the built React app via `embed.FS`.

**Tech Stack (pinned versions):**

| Library | Version | Notes |
|---------|---------|-------|
| React | 19.2 | Latest stable |
| Vite | 7.3 | Build tooling |
| TypeScript | 5.8 | |
| Tailwind CSS | 4.2 | v4 uses CSS-first config |
| shadcn/ui | latest CLI | Copy-paste components, unified radix-ui package |
| Radix UI | radix-ui (unified) | Single package as of Feb 2026 |
| Recharts | 3.7 | React + D3 charts |
| D3 | 7.9 | Scales, layouts, force simulation |
| react-markdown | 10.1 | Markdown rendering |
| Zustand | 5.0 | State management |
| @tanstack/react-table | 8.21 | Headless tables |
| react-router | 7.13 | v7 merged react-router-dom into react-router |
| Lucide React | 0.575 | Icons |

**Design doc:** `docs/plans/2026-02-21-block-spec-react-frontend-design.md`

---

## Phase 1: Scaffold React App + Embed in Go

### Task 1: Initialize React app with Bun

**Files:**
- Create: `client/package.json`
- Create: `client/tsconfig.json`
- Create: `client/index.html`
- Create: `client/src/main.tsx`
- Create: `client/src/App.tsx`
- Create: `client/.gitignore`

**Step 1: Scaffold with Bun + Vite**

```bash
cd client
bun create vite . --template react-ts
```

**Step 2: Install dependencies**

```bash
cd client
bun add react-router zustand react-markdown recharts d3 @tanstack/react-table lucide-react
bun add -d @types/d3 tailwindcss @tailwindcss/vite
```

Note: v7 merged react-router-dom into `react-router`. Import from `react-router` directly.

**Step 3: Verify dev server starts**

```bash
cd client && bun run dev
```

Expected: Vite dev server on localhost:5173

**Step 4: Commit**

```bash
git add client/
git commit -m "feat: scaffold React app with Bun + Vite"
```

### Task 2: Set up Tailwind CSS + shadcn/ui

**Files:**
- Create: `client/src/index.css` (Tailwind directives)
- Create: `client/components.json` (shadcn config)
- Create: `client/src/lib/utils.ts` (cn utility)
- Create: `client/src/components/ui/` (shadcn components)

**Step 1: Configure Tailwind**

Add Tailwind CSS v4 with the Vite plugin. Create `client/src/index.css`:

```css
@import "tailwindcss";
```

Update `client/vite.config.ts` to include the Tailwind plugin:

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
});
```

**Step 2: Initialize shadcn/ui**

```bash
cd client && bunx shadcn@latest init
```

Select: New York style, Zinc color, CSS variables.

**Step 3: Add core shadcn components**

```bash
cd client
bunx shadcn@latest add button card badge tooltip alert tabs scroll-area separator input
```

**Step 4: Verify Tailwind + shadcn render**

Update `App.tsx` with a shadcn Button, confirm it renders styled.

**Step 5: Commit**

```bash
git add client/
git commit -m "feat: set up Tailwind CSS + shadcn/ui"
```

### Task 3: Embed React build in Go binary

**Files:**
- Create: `client.go` (embed directive, at repo root)
- Modify: `cmd/fanout/main.go` — add SPA file server
- Modify: `justfile` — add client build step

**Step 1: Build the React app**

```bash
cd client && bun run build
```

Verify `client/dist/` exists with `index.html` and asset files.

**Step 2: Create `client.go` at repo root**

```go
package main

import "embed"

//go:embed all:client/dist
var clientFS embed.FS
```

Note: this won't compile yet since `clientFS` needs to be in the `main` package alongside `cmd/fanout/main.go`. Instead, create it in `cmd/fanout/`:

```go
// cmd/fanout/client.go
package main

import "embed"

//go:embed all:../../client/dist
var clientDist embed.FS
```

If embed path restrictions prevent `../../`, use an alternative approach: create `internal/web/client.go` that embeds and exports the FS, or use a build step that copies `client/dist/` into a Go-accessible location.

The simplest approach: add a build step that copies `client/dist/` to `internal/web/dist/` before `go build`.

**Step 3: Add SPA file server to Echo**

In `cmd/fanout/main.go`, add a catch-all route that serves the React app. API routes (`/api/*`, `/ws/*`, `/mcp`, `/healthz`, `/readyz`) take priority. Everything else serves `index.html` for client-side routing.

```go
// After all API routes are registered:
e.GET("/*", spaHandler(clientDist))
```

Where `spaHandler` tries to serve the static file, falls back to `index.html`.

**Step 4: Update justfile**

```just
build-client:
    cd client && bun run build

build: build-client
    templ generate
    CGO_ENABLED=1 go build -o fanout ./cmd/fanout
```

**Step 5: Build and verify**

```bash
just build
./fanout
# Visit http://localhost:7520 — should see React app
```

**Step 6: Commit**

```bash
git commit -m "feat: embed React app in Go binary"
```

---

## Phase 2: Block Types + Core Block Components

### Task 4: Define TypeScript block types

**Files:**
- Create: `client/src/lib/types.ts`

**Step 1: Write block type definitions**

```typescript
// Chat event types (WebSocket protocol)
export interface ChatEvent {
  type: "token" | "tool_call" | "tool_result" | "error" | "done";
  content?: string;
  name?: string;
  input?: Record<string, unknown>;
  error?: string;
  id?: string;
  blocks?: Block[];
}

// Block spec
export interface Block {
  type: BlockType;
  data: unknown;
}

export type BlockType =
  | "text"
  | "metrics"
  | "table"
  | "timeseries"
  | "bar"
  | "heatmap"
  | "trace_waterfall"
  | "topology"
  | "flame_graph"
  | "sankey"
  | "dep_matrix"
  | "endpoints"
  | "correlation"
  | "tail";

// Data interfaces for each block type
export interface TextBlockData {
  content: string;
}

export interface MetricsBlockData {
  items: MetricItem[];
}

export interface MetricItem {
  label: string;
  value: number;
  unit: string;
  status: "ok" | "warning" | "danger";
}

export interface TableBlockData {
  columns: TableColumn[];
  rows: Record<string, unknown>[];
}

export interface TableColumn {
  key: string;
  label: string;
  align?: "left" | "right" | "center";
}

export interface TimeseriesBlockData {
  title: string;
  series: { label: string; color?: string; values: number[] }[];
  labels: string[];
  yLabel?: string;
}

export interface BarBlockData {
  title: string;
  bars: { label: string; value: number; color?: string }[];
  yLabel?: string;
  horizontal?: boolean;
}

export interface HeatmapBlockData {
  title: string;
  buckets: number[];
  times: string[];
  values: number[][];
}

export interface TraceWaterfallData {
  spans: TraceSpan[];
}

export interface TraceSpan {
  id: string;
  parent: string | null;
  service: string;
  operation: string;
  start: number;
  duration: number;
  status: string;
}

export interface TopologyData {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

export interface TopologyNode {
  id: string;
  status: string;
  rpm: number;
  p95: number;
  errors: number;
}

export interface TopologyEdge {
  source: string;
  target: string;
  rpm: number;
  errorRate: number;
}

export interface FlameGraphData {
  frames: FlameFrame[];
}

export interface FlameFrame {
  name: string;
  depth: number;
  x: number;
  w: number;
  self: number;
  total: number;
  service: string;
}

export interface SankeyData {
  nodes: { id: string; label: string; rpm: number; status?: string }[];
  links: { source: string; target: string; value: number }[];
}

export interface DepMatrixData {
  services: string[];
  cells: { from: string; to: string; errorRate: number; rpm: number; p95: number }[];
}

export interface EndpointsData {
  endpoints: {
    method: string;
    path: string;
    rpm: number;
    p50: number;
    p95: number;
    p99: number;
    errorRate: number;
    status: string;
  }[];
}

export interface CorrelationData {
  times: string[];
  panels: {
    label: string;
    color: string;
    values: number[];
    baseline?: number;
    markers?: { t: string; label: string; severity: string }[];
  }[];
}

export interface TailData {
  entries: LogEntry[];
}

export interface LogEntry {
  time: number;
  severity: string;
  service: string;
  body: string;
  traceId?: string;
}

// Client message (sent to server)
export interface ClientMessage {
  type: "message" | "cancel" | "clear";
  content?: string;
  window?: number;
  namespace?: string;
}

// Bookmark
export interface Bookmark {
  id: string;
  question: string;
  answer_html: string;
  created_at: string;
}
```

**Step 2: Commit**

```bash
git add client/src/lib/types.ts
git commit -m "feat: define TypeScript block types"
```

### Task 5: Build BlockRenderer + TextBlock + GenericBlock

**Files:**
- Create: `client/src/components/blocks/BlockRenderer.tsx`
- Create: `client/src/components/blocks/TextBlock.tsx`
- Create: `client/src/components/blocks/GenericBlock.tsx`

**Step 1: Create BlockRenderer**

```tsx
import { Block } from "@/lib/types";
import { TextBlock } from "./TextBlock";
import { GenericBlock } from "./GenericBlock";

export function BlockRenderer({ block }: { block: Block }) {
  switch (block.type) {
    case "text":
      return <TextBlock data={block.data} />;
    default:
      return <GenericBlock type={block.type} data={block.data} />;
  }
}
```

Start with just TextBlock and GenericBlock. Other block components are added in subsequent tasks.

**Step 2: Create TextBlock**

Renders markdown using react-markdown.

```tsx
import ReactMarkdown from "react-markdown";
import type { TextBlockData } from "@/lib/types";

export function TextBlock({ data }: { data: TextBlockData }) {
  return (
    <div className="prose prose-sm dark:prose-invert max-w-none">
      <ReactMarkdown>{data.content}</ReactMarkdown>
    </div>
  );
}
```

**Step 3: Create GenericBlock**

Fallback renderer for unknown/unimplemented block types. Renders data as formatted key-value pairs.

```tsx
export function GenericBlock({ type, data }: { type: string; data: unknown }) {
  // Render as key-value pairs for objects, JSON for anything else
}
```

**Step 4: Verify rendering with hardcoded blocks in App.tsx**

Create a test page that renders a few blocks to confirm the pipeline works.

**Step 5: Commit**

```bash
git commit -m "feat: add BlockRenderer, TextBlock, GenericBlock"
```

### Task 6: Build MetricsBlock

**Files:**
- Create: `client/src/components/blocks/MetricsBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx` — add case

**Step 1: Create MetricsBlock**

Grid of stat cards using shadcn Card. Status maps to color (ok=green, warning=amber, danger=red).

**Step 2: Update BlockRenderer with `case "metrics"`**

**Step 3: Verify with hardcoded data**

**Step 4: Commit**

```bash
git commit -m "feat: add MetricsBlock component"
```

### Task 7: Build TableBlock

**Files:**
- Create: `client/src/components/blocks/TableBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx` — add case

**Step 1: Create TableBlock**

Use TanStack Table for the headless table logic, styled with Tailwind. Support column alignment from the block data.

**Step 2: Update BlockRenderer with `case "table"`**

**Step 3: Verify with hardcoded data**

**Step 4: Commit**

```bash
git commit -m "feat: add TableBlock component"
```

---

## Phase 3: Chat Page + WebSocket

### Task 8: Create WebSocket client

**Files:**
- Create: `client/src/lib/ws.ts`

**Step 1: Write WebSocket client**

Handles connection, reconnection with exponential backoff, message parsing, and event callbacks. Based on the current chat.templ WebSocket code but as a TypeScript class/hook.

```typescript
export function useWebSocket(url: string, onEvent: (event: ChatEvent) => void) {
  // Connect, parse JSON events, auto-reconnect
}
```

**Step 2: Commit**

```bash
git commit -m "feat: add WebSocket client with auto-reconnect"
```

### Task 9: Create Zustand chat store

**Files:**
- Create: `client/src/stores/chat.ts`

**Step 1: Define chat store**

```typescript
interface Message {
  id: string;
  role: "user" | "assistant";
  content: string;        // streaming text (tokens)
  blocks?: Block[];       // final structured response
  toolCalls?: ToolCall[];  // active/completed tool calls
  loading: boolean;
}

interface ChatStore {
  messages: Message[];
  connected: boolean;
  sendMessage: (text: string, window: number, namespace: string) => void;
  cancel: () => void;
  clear: () => void;
}
```

The store manages the message list, WebSocket connection, and handles incoming events:
- `token` → append to current message content
- `tool_call` → add to current message tool calls
- `tool_result` → mark tool call complete
- `done` → set message blocks, mark not loading
- `error` → show error

**Step 2: Commit**

```bash
git commit -m "feat: add Zustand chat store"
```

### Task 10: Build chat page UI

**Files:**
- Create: `client/src/pages/ChatPage.tsx`
- Create: `client/src/components/chat/MessageList.tsx`
- Create: `client/src/components/chat/ChatInput.tsx`
- Create: `client/src/components/chat/ChatMessage.tsx`
- Create: `client/src/components/chat/ToolStatus.tsx`
- Modify: `client/src/App.tsx` — add route

**Step 1: Create ChatPage layout**

Full-height flex layout: header, scrollable message area, input bar at bottom. Match current chat.templ layout.

**Step 2: Create MessageList**

Renders messages from the store. Auto-scrolls to bottom on new messages.

**Step 3: Create ChatMessage**

For user messages: styled bubble.
For assistant messages: renders streaming text while loading, then renders `blocks` via BlockRenderer when done.

**Step 4: Create ChatInput**

Textarea with Shift+Enter for newlines, Enter to send. Send button. Window selector. Namespace filter.

**Step 5: Create ToolStatus**

Shows spinner + tool name during tool execution.

**Step 6: Wire up to WebSocket store and verify end-to-end**

The chat page should connect to `/ws/chat`, send messages, and display streamed tokens + final blocks.

Note: at this stage, the Go backend still sends HTML in the `done` event. The React app should handle BOTH `html` (old format) and `blocks` (new format) gracefully during migration.

**Step 7: Commit**

```bash
git commit -m "feat: add chat page with WebSocket streaming"
```

---

## Phase 4: Chart Blocks (Recharts)

### Task 11: Build TimeseriesBlock

**Files:**
- Create: `client/src/components/blocks/TimeseriesBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx`

Use Recharts `LineChart` / `AreaChart`. Map series data to Recharts format.

**Commit:** `feat: add TimeseriesBlock (Recharts)`

### Task 12: Build BarBlock

**Files:**
- Create: `client/src/components/blocks/BarBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx`

Use Recharts `BarChart`. Support horizontal via `layout="vertical"`.

**Commit:** `feat: add BarBlock (Recharts)`

### Task 13: Build HeatmapBlock

**Files:**
- Create: `client/src/components/blocks/HeatmapBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx`

Use Recharts `ScatterChart` with custom rectangular cells, or build with D3 scales + SVG rects for more control.

**Commit:** `feat: add HeatmapBlock`

---

## Phase 5: Domain Viz Blocks (React + D3)

### Task 14: Build TraceWaterfallBlock

**Files:**
- Create: `client/src/components/blocks/TraceWaterfallBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx`

Port `internal/web/static/renderers/trace-waterfall.js` to React + D3. Use `d3-scale` for time axis, SVG rects for spans, color by service.

**Commit:** `feat: add TraceWaterfallBlock (D3)`

### Task 15: Build TopologyBlock

**Files:**
- Create: `client/src/components/blocks/TopologyBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx`

Port `topology-graph.js`. Use `d3-force` for layout simulation, SVG circles for nodes, paths for edges.

**Commit:** `feat: add TopologyBlock (D3)`

### Task 16: Build remaining domain viz blocks

**Files:**
- Create: `client/src/components/blocks/FlameGraphBlock.tsx`
- Create: `client/src/components/blocks/SankeyBlock.tsx`
- Create: `client/src/components/blocks/DepMatrixBlock.tsx`
- Create: `client/src/components/blocks/EndpointsBlock.tsx`
- Create: `client/src/components/blocks/CorrelationBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx`

Port each from the corresponding JS renderer in `internal/web/static/renderers/`. Use D3 for layout math, React for rendering.

**Commit per block or batch:** `feat: add FlameGraphBlock, SankeyBlock, DepMatrixBlock, EndpointsBlock, CorrelationBlock`

### Task 17: Build TailBlock

**Files:**
- Create: `client/src/components/blocks/TailBlock.tsx`
- Modify: `client/src/components/blocks/BlockRenderer.tsx`

Live log tail viewer. Monospace, severity-colored, with trace ID links. Receives streaming `tail` events from WebSocket and appends entries.

**Commit:** `feat: add TailBlock for live log streaming`

---

## Phase 6: Go Backend — Tools Emit Blocks

### Task 18: Define Go block types

**Files:**
- Create: `internal/ai/blocks.go`

**Step 1: Define Block struct and block type constants**

```go
package ai

import "encoding/json"

type BlockType string

const (
    BlockText           BlockType = "text"
    BlockMetrics        BlockType = "metrics"
    BlockTable          BlockType = "table"
    BlockTimeseries     BlockType = "timeseries"
    BlockBar            BlockType = "bar"
    BlockHeatmap        BlockType = "heatmap"
    BlockTraceWaterfall BlockType = "trace_waterfall"
    BlockTopology       BlockType = "topology"
    BlockFlameGraph     BlockType = "flame_graph"
    BlockSankey         BlockType = "sankey"
    BlockDepMatrix      BlockType = "dep_matrix"
    BlockEndpoints      BlockType = "endpoints"
    BlockCorrelation    BlockType = "correlation"
    BlockTail           BlockType = "tail"
)

type Block struct {
    Type BlockType       `json:"type"`
    Data json.RawMessage `json:"data"`
}

// Helper constructors for each block type
func TextBlock(content string) Block { ... }
func MetricsBlock(items []MetricItem) Block { ... }
func TableBlock(columns []TableColumn, rows []map[string]any) Block { ... }
// etc.
```

**Step 2: Commit**

```bash
git commit -m "feat: define Go block types"
```

### Task 19: Update ClientEvent to carry blocks

**Files:**
- Modify: `internal/ai/orchestrator.go` — add Blocks field to ClientEvent, update done event

**Step 1: Add Blocks field to ClientEvent**

```go
type ClientEvent struct {
    Type    ClientEventType `json:"type"`
    Content string          `json:"content,omitempty"`
    Name    string          `json:"name,omitempty"`
    Input   string          `json:"input,omitempty"`
    HTML    string          `json:"html,omitempty"`    // keep for backward compat during migration
    Error   string          `json:"error,omitempty"`
    ID      string          `json:"id,omitempty"`
    Blocks  []Block         `json:"blocks,omitempty"`  // new
}
```

**Step 2: Update orchestrator run loop**

When tools return results, parse them into blocks. When the LLM generates text, wrap in a text block. On `done`, include the accumulated blocks array.

**Step 3: Commit**

```bash
git commit -m "feat: add blocks to ClientEvent"
```

### Task 20: Update tools to return structured data

**Files:**
- Modify: `internal/mcp/tools.go` — each tool handler
- Modify: `internal/service/*.go` — return structured types instead of formatted strings

This is the largest task. Each tool currently returns a string that the LLM reads and reformats as HTML. Instead, tools should return structured data that maps to blocks.

The approach for each tool:
1. The tool still returns a text summary for the LLM to reason about
2. Additionally, the tool emits typed blocks for the client

This can be done via a `ToolResult` struct:

```go
type ToolResult struct {
    Text   string  // for the LLM to read
    Blocks []Block // for the client to render
}
```

Update each tool:
- `status` → MetricsBlock (health stats) + TableBlock (top issues)
- `diagnose` → MetricsBlock (latencies) + TableBlock (endpoints) + TimeseriesBlock (trend)
- `find` → TableBlock (matching spans/logs)
- `trace` → TraceWaterfallBlock
- `timeline` → TimeseriesBlock
- `topology` → TopologyBlock
- `compare` → TableBlock (side-by-side)
- `query` → TableBlock (raw results)
- `schema` → TextBlock (schema text)

**Commit per tool or batch:** `feat: update tools to emit typed blocks`

### Task 21: Update system prompt

**Files:**
- Modify: `internal/ai/orchestrator.go` — staticSystemPrompt

Remove all HTML formatting instructions. The new prompt should tell the LLM to write plain text analysis only — no markup, no formatting.

```go
const staticSystemPrompt = `You are the AI assistant for Fanout, an observability platform.
You help users understand system health, investigate issues, and analyze telemetry data.

Tools: status (start here) → diagnose (deep-dive) → find (search spans/logs) →
tail (live log streaming) → trace (full trace, needs trace_id) → timeline (trends) →
topology (dependency map) → compare (side-by-side) → query (custom SQL, last resort).

Write plain text. Be direct, cite specific numbers, explain root causes with next steps.
Data visualizations are rendered automatically from tool results — do not describe data
that the user can already see in the charts and tables.`
```

**Commit:** `feat: simplify system prompt, remove HTML instructions`

---

## Phase 7: Cleanup

### Task 22: Remove render tool

**Files:**
- Modify: `internal/mcp/tools.go` — delete render tool registration
- Modify: `internal/ai/tools.go` — delete render tool handler
- Delete: `internal/web/static/renderers/*.js` (after viz blocks are ported)
- Delete: `internal/web/static/core.js` (FanoutViz registry)
- Delete: `internal/web/static/viz.css`

**Commit:** `refactor: remove render tool and FanoutViz registry`

### Task 23: Remove HTML sanitizer

**Files:**
- Modify: `internal/ai/orchestrator.go` — remove bluemonday import, sanitizer field, NewSanitizer()
- Modify: `go.mod` — remove `microcosm-cc/bluemonday` dependency

**Commit:** `refactor: remove HTML sanitizer (no longer needed)`

### Task 24: Remove Templ templates and Shoelace

**Files:**
- Delete: `internal/web/chat.templ`
- Delete: `internal/web/chat_templ.go`
- Delete: `internal/web/showcase.templ`
- Delete: `internal/web/showcase_templ.go`
- Delete: `internal/web/showcase_data.go`
- Modify: `internal/api/ui.go` — remove templ handlers, keep only API routes
- Modify: `go.mod` — remove templ dependency if no longer used elsewhere

**Commit:** `refactor: remove Templ templates and Shoelace`

### Task 25: Final integration and build verification

**Step 1: Update justfile with final build sequence**

```just
build:
    cd client && bun run build
    CGO_ENABLED=1 go build -o fanout ./cmd/fanout

dev:
    cd client && bun run dev &
    go run ./cmd/fanout

check:
    cd client && bun run typecheck
    go vet ./...
    golangci-lint run
```

**Step 2: Full build and test**

```bash
just build
./fanout
# Verify: chat works, blocks render, viz displays, bookmarks work
```

**Step 3: Commit**

```bash
git commit -m "chore: finalize build pipeline and cleanup"
```

---

## Task Dependency Graph

```
Task 1 (scaffold) → Task 2 (tailwind/shadcn) → Task 3 (embed in Go)
                                                        ↓
Task 4 (types) → Task 5 (BlockRenderer) → Task 6 (metrics) → Task 7 (table)
                                                        ↓
Task 8 (websocket) → Task 9 (store) → Task 10 (chat page)
                                                        ↓
Tasks 11-13 (chart blocks) ──────────────────────────── ↓
Tasks 14-17 (domain viz blocks) ─────────────────────── ↓
                                                        ↓
Task 18 (Go block types) → Task 19 (ClientEvent) → Task 20 (tools) → Task 21 (prompt)
                                                        ↓
Task 22 (remove render) → Task 23 (remove sanitizer) → Task 24 (remove templ) → Task 25 (final)
```

## Notes

- During migration (Tasks 10-20), the React app should handle both `html` (old) and `blocks` (new) in the `done` event. This allows incremental tool migration.
- The `/viz` showcase page is development-only and can be dropped or rebuilt as a Storybook-style page.
- Bookmarks API already returns JSON — React app consumes it directly.
- Auth token handling: pass via WebSocket query param (same as current).
