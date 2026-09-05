# Logged-in UI Shell Rebuild Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the header-toggle shell with a persistent rail, turn the chat page into a pane with a sticky composer and visible agent activity, give dashboard widgets native ECharts bodies with per-widget configuration, and replace the sign-in code field with a six-digit PinInput, fixing the CSP font, "Ms", new-chat 404, header clipping and fake "Live" defects on the way.

**Architecture:** `ui/host` is a React 19 + Mantine 9 single-page app served from `internal/ui/dist`; chat runs over AG-UI (`HttpAgent`) and renders tool results as sandboxed MCP-app iframes built from `ui/apps` into `internal/mcp/apps`. The work splits `App.tsx` into a session (`App.tsx`), a shell (`shell.tsx`), a rail (`rail.tsx`) and a chat pane (`chat.tsx`), moves the pure chart and format helpers up to `ui/` so both bundles share them, and rebuilds `dashboard.tsx` around a `widgets/` directory with one file per widget type.

**Tech Stack:** React 19.2.8, Mantine 9.5.2 (`@mantine/core`, `@mantine/hooks`), TanStack Router 1.170 and Query 5.102, `@ag-ui/client` 0.0.59, `@modelcontextprotocol/ext-apps` 1.7.5, `react-grid-layout` 2.2.4 (`/legacy` import), ECharts 6.1.0 (SVG renderer), Phosphor icons, Vitest 4 + happy-dom, Bun for package management, `just` recipes for builds and the gate.

**Spec:** `docs/superpowers/specs/2026-09-05-logged-in-ui-shell-design.md`

## Global Constraints

- Branch `ui/shell-rebuild`. Every commit message ends with the trailer line `Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N`.
- The lefthook pre-commit hook runs `cd ui/host && bun run lint` (which is `tsc --noEmit`) whenever staged files match `ui/host/**/*.{ts,tsx}` or `ui/theme.ts`. Every task must leave `bun run lint` green in `ui/host` before committing. `ui/apps` has the same `lint` script; run it when apps files change.
- Naming: the feature is **Chat / Chats**, the button is **New chat**. The strings "Investigations", "Investigation", "Conversation history" and "Guidance" must not appear in `ui/host/src` or `ui/apps/src` when the plan is done.
- `ui/` (the directory holding `theme.ts`, `tokens.ts`) has no `node_modules`. Files placed there may import only sibling files with relative paths, never bare package names.
- Embedded assets (`internal/ui/dist`, `internal/mcp/apps`) are committed. `just check` fails when they are stale, so a build in a task is followed either by committing the rebuilt assets (Tasks 7 and 14) or by discarding them with `git checkout -- internal/mcp/apps internal/ui/dist` (every other task).
- `echarts` is pinned to `6.1.0` in `ui/host`, the same version `ui/apps` uses.
- Widget types stay exactly `overview | topology | activity | assistant | performance | trace | logs` (server allowlist in `internal/dashboard/service.go`). No Go changes.
- Dashboard windows stay `15m | 1h | 6h | 24h | 168h | 720h`.
- Widget data refetch interval 30 000 ms, 15 000 ms while a query is in error. Dashboard list and selected-dashboard polls 30 000 ms.
- Copy in code comments, commit messages and docs is plain English prose.
- Run tests from `ui/host` with `bun run test <file>` (Vitest accepts a path filter). Run the full suite with `just ui-test`.

---

## File Structure

**Shared (`ui/`)**
- `ui/format.ts` — moved from `ui/apps/src/format.ts` unchanged: `integer`, `percent`, `duration`, `windowLabel`, `timelineTimestamp`.
- `ui/contracts.ts` — moved from `ui/apps/src/contracts.ts` unchanged: the observability result types.
- `ui/chart.ts` — new: `healthColor`, `chartTheme`, `statusHex`, `seriesColor`, `severityColor`, `severityHex`, pure functions over `./tokens`.

**Host (`ui/host/src/`)**
- `api.ts` — new: `getJSON<T>(url)` and the dashboard record types, shared by `rail.tsx` and `dashboard.tsx`.
- `app-context.ts` — new: `FanoutAppContext`, `useFanoutApp`, `createDashboardPrompt`. Breaks the circular import between the session, the shell and the pages.
- `App.tsx` — the session only: agent, thread state, drafts, subscriptions, keyboard "/" shortcut; renders `<Shell><Outlet /></Shell>` inside the context provider.
- `shell.tsx` — new: `AppShell` with header (burger, brand, colour-scheme toggle, account menu), navbar `<Rail>`, mobile `<Drawer>` with the same rail, Cmd-K handling.
- `rail.tsx` — new: New chat button, Chats section (threads, groups, rename/delete modals), Dashboards section, search field. Router-free; callbacks only.
- `chat.tsx` — new: `ChatPage`, `Composer`, `Welcome`, `ChatMessage`, `toolTitle`, `activityLabel`.
- `echart.tsx` — new: the `EChart` component (copy of `ui/apps/src/echart.tsx` with `height: number | string`).
- `dashboard-layout.ts` — gains `WidgetType`, `widgetDefaults`, `nextDashboardSlot`.
- `widgets/data.ts` — new: `Filters`, `widgetParams`, `useObservability`, `useLastUpdated`, `dashboardWindows`, `windowName`.
- `widgets/widget-card.tsx` — new: card chrome, hover actions menu, remove, configure modal wiring.
- `widgets/configure.tsx` — new: `ConfigureWidget` modal (service / severity / search / trace id).
- `widgets/overview.tsx`, `topology.tsx`, `activity.tsx`, `performance.tsx`, `trace.tsx`, `logs.tsx`, `assistant.tsx` — new: one body per widget type.
- `dashboard.tsx` — page rewrite: header, control row, updated line, grid.
- `auth.tsx` — `ViewerContext`/`useViewer`, PinInput code step, resend and change-email, heading sizes.
- `mcp-app-frame.tsx` — CSP `font-src`, 240px floor.
- `index.css` — rail row, chat pane, widget card rules.
- `routes/chat.index.tsx` — renders `ChatPage` (draft) instead of redirecting.
- Deleted: `chat-history.tsx`, `chat-history.test.tsx`.

**Apps (`ui/apps/src/`)**
- `components.tsx` — `ViewHeader` slimmed; chart helpers removed (imported from `ui/chart.ts`).
- `overview.tsx`, `topology.tsx`, `performance.tsx`, `trace.tsx`, `logs.tsx` — import paths updated, `eyebrow` prop dropped.
- Deleted: `format.ts`, `contracts.ts` (moved up).

**Tests (`ui/host/src/`)**
- `mcp-app-frame.test.tsx` (extend), `auth.test.tsx` (extend), `dashboard-layout.test.ts` (extend), `rail.test.tsx` (new, replaces `chat-history.test.tsx`), `chat.test.tsx` (new), `app.test.tsx` (new), `widgets/widgets.test.tsx` (new), `dashboard.test.tsx` (new).

---

## Phase A: foundations and defects

### Task 1: Always allow inlined fonts in the MCP app CSP

**Files:**
- Modify: `ui/host/src/mcp-app-frame.tsx:583-602`
- Test: `ui/host/src/mcp-app-frame.test.tsx`

**Interfaces:**
- Produces: `mcpAppCSP(meta: unknown): string` unchanged signature; output now always contains `font-src 'self' data:`.

- [ ] **Step 1: Write the failing test**

Append inside the `describe("MCPAppFrame")` block, after the existing "builds CSP directives only from safe declared domains" test:

```tsx
  it("always allows inlined fonts so embedded views render in the product typeface", () => {
    expect(mcpAppCSP({})).toContain("font-src 'self' data:");
    expect(mcpAppCSP(undefined)).toContain("font-src 'self' data:");
    const policy = mcpAppCSP({ ui: { csp: { resourceDomains: ["https://cdn.example.com"] } } });
    expect(policy).toContain("font-src 'self' data: https://cdn.example.com");
  });
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui/host && bun run test src/mcp-app-frame.test.tsx`
Expected: FAIL, `expected "default-src 'none'; …" to contain "font-src 'self' data:"`.

- [ ] **Step 3: Change the directive list**

In `mcpAppCSP`, replace the `directives` array and the conditional `font-src` push with:

```ts
  const directives = [
    "default-src 'none'",
    `script-src 'self' 'unsafe-inline'${resourceSuffix}`,
    `style-src 'self' 'unsafe-inline'${resourceSuffix}`,
    `img-src 'self' data:${resourceSuffix}`,
    `media-src 'self' data:${resourceSuffix}`,
    // The apps build inlines their woff2 files as data: URIs; without this
    // every embedded view falls back to the system font.
    `font-src 'self' data:${resourceSuffix}`,
    `connect-src ${connect.length ? connect.join(" ") : "'none'"}`,
  ];
  if (frames.length) directives.push(`frame-src ${frames.join(" ")}`);
  if (bases.length) directives.push(`base-uri ${bases.join(" ")}`);
```

Delete the line `if (resources.length) directives.push(`font-src 'self' ${resources.join(" ")}`);`.

- [ ] **Step 4: Run the test file to verify it passes**

Run: `cd ui/host && bun run test src/mcp-app-frame.test.tsx`
Expected: PASS, all tests in the file.

- [ ] **Step 5: Lint and commit**

```bash
cd ui/host && bun run lint
cd /Users/v/Projects/labstack/fanout
git add ui/host/src/mcp-app-frame.tsx ui/host/src/mcp-app-frame.test.tsx
git commit -m "fix(ui): let embedded views load their inlined fonts

The MCP app CSP only emitted font-src when a resource domain was declared, so
the data: woff2 files the apps build inlines were blocked and every embedded
view rendered in the system font.

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

### Task 2: Move format, contracts and chart helpers to `ui/`

**Files:**
- Move: `ui/apps/src/format.ts` → `ui/format.ts`; `ui/apps/src/contracts.ts` → `ui/contracts.ts`
- Create: `ui/chart.ts`
- Modify: `ui/apps/src/components.tsx`, `ui/apps/src/overview.tsx`, `ui/apps/src/topology.tsx`, `ui/apps/src/performance.tsx`, `ui/apps/src/trace.tsx`, `ui/apps/src/logs.tsx`

**Interfaces:**
- Produces (for Tasks 9 to 11): `import { integer, percent, duration, windowLabel, timelineTimestamp } from "../../format"` and `import type { Overview, Topology, Performance, TraceDetail, Logs, Result, ServiceHealth, Endpoint, LogEntry, TraceSpan, Edge } from "../../contracts"` from `ui/host/src`, and `import { healthColor, chartTheme, statusHex, seriesColor, severityColor, severityHex } from "../../chart"`. From `ui/host/src/widgets` the same paths are `../../../format`, `../../../contracts`, `../../../chart`.

- [ ] **Step 1: Move the two pure modules**

```bash
cd /Users/v/Projects/labstack/fanout
git mv ui/apps/src/format.ts ui/format.ts
git mv ui/apps/src/contracts.ts ui/contracts.ts
```

- [ ] **Step 2: Create `ui/chart.ts`**

```ts
/* Chart and status helpers shared by the browser host and the embedded views.
 * Pure functions over ./tokens only: this directory has no node_modules, so
 * nothing here may import a package. */
import { bad, chart, info, ok, series, warn } from "./tokens";

export function healthColor(health: string) {
  return health === "healthy" ? "ok" : health === "degraded" ? "warn" : "bad";
}

/* A chart is drawn into a canvas or SVG that cannot read CSS custom
   properties, so these hand ECharts resolved values from the same ramps
   Mantine gets. The shade differs by scheme for the same reason the accent
   does: the palette's own hue reads on Ayu, a darker stop is needed on white. */
export function chartTheme(dark: boolean) {
  return chart[dark ? "dark" : "light"];
}

export function statusHex(dark: boolean) {
  const shade = dark ? 5 : 7;
  return { ok: ok[shade], warn: warn[shade], bad: bad[shade], info: info[shade] };
}

/** One colour per service or metric, where the colour identifies rather than
 *  grades. Hashed so a service keeps its colour between renders, and drawn from
 *  a palette with no health hue in it. */
export function seriesColor(name: string, dark: boolean) {
  const palette = series[dark ? "dark" : "light"];
  let hash = 0;
  for (const character of name) hash = (hash * 31 + character.charCodeAt(0)) | 0;
  return palette[Math.abs(hash) % palette.length];
}

export function severityColor(value: string) {
  const severity = String(value).toUpperCase();
  if (severity === "ERROR" || severity === "FATAL") return "bad";
  if (severity === "WARN" || severity === "WARNING") return "warn";
  if (severity === "INFO") return "info";
  return "gray";
}

export function severityHex(value: string, dark: boolean) {
  const status = statusHex(dark);
  const severity = String(value).toUpperCase();
  if (severity === "ERROR" || severity === "FATAL") return status.bad;
  if (severity === "WARN" || severity === "WARNING") return status.warn;
  if (severity === "INFO") return status.info;
  return chartTheme(dark).muted;
}
```

- [ ] **Step 3: Strip the helpers from `ui/apps/src/components.tsx`**

Delete the `healthColor`, `chartTheme`, `statusHex` and `seriesColor` functions and the comment block above `chartTheme` (lines 96 to 122 of the current file). Change the tokens import at the top to:

```ts
import { fanoutCssVariables, fanoutThemeConfig } from "../../theme";
```

(remove the `import { bad, chart, info, ok, series, warn } from "../../tokens";` line).

- [ ] **Step 4: Update the five app entry files**

In each of `overview.tsx`, `topology.tsx`, `performance.tsx`, `trace.tsx`, `logs.tsx`:

- change `from "./contracts"` to `from "../../contracts"`;
- change `from "./format"` to `from "../../format"`;
- remove `healthColor`, `chartTheme`, `statusHex`, `seriesColor` from the `./components` import and add `import { … } from "../../chart";` with exactly the names that file uses. For reference: `overview.tsx` uses `healthColor`; `topology.tsx` uses `chartTheme`, `statusHex`; `performance.tsx` uses `chartTheme`, `healthColor`, `seriesColor`, `statusHex`; `trace.tsx` uses `seriesColor` and its own `severityColor`; `logs.tsx` uses `chartTheme`, `statusHex` and its own `severityColor` and `severityHex`.
- in `trace.tsx` delete the local `function severityColor(value: string) {…}` and import `severityColor` from `"../../chart"`;
- in `logs.tsx` delete the local `severityColor` and `severityHex` functions and import both from `"../../chart"`.

- [ ] **Step 5: Type-check and build the apps, then discard the built HTML**

```bash
cd ui/apps && bun run lint && bun run build
cd /Users/v/Projects/labstack/fanout && git checkout -- internal/mcp/apps
git status --short
```

Expected: lint clean, five `✓ built` lines, and `git status` shows only the `ui/` changes.

- [ ] **Step 6: Commit**

```bash
git add ui/format.ts ui/contracts.ts ui/chart.ts ui/apps/src
git commit -m "refactor(ui): share format, contracts and chart helpers between host and apps

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

### Task 3: Expose the signed-in account to the app

**Files:**
- Modify: `ui/host/src/auth.tsx`
- Test: `ui/host/src/auth.test.tsx`

**Interfaces:**
- Produces: `export type Viewer = { id: string; email: string; name: string; role: string }` and `export function useViewer(): Viewer` from `./auth`. Available anywhere under `AuthGate` once authenticated.

- [ ] **Step 1: Write the failing test**

Append to `auth.test.tsx`, inside the `describe` block:

```tsx
  it("exposes the signed-in account through useViewer", async () => {
    window.happyDOM.setURL("https://fanout.example.com/");
    fetchMock.mockImplementation(async (input) => authResponse(input, {
      id: "viewer-123",
      email: "v@example.com",
      name: "Vee",
      role: "viewer",
    }));
    function Probe() {
      const viewer = useViewer();
      return <div>Signed in as {viewer.email} ({viewer.role})</div>;
    }
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(
      <MantineProvider>
        <AuthGate><Probe /></AuthGate>
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Signed in as v@example.com (viewer)"));
    await act(async () => root.unmount());
  });
```

Change the import line to `import AuthGate, { useViewer } from "./auth";`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui/host && bun run test src/auth.test.tsx`
Expected: FAIL, `useViewer` is not exported (type error surfaces as a runtime `TypeError: useViewer is not a function`).

- [ ] **Step 3: Add the viewer context**

In `auth.tsx`, after the `RuntimeStatusContext` declaration:

```ts
export type Viewer = { id: string; email: string; name: string; role: string };
const ViewerContext = createContext<Viewer | null>(null);

export function useViewer(): Viewer {
  const viewer = useContext(ViewerContext);
  if (!viewer) throw new Error("Fanout viewer is unavailable");
  return viewer;
}

function viewerFromMe(user: unknown): Viewer | null {
  if (!user || typeof user !== "object") return null;
  const record = user as Record<string, unknown>;
  if (typeof record.id !== "string" || record.id === "") return null;
  return {
    id: record.id,
    email: typeof record.email === "string" ? record.email : "",
    name: typeof record.name === "string" ? record.name : "",
    role: typeof record.role === "string" ? record.role : "",
  };
}
```

Inside `AuthGate`, add state `const [account, setAccount] = useState<Viewer | null>(null);` and a loader:

```ts
  async function loadAccount() {
    const response = await fetch("/api/auth/me", { credentials: "same-origin" });
    if (!response.ok) { setViewer("none"); setAccount(null); return; }
    const user = await response.json().catch(() => null);
    setViewer(browserViewerFromMe(user));
    setAccount(viewerFromMe(user));
  }
```

Replace the inline `/api/auth/me` fetch chain in the first effect with `loadAccount().catch(() => setViewer("none")).finally(() => setSessionReady(true));`.

Everywhere the code sets `setViewer("user")` after a successful `login-link`, `verify` or `setup` call, follow it with `void loadAccount();` (three places: the login-token effect, `submit` for the setup and verify branches, and the "Continue to Fanout" button). The `browserViewerFromMe` import stays.

Wrap the authenticated render:

```tsx
  if (authenticated) return <RuntimeStatusContext.Provider value={status}>
    <ViewerContext.Provider value={account ?? { id: "", email, name, role: "" }}>{children}</ViewerContext.Provider>
  </RuntimeStatusContext.Provider>;
```

The fallback covers the instant between a successful verify and the `/me` reload; `email` and `name` are the form fields already in state.

- [ ] **Step 4: Run the test file to verify it passes**

Run: `cd ui/host && bun run test src/auth.test.tsx`
Expected: PASS, seven tests.

- [ ] **Step 5: Lint and commit**

```bash
cd ui/host && bun run lint
cd /Users/v/Projects/labstack/fanout
git add ui/host/src/auth.tsx ui/host/src/auth.test.tsx
git commit -m "feat(ui): expose the signed-in account to the app

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

## Phase B: shell

### Task 4: Shared API helper and the rail

**Files:**
- Create: `ui/host/src/api.ts`, `ui/host/src/rail.tsx`
- Test: `ui/host/src/rail.test.tsx` (new)
- Delete: `ui/host/src/chat-history.tsx`, `ui/host/src/chat-history.test.tsx`
- Modify: `ui/host/src/index.css` (rename the drawer row rules)

**Interfaces:**
- Produces from `./api`: `getJSON<T>(url: string): Promise<T>`, `type DashboardSummary = { id: string; name: string; description: string; is_default: boolean; widget_count: number; updated_at: string }`, `type DashboardRecord`, `type Envelope<T> = { data: T }`, `dashboardsQueryKey = ["dashboards"] as const`.
- Produces from `./rail`: `export const threadHistoryQueryKey = ["agent-threads"] as const`, `export type RailHandle = { focusSearch(): void }`, `export type RailProps = { agentAvailable: boolean; activeThreadID?: string; activeDashboardID?: string; autoFocusSearch?: boolean; onNewChat: () => void; onSelectThread: (threadID: string) => void; onDeletedThread: (threadID: string) => void; onSelectDashboard: (dashboardID: string) => void; onCreateDashboard: () => void; ref?: Ref<RailHandle> }`, default export `Rail`.
- Consumes: `authorizedFetch` from `./auth`.

- [ ] **Step 1: Create `api.ts`**

