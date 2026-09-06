# Logged-in UI: shell rebuild, chat pane, native dashboard widgets, sign-in code step

Date: 2026-09-05
Status: approved design, pending implementation plan
Scope: `ui/host` (React host app), `ui/apps` (embedded MCP views, small changes), `ui/` shared modules. No Go changes are expected; the server widget allowlist and routes stay as they are.

## Why

An audit of the logged-in product on 2026-09-05 (local build, seeded traffic, desktop and 390px, light and dark) found that the shell hides navigation behind three separate menus, the chat page is a marketing hero with a fixed composer, dashboard widgets are table walls with 40 to 60 percent empty card space, and the sign-in code step is a plain text field. Several defects were found along the way: embedded views render in the system font because the iframe CSP blocks inlined fonts; a metric prints "994.6 Ms"; every new chat fires a 404; the header clips at 390px; the "Live" dot is hard-coded.

The user chose to rebuild the shell with a persistent rail and to give dashboard widgets native charts.

## 1. Shell

### Layout

```
┌──────────────────────────────────────────────────────────────┐
│ ▣ FANOUT                                   ☾  (avatar ▾)     │  header 52px
├────────────┬─────────────────────────────────────────────────┤
│ + New chat │                                                 │
│ CHATS      │                                                 │
│ ● payments │            <Outlet>                             │
│   latency… │   chat pane  |  dashboard page                  │
│   See all  │                                                 │
│ DASHBOARDS │                                                 │
│ ● System … │                                                 │
│ + Create   │                                                 │
│ ⌕ Search ⌘K│                                                 │
└────────────┴─────────────────────────────────────────────────┘
```

- Mantine `AppShell` with `header` (52px) and `navbar` (256px). No `footer`.
- Navbar breakpoint `md`: below it the rail is hidden and a hamburger `ActionIcon` in the header opens the same rail content in a `Drawer`. There is no separate icon-only rail state.
- The rail is the only navigation surface. The header keeps the brand lockup, the colour-scheme toggle, and an avatar `Menu`. The Dashboard/Chat toggle button, the history icon, the plus icon, the "Live" indicator and the footer are removed.
- Avatar menu items: the signed-in email (disabled label), "Fanout on GitHub", "LabStack", divider, "Sign out". The copyright line goes away.
- Header at 390px shows: hamburger, brand, colour-scheme toggle, avatar. Nothing else, so nothing clips.

### Rail contents

- "New chat" primary button at the top. Hidden when the agent is unavailable; the Chats section is then omitted entirely and the rail shows Dashboards only.
- **Chats**: the thread list from `GET /api/agent/threads`, grouped Today / Yesterday / Previous 7 days / Older (reuse `groupThreads` and `threadTime` from `chat-history.tsx`). Each row: title, relative time, hover-only actions menu with Rename and Delete (reuse the existing modals). Active thread highlighted with `aria-current="page"`. The first page (30) renders inline; "See all" appends further pages with the existing `useInfiniteQuery`.
- **Dashboards**: the list from `GET /api/dashboards`. Each row: name, a small "Default" badge when `is_default`. Active dashboard highlighted. "Create with AI" at the bottom of the section opens a new chat with the existing prompt. Hidden when the agent is unavailable.
- **Search** field at the bottom of the rail. Typing filters both sections client-side; for Chats it also drives the server `q` parameter with the existing 250ms debounce. Cmd-K / Ctrl-K focuses it. "/" still focuses the chat composer.
- `ChatHistoryDrawer` is deleted. Its query key, grouping helpers and modals move into a new `rail.tsx` (or stay in a renamed `threads.tsx` module that the rail imports).

### Naming

One name for the feature: **Chat / Chats**. "Investigations", "Conversation history" and "Guidance" are removed from the UI. Tooltips and aria-labels follow the same word.

### Routing

URLs are unchanged: `/chat`, `/chat/$threadId`, `/dashboards`, `/dashboards/$dashboardId`, and the `/` redirect logic in `routes/index.tsx`. `notFoundComponent` still redirects to `/`.

## 2. Chat pane

### Layout

- `AppShell.Main` for chat routes is a flex column filling the viewport height under the header: a scrolling message list and a composer that is `position: sticky; bottom: 0` inside the pane. No `position: fixed`, no reserved bottom padding beyond the composer's own height plus 16px.
- Message column max width 880px, centred, horizontal padding 16px at base and 32px from `sm`.

### Empty state

- Brand mark (icon only; the header already carries the wordmark), one heading in the mono display face at 24px: "What do you want to know about your system?", and four suggestion chips (`Button variant="default" size="sm"`) that wrap: "Summarize system health for the last hour", "Find the source of elevated errors", "Map the current service dependencies", "Show the slowest endpoints". Clicking sends the text.
- No 56px headline, no "Your system, understood" eyebrow, no numbered cards.

### Messages

