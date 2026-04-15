# Service Detail — Vision & Approach

## The Job

Service Detail answers: **why is this service unhealthy, and what's causing it?**

You click a service from Home, see its metrics, endpoints, errors, and dependencies — all deterministic, all from one `DiagnoseEnhanced()` call. No LLM tokens. The Investigate button is the escalation path after you've seen the basics.

## Principles

1. **One backend call** — `DiagnoseEnhanced()` already returns everything. No new queries.
2. **Vertical stack** — full-width sections top to bottom. Charts get room to breathe. Scrolls naturally on mobile.
3. **Bookmarkable** — `/service/:name` URL. Paste it in Slack during an incident.
4. **Deterministic** — same data, same page, every time. AI only behind Investigate.

## Route

```
GET /service/:name
```

React Router handles this client-side. The page fetches `GET /api/service/:name?window=60` on mount.

## API

One endpoint:

```
GET /api/service/:name?window=60&namespace=
```

Calls `service.DiagnoseEnhanced()` and returns the result directly. The existing `DiagnoseResult` and `DiagnoseEnhancedResult` types already have everything needed — no new response types required.

The handler is thin: parse params, call `DiagnoseEnhanced()`, return JSON. Cap window at 1440 like Home.

## Page Layout (Vertical Stack)

Top to bottom, full-width:

### 1. Header

Service name, health badge, "started ~Xm ago" (from incident tracker if available), time range selector, Investigate button (primary, right-aligned).

Back link to Home (← Home).

### 2. Metric Cards

Four cards in a row: Error Rate, P95, P50, Traffic.

Each card shows the current value. When `DiagnoseEnhanced()` returns baseline data, show it as a secondary line: "baseline 0.3%". Color the value by health thresholds (same as Home).

On mobile, 2x2 grid.

### 3. Charts — Error Rate + P95 Latency

Two timeseries charts side by side (stack on mobile).

Data source: `DiagnoseEnhanced().Buckets` — per-minute rollup data returned by the diagnose call.

When change points are detected, render them as vertical dashed blue lines on the chart with a label.

When baseline is available, render it as a faint horizontal dashed line.

### 4. Top Endpoints

Full-width table. Columns: Operation, Rate (spans/min), Errors (%), P50, P95, P99.

Data source: `DiagnoseEnhanced().SlowOps` for operations with latency data, supplemented by a span group-by query for the full endpoint list.

Sorted by error rate descending (worst first). Color error rate and P95 by thresholds.

Since `DiagnoseEnhanced()` only returns slow ops (P95 > 100ms), we need to also call `Spans()` with `GroupBy=["operation"]` for the full endpoint list. This is one additional query beyond DiagnoseEnhanced.

### 5. Top Errors

Count-first layout (same as Home incident cards). Shows error message, count, and example trace IDs as clickable links.

Data source: `DiagnoseEnhanced().TopErrors` — already grouped by operation + exception type with example trace IDs.

Clicking a trace ID opens the AI chat with: "Show me trace {traceID} for {service}."

### 6. Dependencies

Downstream services with call rate, average latency, and error rate.

Data source: `DiagnoseEnhanced().Dependencies` — top 10 downstream services.

Each dependency row is clickable → navigates to that service's detail page.

### 7. Change Points (if detected)

Timeline of detected metric shifts with timestamps and magnitude.

Data source: `DiagnoseEnhanced().ChangePoints`.

Only shown when change points exist. Hidden when the service is stable.

## Data Flow

```
Browser                    Server
  │                          │
  │  GET /api/service/:name  │
  │ ──────────────────────→  │
  │                          │  DiagnoseEnhanced(service, window)
  │                          │  + Spans(service, groupBy=["operation"])
  │                          │
  │  ← JSON response ──────  │
  │                          │
  │  Render page             │
```

Two backend queries total:
1. `DiagnoseEnhanced()` — metrics, errors, slow ops, deps, change points, baseline
2. `Spans(groupBy=["operation"])` — full endpoint list (DiagnoseEnhanced only returns slow ops)

## Frontend Components

| Component | Responsibility |
|-----------|---------------|
| `ServicePage.tsx` | Page shell: fetch data, manage time window, layout sections |
| `ServiceHeader.tsx` | Name, badge, started time, back link, Investigate button |
| `MetricCards.tsx` | 4 metric cards with baseline comparison |
| `ServiceChart.tsx` | Timeseries chart (reusable for error rate + latency) |
| `EndpointTable.tsx` | Full-width endpoint table with sortable columns |
| `ErrorList.tsx` | Top errors with counts and trace links |
| `DependencyList.tsx` | Downstream services, clickable to their detail page |
| `ChangePoints.tsx` | Timeline of detected metric shifts |

Reuse from Home: `Sparkline.tsx` (for inline sparklines if needed).

## Backend Changes

Minimal:

1. **New handler** — `GET /api/service/:name` in `internal/api/ui.go`. Calls `DiagnoseEnhanced()` + `Spans(groupBy)`. Returns combined JSON.
2. **New response type** — `ServiceDetailResult` wrapping diagnose + endpoints data in one response.
3. **Route registration** — add to `RegisterUIRoutes`.

No new service layer methods. No new DuckDB queries.

## Navigation

- **Home → Service Detail**: Click service row or incident card service name
- **Service Detail → Home**: ← Home link in header
- **Service Detail → Investigate**: Investigate button opens `/chat?q=Investigate {service}...`
- **Service Detail → Service Detail**: Click a dependency → navigate to that service's detail page
- **Service Detail → Trace**: Click trace ID → opens chat with trace query

## Refresh & Interaction

- **Auto-refresh**: every 30 seconds (same as Home)
- **Time range selector**: 15m, 1h, 6h, 24h (same as Home)
- **Click endpoint row**: opens chat with "Investigate the {operation} endpoint on {service}"
- **Click dependency**: navigates to `/service/:dep-name`
- **Click trace ID**: opens chat with "Show me trace {traceID}"

## What Service Detail Is Not

- **Not a metrics explorer** — no custom queries, no arbitrary chart building
- **Not a trace viewer** — trace IDs link to the AI chat for full trace rendering
- **Not editable** — no alert creation, no configuration, just read-only diagnostics
- **Not AI-generated** — all data from deterministic queries

## Build Scope

Everything above ships as one unit. No v1/v2 split — the page is straightforward since the data layer already exists.