```ts
import { authorizedFetch } from "./auth";

export type Envelope<T> = { data: T };
export type DashboardSummary = { id: string; name: string; description: string; is_default: boolean; widget_count: number; updated_at: string };
export type DashboardLayoutRecord = { i: string; x: number; y: number; w: number; h: number; minW?: number; minH?: number };
export type DashboardWidgetRecord = { id: string; type: string; title: string; config?: Record<string, unknown>; enabled: boolean };
export type DashboardState = { layout: DashboardLayoutRecord[]; widgets: DashboardWidgetRecord[]; filters: { window: string; namespace: string } };
export type DashboardRecord = { id: string; name: string; description: string; is_default: boolean; state: DashboardState; updated_at: string };

export const dashboardsQueryKey = ["dashboards"] as const;

export async function getJSON<T>(url: string): Promise<T> {
  const response = await authorizedFetch(url);
  if (!response.ok) throw new Error(`Request failed (${response.status})`);
  return response.json() as Promise<T>;
}
```

- [ ] **Step 2: Write the failing rail test**

Create `rail.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, createRef } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Rail, { type RailHandle } from "./rail";

const fetchMock = vi.fn<typeof fetch>();

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function threadsPage(query = "") {
  return json({
    threads: [{ threadId: "thread-checkout", title: query ? `Result for ${query}` : "Checkout latency", updatedAt: "2026-07-22 03:00:00" }],
    nextCursor: "",
  });
}

function dashboards() {
  return json({ dashboards: [
    { id: "dash-main", name: "System overview", description: "", is_default: true, widget_count: 4, updated_at: "2026-07-22 03:00:00" },
    { id: "dash-checkout", name: "Checkout", description: "", is_default: false, widget_count: 2, updated_at: "2026-07-22 03:00:00" },
  ] });
}

function respond(input: RequestInfo | URL, init?: RequestInit) {
  const url = new URL(String(input), "http://localhost");
  if (url.pathname === "/api/dashboards") return dashboards();
  if (url.pathname === "/api/agent/threads") return threadsPage(url.searchParams.get("q") ?? "");
  if (url.pathname.startsWith("/api/agent/threads/") && init?.method === "PATCH") return json({ title: "Checkout follow-up" });
  if (url.pathname.startsWith("/api/agent/threads/") && init?.method === "DELETE") return new Response(null, { status: 204 });
  throw new Error(`unexpected request: ${url.pathname}`);
}

function mount(props: Partial<Parameters<typeof Rail>[0]> = {}) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const handlers = {
    onNewChat: vi.fn(), onSelectThread: vi.fn(), onDeletedThread: vi.fn(), onSelectDashboard: vi.fn(), onCreateDashboard: vi.fn(),
  };
  const ref = createRef<RailHandle>();
  const render = () => root.render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <Rail ref={ref} agentAvailable activeThreadID="thread-checkout" activeDashboardID="dash-main" {...handlers} {...props} />
      </MantineProvider>
    </QueryClientProvider>,
  );
  return { root, handlers, ref, render };
}

function setValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

describe("Rail", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async (input, init) => respond(input, init));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("lists chats and dashboards, marks the active rows, and selects", async () => {
    const { root, handlers, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("Checkout latency"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    expect(document.body.textContent).toContain("Chats");
    expect(document.body.textContent).toContain("Dashboards");
    expect(document.body.textContent).not.toContain("Investigation");

    const activeRows = document.querySelectorAll('[aria-current="page"]');
    expect(activeRows).toHaveLength(2);

    const thread = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Checkout latency"));
    await act(async () => thread?.click());
    expect(handlers.onSelectThread).toHaveBeenCalledWith("thread-checkout");

    const dashboard = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Checkout") && !button.textContent.includes("latency"));
    await act(async () => dashboard?.click());
    expect(handlers.onSelectDashboard).toHaveBeenCalledWith("dash-checkout");

    const newChat = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.trim() === "New chat");
    await act(async () => newChat?.click());
    expect(handlers.onNewChat).toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("hides chat affordances when the agent is unavailable", async () => {
    const { root, render } = mount({ agentAvailable: false });
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    expect(document.body.textContent).not.toContain("New chat");
    expect(document.body.textContent).not.toContain("Chats");
    expect(document.body.textContent).not.toContain("Create with AI");
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/agent/threads"))).toBe(false);
    await act(async () => root.unmount());
  });

  it("searches chats on the server and filters dashboards locally", async () => {
    const { root, ref, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("Checkout latency"));
    act(() => ref.current?.focusSearch());
    const search = document.querySelector('input[aria-label="Search chats and dashboards"]') as HTMLInputElement;
    expect(document.activeElement).toBe(search);
    await act(async () => setValue(search, "checkout"));
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes("q=checkout"))).toBe(true), { timeout: 1500 });
    await vi.waitFor(() => expect(document.body.textContent).toContain("Result for checkout"));
    expect(document.body.textContent).toContain("Checkout");
    expect(document.body.textContent).not.toContain("System overview");
    await act(async () => root.unmount());
  });

  it("renames and deletes a chat", async () => {
    const { root, handlers, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.querySelector('button[aria-label="Actions for Checkout latency"]')).not.toBeNull());
    const openActions = () => document.querySelector('button[aria-label="Actions for Checkout latency"]') as HTMLButtonElement;

    await act(async () => openActions().click());
    const rename = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Rename"));
    await act(async () => rename?.click());
    const name = document.querySelector('input[value="Checkout latency"]') as HTMLInputElement;
    await act(async () => setValue(name, "Checkout follow-up"));
    const save = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.trim() === "Save");
    await act(async () => save?.click());
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(true));

    await act(async () => openActions().click());
    const remove = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Delete"));
    await act(async () => remove?.click());
    const confirm = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find((button) => button.textContent?.trim() === "Delete");
    await act(async () => confirm?.click());
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(true));
    expect(handlers.onDeletedThread).toHaveBeenCalledWith("thread-checkout");
    await act(async () => root.unmount());
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd ui/host && bun run test src/rail.test.tsx`
Expected: FAIL, `Cannot find module './rail'`.

- [ ] **Step 4: Create `rail.tsx`**

```tsx
import { ActionIcon, Alert, Badge, Box, Button, Center, Group, Kbd, Loader, Menu, Modal, ScrollArea, Stack, Text, TextInput, UnstyledButton } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { DotsThree, MagnifyingGlass, PencilSimple, Plus, Sparkle, Trash } from "@phosphor-icons/react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react";
import { dashboardsQueryKey, getJSON, type DashboardSummary } from "./api";
import { authorizedFetch } from "./auth";

export const threadHistoryQueryKey = ["agent-threads"] as const;

export type RailHandle = { focusSearch(): void };

export type RailProps = {
  agentAvailable: boolean;
  activeThreadID?: string;
  activeDashboardID?: string;
  /** The mobile drawer mounts a second rail; it focuses search on open. */
  autoFocusSearch?: boolean;
  onNewChat: () => void;
  onSelectThread: (threadID: string) => void;
  onDeletedThread: (threadID: string) => void;
  onSelectDashboard: (dashboardID: string) => void;
  onCreateDashboard: () => void;
  ref?: Ref<RailHandle>;
};

type ThreadSummary = { threadId: string; title: string; updatedAt: string };
type ThreadPage = { threads: ThreadSummary[]; nextCursor: string };

async function fetchThreads(query: string, cursor: string): Promise<ThreadPage> {
  const params = new URLSearchParams({ limit: "30" });
  if (query) params.set("q", query);
  if (cursor) params.set("cursor", cursor);
  const response = await authorizedFetch(`/api/agent/threads?${params}`);
  if (!response.ok) throw new Error(`Unable to load chats (${response.status})`);
  return response.json();
}

export default function Rail({ agentAvailable, activeThreadID, activeDashboardID, autoFocusSearch = false, onNewChat, onSelectThread, onDeletedThread, onSelectDashboard, onCreateDashboard, ref }: RailProps) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [query] = useDebouncedValue(search.trim(), 250);
  const [renaming, setRenaming] = useState<ThreadSummary | null>(null);
  const [deleting, setDeleting] = useState<ThreadSummary | null>(null);
  const [renameTitle, setRenameTitle] = useState("");
  const [mutationError, setMutationError] = useState("");
  const [busy, setBusy] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  useImperativeHandle(ref, () => ({ focusSearch: () => searchRef.current?.focus() }), []);
  useEffect(() => { if (autoFocusSearch) requestAnimationFrame(() => searchRef.current?.focus()); }, [autoFocusSearch]);

  const history = useInfiniteQuery({
    queryKey: [...threadHistoryQueryKey, query],
    queryFn: ({ pageParam }) => fetchThreads(query, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
    enabled: agentAvailable,
  });
  const dashboards = useQuery({ queryKey: dashboardsQueryKey, queryFn: () => getJSON<{ dashboards: DashboardSummary[] }>("/api/dashboards"), refetchInterval: 30_000 });
  const threads = useMemo(() => history.data?.pages.flatMap((page) => page.threads) ?? [], [history.data]);
  const groups = useMemo(() => groupThreads(threads), [threads]);
  const visibleDashboards = useMemo(() => {
    const items = dashboards.data?.dashboards ?? [];
    const needle = query.toLowerCase();
    return needle ? items.filter((item) => item.name.toLowerCase().includes(needle)) : items;
  }, [dashboards.data, query]);

  function beginRename(thread: ThreadSummary) { setMutationError(""); setRenameTitle(thread.title); setRenaming(thread); }

  async function renameThread() {
    if (!renaming || !renameTitle.trim()) return;
    setBusy(true); setMutationError("");
    try {
      const response = await authorizedFetch(`/api/agent/threads/${encodeURIComponent(renaming.threadId)}`, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ title: renameTitle.trim() }) });
      if (!response.ok) throw new Error(`Unable to rename chat (${response.status})`);
      await queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      setRenaming(null);
    } catch (cause) {
      setMutationError(cause instanceof Error ? cause.message : "Unable to rename this chat.");
    } finally { setBusy(false); }
  }

  async function deleteThread() {
    if (!deleting) return;
    setBusy(true); setMutationError("");
    try {
      const response = await authorizedFetch(`/api/agent/threads/${encodeURIComponent(deleting.threadId)}`, { method: "DELETE" });
      if (!response.ok) throw new Error(`Unable to delete chat (${response.status})`);
      const deletedID = deleting.threadId;
      setDeleting(null);
      await queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      onDeletedThread(deletedID);
    } catch (cause) {
      setMutationError(cause instanceof Error ? cause.message : "Unable to delete this chat.");
    } finally { setBusy(false); }
  }

  return <Stack gap="sm" h="100%" component="nav" aria-label="Navigation">
    {agentAvailable && <Button leftSection={<Plus size={16} weight="bold" />} onClick={onNewChat}>New chat</Button>}
    <ScrollArea type="auto" offsetScrollbars flex={1} mx={-4} px={4}>
      <Stack gap="lg" pb="sm">
        {agentAvailable && <Stack gap={4}>
          <SectionLabel>Chats</SectionLabel>
          {history.isLoading && <Center py="md"><Loader size="xs" /></Center>}
          {history.isError && <Alert color="bad" radius="md" p="xs">Chats could not be loaded.</Alert>}
          {!history.isLoading && !history.isError && threads.length === 0 && <Text c="dimmed" size="sm" px="sm" py="xs">{query ? "No matching chats" : "No chats yet"}</Text>}
          {groups.map((group) => <Stack key={group.label} gap={2}>
            {groups.length > 1 && <Text c="dimmed" size="xs" px="sm" pt={4}>{group.label}</Text>}
            {group.threads.map((thread) => <ThreadRow key={thread.threadId} thread={thread} active={thread.threadId === activeThreadID} onSelect={() => onSelectThread(thread.threadId)} onRename={() => beginRename(thread)} onDelete={() => { setMutationError(""); setDeleting(thread); }} />)}
          </Stack>)}
          {history.hasNextPage && <Button variant="subtle" color="gray" size="compact-sm" loading={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>See all</Button>}
        </Stack>}
        <Stack gap={4}>
          <SectionLabel>Dashboards</SectionLabel>
          {dashboards.isLoading && <Center py="md"><Loader size="xs" /></Center>}
          {dashboards.isError && <Alert color="bad" radius="md" p="xs">Dashboards could not be loaded.</Alert>}
          {visibleDashboards.map((dashboard) => {
            const active = dashboard.id === activeDashboardID;
            return <UnstyledButton key={dashboard.id} className="rail-row" data-active={active || undefined} aria-current={active ? "page" : undefined} p="sm" onClick={() => onSelectDashboard(dashboard.id)}>
              <Group justify="space-between" gap="sm" wrap="nowrap">
                <Text size="sm" fw={active ? 600 : 500} truncate>{dashboard.name}</Text>
                {dashboard.is_default && <Badge size="xs" variant="light" color="gray">Default</Badge>}
              </Group>
            </UnstyledButton>;
          })}
          {agentAvailable && <Button variant="subtle" color="gray" size="compact-sm" justify="flex-start" leftSection={<Sparkle size={14} weight="fill" />} onClick={onCreateDashboard}>Create with AI</Button>}
        </Stack>
      </Stack>
    </ScrollArea>
    <TextInput ref={searchRef} value={search} onChange={(event) => setSearch(event.currentTarget.value)} leftSection={<MagnifyingGlass size={15} />} rightSection={<Kbd size="xs">⌘K</Kbd>} rightSectionWidth={44} placeholder="Search" aria-label="Search chats and dashboards" size="sm" />
    <Modal opened={renaming !== null} onClose={() => !busy && setRenaming(null)} title="Rename chat" centered>
      <form onSubmit={(event) => { event.preventDefault(); void renameThread(); }}>
        <Stack>
          <TextInput label="Name" value={renameTitle} onChange={(event) => setRenameTitle(event.currentTarget.value)} maxLength={120} autoFocus />
          {mutationError && <Alert color="bad">{mutationError}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setRenaming(null)} disabled={busy}>Cancel</Button>
            <Button type="submit" loading={busy} disabled={!renameTitle.trim()}>Save</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
    <Modal opened={deleting !== null} onClose={() => !busy && setDeleting(null)} title="Delete chat?" centered>
      <Stack>
        <Text size="sm">This permanently removes <Text span fw={650}>{deleting?.title}</Text> and its saved messages.</Text>
        {mutationError && <Alert color="bad">{mutationError}</Alert>}
        <Group justify="flex-end">
          <Button variant="default" onClick={() => setDeleting(null)} disabled={busy}>Cancel</Button>
          <Button color="bad" loading={busy} onClick={() => void deleteThread()}>Delete</Button>
        </Group>
      </Stack>
    </Modal>
  </Stack>;
}

function SectionLabel({ children }: { children: string }) {
  return <Text c="dimmed" size="xs" fw={700} tt="uppercase" lts="0.08em" px="sm">{children}</Text>;
}

function ThreadRow({ thread, active, onSelect, onRename, onDelete }: { thread: ThreadSummary; active: boolean; onSelect: () => void; onRename: () => void; onDelete: () => void }) {
  return <Box data-active={active || undefined} className="rail-row">
    <Group gap={2} wrap="nowrap">
      <UnstyledButton onClick={onSelect} aria-current={active ? "page" : undefined} p="sm" flex={1} style={{ minWidth: 0 }}>
        <Group justify="space-between" gap="sm" wrap="nowrap">
          <Text size="sm" fw={active ? 600 : 500} truncate>{thread.title}</Text>
          <Text c="dimmed" size="xs" style={{ flexShrink: 0 }}>{threadTime(thread.updatedAt)}</Text>
        </Group>
      </UnstyledButton>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <ActionIcon className="rail-row-actions" variant="subtle" color="gray" size="sm" mr={4} aria-label={`Actions for ${thread.title}`}><DotsThree size={18} weight="bold" /></ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<PencilSimple size={15} />} onClick={onRename}>Rename</Menu.Item>
          <Menu.Item color="bad" leftSection={<Trash size={15} />} onClick={onDelete}>Delete</Menu.Item>
        </Menu.Dropdown>
      </Menu>
    </Group>
  </Box>;
}

function parseSQLiteTime(value: string): Date {
  if (value.includes("T")) return new Date(value);
  return new Date(`${value.replace(" ", "T")}Z`);
}

function dayStart(value: Date): number {
  return new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();
}

export function groupThreads(threads: ThreadSummary[]): Array<{ label: string; threads: ThreadSummary[] }> {
  const today = dayStart(new Date());
  const day = 24 * 60 * 60 * 1000;
  const groups = new Map<string, ThreadSummary[]>();
  for (const thread of threads) {
    const age = Math.floor((today - dayStart(parseSQLiteTime(thread.updatedAt))) / day);
    const label = age <= 0 ? "Today" : age === 1 ? "Yesterday" : age <= 7 ? "Previous 7 days" : "Older";
    const group = groups.get(label) ?? [];
    group.push(thread);
    groups.set(label, group);
  }
  return [...groups].map(([label, items]) => ({ label, threads: items }));
}

export function threadTime(value: string): string {
  const date = parseSQLiteTime(value);
  const today = dayStart(new Date());
  const age = Math.floor((today - dayStart(date)) / (24 * 60 * 60 * 1000));
  if (age <= 1) return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(date);
}
```

- [ ] **Step 5: Rename the row rules in `index.css`**

Replace the four `.chat-history-*` rules with:

```css
.rail-row {
  border-radius: var(--mantine-radius-md);
  transition: background-color 120ms ease;
}

.rail-row:hover {
  background: var(--mantine-color-default-hover);
}

.rail-row[data-active] {
  background: var(--mantine-primary-color-light);
}

.rail-row-actions {
  opacity: 0;
  transition: opacity 120ms ease;
}

.rail-row:hover .rail-row-actions,
.rail-row:focus-within .rail-row-actions {
  opacity: 1;
}

@media (hover: none) {
  .rail-row-actions {
    opacity: 1;
  }
}
```

- [ ] **Step 6: Delete the drawer**

```bash
cd /Users/v/Projects/labstack/fanout
git rm -q ui/host/src/chat-history.tsx ui/host/src/chat-history.test.tsx
```

`App.tsx` still imports `./chat-history`; Task 5 replaces that import. Until then `bun run lint` fails on `App.tsx`, so this task and Task 5 are committed together at the end of Task 5. Run the rail test alone now.

- [ ] **Step 7: Run the rail test to verify it passes**

Run: `cd ui/host && bun run test src/rail.test.tsx`
Expected: PASS, four tests.

---

### Task 5: App context, shell, and the session split

**Files:**
- Create: `ui/host/src/app-context.ts`, `ui/host/src/shell.tsx`
- Modify: `ui/host/src/App.tsx` (rewrite), `ui/host/src/routes/dashboards.index.tsx`, `ui/host/src/routes/dashboards.$dashboardId.tsx`, `ui/host/src/routes/chat.index.tsx`, `ui/host/src/routes/chat.$threadId.tsx`, `ui/host/src/dashboard.tsx` (imports only), `ui/host/src/index.css`
- Test: `ui/host/src/app.test.tsx` (new)

**Interfaces:**
- Produces from `./app-context`:

```ts
export type FanoutAppContextValue = {
  agentAvailable: boolean;
  threadID: string;            // the thread the composer sends to (route param or draft id)
  threadMissing: boolean;      // the route thread returned 404
  messages: Message[];
  messageTimes: Record<string, number>;  // message id -> epoch ms, when known
  ready: boolean;
  running: boolean;
  activity: string;            // "Checking system health…" while a tool runs, else ""
  input: string;
  setInput: (value: string) => void;
  error: string;
  bottomRef: RefObject<HTMLDivElement | null>;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  send: (text: string) => Promise<void>;
  submit: (event: FormEvent) => void;
  stop: () => void;
  retry: () => void;
  openChat: (prompt?: string) => void;   // new thread, optional first prompt
  newThread: () => void;                 // go to /chat draft
  selectThread: (threadID: string) => void;
};
export const FanoutAppContext: Context<FanoutAppContextValue | null>;
export function useFanoutApp(): FanoutAppContextValue;
export const createDashboardPrompt: string;
```