- No avatars, no uppercase role labels.
- User: right-aligned `Paper` with the brand-light background, radius `lg`, max width 70%.
- Assistant: markdown (`react-markdown` + `remark-gfm`) left-aligned, full column width, inside `Typography`.
- Hovering a message reveals a dimmed timestamp and, on assistant messages, a Copy button (`navigator.clipboard`). Message timestamps come from the AG-UI message when present; otherwise the run's finalisation time is used, held in component state keyed by message id.
- Tool messages (`role === "tool"`) stay hidden.

### Running state

- While `running`, the composer's send button becomes a Stop button that calls `agent.abortRun()` and sets `running` to false.
- Above the loader, an activity line shows what the agent is doing, driven by the `onToolCallStartEvent` and `onToolCallEndEvent` callbacks of the `HttpAgent.subscribe` subscriber (present in the installed `@ag-ui/client` 0.0.59). The tool name maps through the existing `toolTitle` table to text such as "Checking system health…". The line clears when the run finalises.

### Embedded views (MCP apps)

- `mcpAppCSP` always emits `font-src 'self' data:` (and keeps appending declared resource domains). This is the fix for every embedded view rendering in the system font and logging eight font errors per view.
- `appMinimumHeights` is removed; the frame's floor becomes 240px and the height follows the app's `sizechange` reports. The `useApp` hook in `@modelcontextprotocol/ext-apps` reports size changes by default (`autoResize`), which the audit confirmed in the installed package.
- The `ViewHeader` in `ui/apps/src/components.tsx` slims to: title (`Title order={2}` at `lg`), summary text, and a refresh `ActionIcon` with a tooltip. The uppercase eyebrow is dropped. Vertical padding halves.
- The frame `Paper` loses its `shadow="md"`; it keeps `radius="lg"` and a 1px border so it reads as part of the message column rather than a floating card.

### Threads

- `/chat` renders a draft pane. No thread fetch happens until the first message is sent; the thread id is minted client-side (existing `createID`) and the route updates to `/chat/$threadId` on first send. The id of a thread created this way is remembered in component state so the route change to `/chat/` does not trigger a fetch for a thread the server has not persisted yet. This removes the 404 per new chat.
- `/chat/$threadId` fetches the thread. A 404 on that fetch means the thread was deleted or never existed: show an inline alert "This chat no longer exists" with a "New chat" button.
- Run failures show an inline alert with "Retry", which re-sends the last user message.

## 3. Dashboard

### Page header

- Title (`Title order={1}`, 32px mono), description on the next line.
- One control row under it: window `Select` (same six values), namespace `TextInput`, "Add view" `Menu` button, "Ask Fanout" `Button variant="subtle"` (agent only). No surrounding `Paper`.
- No "Refresh" button. Widget data refetches every 30s (`refetchInterval: 30_000`; failed queries keep the existing 15s retry). A dimmed "Updated 11:16" line sits at the right of the control row and uses the newest `dataUpdatedAt` across the widget queries.
- No "Saved" pill. The save mutation is silent on success; on failure the existing "Dashboard changes not saved" alert with Retry remains.
- Dashboard switching lives in the rail. The "Dashboards" dropdown above the title is removed. The dashboard list poll drops from 3s to 30s; the selected dashboard poll drops from 3s to 30s and still pauses during a pending or failed save.
- "Create with AI" moves to the rail (section 1).

### Widgets: native ECharts

- `echarts` (same version as `ui/apps`, 6.1.0) is added to `ui/host`.
- `ui/apps/src/format.ts` moves to `ui/format.ts`, and the chart helpers `chartTheme`, `statusHex`, `seriesColor`, `healthColor` move from `ui/apps/src/components.tsx` to `ui/chart.ts`. Both bundles import those from `ui/`; they have no bare imports, which matters because `ui/` has no `node_modules` and a bare `echarts` import from there would not resolve. The 22-line `EChart` component stays per bundle: `ui/apps/src/echart.tsx` as today and a copy at `ui/host/src/echart.tsx`. It keeps its `ResizeObserver` so charts follow grid resizes.
- Widget bodies (`ui/host/src/widgets/*.tsx`, one file per type):
  - **overview**: health tile (word plus coloured dot), error-rate tile with a sparkline from `performance.points`, the healthy/degraded/unhealthy `Progress` bar with a legend, and a services-count tile. Data: `/api/observability/overview` and `/api/observability/performance`.
  - **topology**: ECharts force graph (nodes coloured by health, edges weighted by calls, red when `error_rate >= 0.05`), with "N services · M routes" under it. Clicking a node calls `onOpenChat("Investigate the <service> service. Explain its errors and latency.")`.
  - **activity**: table of services with health badge, p95 and error rate, sorted unhealthy first. Empty state stays.
  - **performance**: line chart of operations and p95 over `points`, then the top three endpoints (method badge, path, calls, p95).
  - **trace**: the four tiles (duration, spans, services, status) and a mini waterfall of the six longest spans (bars on a shared timeline, error spans in `bad`). Trace id shown short (`shortID`) with a copy button. The `tt="capitalize"` on `Metric` is removed; units are formatted with `duration()`.
  - **logs**: stacked severity histogram from `buckets`, then the last five entries with time, severity badge, service and body.
  - **assistant**: three suggested questions as buttons, each already scoped to the dashboard's window ("Summarize the last 6 hours", "What changed in the last 6 hours?", "Which service needs attention right now?"). Card default width 4 columns.
