# Agent-built dashboards

Status: draft for review · 2026-10-04 · branch `feat/agent-dashboards`

## Goal

Fanout dashboards become the best way to look at a running system. An agent
builds a correct, professional, interactive dashboard from one sentence, on
real telemetry, in under a minute. An SRE who knows Grafana should prefer the
result to a board they would have built by hand, and should be able to keep
refining it by talking to it.

Grafana is the reference for breadth and interaction quality. Fanout goes past
it in four places that follow from its architecture:

1. **Authoring by intent.** The agent writes a typed, validated spec, previews
   every panel against live data, explains any empty panel, and edits by small
   patches. Nobody writes queries or picks chart settings by hand.
2. **One store, every signal.** Spans, logs and metrics live in the same
   DuckDB engine, so one panel can join them, and every chart point can drill
   to the exact traces and logs behind it.
3. **Context arrives on its own.** Deploys, derived from `service.version`
   changes, and anomalies, from the existing detector, are drawn on every time
   chart for the affected service without configuration.
4. **The same panel everywhere.** A panel renders identically on a dashboard,
   inline in a chat answer, and in an MCP client, from one spec and one
   compile layer.

## Success criteria

The goal is met when every item below holds on a local Fanout loaded with
fanout-demo telemetry (the OpenTelemetry Demo: frontend, checkout, cart,
payment, shipping, email, recommendation, fraud-detection, accounting, flagd,
kafka, load-generator). Each criterion names how it is measured.

| # | Criterion | Target | Measured by |
|---|---|---|---|
| S1 | Benchmark prompts produce a saved dashboard | 10 of 10 | agent eval harness |
| S2 | Panels that return rows, or carry an explained empty state, at save time | 100% | eval harness, `preview_panels` |
| S3 | Specs that fail validation after the agent's own correction loop | 0 | eval harness |
| S4 | Median time from prompt to saved dashboard, default model | ≤ 45 s | eval harness |
| S5 | Follow-up edits that change only the panels they name, over 5 consecutive edits | 100% | eval harness spec diff |
| S6 | First full render of a 12-panel, 24-hour dashboard | ≤ 1.5 s p95 | Playwright performance test |
| S7 | Panel query latency with a warm read cache | ≤ 500 ms p95 | Go benchmark on demo data |
| S8 | Requests per dashboard refresh | 1 batch request, plus 1 for annotations; variable options load once per range change | Playwright network log |
| S9 | Panel types | the 15 listed under Visualizations | type registry test |
| S10 | Contrast and color | WCAG AA in both themes; categorical palette passes the CVD validator | a11y test, `validate_palette.js` |
| S11 | Shared link reproduces the view | time, variables, comparison and focused panel in the URL | router test |
| S12 | Correctness rules from #232 | items 1, 9, 12, 14, 17, 24, 30 resolved | regression tests named per item |
| S13 | Parity with the capability preview | every in-scope preview capability shipped; the checkout-incident and payments-database boards rebuilt from their prompts | side-by-side screenshot review at milestone 4 |

The benchmark prompts, run against fanout-demo data:

1. Give me a health overview of the shop.
2. Why is checkout slow right now?
3. Show errors by service and the top error messages.
4. Frontend latency by route with p50, p95 and p99.
5. Compare this hour with the previous hour for every service.
6. Show the slowest PlaceOrder traces and what they logged.
7. Kafka throughput between checkout and its consumers.
8. Database and cache calls by service, with their latency.
9. Show recent deploys and whether they changed latency or errors.
10. Log volume by severity and service, with a stream of the errors.

## What exists today

- `internal/dashboard`: owner-scoped dashboards in SQLite, a fixed allowlist of
  seven widget types (`overview`, `topology`, `activity`, `assistant`,
  `performance`, `trace`, `logs`), a 12-column layout, one shared window and
  namespace, and an untyped `config` map the server never validates.
- `internal/mcp/dashboards.go`: `list_dashboards`, `get_dashboard`,
  `create_dashboard`, `replace_dashboard`. The agent composes widgets and
  computes grid coordinates itself.