- Consumes: `Rail` and `RailHandle` from `./rail`; `useViewer`, `logout`, `useRuntimeStatus`, `authorizedFetch` from `./auth`; `ChatPage` from `./chat` (Task 6 creates it; until then `routes/chat.*` keep importing `ChatPage` from `../App`, see Step 8).

- [ ] **Step 1: Create `app-context.ts`**

```ts
import type { Message } from "@ag-ui/client";
import { createContext, useContext, type FormEvent, type RefObject } from "react";

export type FanoutAppContextValue = {
  agentAvailable: boolean;
  threadID: string;
  threadMissing: boolean;
  messages: Message[];
  messageTimes: Record<string, number>;
  ready: boolean;
  running: boolean;
  activity: string;
  input: string;
  setInput: (value: string) => void;
  error: string;
  bottomRef: RefObject<HTMLDivElement | null>;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  send: (text: string) => Promise<void>;
  submit: (event: FormEvent) => void;
  stop: () => void;
  retry: () => void;
  openChat: (prompt?: string) => void;
  newThread: () => void;
  selectThread: (threadID: string) => void;
};

export const FanoutAppContext = createContext<FanoutAppContextValue | null>(null);

export function useFanoutApp(): FanoutAppContextValue {
  const context = useContext(FanoutAppContext);
  if (!context) throw new Error("Fanout app context is unavailable");
  return context;
}

export const createDashboardPrompt = "Create a new dashboard for me. First ask what I want to monitor, then design it when you have enough context.";
```

- [ ] **Step 2: Create `shell.tsx`**

```tsx
import { ActionIcon, Alert, AppShell, Avatar, Burger, Drawer, Group, Menu, Tooltip, useComputedColorScheme, useMantineColorScheme } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { GithubLogo, GlobeHemisphereWest, Moon, SignOut, Sun } from "@phosphor-icons/react";
import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { createDashboardPrompt, useFanoutApp } from "./app-context";
import { logout, useViewer } from "./auth";
import { BrandLockup } from "./brand";
import Rail, { type RailHandle, type RailProps } from "./rail";

export default function Shell({ children }: { children: ReactNode }) {
  const { agentAvailable, threadID, threadMissing, newThread, selectThread, openChat } = useFanoutApp();
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const { dashboardId } = useParams({ strict: false }) as { dashboardId?: string };
  const isChat = pathname === "/chat" || pathname.startsWith("/chat/");
  const [drawerOpened, drawer] = useDisclosure(false);
  const [signOutError, setSignOutError] = useState("");
  const railRef = useRef<RailHandle>(null);

  // A navigation from inside the drawer should close it.
  useEffect(() => { drawer.close(); }, [pathname]);

  useEffect(() => {
    const shortcuts = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        if (railRef.current) railRef.current.focusSearch(); else drawer.open();
      }
    };
    window.addEventListener("keydown", shortcuts);
    return () => window.removeEventListener("keydown", shortcuts);
  }, []);

  const railProps: Omit<RailProps, "ref"> = {
    agentAvailable,
    activeThreadID: isChat && !threadMissing && pathname !== "/chat" ? threadID : undefined,
    activeDashboardID: dashboardId,
    onNewChat: newThread,
    onSelectThread: selectThread,
    onDeletedThread: (deletedID) => { if (deletedID === threadID) newThread(); },
    onSelectDashboard: (id) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: id } }),
    onCreateDashboard: () => openChat(createDashboardPrompt),
  };

  return <AppShell header={{ height: 52 }} navbar={{ width: 256, breakpoint: "md", collapsed: { mobile: true } }} padding={0}>
    <AppShell.Header>
      <Group h="100%" px={{ base: "sm", sm: "md" }} justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap">
          <Burger hiddenFrom="md" opened={drawerOpened} onClick={drawer.toggle} size="sm" aria-label="Open navigation" />
          <BrandLockup size="small" />
        </Group>
        <Group gap="xs" wrap="nowrap">
          <ColorSchemeToggle />
          <AccountMenu onError={setSignOutError} />
        </Group>
      </Group>
    </AppShell.Header>
    <AppShell.Navbar p="sm"><Rail ref={railRef} {...railProps} /></AppShell.Navbar>
    <Drawer opened={drawerOpened} onClose={drawer.close} hiddenFrom="md" size={288} padding="sm" title={<BrandLockup size="small" />} overlayProps={{ backgroundOpacity: 0.24, blur: 1 }}>
      <Rail {...railProps} autoFocusSearch={drawerOpened} />
    </Drawer>
    <AppShell.Main>
      {signOutError && <Alert color="bad" m="md" withCloseButton onClose={() => setSignOutError("")}>{signOutError}</Alert>}
      {children}
    </AppShell.Main>
  </AppShell>;
}

function ColorSchemeToggle() {
  const { setColorScheme } = useMantineColorScheme();
  // Reading the computed scheme rather than the stored one means the button
  // offers the opposite of what is on screen even while the setting is "auto".
  const scheme = useComputedColorScheme("light", { getInitialValueInEffect: true });
  const next = scheme === "dark" ? "light" : "dark";
  return <Tooltip label={`Switch to ${next} theme`}>
    <ActionIcon variant="subtle" color="gray" aria-label={`Switch to ${next} theme`} onClick={() => setColorScheme(next)}>
      {scheme === "dark" ? <Sun size={17} weight="bold" /> : <Moon size={17} weight="bold" />}
    </ActionIcon>
  </Tooltip>;
}

function AccountMenu({ onError }: { onError: (message: string) => void }) {
  const viewer = useViewer();
  const initial = (viewer.name || viewer.email || "?").trim().charAt(0).toUpperCase();
  return <Menu position="bottom-end" withinPortal shadow="md">
    <Menu.Target>
      <ActionIcon variant="subtle" color="gray" size="lg" radius="xl" aria-label="Account menu">
        <Avatar size={26} radius="xl" color="brand">{initial}</Avatar>
      </ActionIcon>
    </Menu.Target>
    <Menu.Dropdown>
      <Menu.Label>{viewer.email || "Signed in"}</Menu.Label>
      <Menu.Item component="a" href="https://github.com/labstack/fanout" target="_blank" rel="noreferrer" leftSection={<GithubLogo size={15} weight="bold" />}>Fanout on GitHub</Menu.Item>
      <Menu.Item component="a" href="https://labstack.com" target="_blank" rel="noreferrer" leftSection={<GlobeHemisphereWest size={15} />}>LabStack</Menu.Item>
      <Menu.Divider />
      <Menu.Item leftSection={<SignOut size={15} />} onClick={() => void logout().catch((cause) => onError(cause instanceof Error ? cause.message : "Sign-out failed — your session is still active."))}>Sign out</Menu.Item>
    </Menu.Dropdown>
  </Menu>;
}
```

- [ ] **Step 3: Rewrite `App.tsx` as the session**

Replace the whole file with:

```tsx
import { HttpAgent, type Message } from "@ag-ui/client";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useParams } from "@tanstack/react-router";
import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { FanoutAppContext } from "./app-context";
import AuthGate, { authorizedFetch, useRuntimeStatus } from "./auth";
import { activityLabel } from "./chat";
import { createID } from "./id";
import { threadHistoryQueryKey } from "./rail";
import Shell from "./shell";

function Session() {
  const { agent_available: agentAvailable } = useRuntimeStatus();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { threadId: routeThreadID } = useParams({ strict: false }) as { threadId?: string };
  // A draft is a thread the browser has named but the server has not seen.
  // Its id is remembered so the route change on first send does not trigger
  // a fetch for a thread that does not exist yet.
  const [draftID, setDraftID] = useState(createID);
  const draftsRef = useRef(new Set<string>());
  const threadID = routeThreadID ?? draftID;
  const [messages, setMessages] = useState<Message[]>([]);
  const [messageTimes, setMessageTimes] = useState<Record<string, number>>({});
  const [loadedThreadID, setLoadedThreadID] = useState("");
  const [threadMissing, setThreadMissing] = useState(false);
  const [running, setRunning] = useState(false);
  const [activity, setActivity] = useState("");
  const [input, setInput] = useState("");
  const [error, setError] = useState("");
  const pendingPromptRef = useRef("");
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const agent = useMemo(() => new HttpAgent({ url: "/api/agent", threadId: threadID, fetch: (url, init) => authorizedFetch(url, init) }), [threadID]);
  const ready = !agentAvailable || loadedThreadID === threadID;

  useEffect(() => {
    let active = true;
    setMessages([]);
    setMessageTimes({});
    setLoadedThreadID("");
    setThreadMissing(false);
    setRunning(false);
    setActivity("");
    setError("");
    if (!agentAvailable) { setLoadedThreadID(threadID); return; }
    const isDraft = !routeThreadID || draftsRef.current.has(threadID);
    if (isDraft) {
      agent.setMessages([]);
      setLoadedThreadID(threadID);
    } else {
      authorizedFetch(`/api/agent/threads/${encodeURIComponent(threadID)}`).then(async (response) => {
        if (response.status === 404) return null;
        if (!response.ok) throw new Error(`Unable to load thread (${response.status})`);
        return response.json() as Promise<{ messages?: Message[] }>;
      }).then((thread) => {
        if (!active) return;
        if (thread === null) { setThreadMissing(true); setLoadedThreadID(threadID); return; }
        agent.setMessages(thread.messages ?? []);
        setMessages([...(thread.messages ?? [])]);
        setLoadedThreadID(threadID);
      }).catch(() => {
        if (!active) return;
        pendingPromptRef.current = "";
        setError("This chat could not be restored. Start a new chat or try again.");
      });
    }
    const subscription = agent.subscribe({
      onEvent: ({ messages: next }) => setMessages([...next] as Message[]),
      onRunInitialized: () => { setRunning(true); setError(""); },
      onToolCallStartEvent: ({ event }) => setActivity(activityLabel(event.toolCallName)),
      onToolCallEndEvent: () => setActivity(""),
      onRunFinalized: ({ messages: next }) => {
        const finished = [...next] as Message[];
        setMessages(finished);
        setMessageTimes((times) => {
          const stamped = { ...times };
          for (const message of finished) if (message.role === "assistant" && !stamped[message.id]) stamped[message.id] = Date.now();
          return stamped;
        });
        setRunning(false);
        setActivity("");
        draftsRef.current.delete(threadID);
        void queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      },
      onRunFailed: (failure) => {
        console.error("Agent run failed", failure);
        setError("Fanout could not complete this analysis.");
        setRunning(false);
        setActivity("");
        void queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      },
    });
    return () => { active = false; subscription.unsubscribe(); agent.abortRun(); };
  }, [agent, agentAvailable, queryClient, routeThreadID, threadID]);

  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [messages, running]);

  useEffect(() => {
    if (!agentAvailable) return;
    const shortcuts = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const editing = target?.matches("input, textarea, [contenteditable='true']");
      if (event.key === "/" && !editing) {
        event.preventDefault();
        if (routeThreadID) requestAnimationFrame(() => inputRef.current?.focus());
        else void navigate({ to: "/chat" }).then(() => requestAnimationFrame(() => inputRef.current?.focus()));
      }
      if (event.key === "Escape" && target === inputRef.current) { setInput(""); inputRef.current?.blur(); }
    };
    window.addEventListener("keydown", shortcuts);
    return () => window.removeEventListener("keydown", shortcuts);
  }, [agentAvailable, navigate, routeThreadID]);

  async function run() {
    setRunning(true);
    setError("");
    try { await agent.runAgent(); } catch (cause) {
      console.error("Agent run failed", cause);
      setError("Fanout could not complete this analysis.");
      setRunning(false);
      setActivity("");
    }
  }

  async function send(text: string) {
    const content = text.trim();
    if (!agentAvailable || !content || running || !ready || threadMissing) return;
    if (!routeThreadID) {
      draftsRef.current.add(threadID);
      void navigate({ to: "/chat/$threadId", params: { threadId: threadID }, replace: true });
    }
    const message = { id: createID(), role: "user", content } as Message;
    agent.addMessage(message);
    setMessages([...agent.messages]);
    setMessageTimes((times) => ({ ...times, [message.id]: Date.now() }));
    setInput("");
    await run();
  }

  useEffect(() => {
    const prompt = pendingPromptRef.current;
    if (!ready || !routeThreadID || !prompt) return;
    pendingPromptRef.current = "";
    void send(prompt);
  }, [ready, routeThreadID, threadID]);

  function submit(event: FormEvent) { event.preventDefault(); void send(input); }
  function stop() { agent.abortRun(); setRunning(false); setActivity(""); }
  function retry() { if (!running && agent.messages.some((message) => message.role === "user")) void run(); }
  function openChat(prompt?: string) {
    if (!agentAvailable) return;
    const nextThreadID = createID();
    draftsRef.current.add(nextThreadID);
    pendingPromptRef.current = prompt ?? "";
    void navigate({ to: "/chat/$threadId", params: { threadId: nextThreadID } });
  }
  function newThread() {
    agent.abortRun();
    pendingPromptRef.current = "";
    setDraftID(createID());
    void navigate({ to: "/chat" });
  }
  function selectThread(selectedThreadID: string) {
    void navigate({ to: "/chat/$threadId", params: { threadId: selectedThreadID } });
  }

  return <FanoutAppContext.Provider value={{ agentAvailable, threadID, threadMissing, messages, messageTimes, ready, running, activity, input, setInput, error, bottomRef, inputRef, send, submit, stop, retry, openChat, newThread, selectThread }}>
    <Shell><Outlet /></Shell>
  </FanoutAppContext.Provider>;
}

export default function App() {
  const queryClient = useMemo(() => new QueryClient(), []);
  return <QueryClientProvider client={queryClient}><AuthGate><Session /></AuthGate></QueryClientProvider>;
}
```

- [ ] **Step 4: Create a minimal `chat.tsx` so the session compiles**

Task 6 fills this file in. For now create `ui/host/src/chat.tsx` with the three pieces the session and the routes need, moved verbatim from the old `App.tsx`: `ChatPage`, `Composer`, `Welcome`, `ChatMessage`, `toolTitle`, plus:

```tsx
const activityLabels: Record<string, string> = {
  observability_overview: "Checking system health…",
  service_topology: "Mapping service dependencies…",
  service_performance: "Reading performance signals…",
  trace_detail: "Inspecting a trace…",
  search_logs: "Searching logs…",
  intelligence_snapshot: "Reviewing detected anomalies…",
};

export function activityLabel(toolName: string): string {
  return activityLabels[toolName] ?? "Working on it…";
}
```

Change every `useFanoutApp` import in the moved code to `import { useFanoutApp } from "./app-context";` and change `ChatPage` to read `threadMissing` and `retry` from the context so the file type-checks against the new context shape (the visuals are redone in Task 6). Keep the old `Composer` markup for now but drop its `pos="fixed"` wrapper, replacing it with `<Box pb="md" pt="sm">…</Box>`, because the footer it was offset against is gone.

- [ ] **Step 5: Update the routes**

`routes/chat.index.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { ChatPage } from "../chat";

export const Route = createFileRoute("/chat/")({
  component: ChatPage,
});
```

`routes/chat.$threadId.tsx`: change the import to `import { ChatPage } from "../chat";`.

`routes/dashboards.index.tsx` and `routes/dashboards.$dashboardId.tsx`: change `import { useFanoutApp } from "../App";` to `import { useFanoutApp } from "../app-context";`.

`dashboard.tsx`: replace the local `getJSON`, `Envelope`, `DashboardRecord`, `DashboardSummary` declarations with `import { dashboardsQueryKey, getJSON, type DashboardRecord, type DashboardSummary, type Envelope } from "./api";` and use `dashboardsQueryKey` where `["dashboards"]` appears. (The page is rewritten in Task 11; this keeps it compiling.)

- [ ] **Step 6: Remove the dead CSS**

In `index.css` delete the `.chat-composer-field` rules' dependence on a fixed footer: keep the two `.chat-composer-field` rules as they are (they only style border and focus ring). Nothing else changes in this step.

- [ ] **Step 7: Write the session test**

Create `app.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from "@tanstack/react-router";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

declare global {
  interface Window { happyDOM: { setURL(url: string): void } }
}

const agentMocks = vi.hoisted(() => ({ runAgent: vi.fn(async () => undefined), abortRun: vi.fn(), instances: [] as Array<{ threadId: string }> }));

vi.mock("@ag-ui/client", () => ({
  HttpAgent: class {
    threadId: string;
    messages: Array<{ id: string; role: string; content?: string }> = [];
    constructor(options: { threadId: string }) { this.threadId = options.threadId; agentMocks.instances.push(this); }
    subscribe() { return { unsubscribe: () => undefined }; }
    setMessages(messages: Array<{ id: string; role: string; content?: string }>) { this.messages = messages; }
    addMessage(message: { id: string; role: string; content?: string }) { this.messages = [...this.messages, message]; }
    runAgent = agentMocks.runAgent;
    abortRun = agentMocks.abortRun;
  },
}));

vi.mock("./auth", () => ({
  default: ({ children }: { children: React.ReactNode }) => children,
  useRuntimeStatus: () => ({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: false }),
  useViewer: () => ({ id: "viewer-1", email: "v@example.com", name: "Vee", role: "admin" }),
  authorizedFetch: (input: RequestInfo | URL, init?: RequestInit) => fetch(input, init),
  logout: vi.fn(async () => undefined),
  clearSession: vi.fn(),
}));

import App from "./App";
import { ChatPage } from "./chat";

const fetchMock = vi.fn<typeof fetch>();

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function setValue(input: HTMLTextAreaElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
}

describe("Session", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    agentMocks.runAgent.mockClear();
    agentMocks.instances.length = 0;
    fetchMock.mockImplementation(async (input) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/dashboards") return json({ dashboards: [] });
      if (url.pathname === "/api/agent/threads") return json({ threads: [], nextCursor: "" });
      if (url.pathname.startsWith("/api/agent/threads/")) return json({ message: "not found" }, 404);
      throw new Error(`unexpected request: ${url.pathname}`);
    });
    window.happyDOM.setURL("https://fanout.example.com/chat");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("starts a draft without fetching a thread and names the thread on first send", async () => {
    const rootRoute = createRootRoute({ component: App });
    const chatIndex = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const chatThread = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatIndex, chatThread]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.querySelector('textarea[aria-label="Message Fanout"]')).not.toBeNull());
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/agent/threads/"))).toBe(false);

    const composer = document.querySelector('textarea[aria-label="Message Fanout"]') as HTMLTextAreaElement;
    await act(async () => setValue(composer, "Summarize system health"));
    await act(async () => composer.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));

    await vi.waitFor(() => expect(agentMocks.runAgent).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(window.location.pathname).toMatch(/^\/chat\/[0-9a-f-]{36}$/));
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/agent/threads/"))).toBe(false);
    expect(document.body.textContent).not.toContain("no longer exists");

    await act(async () => root.unmount());
  });

  it("explains a thread that no longer exists", async () => {
    window.happyDOM.setURL("https://fanout.example.com/chat/deleted-thread");
    const rootRoute = createRootRoute({ component: App });
    const chatThread = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatThread]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("This chat no longer exists"));
    expect(document.body.textContent).toContain("New chat");
    await act(async () => root.unmount());
  });
});
```

The `Outlet` import is unused; remove it if `tsc` complains. `ChatPage` in Task 6 renders the "This chat no longer exists" alert; the placeholder from Step 4 must already render that text when `threadMissing` is true, so add to the Step 4 `ChatPage`: `if (threadMissing) return <Container size="sm" py="xl"><Alert color="warn" title="This chat no longer exists"><Button size="compact-sm" variant="light" onClick={newThread}>New chat</Button></Alert></Container>;` reading `newThread` from the context.

- [ ] **Step 8: Run tests and lint**

Run: `cd ui/host && bun run lint && bun run test`
Expected: lint clean; every test file passes (auth, auth-session, dashboard-layout, mcp-app-frame, rail, app).

- [ ] **Step 9: Build the host and check the shell by hand**

```bash
cd /Users/v/Projects/labstack/fanout && just ui-host
```

