# Service Detail — Vision & Approach

## The Job

Service Detail answers: **why is this service unhealthy, and what's causing it?**

You click a service from Home, see its metrics, endpoints, errors, and dependencies — all deterministic, all from one `DiagnoseEnhanced()` call. No LLM tokens. The Investigate button is the escalation path after you've seen the basics.

## Principles

1. **One HTTP request, three internal queries** — the handler calls `DiagnoseEnhanced()`, `Spans(groupBy=["operation"])`, and a rollup-buckets query, then merges the results into one JSON response.
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

The handler calls three internal queries and merges results into a `ServiceDetailResult`:

1. `DiagnoseEnhanced()` — metrics, health, top errors, slow ops, dependencies, change points, baseline P95
2. `Spans(groupBy=["operation"])` — full endpoint list with per-operation P50/P95/error rate
3. `QueryRollupBuckets()` — per-minute error rate + P95 timeseries for charts

Returns a new `ServiceDetailResult` type that wraps all three. Cap window at 1440 like Home.

**Note:** `DiagnoseResult` fields currently lack `json:` struct tags (PascalCase by default). Either add snake_case tags to match Home's convention, or define `ServiceDetailResult` with explicit fields and tags instead of embedding.

## Page Layout (Vertical Stack)

Top to bottom, full-width:

### 1. Header

Service name, health badge, "started ~Xm ago" (from incident tracker if available), time range selector, Investigate button (primary, right-aligned).

Back link to Home (← Home).

### 2. Metric Cards

Four cards in a row: Error Rate, P95, P50, Traffic.

Each card shows the current value. Color the value by health thresholds (same as Home).

For P95: when `BaselineComparison.BaselineP95Ms` is available, show it as a secondary line: "baseline 120ms". Note: the baseline only covers P95 latency today — `queryBaseline()` does not compute a baseline for error rate. If error rate baseline is needed later, `queryBaseline()` must be extended to also query `AVG(error_rate)` from rollups.

On mobile, 2x2 grid.

### 3. Charts — Error Rate + P95 Latency

Two timeseries charts side by side (stack on mobile).

Data source: `QueryRollupBuckets(service, start, end)` — per-minute P50, P95, error rate, and span count. This is a separate query from `DiagnoseEnhanced()` because the diagnose call uses rollup buckets internally for change-point detection but discards them — only the change points are returned. The handler must call `QueryRollupBuckets()` directly and include the buckets in `ServiceDetailResult`.

When change points are detected (from `DiagnoseEnhanced().ChangePoints`), render them as vertical dashed blue lines on the chart with a label.

When baseline P95 is available, render it as a faint horizontal dashed line on the latency chart.

### 4. Top Endpoints

Full-width table. Columns: Operation, Rate (spans/min), Errors (%), P50, P95, P99.

Data source: `Spans(service, groupBy=["operation"])` — returns all operations with span count, error rate, P50, P95, P99, and exemplar trace IDs.

`DiagnoseEnhanced().SlowOps` is redundant here since the span group-by gives the full endpoint list. The handler uses `Spans()` for this section, not `SlowOps`.

Sorted by error rate descending (worst first). Color error rate and P95 by thresholds.

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

Three internal queries, one HTTP response:
1. `DiagnoseEnhanced()` — metrics, health, top errors, slow ops, deps, change points, baseline P95
2. `Spans(service, groupBy=["operation"])` — full endpoint list with per-operation metrics
3. `QueryRollupBuckets(service, start, end)` — per-minute timeseries for charts

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

1. **New response type** — `ServiceDetailResult` in `internal/service/types.go` with explicit `json:` snake_case tags. Wraps diagnose data, endpoints, and rollup buckets.
2. **New service method** — `ServiceDetail()` in `internal/service/service_detail.go`. Calls `DiagnoseEnhanced()` + `Spans(groupBy)` + `QueryRollupBuckets()` and assembles the result.
3. **New handler** — `GET /api/service/:name` in `internal/api/ui.go`. Thin wrapper around `ServiceDetail()`.
4. **Add JSON tags** — to `DiagnoseResult` fields in `types.go` for consistent snake_case serialization.
5. **Route registration** — add to `RegisterUIRoutes`.

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