- `internal/observability`: purpose-built reads (overview, topology,
  performance, trace, logs, dependencies) behind `/api/observability/*`.
- `internal/query/sql_boundary.go`: a read-only SQL path that parses with
  DuckDB's own parser, admits one SELECT over `spans`, `logs`, `metrics`,
  `service_rollup` and `edge_rollup`, and pins one Parquet snapshot. Nothing
  public reaches it.
- `ui/host`: React 19, Mantine 9, TanStack Router and Query, ECharts 6.1 drawn
  as SVG, `react-grid-layout` 2.2.4 through its legacy API. Widgets poll their
  own endpoints every 30 seconds.

The widget model cannot express a chart of an arbitrary measure, a variable,
a threshold, a drill-down, or a comparison. Issue #232 records the visible
consequences: stat tiles that read a partial bucket, generated widgets that are
permanently empty, state missing from the URL, mixed units and a scaled second
series in one chart, undownsampled 24-hour series, wasted card space and
duplicate fetches.

## Approaches considered

**A. Semantic panel spec, compiled by Fanout (chosen).** The agent states what
each panel means: a signal, filters, measures, grouping, unit, thresholds and
interactions. Fanout compiles the query to DuckDB SQL and the visualization to
ECharts options. SQL remains available as an escape hatch for data, never for
styling.

**B. The agent writes SQL and raw ECharts options.** Maximum flexibility, but
research on model-generated charts finds the same failures across Vega-Lite,
ECharts and Matplotlib: wrong axis types, misparsed dates, stacking of
non-additive values, misused color, and edits that land on the wrong property.
Raw options are long, cannot be validated semantically, and drift from the
design system.

**C. A general grammar such as Vega-Lite for charts plus SQL for data.** Models
know Vega-Lite well, but it adds a second renderer, its interaction model does
not cover linked crosshairs or drill-down, and theming would be duplicated.
Published evaluations show semantic specs are about 85% shorter with
localized edits, while the quality gain over native grammars is small and
shrinks with stronger models, so the deciding factors are length, edit
locality and control, which favor A.

## Architecture

```
agent / chat / MCP client                browser dashboard
        │                                         │
        ▼                                         ▼
 MCP tools ──────────────┐            /api/panels/query (batch)
 get_telemetry_schema    │            /api/annotations
 preview_panels          │            /api/variables/resolve
 create/replace/edit     │                        │
 _dashboard              ▼                        ▼
               internal/panel ─────────────────────────────
               spec types + validation (JSON Schema source)
               query compiler (structured → DuckDB SQL)
               SQL panel guard (reuses sql_boundary)
               executor: snapshot-bound, parameterized, bounded
               frames: columnar results with column roles and units
                             │
                             ▼
               internal/query (DuckDB, pinned Parquet snapshot,
               read caches, rollups) · internal/dashboard (SQLite:
               dashboards, versions)
```

### Units and boundaries

- **`internal/panel`** owns the panel spec: Go types, validation with
  path-addressed errors and suggestions, the query compiler, the SQL panel
  guard, variable binding, time bucketing and result frames. It depends on
  `internal/query` for execution and knows nothing about HTTP or MCP.
- **`internal/dashboard`** stores dashboard specs and their versions, applies
  edit operations, and lays out panels. It depends on `internal/panel` for
  validation only.
- **`internal/annotations`** derives deploy events from `service.version`
  first-seen times and exposes detector anomalies as time ranges.
- **`internal/api`** and **`internal/mcp`** are thin adapters over the above.
- **`ui/panels`** (new, pure TypeScript, sibling imports only, like the rest of
  `ui/`) compiles a panel spec plus a frame into chart options and table
  models. `ui/host` and `ui/apps` both render through it, so a chat view and a
  dashboard panel are identical.
- **`ui/host/src/dashboards`** replaces `dashboard.tsx` and `widgets/`: the
  page, time and variable controls, the grid, panel chrome, inspect, drill-down
  and version history.

## Dashboard spec, version 1