Start the local instance (memory note "local UI screenshot loop") and open `http://127.0.0.1:7520/chat`. Confirm: rail on the left with New chat, Chats, Dashboards, Search; header has burger (below 992px), brand, theme toggle, avatar; no footer; avatar menu shows the email and Sign out; Cmd-K focuses search; at 390px the burger opens the drawer and nothing clips. Then discard the built assets:

```bash
git checkout -- internal/ui/dist
```

- [ ] **Step 10: Commit Tasks 4 and 5 together**

```bash
git add ui/host/src
git commit -m "feat(ui): replace the header toggle and history drawer with a persistent rail

The rail is the only navigation: New chat, the chat list with rename and
delete, the dashboard list, and search on Cmd-K. The footer, the Live dot and
the Dashboard/Chat toggle are gone; sign out and the external links live in
the account menu. The session no longer fetches a thread it has just named,
which removes the 404 on every new chat.

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

## Phase C: chat pane and embedded views

### Task 6: The chat pane

**Files:**
- Modify: `ui/host/src/chat.tsx` (full rewrite of the Task 5 placeholder), `ui/host/src/index.css`
- Test: `ui/host/src/chat.test.tsx` (new)

**Interfaces:**
- Produces from `./chat`: `ChatPage()`, `activityLabel(toolName: string): string`, `toolTitle(name: string): string`. `Composer` and `Welcome` stay module-private.
- Consumes: `useFanoutApp` from `./app-context`; `MCPAppFrame` and `MCPAppContent` from `./mcp-app-frame`; `BrandMark` from `./brand`.

- [ ] **Step 1: Write the failing test**

Create `chat.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import type { Message } from "@ag-ui/client";
import { act, createRef, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FanoutAppContext, type FanoutAppContextValue } from "./app-context";
import { ChatPage } from "./chat";

function value(overrides: Partial<FanoutAppContextValue> = {}): FanoutAppContextValue {
  return {
    agentAvailable: true, threadID: "thread-1", threadMissing: false, messages: [], messageTimes: {}, ready: true, running: false, activity: "",
    input: "", setInput: vi.fn(), error: "", bottomRef: createRef<HTMLDivElement>(), inputRef: createRef<HTMLTextAreaElement>(),
    send: vi.fn(async () => undefined), submit: vi.fn((event: FormEvent) => event.preventDefault()), stop: vi.fn(), retry: vi.fn(),
    openChat: vi.fn(), newThread: vi.fn(), selectThread: vi.fn(),
    ...overrides,
  };
}

async function mount(context: FanoutAppContextValue) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(<MantineProvider><FanoutAppContext.Provider value={context}><ChatPage /></FanoutAppContext.Provider></MantineProvider>));
  return root;
}

function button(text: string) {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((item) => item.textContent?.trim() === text);
}