- Card chrome: title only (the uppercase type eyebrow is removed). The header is the drag handle. Actions live in a hover-revealed `Menu` (`ActionIcon` with three dots): Configure, Remove. Remove saves immediately, as today; it is no longer a bare red X. `draggableCancel` gains `[role=dialog]` so popovers do not start drags.
- Configure opens a small `Modal` writing `widget.config`: `service` (select fed by the overview's service list) for overview, performance and topology; `severity` (select) and `search` (text) for logs; `trace_id` (text) for trace. Saving happens on Save.
- Default sizes per type replace the current `wide ? 8 : 4` rule: overview 4x4, activity 4x5, assistant 4x3, topology 8x5, performance 8x5, logs 8x5, trace 8x4. `widgetMinimumRows` becomes per-type `{ w, h, minW, minH }`.
- Placement: `nextDashboardSlot(layout, width, columns)` in `dashboard-layout.ts` returns the first `(x, y)` where the widget fits on the last occupied row, falling back to a new row at `x = 0`. Existing `compactDashboardLayout` stays for the narrow breakpoints.
- Card padding 16px, `ScrollArea` only where a body can overflow (activity, logs, performance table).

## 4. Sign-in and setup

- Code step replaces the `TextInput` with Mantine `PinInput`: `length={6}`, `type="number"`, `oneTimeCode`, autofocus, `aria-label="Verification code"`. When six digits are entered the form submits itself. A failed verify clears the input, shows the server message in the existing `Alert`, and refocuses.
- Under the input: "Sent to <email> · Change email" (returns to the email step and clears the code), a "Resend code" `Button variant="subtle"` with a 30-second client cooldown that shows "Resend in 12s", and the hint "Codes expire in 5 minutes" (the server TTL in `internal/auth/code.go`). Server rate-limit responses from `/api/auth/start` are shown verbatim in the alert.
- Headings on every auth surface: 28px at base, 32px from `sm`. Copy: "Sign in" (no self-signup), "Sign in or create an account" (self-signup) stays but at the smaller size, "Create the first admin", "Save your ingest token".
- Setup success: the copy button copies the full header line (`Authorization: Bearer fo_…`) and the button label becomes "Copy header". A second, smaller "Copy token only" link copies the bare token.

## 5. Defects fixed as part of the work

| Defect | Where | Fix |
|---|---|---|
| Embedded views render in the system font, eight console errors per view | `ui/host/src/mcp-app-frame.tsx` `mcpAppCSP` | always emit `font-src 'self' data:` |
| "994.6 Ms" | `ui/host/src/dashboard.tsx` `Metric` | drop `tt="capitalize"`, format with `duration()` |
| 404 on every new chat | `ui/host/src/App.tsx` thread load effect | draft pane, fetch only existing threads |
| Header clips at 390px | `ui/host/src/App.tsx` header | header reduced to four controls, rail in drawer |
| Hard-coded "Live" dot | `ui/host/src/App.tsx` header | removed |
| Dashboard polls every 3s | `ui/host/src/dashboard.tsx` | 30s |

## 6. Testing and gates

- Vitest (`ui/host`, happy-dom):
  - rail renders Chats and Dashboards sections from mocked fetches, marks the active row, hides Chats and "New chat" when the agent is unavailable;
  - `/chat` draft pane makes no request to `/api/agent/threads/:id` until the first send;
  - `mcpAppCSP({})` output contains `font-src 'self' data:`;
  - `nextDashboardSlot` fills the free slot on the last row and starts a new row when full;
  - the code step submits automatically after six digits and "Change email" returns to the email step (extend `auth.test.tsx`);
  - existing `mcp-app-frame`, `auth-session`, `dashboard-layout` tests keep passing.
- `just check` is the gate. `just ui` rebuilds `internal/ui/dist` and `internal/mcp/apps` and the rebuilt assets are committed, because `ui-check` fails when the embedded assets are stale.
- Manual pass on the local seeded instance (see memory note "local UI screenshot loop") at 1440x900 and 390x844, light and dark: shell, empty chat, chat with two embedded views, dashboard with all seven widgets, sign-in code step, setup token screen.

## Out of scope

Settings page, alerts UI, dashboard editing on mobile (read-only there as today), new widget types (the server allowlist in `internal/dashboard/service.go` stays at seven), changes to the agent system prompt, and the marketing site.