The spec is the contract between the agent, the API, storage and the renderer.
Go structs are its single source; the MCP SDK derives the JSON Schema the agent
sees from them.

```json
{
  "version": 1,
  "name": "Checkout latency",
  "description": "Latency, errors and suspects for checkout.",
  "time": { "range": "1h", "refresh": "30s", "compare": "previous_period" },
  "variables": [
    { "name": "service", "kind": "query", "from": "spans", "field": "service", "default": "checkout" },
    { "name": "route", "kind": "query", "from": "spans", "field": "http_route",
      "where": ["service = $service", "kind = 'SERVER'"], "include_all": true },
    { "name": "namespace", "kind": "constant", "value": "shop" }
  ],
  "annotations": { "deploys": true, "anomalies": true },
  "panels": [
    {
      "id": "latency",
      "title": "Latency for $service",
      "viz": "timeseries",
      "width": 8,
      "query": {
        "from": "spans",
        "where": ["service = $service", "kind = 'SERVER'", "http_route = $route"],
        "measures": ["p50(duration_ms)", "p95(duration_ms)", "p99(duration_ms)"],
        "bucket": "auto"
      },
      "unit": "ms",
      "thresholds": [{ "value": 1500, "status": "bad", "label": "p99 budget" }],
      "drill": "traces"
    }
  ]
}
```

### Top level

| Field | Rule |
|---|---|
| `version` | `1`. A format change bumps it; there is no reader for other versions. |
| `name` | 1–80 characters, unique per owner. |
| `description` | ≤ 280 characters. |
| `time.range` | `5m`, `15m`, `1h`, `3h`, `6h`, `12h`, `24h`, `2d`, `7d`, `30d`, clamped to retention. Or `time.from` and `time.to` as RFC 3339 instants. |
| `time.refresh` | `off`, `10s`, `30s`, `1m`, `5m`. Default `30s`. |
| `time.compare` | `previous_period` or absent. |
| `variables` | ≤ 12. See Variables. |
| `annotations` | `deploys` and `anomalies`, both default `true`. |
| `panels` | 1–40. |

### Panels

| Field | Rule |
|---|---|
| `id` | `[a-z][a-z0-9_]{0,39}`, unique in the dashboard, chosen by the agent and stable across edits. Edits address panels by `id`. |
| `title` | 1–80 characters; may reference variables. |
| `description` | ≤ 280 characters; shown as the panel's help text. |
| `viz` | One of the types under Visualizations. |
| `width` | 1–12 grid columns. Optional; each viz has a default. |
| `height` | `s`, `m` or `l`. Optional; each viz has a default. |
| `query` or `sql` | Exactly one, except for `text`, which has neither. |
| `min`, `max` | For `gauge`: the scale bounds, both required, `min` below `max`. |
| `unit` | See Units. Optional when the compiler can infer it from the measure. |
| `reduce` | For `stat` and `gauge`: `last`, `mean`, `min`, `max`, `sum`, `window`. Default `window`, which computes the measure over the whole time range rather than reading the last bucket. |
| `thresholds` | Up to 4 of `{ value, status, label? }` with `status` in `ok`, `warn`, `bad`. Direction comes from `better`. |
| `better` | `lower` or `higher`. Inferred for known measures (latency and errors are `lower`). |
| `options` | Viz-specific, closed per viz: `style` (`line`, `area`, `bars`, `stacked`), `scale` (`linear`, `log`), `top` (series limit, default 8, the rest become Other), `legend` (`auto`, `hidden`), `sort`. |
| `click` | `{ "set_variable": "<name>" }`: clicking a bar, row or series sets that variable to the clicked value. |
| `drill` | `traces` (exemplar traces for the clicked bucket or row) or `logs`. |
| `time` | `{ "range": "30d" }` or `{ "shift": "1d" }`, overriding the dashboard range for this panel. |
| `content` | Markdown for `text` panels, ≤ 4000 characters. |

### Structured query