describe("ChatPage", () => {
  afterEach(() => { document.body.innerHTML = ""; });

  it("offers suggestions on an empty draft and sends the chosen one", async () => {
    const context = value();
    const root = await mount(context);
    expect(document.body.textContent).toContain("What do you want to know about your system?");
    expect(document.body.textContent).not.toContain("See what changed");
    const chip = button("Find the source of elevated errors");
    expect(chip).not.toBeUndefined();
    await act(async () => chip?.click());
    expect(context.send).toHaveBeenCalledWith("Find the source of elevated errors");
    await act(async () => root.unmount());
  });

  it("renders markdown tables and code blocks with product chrome", async () => {
    const messages: Message[] = [
      { id: "u1", role: "user", content: "Show the slowest endpoints" } as Message,
      { id: "a1", role: "assistant", content: "| Endpoint | P95 |\n|---|---|\n| GET /orders | 650ms |\n\n```sql\nSELECT 1\n```" } as Message,
    ];
    const root = await mount(value({ messages, messageTimes: { u1: Date.UTC(2026, 8, 5, 18, 16) } }));
    const table = document.querySelector(".chat-markdown table");
    expect(table).not.toBeNull();
    expect(table?.closest(".chat-table")).not.toBeNull();
    expect(document.querySelector(".chat-markdown pre code")?.textContent).toContain("SELECT 1");
    expect(document.querySelector('button[aria-label="Copy code"]')).not.toBeNull();
    expect(document.querySelector('button[aria-label="Copy message"]')).not.toBeNull();
    expect(document.querySelector(".mantine-Avatar-root")).toBeNull();
    await act(async () => root.unmount());
  });

  it("shows the activity line and a stop button while running", async () => {
    const context = value({ running: true, activity: "Checking system health…", messages: [{ id: "u1", role: "user", content: "hi" } as Message] });
    const root = await mount(context);
    expect(document.body.textContent).toContain("Checking system health…");
    const stop = document.querySelector('button[aria-label="Stop"]') as HTMLButtonElement;
    expect(stop).not.toBeNull();
    await act(async () => stop.click());
    expect(context.stop).toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("offers retry after a failed run and a new chat for a missing thread", async () => {
    const failed = value({ error: "Fanout could not complete this analysis.", messages: [{ id: "u1", role: "user", content: "hi" } as Message] });
    let root = await mount(failed);
    await act(async () => button("Retry")?.click());
    expect(failed.retry).toHaveBeenCalled();
    await act(async () => root.unmount());
    document.body.innerHTML = "";

    const missing = value({ threadMissing: true });
    root = await mount(missing);
    expect(document.body.textContent).toContain("This chat no longer exists");
    await act(async () => button("New chat")?.click());
    expect(missing.newThread).toHaveBeenCalled();
    await act(async () => root.unmount());
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui/host && bun run test src/chat.test.tsx`
Expected: FAIL on the first test (`See what changed` still present) and the markdown test (`.chat-table` missing).

- [ ] **Step 3: Rewrite `chat.tsx`**

```tsx
import type { Message } from "@ag-ui/client";
import { ActionIcon, Alert, Box, Button, Center, Container, Group, Loader, Paper, Stack, Table, Text, Textarea, Title, Tooltip, Typography } from "@mantine/core";
import { Check, Copy, PaperPlaneTilt, Stop } from "@phosphor-icons/react";
import { lazy, Suspense, useState, type ComponentProps, type ReactNode } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { useFanoutApp } from "./app-context";
import { BrandMark } from "./brand";
import type { MCPAppContent } from "./mcp-app-frame";

const MCPAppFrame = lazy(() => import("./mcp-app-frame"));

const suggestions = [
  "Summarize system health for the last hour",
  "Find the source of elevated errors",
  "Map the current service dependencies",
  "Show the slowest endpoints",
];

const activityLabels: Record<string, string> = {
  observability_overview: "Checking system health…",
  service_topology: "Mapping service dependencies…",
  service_performance: "Reading performance signals…",
  trace_detail: "Inspecting a trace…",
  search_logs: "Searching logs…",
  intelligence_snapshot: "Reviewing detected anomalies…",
};

export function activityLabel(toolName: string): string {
  return activityLabels[toolName] ?? "Working on it…";
}

export function toolTitle(name: string) {
  return ({ observability_overview: "System health", service_topology: "Service map", service_performance: "Performance", trace_detail: "Trace analysis", search_logs: "Logs" } as Record<string, string>)[name] ?? "System analysis";
}

export function ChatPage() {
  const { agentAvailable, messages, messageTimes, ready, running, activity, error, threadMissing, bottomRef, send, retry, newThread } = useFanoutApp();
  if (!agentAvailable) return <Container size="sm" py={96}><Paper withBorder radius="lg" p={{ base: "xl", sm: 40 }}><Stack gap="md"><Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">Optional capability</Text><Title order={1} fz={28}>Chat is not configured</Title><Text c="dimmed">Add an AI provider key to enable chat. Telemetry ingest, dashboards, traces, logs, and metrics remain available without it.</Text><Button component="a" href="/dashboards" variant="light" mt="sm">Open dashboards</Button></Stack></Paper></Container>;
  const visibleMessages = messages.filter((message) => message.role !== "tool");
  return <Box className="chat-pane">
    <Box className="chat-scroll">
      <Container size={880} px={{ base: "md", sm: "xl" }} py="lg">
        {threadMissing && <Alert color="warn" radius="lg" title="This chat no longer exists"><Group justify="space-between"><Text size="sm">It was deleted, or the link is wrong.</Text><Button size="compact-sm" variant="light" onClick={newThread}>New chat</Button></Group></Alert>}
        {!threadMissing && !ready && <Center mih="40vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Loading chat</Text></Center>}
        {!threadMissing && ready && <>
          {visibleMessages.length === 0 && <Welcome onSelect={send} />}
          <Stack gap="lg" aria-live="polite">
            {visibleMessages.map((message) => <ChatMessage key={message.id} message={message} time={messageTimes[message.id]} send={send} />)}
            {running && <Group gap="xs"><Loader type="dots" size="sm" /><Text c="dimmed" size="sm">{activity || "Analyzing your system"}</Text></Group>}
            {error && <Alert color="bad" radius="lg" title="Something went wrong"><Group justify="space-between"><Text size="sm">{error}</Text><Button size="compact-sm" variant="light" color="bad" onClick={retry}>Retry</Button></Group></Alert>}
            <div ref={bottomRef} />
          </Stack>
        </>}
      </Container>
    </Box>
    {!threadMissing && <Composer />}
  </Box>;
}

function Composer() {
  const { input, setInput, inputRef, submit, send, stop, ready, running } = useFanoutApp();
  return <Box className="chat-composer">
    <Container size={880} px={{ base: "md", sm: "xl" }}>
      <Paper component="form" onSubmit={submit} className="chat-composer-field" withBorder radius={24} py={6} pl="lg" pr={6}>
        <Group align="flex-end" gap="xs" wrap="nowrap">
          <Textarea ref={inputRef} aria-label="Message Fanout" value={input} onChange={(event) => setInput(event.currentTarget.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(input); } }} placeholder={running ? "Fanout is working…" : "Ask about health, errors, or latency…"} disabled={!ready} autosize minRows={1} maxRows={8} variant="unstyled" flex={1} />
          {running
            ? <Tooltip label="Stop"><ActionIcon type="button" variant="default" size={40} radius="xl" aria-label="Stop" onClick={stop}><Stop size={16} weight="fill" /></ActionIcon></Tooltip>
            : <ActionIcon type="submit" variant="filled" size={40} radius="xl" disabled={!input.trim() || !ready} aria-label="Send message"><PaperPlaneTilt size={17} weight="fill" /></ActionIcon>}
        </Group>
      </Paper>
      <Text c="dimmed" size="xs" ta="center" mt={6}>Enter to send, Shift+Enter for a new line</Text>
    </Container>
  </Box>;
}

function Welcome({ onSelect }: { onSelect: (text: string) => Promise<void> }) {
  return <Stack align="center" gap="md" pt={{ base: 32, sm: 88 }} pb="xl" ta="center">
    <BrandMark size="large" />
    <Title order={1} fz={24} fw={500} lh={1.25} maw={560}>What do you want to know about your system?</Title>
    <Group justify="center" gap="xs" mt="xs" maw={720}>
      {suggestions.map((suggestion) => <Button key={suggestion} variant="default" size="sm" radius="xl" onClick={() => void onSelect(suggestion)}>{suggestion}</Button>)}
    </Group>
  </Stack>;
}

function ChatMessage({ message, time, send }: { message: Message; time?: number; send: (text: string) => Promise<void> }) {
  if (message.role === "activity") {
    const activity = message as Message & { activityType?: string; content: MCPAppContent };
    if (activity.activityType === "mcp-app") return <Paper withBorder radius="lg" style={{ overflow: "hidden" }} aria-label={toolTitle(activity.content.toolName)}><Suspense fallback={<Center mih={180}><Loader size="sm" /></Center>}><MCPAppFrame content={activity.content} onMessage={send} /></Suspense></Paper>;
    return null;
  }
  const content = typeof message.content === "string" ? message.content : JSON.stringify(message.content);
  if (!content && message.role === "assistant") return null;
  const user = message.role === "user";
  const stamp = time ? new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(time)) : "";
  return <Box className="chat-message" data-role={user ? "user" : "assistant"}>
    {user
      ? <Paper radius="lg" px="md" py="sm" bg="var(--mantine-color-brand-light)" maw="70%" ml="auto" w="fit-content"><Text style={{ whiteSpace: "pre-wrap" }}>{content}</Text></Paper>
      : <Typography className="chat-markdown"><Markdown remarkPlugins={[remarkGfm]} components={markdownComponents}>{content}</Markdown></Typography>}
    <Group className="chat-message-meta" gap={6} justify={user ? "flex-end" : "flex-start"} mt={4}>
      {stamp && <Text c="dimmed" size="xs">{stamp}</Text>}
      {!user && <CopyButton text={content} label="Copy message" />}
    </Group>
  </Box>;
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return <Tooltip label={copied ? "Copied" : label}>
    <ActionIcon variant="subtle" color="gray" size="sm" aria-label={label} onClick={() => { void navigator.clipboard.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); }); }}>
      {copied ? <Check size={13} weight="bold" /> : <Copy size={13} />}
    </ActionIcon>
  </Tooltip>;
}

/* Every block the model can emit gets product chrome: tables scroll inside
   their own container instead of widening the column, code blocks carry a
   copy button, links open in a new tab. */
const markdownComponents: Components = {
  table: ({ children }) => <Box className="chat-table"><Table striped highlightOnHover withTableBorder verticalSpacing="xs" fz="sm">{children}</Table></Box>,
  thead: ({ children }) => <Table.Thead>{children}</Table.Thead>,
  tbody: ({ children }) => <Table.Tbody>{children}</Table.Tbody>,
  tr: ({ children }) => <Table.Tr>{children}</Table.Tr>,
  th: ({ children }) => <Table.Th>{children}</Table.Th>,
  td: ({ children }) => <Table.Td>{children}</Table.Td>,
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  a: ({ href, children }) => <a href={href} target="_blank" rel="noreferrer">{children}</a>,
};

function CodeBlock({ children }: { children: ReactNode }) {
  const text = codeText(children);
  return <Box className="chat-code" pos="relative">
    <Box className="chat-code-actions" pos="absolute" top={6} right={6}><CopyButton text={text} label="Copy code" /></Box>
    <pre>{children}</pre>
  </Box>;
}

function codeText(node: ReactNode): string {
  if (typeof node === "string") return node;
  if (Array.isArray(node)) return node.map(codeText).join("");
  if (node && typeof node === "object" && "props" in node) return codeText((node as { props: ComponentProps<"code"> }).props.children);
  return "";
}
```

- [ ] **Step 4: Add the pane and message rules to `index.css`**

Append:

```css
/* The chat pane fills the space under the header; messages scroll inside it
   and the composer sits at the bottom of the pane, not of the window. */
.chat-pane {
  display: flex;
  flex-direction: column;
  height: calc(100dvh - var(--app-shell-header-offset, 0px));
}

.chat-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.chat-composer {
  flex: 0 0 auto;
  padding-bottom: var(--mantine-spacing-sm);
  background: var(--mantine-color-body);
}

.chat-message-meta,
.chat-code-actions {
  opacity: 0;
  transition: opacity 120ms ease;
}

.chat-message:hover .chat-message-meta,
.chat-message:focus-within .chat-message-meta,
.chat-code:hover .chat-code-actions,
.chat-code:focus-within .chat-code-actions {
  opacity: 1;
}

@media (hover: none) {
  .chat-message-meta,
  .chat-code-actions {
    opacity: 1;
  }
}

.chat-markdown :where(h1, h2, h3, h4) {
  font-size: var(--mantine-font-size-lg);
  margin-top: var(--mantine-spacing-md);
  margin-bottom: var(--mantine-spacing-xs);
}

.chat-markdown :where(p, ul, ol) {
  margin-top: 0;
  margin-bottom: var(--mantine-spacing-sm);
}

.chat-table {
  overflow-x: auto;
  margin-bottom: var(--mantine-spacing-sm);
}

.chat-code pre {
  margin: 0 0 var(--mantine-spacing-sm);
  padding: var(--mantine-spacing-sm) var(--mantine-spacing-md);
  border: 1px solid var(--mantine-color-default-border);
  border-radius: var(--mantine-radius-md);
  background: var(--mantine-color-default);
  font-family: var(--mantine-font-family-monospace);
  font-size: var(--mantine-font-size-sm);
  overflow-x: auto;
}
```

- [ ] **Step 5: Run the chat tests and the whole suite**

Run: `cd ui/host && bun run test src/chat.test.tsx && bun run test && bun run lint`
Expected: four chat tests pass; the suite passes; lint clean.

- [ ] **Step 6: Check by hand**

`just ui-host`, restart the local instance, open a chat, send "Summarize system health for the last hour". Confirm: the activity line reads "Checking system health…" during the run, the Stop button replaces Send, the embedded view appears without a shadow, the assistant text shows a copy button on hover, a markdown table (ask "list the services as a table") scrolls inside its box at 390px, the composer sits at the bottom of the pane with no dead zone. Then `git checkout -- internal/ui/dist`.

- [ ] **Step 7: Commit**

```bash
git add ui/host/src/chat.tsx ui/host/src/chat.test.tsx ui/host/src/index.css
git commit -m "feat(ui): chat pane with sticky composer, activity line and block chrome

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

### Task 7: Embedded views: frame floor and view polish

**Files:**
- Modify: `ui/host/src/mcp-app-frame.tsx`, `ui/host/src/mcp-app-frame.test.tsx`
- Modify: `ui/apps/src/components.tsx`, `ui/apps/src/overview.tsx`, `ui/apps/src/topology.tsx`, `ui/apps/src/performance.tsx`, `ui/apps/src/trace.tsx`, `ui/apps/src/logs.tsx`
- Rebuild and commit: `internal/mcp/apps/*.html`

**Interfaces:**
- `ViewHeader` props become `{ title: string; summary?: string; onRefresh: () => void | Promise<unknown>; disabled?: boolean }` (no `eyebrow`).
- `ViewStatus` gains a `retry?: () => void` prop rendered as a Retry button on the error alert.
- `MetaFooter` unchanged.

- [ ] **Step 1: Lower the frame floor and update its test**

In `mcp-app-frame.tsx` delete the `appMinimumHeights` map and change:

```ts
  const minimumHeight = appMinimumHeights[content.toolName] ?? 620;
```

to

```ts
  // The app reports its own size through the bridge; the floor only covers
  // the moment before the first report.
  const minimumHeight = 240;
```

Run `rg -n '620|760|700|720|appMinimumHeights' src/mcp-app-frame.test.tsx`; change any expectation of the old floors to `240` and any expectation of a clamped size to `Math.max(240, …)`.

Run: `cd ui/host && bun run test src/mcp-app-frame.test.tsx`
Expected: PASS.

- [ ] **Step 2: Slim `ViewHeader` and add retry to `ViewStatus` in `ui/apps/src/components.tsx`**

Replace both functions:

```tsx
export function ViewHeader({ title, summary, onRefresh, disabled }: { title: string; summary?: string; onRefresh: () => void | Promise<unknown>; disabled?: boolean }) {
  return <Group justify="space-between" align="flex-start" wrap="nowrap" px={{ base: "md", sm: "lg" }} pt="sm" pb="xs">
    <Box miw={0}>
      <Title order={2} fz="lg" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{title}</Title>
      {summary && <Text c="dimmed" size="sm" mt={2}>{summary}</Text>}
    </Box>
    <Tooltip label="Refresh this view"><ActionIcon variant="default" size="md" aria-label="Refresh this view" onClick={() => void onRefresh()} disabled={disabled}><ArrowClockwise size={15} weight="bold" /></ActionIcon></Tooltip>
  </Group>;
}

export function ViewStatus({ error, loading, retry }: { error?: string | null; loading?: string; retry?: () => void }) {
  if (error) return <Alert color="bad" m="md" radius="md"><Group justify="space-between"><Text size="sm">{error}</Text>{retry && <Button size="compact-sm" variant="light" color="bad" onClick={retry}>Retry</Button>}</Group></Alert>;
  if (loading) return <Center mih={160} p="xl"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">{loading}</Text></Center>;
  return null;
}
```

Add `ActionIcon` to the `@mantine/core` import; `Button` is already imported.

- [ ] **Step 3: Update the five apps**

In each app file remove the `eyebrow="…"` prop from `<ViewHeader>` and pass `retry` to `<ViewStatus>`:

- `overview.tsx`: `<ViewHeader title="System health" summary={result?.summary} …>` and `<ViewStatus … retry={() => void callTool("observability_overview")} />`.
- `topology.tsx`: title "Service map", retry calls `service_topology`.
- `performance.tsx`: title stays `result?.data.service || "Performance"`, retry calls `service_performance`.
- `trace.tsx`: title stays the trace id form, retry calls `trace_detail`.
- `logs.tsx`: title "Logs", retry calls `search_logs`.

Then the per-view polish, one edit each:

- `overview.tsx`: the three `Metric` tiles become `SimpleGrid cols={{ base: 3 }}` so they stay one row on narrow widths, and the services table gains a `Table.Th` right-aligned for numeric columns: add `ta="right"` to the Traffic, P95 and Errors `Table.Th` and `Table.Td` cells.
- `topology.tsx`: `EdgeList` page size becomes 6 (`usePagedItems(edges, 6)`); the route text `caller → callee` renders with `<Text component="span" ff="monospace" size="sm">`.
- `performance.tsx`: in `EndpointsView` the numeric columns (`Calls`, `P50`, `P95`, `P99`, `Errors`) get `ta="right"`; the `Text component="code"` for the path becomes `<Text component="span" ff="monospace" size="sm">`.
- `trace.tsx`: the `Duration` column cell already uses `ff="monospace"`; add `ta="right"` to its header and cell. In `TraceLogs` the `Time` cell gets `style={{ whiteSpace: "nowrap" }}`.
- `logs.tsx`: the `Time` cell gets `style={{ whiteSpace: "nowrap" }}`; the filter row's `TextInput` gets `size="xs"` to match the `SegmentedControl`.

- [ ] **Step 4: Type-check and rebuild the apps**

```bash
cd ui/apps && bun run lint && bun run build
git status --short internal/mcp/apps
```

Expected: lint clean, five builds, five modified HTML files.

- [ ] **Step 5: Check by hand**

Restart the local instance (the apps are embedded at binary build time: run `just build` first). In a chat ask for system health, the service map, performance, a trace and logs. Confirm each view: title without eyebrow, refresh icon at the right, no system-font fallback (compare the table text with the host's Plex Sans), height matches content with no blank band under short tables, error alert shows Retry when the network is cut (stop the server briefly and press refresh).

- [ ] **Step 6: Commit sources and rebuilt apps together**

```bash
git add ui/host/src/mcp-app-frame.tsx ui/host/src/mcp-app-frame.test.tsx ui/apps/src internal/mcp/apps
git commit -m "feat(ui): slimmer embedded views that size to their content

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

## Phase D: dashboard

### Task 8: Widget defaults and free-slot placement

**Files:**
- Modify: `ui/host/src/dashboard-layout.ts`
- Test: `ui/host/src/dashboard-layout.test.ts`

**Interfaces:**
- Produces: `export type WidgetType = "overview" | "topology" | "activity" | "assistant" | "performance" | "trace" | "logs"`, `export const widgetTypes: WidgetType[]`, `export const widgetDefaults: Record<WidgetType, { w: number; h: number; minW: number; minH: number }>`, `export function nextDashboardSlot(layout: DashboardLayoutItem[], w: number, h: number, columns = 12): { x: number; y: number }`. `nextDashboardRow` and `compactDashboardLayout` unchanged.

- [ ] **Step 1: Write the failing tests**

Append to `dashboard-layout.test.ts`:

```ts
import { nextDashboardSlot, widgetDefaults, widgetTypes } from "./dashboard-layout";

describe("widget placement", () => {
  it("fills the free space on the last row before starting a new one", () => {
    expect(nextDashboardSlot([], 4, 4)).toEqual({ x: 0, y: 0 });
    expect(nextDashboardSlot([{ i: "a", x: 0, y: 0, w: 4, h: 4 }], 8, 5)).toEqual({ x: 4, y: 0 });
    expect(nextDashboardSlot([{ i: "a", x: 0, y: 0, w: 4, h: 4 }, { i: "b", x: 4, y: 0, w: 4, h: 4 }], 4, 4)).toEqual({ x: 8, y: 0 });
    expect(nextDashboardSlot([{ i: "a", x: 0, y: 0, w: 4, h: 4 }, { i: "b", x: 4, y: 0, w: 8, h: 6 }], 4, 4)).toEqual({ x: 0, y: 6 });
  });

  it("considers only the last row when looking for a gap", () => {
    const layout: DashboardLayoutItem[] = [
      { i: "a", x: 0, y: 0, w: 4, h: 4 },
      { i: "b", x: 0, y: 4, w: 8, h: 4 },
    ];
    expect(nextDashboardSlot(layout, 4, 4)).toEqual({ x: 8, y: 4 });
    expect(nextDashboardSlot(layout, 6, 4)).toEqual({ x: 0, y: 8 });
  });

  it("gives every widget type a default size that fits twelve columns", () => {
    expect(widgetTypes).toEqual(["overview", "topology", "activity", "assistant", "performance", "trace", "logs"]);
    for (const type of widgetTypes) {
      const size = widgetDefaults[type];
      expect(size.w).toBeLessThanOrEqual(12);
      expect(size.minW).toBeLessThanOrEqual(size.w);
      expect(size.minH).toBeLessThanOrEqual(size.h);
    }
    expect(widgetDefaults.assistant.w).toBe(4);
    expect(widgetDefaults.topology).toEqual({ w: 8, h: 5, minW: 4, minH: 4 });
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ui/host && bun run test src/dashboard-layout.test.ts`
Expected: FAIL, `nextDashboardSlot` is not exported.

- [ ] **Step 3: Implement**

Append to `dashboard-layout.ts`:

```ts
export type WidgetType = "overview" | "topology" | "activity" | "assistant" | "performance" | "trace" | "logs";

/** Mirrors WidgetTypes in internal/dashboard/service.go. */
export const widgetTypes: WidgetType[] = ["overview", "topology", "activity", "assistant", "performance", "trace", "logs"];

/** Grid units: twelve columns, 76px rows. Sized so a card's default shape
 *  matches its content instead of leaving a blank band under it. */
export const widgetDefaults: Record<WidgetType, { w: number; h: number; minW: number; minH: number }> = {
  overview: { w: 4, h: 4, minW: 3, minH: 4 },
  activity: { w: 4, h: 5, minW: 3, minH: 4 },
  assistant: { w: 4, h: 3, minW: 3, minH: 3 },
  topology: { w: 8, h: 5, minW: 4, minH: 4 },
  performance: { w: 8, h: 5, minW: 4, minH: 4 },
  logs: { w: 8, h: 5, minW: 4, minH: 4 },
  trace: { w: 8, h: 4, minW: 4, minH: 4 },
};

function overlaps(a: { x: number; y: number; w: number; h: number }, b: { x: number; y: number; w: number; h: number }) {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

/** First free position for a w×h widget on the last row, else a new row. */
export function nextDashboardSlot(layout: DashboardLayoutItem[], w: number, h: number, columns = 12): { x: number; y: number } {
  if (layout.length === 0) return { x: 0, y: 0 };
  const rowY = Math.max(...layout.map((item) => item.y));
  for (let x = 0; x + w <= columns; x += 1) {
    const candidate = { x, y: rowY, w, h };
    if (!layout.some((item) => overlaps(item, candidate))) return { x, y: rowY };
  }
  return { x: 0, y: nextDashboardRow(layout) };
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd ui/host && bun run test src/dashboard-layout.test.ts`
Expected: PASS, five tests.

- [ ] **Step 5: Lint and commit**

```bash
cd ui/host && bun run lint
cd /Users/v/Projects/labstack/fanout
git add ui/host/src/dashboard-layout.ts ui/host/src/dashboard-layout.test.ts
git commit -m "feat(ui): per-type widget sizes and free-slot placement

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

### Task 9: ECharts in the host and the widget data layer

**Files:**
- Modify: `ui/host/package.json`, `ui/host/bun.lock` (via `bun add`)
- Create: `ui/host/src/echart.tsx`, `ui/host/src/widgets/data.ts`
- Test: `ui/host/src/widgets/data.test.tsx` (new)

**Interfaces:**
- Produces from `./echart`: `EChart({ option, height = 260, label, onClick, className })` where `height: number | string`; `useECharts` re-export of `echarts/core` `use`.
- Produces from `./widgets/data`:

```ts
export type Filters = { window: string; namespace: string };
export type WidgetConfig = Record<string, unknown>;
export const dashboardWindows: Array<{ value: string; label: string }>;   // 15m … 720h
export function windowName(value: string): string;                        // "1h" -> "1 hour"
export function configString(config: WidgetConfig | undefined, key: string): string;
export function widgetParams(filters: Filters, config?: WidgetConfig, keys?: string[]): URLSearchParams;
export type ObservabilityKind = "overview" | "topology" | "performance" | "logs" | "trace";
export function observabilityKey(kind: ObservabilityKind, params: URLSearchParams): readonly unknown[];
export function useObservability<T>(kind: ObservabilityKind, params: URLSearchParams, enabled?: boolean): UseQueryResult<Result<T>>;
export function useLastUpdated(): number | null;
```

- [ ] **Step 1: Add the dependency and the chart component**

```bash
cd ui/host && bun add echarts@6.1.0
```

Create `ui/host/src/echart.tsx`:

```tsx
import { AriaComponent, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { init, use, type EChartsCoreOption } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

use([SVGRenderer, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, AriaComponent]);

export { use as useECharts };

/* Same component as ui/apps/src/echart.tsx. It is not shared through ui/
   because that directory has no node_modules to resolve echarts from. */
export function EChart({ option, height = 260, label, onClick, className }: { option: EChartsCoreOption; height?: number | string; label: string; onClick?: (params: unknown) => void; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!ref.current) return;
    const chart = init(ref.current, undefined, { renderer: "svg" });
    chart.setOption({ animationDuration: 280, aria: { enabled: true, decal: { show: true }, description: label }, ...option });
    if (onClick) chart.on("click", onClick);
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(ref.current);
    return () => { observer.disconnect(); chart.dispose(); };
  }, [label, onClick, option]);
  return <div ref={ref} className={className ? `echart ${className}` : "echart"} style={{ height, width: "100%", minWidth: 0 }} role="img" aria-label={label} />;
}
```

- [ ] **Step 2: Write the failing data tests**

Create `ui/host/src/widgets/data.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { configString, useLastUpdated, useObservability, widgetParams, windowName } from "./data";

describe("widget params", () => {
  it("builds the observability query from filters and config", () => {
    const params = widgetParams({ window: "6h", namespace: "prod" }, { service: "orders", severity: "ERROR", search: "timeout" }, ["service", "severity", "search"]);
    expect(params.get("window")).toBe("6h");
    expect(params.get("namespace")).toBe("prod");
    expect(params.get("limit")).toBe("40");
    expect(params.get("service")).toBe("orders");
    expect(params.get("severity")).toBe("ERROR");
    expect(params.get("search")).toBe("timeout");
    expect(widgetParams({ window: "1h", namespace: "" }).has("namespace")).toBe(false);
    expect(widgetParams({ window: "1h", namespace: "" }, { service: "orders" }).has("service")).toBe(false);
  });

  it("names windows and reads string config", () => {
    expect(windowName("1h")).toBe("1 hour");
    expect(windowName("720h")).toBe("30 days");
    expect(windowName("9h")).toBe("9h");
    expect(configString({ service: "orders" }, "service")).toBe("orders");
    expect(configString({ service: 12 }, "service")).toBe("");
    expect(configString(undefined, "service")).toBe("");
  });
});

describe("useObservability and useLastUpdated", () => {
  const fetchMock = vi.fn<typeof fetch>();
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ data: { health: "healthy" } }), { status: 200, headers: { "content-type": "application/json" } }));
  });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("fetches the endpoint and reports the newest update time", async () => {
    function Probe() {
      const overview = useObservability<{ health: string }>("overview", new URLSearchParams({ window: "1h" }));
      const updated = useLastUpdated();
      return <div>{overview.data?.data.health ?? "loading"} {updated ? "updated" : "never"}</div>;
    }
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => root.render(<QueryClientProvider client={client}><Probe /></QueryClientProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("healthy updated"));
    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/observability/overview?window=1h");
    await act(async () => root.unmount());
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd ui/host && bun run test src/widgets/data.test.tsx`
Expected: FAIL, `Cannot find module './data'`.

- [ ] **Step 4: Create `widgets/data.ts`**

```ts
import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { getJSON } from "../api";
import type { Result } from "../../../contracts";

export type Filters = { window: string; namespace: string };
export type WidgetConfig = Record<string, unknown>;

export const dashboardWindows = [
  { value: "15m", label: "15 minutes" },
  { value: "1h", label: "1 hour" },
  { value: "6h", label: "6 hours" },
  { value: "24h", label: "24 hours" },
  { value: "168h", label: "7 days" },
  { value: "720h", label: "30 days" },
];

export function windowName(value: string): string {
  return dashboardWindows.find((item) => item.value === value)?.label ?? value;
}

export function configString(config: WidgetConfig | undefined, key: string): string {
  const value = config?.[key];
  return typeof value === "string" ? value : "";
}

/** Query string for the observability endpoints: the dashboard filters plus
 *  the listed widget config keys when they hold a non-empty string. */
export function widgetParams(filters: Filters, config?: WidgetConfig, keys: string[] = []): URLSearchParams {
  const params = new URLSearchParams({ window: filters.window, limit: "40" });
  if (filters.namespace) params.set("namespace", filters.namespace);
  for (const key of keys) {
    const value = configString(config, key);
    if (value) params.set(key, value);
  }
  return params;
}

export type ObservabilityKind = "overview" | "topology" | "performance" | "logs" | "trace";

export function observabilityKey(kind: ObservabilityKind, params: URLSearchParams) {
  return ["observability", kind, params.toString()] as const;
}

// Every widget refetches on the same cadence; a failed query retries sooner so
// a widget recovers without a manual refresh.
const refetchInterval = (query: { state: { status: string } }) => (query.state.status === "error" ? 15_000 : 30_000);

export function useObservability<T>(kind: ObservabilityKind, params: URLSearchParams, enabled = true): UseQueryResult<Result<T>> {
  return useQuery({
    queryKey: observabilityKey(kind, params),
    queryFn: () => getJSON<Result<T>>(`/api/observability/${kind}?${params}`),
    enabled,
    refetchInterval,
  });
}

function latestUpdate(client: ReturnType<typeof useQueryClient>): number | null {
  const newest = client.getQueryCache().findAll({ queryKey: ["observability"] }).reduce((max, query) => Math.max(max, query.state.dataUpdatedAt), 0);
  return newest || null;
}

/** Newest dataUpdatedAt across every observability query on the page. */
export function useLastUpdated(): number | null {
  const client = useQueryClient();
  const [updated, setUpdated] = useState<number | null>(() => latestUpdate(client));
  useEffect(() => client.getQueryCache().subscribe(() => setUpdated(latestUpdate(client))), [client]);
  return updated;
}
```

- [ ] **Step 5: Run the tests to verify they pass, then lint and commit**

Run: `cd ui/host && bun run test src/widgets/data.test.tsx && bun run lint`
Expected: PASS, three tests; lint clean.

```bash
cd /Users/v/Projects/labstack/fanout
git add ui/host/package.json ui/host/bun.lock ui/host/src/echart.tsx ui/host/src/widgets
git commit -m "feat(ui): echarts in the host and a shared widget data layer

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

### Task 10: Widget card chrome, configure modal, and the seven bodies

**Files:**
- Create: `ui/host/src/widgets/widget-card.tsx`, `ui/host/src/widgets/configure.tsx`, `ui/host/src/widgets/pieces.tsx`, `ui/host/src/widgets/overview.tsx`, `ui/host/src/widgets/topology.tsx`, `ui/host/src/widgets/activity.tsx`, `ui/host/src/widgets/performance.tsx`, `ui/host/src/widgets/trace.tsx`, `ui/host/src/widgets/logs.tsx`, `ui/host/src/widgets/assistant.tsx`
- Modify: `ui/host/src/index.css`
- Test: `ui/host/src/widgets/widgets.test.tsx` (new)

**Interfaces:**
- Produces from `./widgets/widget-card`:

```ts
export type Widget = { id: string; type: WidgetType; title: string; config?: WidgetConfig; enabled: boolean };
export type WidgetBodyProps = { widget: Widget; filters: Filters; dark: boolean; services: string[]; agentAvailable: boolean; onOpenChat: (prompt?: string) => void };
export default function WidgetCard(props: WidgetBodyProps & { onRemove: () => void; onConfigure: (config: WidgetConfig) => void }): JSX.Element;
export const widgetTitles: Record<WidgetType, string>;
```

- Produces from `./widgets/configure`: `ConfigureWidget({ opened, widget, services, onClose, onSave })` and `configurable(type: WidgetType): boolean` (false for `activity` and `assistant`).
- Produces from `./widgets/pieces`: `Metric({ label, value, color?, hint? })`, `Empty({ text })`, `WidgetError({ retry })`, `Sparkline({ values, color, label })`, `HealthBadge({ health, label })`.
- Each body file default-exports `function XWidget(props: WidgetBodyProps)`.
- Consumes: `useObservability`, `widgetParams`, `configString`, `windowName`, `Filters`, `WidgetConfig` from `./data` (Task 9); `EChart`, `useECharts` from `../echart` (Task 9); `WidgetType` from `../dashboard-layout` (Task 8); contracts, format and chart helpers from `ui/` (Task 2).

- [ ] **Step 1: Write the failing tests**

Create `ui/host/src/widgets/widgets.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../echart", () => ({
  EChart: ({ label }: { label: string }) => <div data-chart={label} />,
  useECharts: () => undefined,
}));

import WidgetCard, { type Widget } from "./widget-card";

const fetchMock = vi.fn<typeof fetch>();
const filters = { window: "1h", namespace: "" };
const provenance = { query_id: "q", window: "2026-09-05T17:00:00Z/2026-09-05T18:00:00Z", generated_at: "2026-09-05T18:00:00Z", complete: true, data_source: "test" };
const services = [
  { service: "payments", health: "unhealthy", spans: 1813, error_rate: 0.13, p50_ms: 169, p95_ms: 729.9, log_count: 326, metric_count: 0 },
  { service: "orders", health: "healthy", spans: 2109, error_rate: 0.009, p50_ms: 80, p95_ms: 650.4, log_count: 20, metric_count: 0 },
];
const points = Array.from({ length: 6 }, (_, index) => ({ time: `2026-09-05T17:${String(index * 10).padStart(2, "0")}:00Z`, spans: 100 + index, error_rate: 0.01, p50_ms: 40, p95_ms: 120 + index, log_count: 3, metric_count: 0 }));

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function respond(input: RequestInfo | URL) {
  const url = new URL(String(input), "http://localhost");
  const data = {
    "/api/observability/overview": { health: "unhealthy", counts: { healthy: 1, degraded: 0, unhealthy: 1 }, total_spans: 12221, error_rate: 0.0216, services, service_count: 2 },
    "/api/observability/topology": { nodes: services, edges: [{ caller: "orders", callee: "payments", type: "http", calls: 1700, average_ms: 140, error_rate: 0.13 }] },
    "/api/observability/performance": { points, endpoints: [{ method: "POST", path: "/charge", calls: 1802, p50_ms: 140, p95_ms: 500, p99_ms: 900, error_rate: 0.13, health: "unhealthy" }], heatmap: [], comparison: [] },
    "/api/observability/logs": { entries: [{ time: "2026-09-05T17:59:00Z", severity: "ERROR", service: "payments", body: "POST /charge failed", trace_id: "abc" }], buckets: [{ time: "2026-09-05T17:50:00Z", severity: "ERROR", count: 3 }] },
    "/api/observability/trace": { trace_id: "78bf7f484abf8a4c726d2d24359ec5e4", duration_ms: 994.6, has_error: true, services: ["gateway", "orders"], spans: [{ span_id: "s1", service: "gateway", operation: "GET /api/orders", kind: "server", start: "2026-09-05T17:59:00.000Z", duration_ms: 994.6, status: "ERROR" }, { span_id: "s2", parent_span_id: "s1", service: "orders", operation: "GET /orders/{id}", kind: "server", start: "2026-09-05T17:59:00.010Z", duration_ms: 600, status: "OK" }], logs: [] },
  } as Record<string, unknown>;
  const body = data[url.pathname];
  if (!body) throw new Error(`unexpected request: ${url.pathname}`);
  return json({ schema: "test", summary: "", data: body, provenance });
}

async function mount(widget: Widget, handlers: { onRemove?: () => void; onConfigure?: (config: Record<string, unknown>) => void; onOpenChat?: (prompt?: string) => void } = {}) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => root.render(
    <QueryClientProvider client={client}><MantineProvider>
      <WidgetCard widget={widget} filters={filters} dark={false} services={["payments", "orders"]} agentAvailable onOpenChat={handlers.onOpenChat ?? (() => undefined)} onRemove={handlers.onRemove ?? (() => undefined)} onConfigure={handlers.onConfigure ?? (() => undefined)} />
    </MantineProvider></QueryClientProvider>,
  ));
  return root;
}

function setValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
}

describe("widgets", () => {
  beforeEach(() => { vi.stubGlobal("fetch", fetchMock); fetchMock.mockReset(); fetchMock.mockImplementation(async (input) => respond(input)); });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("overview shows health, an error-rate sparkline and the distribution", async () => {
    const root = await mount({ id: "w1", type: "overview", title: "System health", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("Unhealthy"));
    expect(document.body.textContent).toContain("2.16%");
    expect(document.querySelector('[data-chart="Error rate trend"]')).not.toBeNull();
    expect(document.body.textContent).toContain("1 healthy");
    expect(document.body.textContent).toContain("1 unhealthy");
    await act(async () => root.unmount());
  });

  it("topology draws a graph and counts routes", async () => {
    const root = await mount({ id: "w2", type: "topology", title: "Service map", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("2 services · 1 route"));
    expect(document.querySelector('[data-chart="Service dependency graph"]')).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("trace formats durations in lower-case units", async () => {
    const root = await mount({ id: "w3", type: "trace", title: "Trace focus", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("995ms"));
    expect(document.body.textContent).not.toContain("Ms");
    expect(document.body.textContent).toContain("78bf7f48…c5e4");
    expect(document.body.textContent).toContain("GET /api/orders");
    await act(async () => root.unmount());
  });

  it("logs show a histogram and timestamps", async () => {
    const root = await mount({ id: "w4", type: "logs", title: "Logs", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("POST /charge failed"));
    expect(document.querySelector('[data-chart="Log volume by severity"]')).not.toBeNull();
    expect(document.body.textContent).toMatch(/\d{1,2}:59/);
    await act(async () => root.unmount());
  });

  it("performance charts the trend and lists the top endpoints", async () => {
    const root = await mount({ id: "w5", type: "performance", title: "Performance", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("/charge"));
    expect(document.querySelector('[data-chart="Operations and P95 latency"]')).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("assistant offers window-scoped questions", async () => {
    const openChat = vi.fn();
    const root = await mount({ id: "w6", type: "assistant", title: "Ask Fanout", enabled: true }, { onOpenChat: openChat });
    const question = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Summarize the last 1 hour"));
    expect(question).not.toBeUndefined();
    await act(async () => question?.click());
    expect(openChat).toHaveBeenCalledWith("Summarize the last 1 hour");
    expect(fetchMock).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("removes through the actions menu and saves configuration", async () => {
    const onRemove = vi.fn();
    const onConfigure = vi.fn();
    const root = await mount({ id: "w7", type: "logs", title: "Logs", enabled: true }, { onRemove, onConfigure });
    await vi.waitFor(() => expect(document.body.textContent).toContain("POST /charge failed"));
    expect(document.querySelector('button[aria-label="Remove Logs"]')).toBeNull();
    const actions = document.querySelector('button[aria-label="Actions for Logs"]') as HTMLButtonElement;
    await act(async () => actions.click());
    const configure = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Configure"));
    await act(async () => configure?.click());
    const search = document.querySelector('[role="dialog"] input[aria-label="Search"]') as HTMLInputElement;
    await act(async () => setValue(search, "timeout"));
    const save = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find((button) => button.textContent?.trim() === "Save");
    await act(async () => save?.click());
    expect(onConfigure).toHaveBeenCalledWith(expect.objectContaining({ search: "timeout" }));

    await act(async () => actions.click());
    const remove = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Remove"));
    await act(async () => remove?.click());
    expect(onRemove).toHaveBeenCalled();
    await act(async () => root.unmount());
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ui/host && bun run test src/widgets/widgets.test.tsx`
Expected: FAIL, `Cannot find module './widget-card'`.

- [ ] **Step 3: Create `widgets/pieces.tsx`**

```tsx
import { Badge, Box, Button, Center, Paper, Text } from "@mantine/core";
import { ListMagnifyingGlass, WarningCircle } from "@phosphor-icons/react";
import { EChart } from "../echart";
import { healthColor } from "../../../chart";

export function Metric({ label, value, color, hint }: { label: string; value: string | number; color?: string; hint?: string }) {
  return <Paper withBorder radius="md" p="sm" bg="var(--mantine-color-default)" miw={0}>
    <Text c="dimmed" size="xs" truncate>{label}</Text>
    <Text fw={700} fz="xl" c={color} mt={2} lh={1.2} truncate>{value}</Text>
    {hint && <Text c="dimmed" size="xs" mt={2} truncate>{hint}</Text>}
  </Paper>;
}

export function HealthBadge({ health, label }: { health: string; label: string }) {
  return <Badge color={healthColor(health)} variant="light" tt="none">{label}</Badge>;
}

export function Empty({ text }: { text: string }) {
  return <Center py="xl"><ListMagnifyingGlass size={20} /><Text c="dimmed" size="sm" ml="xs">{text}</Text></Center>;
}

export function WidgetError({ retry }: { retry: () => void }) {
  return <Center py="xl" style={{ flexDirection: "column", gap: 8 }}>
    <Box display="flex" style={{ alignItems: "center", gap: 8 }}><WarningCircle size={20} weight="fill" color="var(--mantine-color-bad-filled)" /><Text c="bad" fw={500} size="sm">Couldn't load this view</Text></Box>
    <Button size="compact-xs" variant="light" color="bad" onClick={retry}>Retry now</Button>
    <Text c="dimmed" size="xs">Retrying automatically</Text>
  </Center>;
}

/** A line with no axes, for a metric tile. */
export function Sparkline({ values, color, label }: { values: number[]; color: string; label: string }) {
  const option = {
    animation: false,
    grid: { left: 0, right: 0, top: 2, bottom: 2 },
    xAxis: { type: "category", show: false, data: values.map((_, index) => index) },
    yAxis: { type: "value", show: false, min: 0 },
    tooltip: { show: false },
    series: [{ type: "line", data: values, showSymbol: false, smooth: 0.3, lineStyle: { width: 1.5, color }, areaStyle: { opacity: 0.12, color } }],
  };
  return <EChart option={option} height={28} label={label} />;
}
```

- [ ] **Step 4: Create `widgets/configure.tsx`**

```tsx
import { Button, Group, Modal, Select, Stack, TextInput } from "@mantine/core";
import { useEffect, useState } from "react";
import type { WidgetType } from "../dashboard-layout";
import { configString, type WidgetConfig } from "./data";
import type { Widget } from "./widget-card";

type Field = "service" | "severity" | "search" | "trace_id";

const fields: Partial<Record<WidgetType, Field[]>> = {
  overview: ["service"],
  topology: ["service"],
  performance: ["service"],
  logs: ["service", "severity", "search"],
  trace: ["trace_id"],
};

export function configurable(type: WidgetType): boolean {
  return (fields[type]?.length ?? 0) > 0;
}

export function ConfigureWidget({ opened, widget, services, onClose, onSave }: { opened: boolean; widget: Widget; services: string[]; onClose: () => void; onSave: (config: WidgetConfig) => void }) {
  const [draft, setDraft] = useState<Record<Field, string>>({ service: "", severity: "", search: "", trace_id: "" });
  useEffect(() => {
    if (!opened) return;
    setDraft({ service: configString(widget.config, "service"), severity: configString(widget.config, "severity"), search: configString(widget.config, "search"), trace_id: configString(widget.config, "trace_id") });
  }, [opened, widget.config]);
  const wanted = fields[widget.type] ?? [];
  const serviceOptions = [...new Set([...services, draft.service].filter(Boolean))];

  function save() {
    const next: WidgetConfig = { ...(widget.config ?? {}) };
    for (const key of wanted) {
      const value = draft[key].trim();
      if (value) next[key] = value; else delete next[key];
    }
    onSave(next);
    onClose();
  }

  return <Modal opened={opened} onClose={onClose} title={`Configure ${widget.title}`} centered>
    <form onSubmit={(event) => { event.preventDefault(); save(); }}>
      <Stack>
        {wanted.includes("service") && <Select label="Service" placeholder="All services" data={serviceOptions} value={draft.service || null} onChange={(value) => setDraft({ ...draft, service: value ?? "" })} clearable searchable aria-label="Service" />}
        {wanted.includes("severity") && <Select label="Severity" placeholder="All severities" data={["ERROR", "WARN", "INFO", "DEBUG"]} value={draft.severity || null} onChange={(value) => setDraft({ ...draft, severity: value ?? "" })} clearable aria-label="Severity" />}
        {wanted.includes("search") && <TextInput label="Search" placeholder="Text the log body must contain" value={draft.search} onChange={(event) => setDraft({ ...draft, search: event.currentTarget.value })} aria-label="Search" />}
        {wanted.includes("trace_id") && <TextInput label="Trace id" placeholder="Leave empty for the most relevant recent trace" value={draft.trace_id} onChange={(event) => setDraft({ ...draft, trace_id: event.currentTarget.value })} ff="monospace" aria-label="Trace id" />}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button type="submit">Save</Button>
        </Group>
      </Stack>
    </form>
  </Modal>;
}
```

- [ ] **Step 5: Create `widgets/widget-card.tsx`**

```tsx
import { ActionIcon, Group, Menu, Paper, Stack, Title } from "@mantine/core";
import { DotsThree, SlidersHorizontal, Trash } from "@phosphor-icons/react";
import { useState, type JSX } from "react";
import type { WidgetType } from "../dashboard-layout";
import ActivityWidget from "./activity";
import AssistantWidget from "./assistant";
import { ConfigureWidget, configurable } from "./configure";
import type { Filters, WidgetConfig } from "./data";
import LogsWidget from "./logs";
import OverviewWidget from "./overview";
import PerformanceWidget from "./performance";
import TopologyWidget from "./topology";
import TraceWidget from "./trace";

export type Widget = { id: string; type: WidgetType; title: string; config?: WidgetConfig; enabled: boolean };

export type WidgetBodyProps = {
  widget: Widget;
  filters: Filters;
  dark: boolean;
  services: string[];
  agentAvailable: boolean;
  onOpenChat: (prompt?: string) => void;
};

export const widgetTitles: Record<WidgetType, string> = { overview: "System health", topology: "Service map", activity: "Recent activity", assistant: "Ask Fanout", performance: "Performance", trace: "Trace focus", logs: "Logs" };

const bodies: Record<WidgetType, (props: WidgetBodyProps) => JSX.Element> = { overview: OverviewWidget, topology: TopologyWidget, activity: ActivityWidget, assistant: AssistantWidget, performance: PerformanceWidget, trace: TraceWidget, logs: LogsWidget };

export default function WidgetCard(props: WidgetBodyProps & { onRemove: () => void; onConfigure: (config: WidgetConfig) => void }) {
  const { widget, services, onRemove, onConfigure } = props;
  const [menuOpened, setMenuOpened] = useState(false);
  const [configuring, setConfiguring] = useState(false);
  const Body = bodies[widget.type];
  return <Paper withBorder radius="lg" p="md" h="100%" className="widget-card" style={{ overflow: "hidden" }}>
    <Stack h="100%" gap="sm">
      <Group justify="space-between" align="center" wrap="nowrap" className="widget-drag" style={{ cursor: "grab" }}>
        <Title order={2} fz="md" fw={500} truncate>{widget.title}</Title>
        <Menu position="bottom-end" withinPortal opened={menuOpened} onChange={setMenuOpened}>
          <Menu.Target>
            <ActionIcon className="widget-actions" data-open={menuOpened || undefined} variant="subtle" color="gray" size="sm" aria-label={`Actions for ${widget.title}`}><DotsThree size={18} weight="bold" /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            {configurable(widget.type) && <Menu.Item leftSection={<SlidersHorizontal size={15} />} onClick={() => setConfiguring(true)}>Configure</Menu.Item>}
            <Menu.Item color="bad" leftSection={<Trash size={15} />} onClick={onRemove}>Remove</Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </Group>
      <div className="widget-body"><Body {...props} /></div>
    </Stack>
    <ConfigureWidget opened={configuring} widget={widget} services={services} onClose={() => setConfiguring(false)} onSave={onConfigure} />
  </Paper>;
}
```

- [ ] **Step 6: Create the seven bodies**

`widgets/overview.tsx`:

```tsx
import { Box, Group, Progress, SimpleGrid, Stack, Text } from "@mantine/core";
import type { Overview, Performance } from "../../../contracts";
import { healthColor, statusHex } from "../../../chart";
import { integer, percent } from "../../../format";
import { useObservability, widgetParams } from "./data";
import { Metric, Sparkline, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

export default function OverviewWidget({ widget, filters, dark }: WidgetBodyProps) {
  const params = widgetParams(filters, widget.config, ["service"]);
  const overview = useObservability<Overview>("overview", params);
  const performance = useObservability<Performance>("performance", params);
  if (overview.isError) return <WidgetError retry={() => void overview.refetch()} />;
  const data = overview.data?.data;
  const errorTrend = (performance.data?.data.points ?? []).map((point) => point.error_rate * 100);
  const total = Math.max(data?.service_count ?? 0, 1);
  const status = statusHex(dark);
  const healthWord = data ? data.health.charAt(0).toUpperCase() + data.health.slice(1) : "—";
  return <Stack gap="sm">
    <SimpleGrid cols={2} spacing="sm">
      <Metric label="Health" value={healthWord} color={data ? healthColor(data.health) : undefined} hint={data ? `${integer.format(data.service_count)} services` : undefined} />
      <Stack gap={0}>
        <Metric label="Error rate" value={data ? percent(data.error_rate) : "—"} color={data && data.error_rate >= 0.01 ? "bad" : undefined} hint={data ? `${integer.format(data.total_spans)} operations` : undefined} />
        {errorTrend.length > 1 && <Box mt={4}><Sparkline values={errorTrend} color={status.bad} label="Error rate trend" /></Box>}
      </Stack>
    </SimpleGrid>
    {data && <Box>
      <Progress.Root size="md" aria-label="Service health distribution">
        <Progress.Section value={data.counts.healthy / total * 100} color="ok" />
        <Progress.Section value={data.counts.degraded / total * 100} color="warn" />
        <Progress.Section value={data.counts.unhealthy / total * 100} color="bad" />
      </Progress.Root>
      <Group mt={6} gap="md">
        <Legend color="ok" text={`${data.counts.healthy} healthy`} />
        <Legend color="warn" text={`${data.counts.degraded} degraded`} />
        <Legend color="bad" text={`${data.counts.unhealthy} unhealthy`} />
      </Group>
    </Box>}
  </Stack>;
}

function Legend({ color, text }: { color: string; text: string }) {
  return <Group gap={6}><Box w={8} h={8} bg={color} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{text}</Text></Group>;
}
```

`widgets/topology.tsx`:

```tsx
import { GraphChart } from "echarts/charts";
import { Stack, Text } from "@mantine/core";
import { useMemo } from "react";
import type { Topology } from "../../../contracts";
import { chartTheme, statusHex } from "../../../chart";
import { EChart, useECharts } from "../echart";
import { useObservability, widgetParams } from "./data";
import { Empty, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

useECharts([GraphChart]);

export default function TopologyWidget({ widget, filters, dark, onOpenChat }: WidgetBodyProps) {
  const params = widgetParams(filters, widget.config, ["service"]);
  const topology = useObservability<Topology>("topology", params);
  const data = topology.data?.data;
  const option = useMemo(() => {
    if (!data) return null;
    const colors = chartTheme(dark);
    const status = statusHex(dark);
    const healthHex = (health: string) => (health === "unhealthy" ? status.bad : health === "degraded" ? status.warn : status.ok);
    return {
      tooltip: { backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } },
      series: [{
        type: "graph", layout: "force", roam: false, draggable: false,
        force: { repulsion: 160, edgeLength: [50, 110], gravity: 0.12 },
        label: { show: true, position: "bottom", color: colors.text, fontSize: 10 },
        edgeSymbol: ["none", "arrow"], edgeSymbolSize: 6,
        data: data.nodes.map((node) => ({ id: node.service, name: node.service, value: node.spans, symbolSize: Math.min(34, 18 + Math.log10(Math.max(node.spans, 1)) * 4), itemStyle: { color: colors.surface, borderColor: healthHex(node.health), borderWidth: 3 } })),
        links: data.edges.map((edge) => ({ source: edge.caller, target: edge.callee, value: edge.calls, lineStyle: { width: Math.min(4, 1 + Math.log10(Math.max(edge.calls, 1))), color: edge.error_rate >= 0.05 ? status.bad : colors.muted, opacity: 0.5, curveness: 0.08 } })),
        emphasis: { focus: "adjacency" },
      }],
    };
  }, [dark, data]);
  if (topology.isError) return <WidgetError retry={() => void topology.refetch()} />;
  if (data && data.nodes.length === 0) return <Empty text="No service relationships in this window" />;
  return <Stack gap={4} h="100%">
    {option && <EChart option={option} height="100%" className="widget-chart" label="Service dependency graph" onClick={(params) => { const item = params as { dataType?: string; data?: { id?: string } }; if (item.dataType === "node" && item.data?.id) onOpenChat(`Investigate the ${item.data.id} service. Explain its errors and latency.`); }} />}
    {data && <Text c="dimmed" size="xs">{data.nodes.length} services · {data.edges.length} {data.edges.length === 1 ? "route" : "routes"}</Text>}
  </Stack>;
}
```

`widgets/activity.tsx`:

```tsx
import { ScrollArea, Table, Text } from "@mantine/core";
import type { Overview } from "../../../contracts";
import { duration, percent } from "../../../format";
import { useObservability, widgetParams } from "./data";
import { Empty, HealthBadge, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

const order: Record<string, number> = { unhealthy: 0, degraded: 1, healthy: 2 };

export default function ActivityWidget({ widget, filters }: WidgetBodyProps) {
  const overview = useObservability<Overview>("overview", widgetParams(filters, widget.config, ["service"]));
  if (overview.isError) return <WidgetError retry={() => void overview.refetch()} />;
  const services = [...(overview.data?.data.services ?? [])].sort((left, right) => (order[left.health] ?? 3) - (order[right.health] ?? 3) || right.error_rate - left.error_rate);
  if (overview.data && services.length === 0) return <Empty text="No recent activity" />;
  return <ScrollArea type="auto" offsetScrollbars h="100%">
    <Table verticalSpacing="xs" fz="sm" highlightOnHover>
      <Table.Thead><Table.Tr><Table.Th>Service</Table.Th><Table.Th ta="right">P95</Table.Th><Table.Th ta="right">Errors</Table.Th></Table.Tr></Table.Thead>
      <Table.Tbody>{services.map((entry) => <Table.Tr key={entry.service}>
        <Table.Td><HealthBadge health={entry.health} label={entry.service} /></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" ff="monospace">{duration(entry.p95_ms)}</Text></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" c={entry.error_rate >= 0.01 ? "bad" : "dimmed"}>{percent(entry.error_rate)}</Text></Table.Td>
      </Table.Tr>)}</Table.Tbody>
    </Table>
  </ScrollArea>;
}
```

`widgets/performance.tsx`:

```tsx
import { LineChart } from "echarts/charts";
import { Badge, Box, Stack, Table, Text } from "@mantine/core";
import { useMemo } from "react";
import type { Performance } from "../../../contracts";
import { chartTheme, seriesColor, statusHex } from "../../../chart";
import { duration, integer, timelineTimestamp } from "../../../format";
import { EChart, useECharts } from "../echart";
import { useObservability, widgetParams } from "./data";
import { Empty, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

useECharts([LineChart]);

export default function PerformanceWidget({ widget, filters, dark }: WidgetBodyProps) {
  const performance = useObservability<Performance>("performance", widgetParams(filters, widget.config, ["service"]));
  const result = performance.data;
  const option = useMemo(() => {
    if (!result || result.data.points.length === 0) return null;
    const colors = chartTheme(dark);
    const window = result.provenance.window;
    return {
      color: [seriesColor("operations", dark), statusHex(dark).warn],
      grid: { left: 36, right: 40, top: 24, bottom: 22 },
      legend: { top: 0, left: 0, textStyle: { color: colors.muted, fontSize: 10 }, icon: "circle", itemWidth: 7, itemHeight: 7 },
      tooltip: { trigger: "axis", backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } },
      xAxis: { type: "category", data: result.data.points.map((point) => timelineTimestamp(point.time, window)), boundaryGap: false, axisLine: { lineStyle: { color: colors.border } }, axisTick: { show: false }, axisLabel: { color: colors.muted, fontSize: 9, hideOverlap: true } },
      yAxis: [
        { type: "value", splitLine: { lineStyle: { color: colors.grid } }, axisLabel: { color: colors.muted, fontSize: 9 } },
        { type: "value", splitLine: { show: false }, axisLabel: { color: colors.muted, fontSize: 9, formatter: (value: number) => duration(value) } },
      ],
      series: [
        { name: "Operations", type: "line", data: result.data.points.map((point) => point.spans), smooth: 0.22, showSymbol: false, lineStyle: { width: 2 }, areaStyle: { opacity: 0.05 } },
        { name: "P95", type: "line", yAxisIndex: 1, data: result.data.points.map((point) => point.p95_ms), smooth: 0.22, showSymbol: false, lineStyle: { width: 2 } },
      ],
    };
  }, [dark, result]);
  if (performance.isError) return <WidgetError retry={() => void performance.refetch()} />;
  if (result && result.data.points.length === 0) return <Empty text="No endpoint activity in this window" />;
  const endpoints = (result?.data.endpoints ?? []).slice(0, 3);
  return <Stack gap="xs" h="100%">
    {option && <Box style={{ flex: 1, minHeight: 120 }}><EChart option={option} height="100%" label="Operations and P95 latency" /></Box>}
    {endpoints.length > 0 && <Table verticalSpacing={4} fz="sm">
      <Table.Tbody>{endpoints.map((endpoint) => <Table.Tr key={`${endpoint.method}-${endpoint.path}`}>
        <Table.Td><Badge variant="light" size="xs" mr={6}>{endpoint.method}</Badge><Text component="span" size="sm" ff="monospace">{endpoint.path}</Text></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" c="dimmed">{integer.format(endpoint.calls)} calls</Text></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" ff="monospace">{duration(endpoint.p95_ms)}</Text></Table.Td>
      </Table.Tr>)}</Table.Tbody>
    </Table>}
  </Stack>;
}
```

`widgets/trace.tsx`:

```tsx
import { ActionIcon, Box, Group, SimpleGrid, Stack, Text, Tooltip } from "@mantine/core";
import { Copy } from "@phosphor-icons/react";
import type { TraceDetail } from "../../../contracts";
import { seriesColor } from "../../../chart";
import { duration, integer } from "../../../format";
import { useObservability, widgetParams } from "./data";
import { Empty, Metric, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

function shortID(value: string) { return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value; }

export default function TraceWidget({ widget, filters, dark }: WidgetBodyProps) {
  const trace = useObservability<TraceDetail>("trace", widgetParams(filters, widget.config, ["trace_id"]));
  if (trace.isError) return <WidgetError retry={() => void trace.refetch()} />;
  const data = trace.data?.data;
  if (data && data.spans.length === 0) return <Empty text="No traces in this window" />;
  const spans = [...(data?.spans ?? [])].sort((left, right) => right.duration_ms - left.duration_ms).slice(0, 6);
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  const total = Math.max(end - start, 1);
  return <Stack gap="sm">
    <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
      <Metric label="Duration" value={data ? duration(data.duration_ms) : "—"} />
      <Metric label="Spans" value={data ? integer.format(data.spans.length) : "—"} />
      <Metric label="Services" value={data ? integer.format(data.services.length) : "—"} />
      <Metric label="Status" value={data ? (data.has_error ? "Error" : "OK") : "—"} color={data ? (data.has_error ? "bad" : "ok") : undefined} />
    </SimpleGrid>
    {spans.length > 0 && <Stack gap={4}>
      {spans.map((span) => {
        const offset = (new Date(span.start).valueOf() - start) / total * 100;
        const width = Math.max(span.duration_ms / total * 100, 0.6);
        const failed = span.status.toUpperCase().includes("ERROR");
        return <Group key={span.span_id} gap="sm" wrap="nowrap">
          <Box w={170} miw={0}><Text size="xs" fw={600} truncate>{span.operation}</Text><Text c="dimmed" size="xs" truncate>{span.service}</Text></Box>
          <Tooltip label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`} withArrow>
            <Box pos="relative" h={10} flex={1} bg="var(--mantine-color-default-hover)" style={{ borderRadius: "var(--mantine-radius-sm)" }}>
              <Box pos="absolute" left={`${offset}%`} w={`${Math.min(width, 100 - offset)}%`} h="100%" bg={failed ? "bad" : seriesColor(span.service, dark)} style={{ borderRadius: "var(--mantine-radius-sm)", minWidth: 3 }} />
            </Box>
          </Tooltip>
          <Text size="xs" ff="monospace" w={56} ta="right">{duration(span.duration_ms)}</Text>
        </Group>;
      })}
    </Stack>}
    {data && <Group gap={4}>
      <Text c="dimmed" size="xs" ff="monospace">Trace {shortID(data.trace_id)}</Text>
      <Tooltip label="Copy trace id"><ActionIcon variant="subtle" color="gray" size="xs" aria-label="Copy trace id" onClick={() => void navigator.clipboard.writeText(data.trace_id)}><Copy size={12} /></ActionIcon></Tooltip>
    </Group>}
  </Stack>;
}
```

`widgets/logs.tsx`:

```tsx
import { BarChart } from "echarts/charts";
import { Badge, Box, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { useMemo } from "react";
import type { Logs } from "../../../contracts";
import { chartTheme, severityColor, severityHex } from "../../../chart";
import { timelineTimestamp } from "../../../format";
import { EChart, useECharts } from "../echart";
import { useObservability, widgetParams } from "./data";
import { Empty, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

useECharts([BarChart]);

export default function LogsWidget({ widget, filters, dark }: WidgetBodyProps) {
  const logs = useObservability<Logs>("logs", widgetParams(filters, widget.config, ["service", "severity", "search"]));
  const result = logs.data;
  const option = useMemo(() => {
    if (!result || result.data.buckets.length === 0) return null;
    const colors = chartTheme(dark);
    const window = result.provenance.window;
    const times = [...new Set(result.data.buckets.map((bucket) => bucket.time))];
    const severities = [...new Set(result.data.buckets.map((bucket) => bucket.severity))];
    const values = new Map(result.data.buckets.map((bucket) => [`${bucket.time} ${bucket.severity}`, bucket.count]));
    return {
      color: severities.map((severity) => severityHex(severity, dark)),
      grid: { left: 28, right: 8, top: 8, bottom: 20 },
      tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } },
      xAxis: { type: "category", data: times.map((time) => timelineTimestamp(time, window)), axisLabel: { color: colors.muted, fontSize: 8, hideOverlap: true }, axisLine: { lineStyle: { color: colors.border } } },
      yAxis: { type: "value", minInterval: 1, splitLine: { lineStyle: { color: colors.grid } }, axisLabel: { color: colors.muted, fontSize: 8 } },
      series: severities.map((severity) => ({ name: severity, type: "bar", stack: "logs", barMaxWidth: 14, data: times.map((time) => values.get(`${time} ${severity}`) ?? 0), itemStyle: { borderRadius: [2, 2, 0, 0] } })),
    };
  }, [dark, result]);
  if (logs.isError) return <WidgetError retry={() => void logs.refetch()} />;
  const entries = (result?.data.entries ?? []).slice(0, 6);
  if (result && entries.length === 0) return <Empty text="No matching logs in this window" />;
  return <Stack gap="xs" h="100%">
    {option && <Box h={72}><EChart option={option} height={72} label="Log volume by severity" /></Box>}
    <ScrollArea type="auto" offsetScrollbars style={{ flex: 1 }}>
      <Table verticalSpacing={4} fz="sm">
        <Table.Tbody>{entries.map((entry, index) => <Table.Tr key={`${entry.time}-${index}`}>
          <Table.Td style={{ whiteSpace: "nowrap" }}><Text component="span" size="xs" ff="monospace" c="dimmed">{result ? timelineTimestamp(entry.time, result.provenance.window, true) : entry.time}</Text></Table.Td>
          <Table.Td><Badge size="xs" color={severityColor(entry.severity)} variant="light">{entry.severity || "LOG"}</Badge></Table.Td>
          <Table.Td><Text component="span" size="sm" fw={600}>{entry.service}</Text></Table.Td>
          <Table.Td style={{ width: "100%" }}><Text size="sm" lineClamp={1} title={entry.body}>{entry.body}</Text></Table.Td>
        </Table.Tr>)}</Table.Tbody>
      </Table>
    </ScrollArea>
  </Stack>;
}
```

`widgets/assistant.tsx`:

```tsx
import { Button, Stack, Text } from "@mantine/core";
import { Sparkle } from "@phosphor-icons/react";
import { windowName } from "./data";
import type { WidgetBodyProps } from "./widget-card";

export default function AssistantWidget({ filters, agentAvailable, onOpenChat }: WidgetBodyProps) {
  if (!agentAvailable) return <Text c="dimmed" size="sm">Configure an AI provider to enable this view. The rest of this dashboard remains available.</Text>;
  const window = windowName(filters.window);
  const questions = [`Summarize the last ${window}`, `What changed in the last ${window}?`, "Which service needs attention right now?"];
  return <Stack gap="xs" align="flex-start">
    {questions.map((question) => <Button key={question} variant="default" size="xs" radius="xl" leftSection={<Sparkle size={13} weight="fill" />} onClick={() => onOpenChat(question)}>{question}</Button>)}
  </Stack>;
}
```

- [ ] **Step 7: Add the card rules to `index.css`**

Append:

```css
.widget-card .widget-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.widget-card .widget-body > * {
  flex: 1;
  min-height: 0;
}

.widget-chart {
  flex: 1;
  min-height: 140px;
}

.widget-actions {
  opacity: 0;
  transition: opacity 120ms ease;
}

.widget-card:hover .widget-actions,
.widget-card:focus-within .widget-actions,
.widget-actions[data-open] {
  opacity: 1;
}

@media (hover: none) {
  .widget-actions {
    opacity: 1;
  }
}
```

- [ ] **Step 8: Run the widget tests, then lint**

Run: `cd ui/host && bun run test src/widgets/widgets.test.tsx && bun run lint`
Expected: PASS, seven tests; lint clean.

- [ ] **Step 9: Commit**

```bash
cd /Users/v/Projects/labstack/fanout
git add ui/host/src/widgets ui/host/src/index.css
git commit -m "feat(ui): native dashboard widgets with charts, hover actions and configuration

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

### Task 11: The dashboard page

**Files:**
- Modify: `ui/host/src/dashboard.tsx` (rewrite), `ui/host/src/index.css` (drop the `.dashboard-grid` rule's fixed min-height if present; keep the react-grid rules)
- Test: `ui/host/src/dashboard.test.tsx` (new)

**Interfaces:**
- Produces: `export default function Dashboard({ dashboardID = "", agentAvailable, onOpenChat, onDashboardChange }: { dashboardID?: string; agentAvailable: boolean; onOpenChat: (prompt?: string) => void; onDashboardChange?: (id: string, replace?: boolean) => void })` (unchanged signature, used by both dashboard routes).
- Consumes: `WidgetCard`, `Widget`, `widgetTitles` from `./widgets/widget-card`; `Filters`, `dashboardWindows`, `useLastUpdated`, `useObservability`, `widgetParams`, `WidgetConfig` from `./widgets/data`; `compactDashboardLayout`, `nextDashboardSlot`, `widgetDefaults`, `widgetTypes`, `WidgetType`, `DashboardLayoutItem` from `./dashboard-layout`; `getJSON`, `dashboardsQueryKey`, `DashboardRecord`, `DashboardSummary`, `DashboardState` from `./api`; `Overview` from `../../contracts`.

- [ ] **Step 1: Write the failing test**

Create `ui/host/src/dashboard.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./echart", () => ({
  EChart: ({ label }: { label: string }) => <div data-chart={label} />,
  useECharts: () => undefined,
}));

import Dashboard from "./dashboard";

const fetchMock = vi.fn<typeof fetch>();
const provenance = { query_id: "q", window: "2026-09-05T17:00:00Z/2026-09-05T18:00:00Z", generated_at: "2026-09-05T18:00:00Z", complete: true, data_source: "test" };
const record = {
  id: "dash-main", name: "System overview", description: "Live health, dependencies, and recent activity.", is_default: true, updated_at: "2026-09-05 18:00:00",
  state: {
    filters: { window: "1h", namespace: "" },
    widgets: [{ id: "w-health", type: "overview", title: "System health", enabled: true }],
    layout: [{ i: "w-health", x: 0, y: 0, w: 4, h: 4, minW: 3, minH: 4 }],
  },
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function respond(input: RequestInfo | URL, init?: RequestInit) {
  const url = new URL(String(input), "http://localhost");
  if (url.pathname === "/api/dashboards") return json({ dashboards: [{ id: record.id, name: record.name, description: record.description, is_default: true, widget_count: 1, updated_at: record.updated_at }] });
  if (url.pathname === `/api/dashboards/${record.id}` && init?.method === "PUT") return json({ ...record, state: JSON.parse(String(init.body)).state });
  if (url.pathname === `/api/dashboards/${record.id}`) return json(record);
  if (url.pathname === "/api/observability/overview") return json({ schema: "t", summary: "", provenance, data: { health: "healthy", counts: { healthy: 1, degraded: 0, unhealthy: 0 }, total_spans: 10, error_rate: 0, services: [{ service: "orders", health: "healthy", spans: 10, error_rate: 0, p50_ms: 1, p95_ms: 2, log_count: 0, metric_count: 0 }], service_count: 1 } });
  if (url.pathname === "/api/observability/performance") return json({ schema: "t", summary: "", provenance, data: { points: [], endpoints: [], heatmap: [], comparison: [] } });
  throw new Error(`unexpected request: ${url.pathname}`);
}

describe("Dashboard", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async (input, init) => respond(input, init));
    localStorage.clear();
  });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("renders the header without refresh chrome and adds a widget into the free slot", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => root.render(
      <QueryClientProvider client={client}><MantineProvider>
        <Dashboard dashboardID="dash-main" agentAvailable onOpenChat={() => undefined} />
      </MantineProvider></QueryClientProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Healthy"));
    const buttons = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).map((button) => button.textContent?.trim());
    expect(buttons).not.toContain("Refresh");
    expect(buttons).not.toContain("Dashboards");
    expect(document.body.textContent).not.toContain("Saved");
    await vi.waitFor(() => expect(document.body.textContent).toMatch(/Updated \d/));

    const addView = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Add view"));
    await act(async () => addView?.click());
    const topology = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Service map"));
    await act(async () => topology?.click());

    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    const body = JSON.parse(String(put?.[1]?.body)) as { state: { layout: Array<{ i: string; x: number; y: number; w: number; h: number }>; widgets: Array<{ type: string }> } };
    expect(body.state.widgets.map((widget) => widget.type)).toEqual(["overview", "topology"]);
    const placed = body.state.layout.find((item) => item.i !== "w-health");
    expect(placed).toMatchObject({ x: 4, y: 0, w: 8, h: 5 });
    await act(async () => root.unmount());
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui/host && bun run test src/dashboard.test.tsx`
Expected: FAIL, the button list still contains "Refresh" and "Dashboards".

- [ ] **Step 3: Rewrite `dashboard.tsx`**

```tsx
import { Alert, Box, Button, Center, Flex, Group, Loader, Menu, Select, Stack, Text, TextInput, Title, useComputedColorScheme } from "@mantine/core";
import { ArrowUpRight, CaretDown, Plus, WarningCircle } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Responsive, WidthProvider } from "react-grid-layout/legacy";
import { dashboardsQueryKey, getJSON, type DashboardRecord, type DashboardState, type DashboardSummary } from "./api";
import type { Overview } from "../../contracts";
import { compactDashboardLayout, nextDashboardSlot, widgetDefaults, widgetTypes, type DashboardLayoutItem, type WidgetType } from "./dashboard-layout";
import { createID } from "./id";
import { dashboardWindows, useLastUpdated, useObservability, widgetParams, type WidgetConfig } from "./widgets/data";
import WidgetCard, { widgetTitles, type Widget } from "./widgets/widget-card";

const Grid = WidthProvider(Responsive);
const dashboardKey = "fanout.dashboard-id";
const emptyState: DashboardState = { layout: [], widgets: [], filters: { window: "1h", namespace: "" } };

export default function Dashboard({ dashboardID = "", agentAvailable, onOpenChat, onDashboardChange }: { dashboardID?: string; agentAvailable: boolean; onOpenChat: (prompt?: string) => void; onDashboardChange?: (id: string, replace?: boolean) => void }) {
  const queryClient = useQueryClient();
  const dark = useComputedColorScheme("light") === "dark";
  const dashboards = useQuery({ queryKey: dashboardsQueryKey, queryFn: () => getJSON<{ dashboards: DashboardSummary[] }>("/api/dashboards"), refetchInterval: 30_000 });
  const [selectedID, setSelectedID] = useState(() => dashboardID || localStorage.getItem(dashboardKey) || "");
  const save = useMutation({
    mutationFn: async (next: DashboardState) => {
      if (!selected.data) throw new Error("No dashboard selected");
      const response = await fetch(`/api/dashboards/${encodeURIComponent(selected.data.id)}`, { method: "PUT", credentials: "same-origin", headers: { "content-type": "application/json", "Fanout-Request": "1" }, body: JSON.stringify({ name: selected.data.name, description: selected.data.description, state: next }) });
      if (!response.ok) throw new Error("Unable to save dashboard");
      return response.json() as Promise<DashboardRecord>;
    },
    scope: { id: `dashboard-${selectedID}` },
    onSuccess: (data) => { queryClient.setQueryData(["dashboard", data.id], data); void queryClient.invalidateQueries({ queryKey: dashboardsQueryKey }); },
    onError: (cause) => console.error("Dashboard save failed", cause),
  });
  // Pause polling while a save is in flight or failing so the refetch cannot clobber unsaved local edits.
  const selected = useQuery({ queryKey: ["dashboard", selectedID], queryFn: () => getJSON<DashboardRecord>(`/api/dashboards/${encodeURIComponent(selectedID)}`), enabled: Boolean(selectedID), refetchInterval: save.isPending || save.isError ? false : 30_000 });
  const [state, setState] = useState<DashboardState>(emptyState);
  const [breakpoint, setBreakpoint] = useState("lg");
  const overview = useObservability<Overview>("overview", widgetParams(state.filters), Boolean(selected.data));
  const services = useMemo(() => (overview.data?.data.services ?? []).map((service) => service.service), [overview.data]);
  const updatedAt = useLastUpdated();

  useEffect(() => {
    if (!dashboardID || dashboardID === selectedID) return;
    setSelectedID(dashboardID);
    localStorage.setItem(dashboardKey, dashboardID);
  }, [dashboardID, selectedID]);
  useEffect(() => {
    const items = dashboards.data?.dashboards;
    if (!items?.length) return;
    if (dashboardID && items.some((item) => item.id === dashboardID)) return;
    if (!dashboardID && selectedID && items.some((item) => item.id === selectedID)) { onDashboardChange?.(selectedID, true); return; }
    const next = items.find((item) => item.is_default) ?? items[0];
    choose(next.id, true);
  }, [dashboardID, dashboards.data, selectedID]);
  useEffect(() => {
    if (save.isPending || save.isError) return;
    if (selected.data?.state) setState(selected.data.state);
  }, [selected.data?.updated_at, save.isPending, save.isError]);
  // A failed save must not follow the user to another dashboard: reset the
  // mutation on switch so state sync and polling resume for the new
  // selection, and a retry can never write the previous dashboard's layout
  // into the newly selected one.
  useEffect(() => { save.reset(); }, [selectedID]);

  const layouts = useMemo(() => {
    const widgetType = new Map(state.widgets.map((widget) => [widget.id, widget.type as WidgetType]));
    const normalized: DashboardLayoutItem[] = state.layout.map((item) => {
      const size = widgetDefaults[widgetType.get(item.i) ?? "overview"];
      return { ...item, h: Math.max(item.h, size.minH), minW: Math.max(item.minW ?? 0, size.minW), minH: Math.max(item.minH ?? 0, size.minH) };
    });
    return { lg: normalized, md: normalized, sm: compactDashboardLayout(normalized, 6), xs: compactDashboardLayout(normalized, 2), xxs: compactDashboardLayout(normalized, 1) };
  }, [state.layout, state.widgets]);

  function choose(id: string, replace = false) { setSelectedID(id); localStorage.setItem(dashboardKey, id); onDashboardChange?.(id, replace); }
  function update(next: DashboardState) { setState(next); save.mutate(next); }
  function add(type: WidgetType) {
    const id = createID();
    const size = widgetDefaults[type];
    const slot = nextDashboardSlot(state.layout, size.w, size.h);
    update({ ...state, widgets: [...state.widgets, { id, type, title: widgetTitles[type], enabled: true }], layout: [...state.layout, { i: id, x: slot.x, y: slot.y, w: size.w, h: size.h, minW: size.minW, minH: size.minH }] });
  }
  function remove(id: string) { update({ ...state, widgets: state.widgets.filter((widget) => widget.id !== id), layout: state.layout.filter((item) => item.i !== id) }); }
  function configure(id: string, config: WidgetConfig) { update({ ...state, widgets: state.widgets.map((widget) => (widget.id === id ? { ...widget, config } : widget)) }); }

  if (dashboards.isLoading || (selectedID && selected.isLoading)) return <LoadingState label="Loading your dashboard…" />;
  if (dashboards.isError || selected.isError) return <LoadingState label="Your dashboard is unavailable. Try refreshing." />;
  const item = selected.data;
  if (!item) return <LoadingState label="Preparing your dashboard…" />;

  return <Box component="main" maw={1440} mx="auto" px={{ base: "md", sm: "xl" }} pt={{ base: "lg", sm: "xl" }} pb="xl">
    <Stack gap="md" mb="lg">
      <Box>
        <Title order={1} fz={32} lts="-0.03em">{item.name}</Title>
        <Text c="dimmed" mt={2}>{item.description || "A focused view of the signals that matter now."}</Text>
      </Box>
      <Flex align={{ base: "stretch", md: "center" }} justify="space-between" direction={{ base: "column", md: "row" }} gap="sm" role="group" aria-label="Dashboard controls">
        <Group gap="sm" wrap="wrap">
          <Select aria-label="Window" value={state.filters.window} onChange={(window) => window && update({ ...state, filters: { ...state.filters, window } })} data={dashboardWindows} w={{ base: "100%", xs: 150 }} size="sm" />
          <TextInput aria-label="Namespace" value={state.filters.namespace} onChange={(event) => setState({ ...state, filters: { ...state.filters, namespace: event.currentTarget.value } })} onBlur={(event) => update({ ...state, filters: { ...state.filters, namespace: event.currentTarget.value } })} placeholder="All namespaces" w={{ base: "100%", xs: 200 }} size="sm" />
        </Group>
        <Group gap="sm" wrap="nowrap" justify="flex-end">
          {updatedAt && <Text c="dimmed" size="xs">Updated {new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(updatedAt))}</Text>}
          <Menu shadow="md" position="bottom-end" withinPortal>
            <Menu.Target><Button variant="default" size="sm" leftSection={<Plus size={15} weight="bold" />} rightSection={<CaretDown size={13} weight="bold" />}>Add view</Button></Menu.Target>
            <Menu.Dropdown>{widgetTypes.filter((type) => agentAvailable || type !== "assistant").map((type) => <Menu.Item key={type} onClick={() => add(type)}>{widgetTitles[type]}</Menu.Item>)}</Menu.Dropdown>
          </Menu>
          {agentAvailable && <Button variant="subtle" color="gray" size="sm" rightSection={<ArrowUpRight size={15} weight="bold" />} onClick={() => onOpenChat()}>Ask Fanout</Button>}
        </Group>
      </Flex>
    </Stack>

    {save.isError && <Alert color="bad" radius="lg" mb="lg" icon={<WarningCircle size={18} weight="fill" />} title="Dashboard changes not saved">
      <Group justify="space-between" gap="sm">
        <Text size="sm">Your latest edits are kept on this screen but Fanout could not store them.</Text>
        <Button size="compact-sm" color="bad" variant="light" onClick={() => save.mutate(state)}>Retry save</Button>
      </Group>
    </Alert>}

    <Grid className="dashboard-grid" layouts={layouts} breakpoints={{ lg: 1100, md: 800, sm: 600, xs: 420, xxs: 0 }} cols={{ lg: 12, md: 10, sm: 6, xs: 2, xxs: 1 }} rowHeight={76} margin={[16, 16]} containerPadding={[0, 0]} compactType="vertical" draggableHandle=".widget-drag" draggableCancel="button,input,select,textarea,a,label,[role=menu],[role=dialog]" onBreakpointChange={setBreakpoint} onDragStop={(layout: readonly DashboardLayoutItem[]) => { if (breakpoint === "lg") update({ ...state, layout: [...layout] }); }} onResizeStop={(layout: readonly DashboardLayoutItem[]) => { if (breakpoint === "lg") update({ ...state, layout: [...layout] }); }}>
      {state.widgets.map((widget) => <div key={widget.id}>
        <WidgetCard widget={widget as Widget} filters={state.filters} dark={dark} services={services} agentAvailable={agentAvailable} onOpenChat={onOpenChat} onRemove={() => remove(widget.id)} onConfigure={(config) => configure(widget.id, config)} />
      </div>)}
    </Grid>
  </Box>;
}

function LoadingState({ label }: { label: string }) {
  return <Center mih="50vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">{label}</Text></Center>;
}
```

The `save` mutation uses `fetch` directly with the `Fanout-Request` header so the test can stub `fetch`; it matches what `authorizedFetch` sends for a same-origin call. If you prefer, keep `authorizedFetch` and stub `fetch` the same way (the auth module calls global `fetch`); both work, keep one.

- [ ] **Step 4: Run the tests, then the whole suite and lint**

Run: `cd ui/host && bun run test src/dashboard.test.tsx && bun run test && bun run lint`
Expected: PASS; suite green; lint clean.

- [ ] **Step 5: Check by hand**

`just ui-host`, restart the local instance, open the default dashboard, add every view type, drag one by its header, resize one, open Configure on Logs and set severity ERROR, remove one through the menu, switch the window to 6 hours. Confirm: no Refresh button, no Saved pill, an "Updated 11:16" line, cards match their content height, the red X is gone, "994.6ms" reads lower-case, the topology graph and performance chart render in both colour schemes, at 390px every card stacks full width. Then `git checkout -- internal/ui/dist`.

- [ ] **Step 6: Commit**

```bash
git add ui/host/src/dashboard.tsx ui/host/src/dashboard.test.tsx ui/host/src/index.css
git commit -m "feat(ui): dashboard page with native widgets and a quieter header

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

## Phase E: sign-in and setup

### Task 12: Six-digit code entry, resend, change email, smaller headings

**Files:**
- Modify: `ui/host/src/auth.tsx`
- Test: `ui/host/src/auth.test.tsx`

**Interfaces:**
- No new exports. The code step submits `{ email, code }` to `/api/auth/verify` exactly as today; resend posts `{ email }` to `/api/auth/start` again.

- [ ] **Step 1: Write the failing tests**

Append to `auth.test.tsx` inside the `describe` block:

```tsx
  it("verifies automatically after six digits and offers resend and change email", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "setInterval", "clearTimeout", "clearInterval", "Date"] });
    window.happyDOM.setURL("https://fanout.example.com/");
    const posts: Array<{ path: string; body: unknown }> = [];
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === "/api/auth/status") return json({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: false });
      if (path === "/api/auth/me") return json({ message: "not authenticated" }, 401);
      if (init?.method === "POST") {
        posts.push({ path, body: JSON.parse(String(init.body)) });
        if (path === "/api/auth/start") return json({ code_sent: true });
        if (path === "/api/auth/verify") return json({ status: "authenticated" });
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><AuthGate><div>Fanout application</div></AuthGate></MantineProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Sign in"));
    expect(document.body.textContent).not.toContain("Sign in to investigate");

    const email = document.querySelector('input[type="email"]') as HTMLInputElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(email, "v@example.com");
      email.dispatchEvent(new InputEvent("input", { bubbles: true, data: "v@example.com", inputType: "insertText" }));
    });
    const send = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Send code"));
    await act(async () => send?.click());
    await vi.waitFor(() => expect(posts.map((post) => post.path)).toEqual(["/api/auth/start"]));
    expect(document.body.textContent).toContain("Sent to v@example.com");
    expect(document.body.textContent).toContain("Codes expire in 5 minutes");

    const cells = Array.from(document.querySelectorAll<HTMLInputElement>('[data-pin-input] input, input[inputmode="numeric"]'));
    expect(cells).toHaveLength(6);
    for (const [index, digit] of ["1", "2", "3", "4", "5", "6"].entries()) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(cells[index], digit);
        cells[index].dispatchEvent(new InputEvent("input", { bubbles: true, data: digit, inputType: "insertText" }));
      });
    }
    await vi.waitFor(() => expect(posts.at(-1)).toEqual({ path: "/api/auth/verify", body: { email: "v@example.com", code: "123456" } }));

    const resend = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Resend"));
    expect(resend?.disabled).toBe(true);
    expect(resend?.textContent).toMatch(/Resend in \d+s/);
    await act(async () => { vi.advanceTimersByTime(31_000); });
    await vi.waitFor(() => expect(Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Resend"))?.disabled).toBe(false));

    const change = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Change email"));
    await act(async () => change?.click());
    expect((document.querySelector('input[type="email"]') as HTMLInputElement).disabled).toBe(false);
    expect(document.querySelectorAll('input[inputmode="numeric"]')).toHaveLength(0);

    vi.useRealTimers();
    await act(async () => root.unmount());
  });
```

Also update the existing "shows login instead of redirecting" test: replace `toContain("Sign in to investigate")` with `toContain("Sign in")` and add `expect(document.body.textContent).not.toContain("Sign in to investigate")`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ui/host && bun run test src/auth.test.tsx`
Expected: FAIL, "Sent to v@example.com" not found.

- [ ] **Step 3: Implement the code step**

In `auth.tsx`:

Add `PinInput` to the `@mantine/core` import and `ArrowLeft` to the Phosphor import.

Add state next to `codeSent`:

```ts
  const [resendAt, setResendAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!codeSent) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [codeSent]);
  const resendWait = Math.max(0, Math.ceil((resendAt - now) / 1000));
```

Add helpers next to `submit`:

```ts
  async function sendCode() {
    setBusy(true);
    setError("");
    try {
      await jsonRequest("/api/auth/start", { email });
      setCodeSent(true);
      setCode("");
      setResendAt(Date.now() + 30_000);
      setNow(Date.now());
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
    } finally {
      setBusy(false);
    }
  }

  async function verifyCode(value: string) {
    setBusy(true);
    setError("");
    try {
      await jsonRequest("/api/auth/verify", { email, code: value });
      setViewer("user");
      void loadAccount();
    } catch (cause) {
      setCode("");
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }

  function changeEmail() {
    setCodeSent(false);
    setCode("");
    setError("");
  }
```

Change `submit` so the non-setup branches call these:

```ts
      } else if (!codeSent) {
        await sendCode();
        return;
      } else {
        await verifyCode(code);
        return;
      }
```

(`sendCode` and `verifyCode` manage `busy` themselves; `return` before the outer `finally` runs is fine because the outer `setBusy(false)` is harmless.)

Replace the heading block sizes: every `fz={{ base: 30, sm: 36 }}` becomes `fz={{ base: 28, sm: 32 }}`, including the setup-complete page. Change the sign-in heading text from `"Sign in to investigate"` to `"Sign in"` (both occurrences: the OIDC surface and the local form). Change the description for the code step to `codeSent ? "" : …` (the sent line moves under the input).

Replace the verification-code `TextInput` with:

```tsx
      {!status?.setup_required && codeSent && <Stack gap="xs">
        <Text size="sm" fw={600}>Verification code</Text>
        <PinInput length={6} type="number" oneTimeCode autoFocus value={code} onChange={setCode} onComplete={(value) => void verifyCode(value)} disabled={busy} error={Boolean(error)} size="md" radius="md" aria-label="Verification code" getInputProps={(index) => ({ "aria-label": `Digit ${index + 1}` })} />
        <Group justify="space-between" gap="xs">
          <Text c="dimmed" size="xs">Sent to {email}</Text>
          <Button variant="subtle" size="compact-xs" leftSection={<ArrowLeft size={12} weight="bold" />} onClick={changeEmail} disabled={busy}>Change email</Button>
        </Group>
        <Group justify="space-between" gap="xs">
          <Text c="dimmed" size="xs">Codes expire in 5 minutes</Text>
          <Button variant="subtle" size="compact-xs" onClick={() => void sendCode()} disabled={busy || resendWait > 0}>{resendWait > 0 ? `Resend in ${resendWait}s` : "Resend code"}</Button>
        </Group>
      </Stack>}
```

Hide the submit button while the code step is showing (the PinInput submits itself): wrap the `<Button type="submit" …>` in `{!(codeSent && !status?.setup_required) && (…)}`. The email `TextInput` keeps `disabled={codeSent}`.

If Mantine's `PinInput` rejects `getInputProps` in this version, drop that prop; the test selects cells by `inputmode="numeric"`, which `type="number"` sets. If the cells do not advance focus under happy-dom, set each cell's value in order as the test does; `PinInput` reads every cell on change and calls `onComplete` when all six are filled.

- [ ] **Step 4: Setup success copies the whole header**

On the setup-complete surface change the copy button to copy the header line and add a token-only link:

```tsx
      <Group grow align="stretch">
        <Button variant="light" radius="md" leftSection={copied ? <Check size={16} weight="bold" /> : <Copy size={16} />} onClick={() => void copyText(`${setupResult.ingest_header_name ?? "Authorization"}: Bearer ${setupResult.ingest_token}`)}>{copied ? "Copied" : "Copy header"}</Button>
        <Button radius="md" rightSection={<ArrowRight size={16} weight="bold" />} onClick={() => { setViewer("user"); void loadAccount(); setSetupResult(null); }}>Continue to Fanout</Button>
      </Group>
      <Button variant="subtle" size="compact-xs" onClick={() => void copyText(setupResult.ingest_token ?? "")}>Copy token only</Button>
```

with `copyIngestToken` generalised to:

```ts
  async function copyText(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      setError("Clipboard access failed. Select and copy the token manually.");
    }
  }
```

- [ ] **Step 5: Run the tests, lint, commit**

Run: `cd ui/host && bun run test src/auth.test.tsx && bun run lint`
Expected: PASS, eight tests; lint clean.

```bash
cd /Users/v/Projects/labstack/fanout
git add ui/host/src/auth.tsx ui/host/src/auth.test.tsx
git commit -m "feat(ui): six-digit code entry with resend and change email

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

## Phase F: naming sweep and the gate

### Task 13: Naming sweep and spec alignment

**Files:**
- Modify: any file the sweep finds under `ui/host/src` and `ui/apps/src`; `docs/superpowers/specs/2026-09-05-logged-in-ui-shell-design.md`

- [ ] **Step 1: Sweep for the retired words**

```bash
cd /Users/v/Projects/labstack/fanout
rg -n -i 'investigation|conversation history|guidance' ui/host/src ui/apps/src
```

Expected after the earlier tasks: no matches except inside the agent prompt strings that the product sends ("Investigate the X service…" is a verb in a prompt and stays). If `rg` reports a UI string, rename it to the Chat wording and re-run the relevant test file.

- [ ] **Step 2: Sweep for the removed chrome**

```bash
rg -n 'AppShell.Footer|ProductFooter|ChatHistoryDrawer|appMinimumHeights|tt="capitalize"|Refresh</Button>' ui/host/src
```

Expected: no matches.

- [ ] **Step 3: Align the spec with two implementation decisions**

In the spec, section 3 "Widgets: native ECharts", the Configure bullet reads `Configure is a `Popover` writing `widget.config`` and ends with `Saving happens on popover close.`. Change to: `Configure opens a small `Modal` writing `widget.config` … Saving happens on Save.` Commit:

```bash
git add docs/superpowers/specs/2026-09-05-logged-in-ui-shell-design.md
git commit -m "docs: align the shell spec with the configure modal

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

---

### Task 14: Rebuild embedded assets, run the gate, verify by hand

**Files:**
- Rebuild and commit: `internal/ui/dist/**`, `internal/mcp/apps/*.html`

- [ ] **Step 1: Rebuild both bundles and the binary**

```bash
cd /Users/v/Projects/labstack/fanout
just build
git status --short internal/ui/dist internal/mcp/apps | head
```

Expected: modified files under both embedded trees.

- [ ] **Step 2: Run the full gate**

```bash
just check
```

Expected: ends with `All checks passed`. `ui-check` passes because the embedded assets now match a fresh build; `ui-test` runs the whole Vitest suite; `ui-audit`, `lint`, `test` and the docs and site checks run as before. Fix anything that fails before moving on; do not skip a recipe.

- [ ] **Step 3: Manual pass on the seeded local instance**

Follow the memory note "local UI screenshot loop": fresh data dir, `FANOUT_AI_API_KEY` from `.env`, setup page, then seed sixty minutes of traffic with the protobuf seeder. Walk this list at 1440×900 and 390×844, light and dark, and fix what fails:

1. Setup: "Create the first admin" at 28/32px; success page copies the header line; "Copy token only" copies the bare token.
2. Sign-in (a second private window, sign out first): "Sign in", six cells, auto-verify, "Change email" returns to the email step, "Resend in 30s" counts down.
3. Shell: rail lists Chats and Dashboards; New chat; search filters both; Cmd-K focuses search; avatar menu shows the email and signs out; no footer; no Live dot; 390px burger opens the drawer and nothing clips.
4. Chat: empty state with four chips; send a chip; activity line names the tool; Stop button works mid-run; embedded views in Plex, sized to content, refresh icon works; copy on hover; a markdown table scrolls inside its box at 390px; a code block has a copy button; reload the thread URL and it restores; open a deleted thread URL and it explains itself.
5. Dashboard: header without Refresh or Saved; "Updated" time; every widget type added lands in a free slot; drag by header; resize; Configure on Logs and Trace; Remove via the menu; window and namespace filters change the data; "994.6ms" lower-case; charts in both schemes.
6. Console: no font errors, no 404 on new chat.

- [ ] **Step 4: Commit the embedded assets**

```bash
git add internal/ui/dist internal/mcp/apps
git commit -m "chore(ui): rebuild embedded assets for the shell rebuild

Claude-Session: https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N"
```

- [ ] **Step 5: Hand off**

Use the superpowers:finishing-a-development-branch skill to decide between merging and opening a pull request. The pull request description, if one is opened, lists the spec path, the six areas above, and ends with `https://claude.ai/code/session_015K38gEgWmGkXJyyC8GiZ8N`.