| Field | Rule |
|---|---|
| `from` | `spans`, `logs`, `metrics`. |
| `where` | List of filter expressions joined by AND. Each is a SQL boolean expression over the signal's columns, attribute lookups and variables, checked by the guard below. |
| `measures` | 1–6 of `fn(field[, arg]) [as alias]`. Functions: `count()`, `rate()` (per second), `error_rate()`, `avg`, `min`, `max`, `sum`, `p50`, `p75`, `p90`, `p95`, `p99`, `quantile(field, q)`, `count_distinct(field)`, `share()` (percent of the total). For `metrics`: `value` aggregations plus `increase()` and `rate()` for cumulative sums, and `histogram_quantile(q)` for histogram points. |
| `by` | 0–3 dimensions: columns or `attributes['key']` / `resource['key']` with literal keys. |
| `bucket` | `auto`, an interval such as `1m`, or absent for no time grouping. `auto` picks the finest standard interval, 10 seconds or longer, that gives at most one point per four pixels of panel width, sent by the client, so a 24-hour line stays legible (#232 item 17). |
| `histogram` | `{ "field": "duration_ms", "buckets": "log2" }`, for `heatmap` and `histogram`. |
| `sort`, `limit` | Order by a measure alias; `limit` ≤ 1000. |

Filter expressions are parsed by DuckDB as `SELECT 1 FROM <signal> WHERE (<expr>)`
and walked like `validateSQLNode`, with a narrower allowlist: column
references, attribute lookups with literal keys, comparison and boolean
operators, `IN`, `LIKE`, `ILIKE`, `IS NULL`, literals, and parameters. No
subqueries, no function calls outside a small scalar allowlist (`lower`,
`upper`, `coalesce`, `starts_with`, `contains`).

### SQL panels

`sql` holds one read-only SELECT over the approved relations, checked by the
existing guard in `internal/query/sql_boundary.go`. Two macros expand before
parsing, on identifier arguments only:

- `$__window(col)` becomes
  `col >= $__from::TIMESTAMP_NS::TIMESTAMPTZ_NS AND col < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS`,
  following the timestamp binding rule in AGENTS.md.
- `$__bucket(col)` becomes `time_bucket($__interval, col)`.

### Variables

| Kind | Fields |
|---|---|
| `query` | `from`, `field`, optional `where`; values are the distinct values of `field` in the dashboard window, limited to 500, sorted by frequency. A `where` may reference earlier variables, which forms a dependency chain resolved in order. |
| `custom` | `options`: a fixed list. |
| `constant` | `value`. |
| `text` | free text, for search panels. |

Every kind accepts `default`, `multi` and `include_all`. Variables are never
interpolated into SQL text: `$name` is a DuckDB named parameter, bound with the
value. A multi-value variable binds a list. When a variable is set to All, the
compiler drops filters that reference it; SQL panels see `NULL` and are written
as `($route IS NULL OR http_route = $route)`.

### Units

`ms`, `s`, `ns` (all formatted as human durations: `850 ms`, `4m 0s`, `10m`),
`percent`, `ratio`, `count`, `per_second`, `per_minute`, `bytes`, `none`.
One axis never mixes units; a panel whose measures have different units is a
validation error with a suggestion to split it.

### Visualizations

| `viz` | Use | Notes |
|---|---|---|
| `stat` | one headline number | reducer, sparkline, change versus previous period, threshold status |
| `gauge` | a bounded value | pool usage, budget left; min and max required |
| `timeseries` | change over time | line, area, bars, stacked (additive measures only); thresholds; annotations; comparison |
| `bar` | compare categories | horizontal by default, grouped or stacked, top N, click to filter |
| `table` | detail rows | column formats: unit, bar, status, sparkline, trace link, service link, log template |
| `heatmap` | distribution over time | latency buckets from `duration_ms`; metric histograms |
| `histogram` | one distribution | optionally split by a dimension |
| `scatter` | two measures per item | log or linear axes; colored by one dimension |
| `state_timeline` | health over time per item | the Grafana state timeline, graded by thresholds |
| `logs` | a log stream | severity, highlighted search, trace links |
| `log_patterns` | grouped log messages | `body_template` counts with trend |
| `traces` | a trace list | slowest or erroring, with drill-down |
| `service_map` | dependencies | from `edge_rollup`; error edges thicker and darker than healthy ones (#232 item 22) |
| `health` | system health | ported from today's overview widget |
| `text` | notes | Markdown; the agent uses it to explain what a board shows |

Validation rejects combinations that cannot render correctly: stacking a
non-additive measure such as a percentile or an error rate, a `heatmap`
without a `histogram`, `state_timeline` without thresholds, more than one unit
per panel.

### Layout

The agent states `width`, `height` and order. The server packs panels on
every save: in order, each panel takes the first position, scanning from the
top and then from the left, where its width and height fit. Short panels are
never stretched to match a tall neighbour; the space under them is filled by
later panels that fit, so no card is padded with a blank band (#232 item 24). Users may then drag
and resize; their coordinates are stored as an optional `grid` per panel and
win over packing; adding a panel places only the new panel, removing one compacts upward.

### Edit operations

`edit_dashboard` and `PATCH /api/dashboards/{id}` take an ordered list of
typed operations, applied atomically, validated as a whole, and saved as one
version:

`add_panel { panel, after? }`, `update_panel { id, set }` (shallow merge of
panel fields), `remove_panel { id }`, `move_panel { id, after }`,
`set_variable { variable }`, `remove_variable { name }`, `set_time { time }`,
`rename { name, description? }`.

Typed operations replace RFC 6902 JSON Patch because panel ids are stable
names rather than array indices, which is the misaligned-edit failure the
research describes.

## Execution

1. Validate the spec, resolve variables in dependency order, and choose each
   panel's window and bucket from the dashboard time, the panel override and
   the client's panel width.
2. Compile structured queries to SQL. Check SQL panels with the guard.
3. Bind the Parquet snapshot for the window through `bindSnapshot`, so file
   pruning applies, then run with named parameters on one connection under the
   read gate. Each panel has a 10-second timeout. Tables, bars and SQL panels
   cap at 1000 rows; time series cap at 2000 points per series, and a series
   beyond `top` folds into Other. One request holds at most 200,000 cells;
   past that, time series drop their oldest buckets and say so. When the
   whole request runs out of time, finished panels still return and the rest
   report that they did not run.
4. With comparison on, run the same query shifted by the range and return it
   alongside.
5. Return a frame per panel: columns with `name`, `type`, `role` (`time`,
   `dimension`, `measure`) and `unit`, values in columnar arrays, time as
   integer Unix milliseconds, plus `status` (`ok`, `empty`, `error`), timing,
   and the SQL that ran.
6. When a panel is empty, the executor reruns its count with each filter
   removed in turn, bounded to eight reruns, and reports the filter that empties
   it ("no spans match `http_route = '/cart'` for service frontend; seen routes
   include /api/cart"). This is what lets the agent fix or drop a panel before
   saving (#232 item 9).

A dashboard refresh sends one `POST /api/panels/query` with every visible
panel. Panels scrolled out of view, and all panels in a background tab, do not
refresh (#232 item 30).

## API

New product routes follow AGENTS.md naming:

| Route | Purpose |
|---|---|
| `GET /api/dashboards`, `POST /api/dashboards` | list, create (the body is a spec) |
| `GET /api/dashboards/{id}`, `PUT`, `PATCH`, `DELETE` | read, replace, edit by operations, delete |
| `GET /api/dashboards/{id}/versions` | version list with author (user or agent) and message |
| `POST /api/dashboards/{id}/versions/{version}/restore` | restore as a new version |
| `POST /api/panels/query` | run inline panels for a time range and variable values; used by dashboards, previews and chat |
| `POST /api/panels/exemplars` | traces behind one bucket, bar or row of a panel |
| `POST /api/variables/resolve` | options for the query variables of a spec |
| `GET /api/annotations` | deploys and anomalies for a range, namespace and services |
| `GET /api/telemetry/schema` | columns, units, attribute keys with types and cardinality, metric names, services |

`/api/observability/*` stays for the chat views until those views render
through `ui/panels`, then is removed with them.

## MCP tools

| Tool | Behavior |
|---|---|
| `get_telemetry_schema` | signals, columns, units, top attribute keys per service, metric names; the agent's map of what exists |
| `preview_panels` | validate and run panels without saving; per panel: `ok` with row count and a short summary, `empty` with the diagnosis, or `invalid` with path, message and suggestion |
| `create_dashboard` | validate, preview, pack and save; refuses invalid panels; returns empty-panel diagnoses as warnings |
| `replace_dashboard` | full replacement, same checks |
| `edit_dashboard` | typed operations, same checks |
| `get_dashboard`, `list_dashboards` | as today, returning the v1 spec |
| `list_dashboard_versions`, `restore_dashboard_version` | history |
| `query_telemetry` | run one panel spec and attach it to the chat answer as a view |

The agent prompt describes the loop: read the schema, draft, preview, fix
invalid panels, replace or explain empty ones, then save. Tool descriptions
carry the measure grammar and two complete examples, because the schema alone
does not teach intent.

## Annotations

- **Deploys.** The rollup cycle maintains `version_rollup(namespace, service,
  service_version, first_seen, last_seen)` from new batches, in its own
  analytical table as AGENTS.md requires. A deploy is the `first_seen` of a
  version other than the service's first observed version.
- **Anomalies.** The detector's findings become time ranges with service,
  title and severity. If the current snapshot does not retain history, the
  detector writes each finding to a bounded `anomaly_log` table.

Both render as a dashed vertical marker or a shaded band, with a tooltip, on
every time panel whose filters include the affected service, and on all time
panels when no service filter applies.

## Browser

- **Page.** Header with name, time picker (relative ranges, absolute range,
  zoom-out, history), refresh interval, comparison switch, and edit and
  history buttons. Below it the variable bar, then the grid.
- **URL.** `?from=now-1h&to=now&var-service=checkout&compare=1&view=latency`
  holds every piece of view state. Opening the link reproduces the view; an
  unknown dashboard id is a not-found state, not the default dashboard
  (#232 item 12).
- **Panel chrome.** Title, help text, a status line (loading, stale, empty
  with its diagnosis, error with the message), and a menu: View (full screen),
  Inspect (data table, query, spec, timing), Explain in chat, Duplicate,
  Remove, Copy link.
- **Interaction.** Linked crosshair across time panels (`echarts.connect`),
  brush to zoom (sets the dashboard range), click to filter (sets a
  variable, shown as a removable chip), click to drill (a drawer with exemplar
  traces, the existing waterfall, and the trace's logs).
- **Rendering.** ECharts with the canvas renderer for dashboard panels, LTTB
  sampling as a guard behind server bucketing, and the chart theme read from
  `ui/tokens.ts`. Tables use `@tanstack/react-table` v9 drawn with Mantine.
- **Edit mode.** Drag and resize through the `react-grid-layout` v2 API;
  each save is a version authored by the user.
- **Agent in chat.** A build shows a receipt in the chat as it happens:
  read schema, context found (deploys, anomalies), panels drafted,
  validation with each correction named, preview with per-panel status and
  total time, and the save with a link that opens the dashboard. Every
  conversational edit shows its change chips (`+ panel pool`,
  `~ latency.thresholds`) and links to the new version. The rail shows the
  request each dashboard was built from.
- **Accessibility.** Every chart has the Inspect data table, an `aria-label`
  summary, keyboard-reachable menus, and status conveyed by Fanout's health
  shapes as well as color. Categorical colors use the first four series slots,
  which pass the CVD validator; panels with more series rely on the legend and
  direct labels.

## Data for development and tests

- **fanout-demo snapshot.** Copy `data/telemetry/batches/` from the demo host
  into a scratch data directory and boot a local Fanout on it with a fresh
  control database. Never copy `data/control/` or secrets. Refresh the
  snapshot when the demo's services change. When the demo runs a release
  that writes an older batch format, replay the copied batches into the
  local instance through OTLP with a throwaway converter instead; Fanout
  itself keeps no legacy reader.
- **Fixtures.** Go tests use small deterministic Parquet fixtures written by
  test helpers, so CI never depends on the snapshot.

## Testing

- **Go.** Golden tests from spec to SQL for every measure, filter and macro;
  validation tests asserting path, message and suggestion; execution tests on
  fixtures covering buckets, top N with Other, comparison, empty diagnosis,
  timeouts and caps; edit-operation and layout-packing tests; migration ledger
  extended in `internal/db/migrations_test.go`; API and MCP tool tests.
- **Browser.** Vitest for every `ui/panels` compile function, the URL state,
  panel chrome states, and the grid, with ResizeObserver stubbed as happy-dom
  requires.
- **End to end.** A Playwright script against the local demo snapshot that
  opens the benchmark dashboards, fails on console errors, records render time
  (S6) and request count (S8), and saves screenshots for review.
- **Agent eval.** A harness that runs the ten benchmark prompts against the
  local instance and reports S1–S5 per run, so prompt and tool changes are
  measured rather than judged by eye.

## Milestones

Each milestone ships on its own and leaves `just check` green.

1. **Foundation.** Spec types and validation, structured query compiler, SQL
   panels with parameters, executor and frames, `POST /api/panels/query`,
   dashboard storage with versions (replacing the widget tables), layout
   packing, edit operations, MCP tools `get_telemetry_schema`,
   `preview_panels`, `create_dashboard`, `replace_dashboard`,
   `edit_dashboard`, `get_dashboard`, `list_dashboards`, and the new
   dashboard page with time, URL state, variables, grid, panel chrome,
   inspect, and the `stat`, `gauge`, `timeseries`, `bar`, `table` and `text`
   visualizations. The default dashboard is rebuilt from panels.
2. **Analysis.** Click to filter, drill-down drawer, linked crosshair, brush
   zoom, comparison, per-panel time, annotations, a bar split at the latest
   deploy ("before deploy" and "since deploy"), the stat sparkline, the table
   column formats (bar, status, sparkline, trace link, service link, log
   template), and the `heatmap`, `histogram`, `scatter`, `state_timeline`,
   `logs`, `log_patterns`, `traces`, `service_map` and `health`
   visualizations.
3. **Authoring quality.** The eval harness on the demo snapshot, prompt and
   tool iteration until S1–S5 hold, the build receipt and edit change chips
   in chat, `query_telemetry` with chat views through `ui/panels`, Explain in
   chat, version history and restore, full-screen view, keyboard shortcuts.
4. **Performance and finish.** Rollup routing for measures the read caches
   cover, the 12-panel performance test in CI, a visual and accessibility
   review with screenshots in both themes, removal of `/api/observability/*`
   and the old widgets, documentation, and a reseeded demo. The milestone
   closes with a preview parity review (S13): the agent rebuilds the
   checkout-incident and payments-database boards of the capability preview
   from their prompts on demo data, and their screenshots are compared side
   by side with the preview. The preview's error-budget board depends on SLO
   data, which is out of scope.

## Out of scope

SLO targets and error budgets (a separate project: they need a new resource,
evaluation and UI), alerts created from panels, public snapshots, PDF export,
and a hand-written SQL editor. The spec reserves no fields for them; each
will extend it with a version bump when designed.

## Risks

- **Named parameters.** The plan's first task verifies that the DuckDB Go
  driver binds `$name` parameters inside the guarded statement. If it does
  not, the compiler rewrites `$name` to positional parameters using the
  positions DuckDB's parser reports, with the same binding values.
- **Attribute discovery cost.** `get_telemetry_schema` samples recent batches
  for attribute keys rather than scanning the window, and caches the result
  for a minute.
- **Query cost on large windows.** Snapshot pruning and bucketing bound most
  panels; milestone 4 routes covered measures to the read caches. Until then
  the 10-second timeout and the read gate protect ingest.
- **Model variance.** The eval harness exists so that a prompt or model change
  that regresses S1–S5 is caught before it ships.
