# Home Command Center — Design

> Status: approved-for-planning · Date: 2026-06-03 · Supersedes the rendering
> of the current triage Home (`docs/home-vision.md`), not its data foundation.

## The Job

The current Home (`/`) answers *"is anything broken?"* well. When the system is
healthy it shows only a summary line plus a flat list of healthy service rows —
correct, but not very informative. This redesign keeps the triage strength and
adds **always-on situational awareness** (an activity trend, a fleet heatmap, a
recent-errors feed) so Home is genuinely useful even when nothing is on fire — a
glanceable "what's going on right now" command center. It stays deterministic
(rollup-derived, no LLM calls) and zero-config, consistent with the 2026 product
vision.

**Home is one dashboard that shows everything and is the doorway into
everything.** It builds **no new investigation UI** — every panel routes into a
surface that already exists (Service Detail, Investigate / AI chat + its MCP
tools, Alerts). Deep dives reuse existing features; Home never re-implements
exploration inline. See the Doorways map below.

A visual mockup of the target design lives at `fanout-home-mockup.html`
(open over a local static server; the dashed top bar is mockup-only controls).

> **Reviewed 2026-06-03** by an independent Claude agent and Codex. Findings
> incorporated below: cache-key collision (blocker), recent-errors gated to avoid
> an always-on spans scan, recent-errors window honesty, Recharts for the activity
> chart, generated-`types.ts` nuance, the `Limit: 200` fleet cap, nonfatal
> optional-section handling, namespace-preserving tile links, and a corrected
> test strategy. The alerts-ignore-namespace behavior is pre-existing and left
> out of scope (noted under Known Limitations).

## Layout: Two-Column Rail

Fixed structure, dynamic emphasis. On wide screens (`≥ 980px`) a main column
plus a sticky `340px` right rail; below that breakpoint everything collapses to
a single column.

```
┌ window selector ───────────────────────────────── ● Live ┐
│  Health strip  (full width)                               │
├───────────────────────────────────┬───────────────────────┤
│ MAIN COLUMN                        │ RIGHT RAIL (sticky)   │
│  • Activity chart                  │  • Alerts (or none)   │
│  • Incident cards (when present)   │  • Recent errors      │
│  • Service heatmap                 │  • Top movers   (v2)  │
│                                    │  • Topology map (v2)  │
└───────────────────────────────────┴───────────────────────┘
```

**Density adapts to state, layout does not:**

- **Empty** — unchanged "Welcome to Fanout" + OTLP endpoint. No rail, no chart.
- **Calm** — health strip, activity chart, full green heatmap, recent-errors
  (often empty → "No errors in window"), alerts ("No alerts firing"). Informative,
  not blank.
- **Incident** — incident cards push to the top of the main column; heatmap sorts
  worst-first; alerts + recent errors light up in the rail.

## Components

### Reused as-is
- `summary-header.tsx` — global health strip.
- `incident-card.tsx` — rich degraded/unhealthy cards.
- `empty-state.tsx` — first-run welcome.

### New (Phase 1)
- `activity-chart.tsx` — global throughput (area) + error-rate (line overlay)
  across the window. **Use Recharts** (already a dependency, `^3.8.1`) with the
  shared `lib/chart-theme.ts` tokens, matching the app's other charts and getting
  axes/tooltips for free. (Inline SVG like `sparkline.tsx` is fine only if this
  ends up a glanceable, axis-less element — don't hand-roll a charting abstraction.)
- `service-heatmap.tsx` — color-coded tile per service, sorted by health score
  (worst first). Each tile: status dot, name, faint traffic sparkline, traffic/min,
  error rate. Click → `/service/:name`, **preserving the active `?namespace=`**
  query param (tiles in a non-default namespace must not drop it). Replaces the
  healthy-rows list; shows the services returned by the overview query (up to the
  handler's `Limit: 200`, severity-sorted), so unhealthy services appear as
  red/amber tiles in addition to their detailed incident cards above. Don't build
  a fleet beyond 200 — that cap is the existing contract; revisit only if real
  deployments exceed it.
- `recent-errors.tsx` — global feed of top error messages across services
  (service tag · message · count), labeled **"Recent errors · last 5m"** so the
  fixed 5-minute scan window (below) isn't conflated with the page's time-window
  selector. Empty → "No errors in last 5m". Each row is a **doorway**: clicking it
  opens Investigate (reuse `buildChatPath`, as the incident card already does)
  pre-filled with the service + error message. No new UI — it routes into chat.

### New (Phase 2 fast-follow)
- `top-movers.tsx` — services whose error rate / p95 deviate most from a trailing
  24h baseline. Requires new baseline math (see Phase 2).
- `topology-map.tsx` — compact dependency graph (SVG) with blast-radius
  highlighting, sourced from existing topology data.

`service-row.tsx` becomes unused once the heatmap lands; remove it if nothing else
references it.

## Doorways — deep-dive routing (reuse existing surfaces only)

Every interactive element on Home lands on something already built. No new
investigation pages or inline explorers.

| Home element | Click target | Existing surface |
|---|---|---|
| Heatmap tile | `/service/:name` (preserve `?namespace=`) | Service Detail |
| Incident card → Investigate | `buildChatPath(prompt, token)`, pre-loaded | Investigate / AI chat + MCP tools |
| Recent-error row | `buildChatPath(prompt)` with service + message | Investigate / AI chat |
| Alerts panel | `/alerts` | Alerts page |
| Health strip · activity chart | — (context only, no doorway) | — |
| _(v2)_ topology node / top-mover | `/service/:name` or Investigate | Service Detail / chat |

## Backend (Phase 1)

All additions extend the existing unified `Service.Overview` /
`GET /api/overview` path — no new endpoints. Sections stay opt-in via
`OverviewParams.Include`, so the MCP `overview` tool is unaffected (it never
requests the new sections).

**Cache-key (blocker — must do).** `overviewSnapshot` caches by
`overview:<window>:<ns>:<limit>:<wantSparkline>:<wantIncidents>`
(`overview.go:78`). Both new sections are fetched into that snapshot, so the key
**must** gain `wantActivity` and `wantRecentErrors` dimensions and they must be
added to `overviewSnapshot`'s signature — otherwise an MCP `overview` call (same
window/ns/limit, both new flags false) populates the cache without these sections
and a following Home call gets a hit with nil activity/errors (and vice-versa).
Also update the `OverviewParams.Include` doc comment (`overview.go:20`) and add
the `wantActivity`/`wantRecentErrors := containsStr(...)` gating lines.

**Nonfatal optional sections.** Like `overviewSparklines`/`overviewTopErrors`
today, a failure in either new query must `slog` and continue with an empty
section — only the base overview query is allowed to fail the request.

### 1. Global activity timeseries — `Include: "activity"`

New result section. The per-minute rollup is already shaped for this; the existing
`overviewSparklines` query is per-service, so add a global-aggregate query:

```sql
SELECT bucket,
       SUM(spans)::BIGINT AS spans,
       CASE WHEN SUM(spans) > 0
            THEN SUM(spans * error_rate) / SUM(spans) ELSE 0 END AS error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL <window> MINUTE
  AND (? = '' OR namespace = ?)
GROUP BY bucket
ORDER BY bucket;
```

Types (`internal/service/types.go`):

```go
type OverviewActivity struct {
    Buckets []ActivityBucket `json:"buckets"`
}
type ActivityBucket struct {
    T         string  `json:"t"`          // RFC3339 bucket start
    Spans     int64   `json:"spans"`
    ErrorRate float64 `json:"error_rate"`
}
```

Add `Activity *OverviewActivity` to `OverviewResult`, populate in
`buildOverviewResult` when `wantActivity`, cache it on `overviewSnapshot` (see
cache-key note above). This is a dedicated small aggregate over the already-
materialized `service_rollup` (one row per service per minute) — keep it separate
rather than reconstructing global buckets from the per-service sparkline arrays,
which aren't guaranteed to align by bucket index when a service has gaps.

### 2. Recent errors feed — `Include: "recent_errors"`

Global variant of `overviewTopErrors` (no `service IN (...)` filter, scan window
**fixed at 5 min** regardless of the page's selector — to bound the raw `spans`
scan — `LIMIT 8`):

**Gate to keep calm-state cheap.** This is the only always-on raw-`spans` scan.
The rollup pass already computes per-service error rates first, so skip the query
entirely when no service in that pass has `errorRate > 0` (a healthy fleet),
returning an empty feed. This avoids a 5-min spans scan every 30s poll just to
render "No errors". Today's `overviewTopErrors` only runs when incidents exist;
this gate preserves that "no cost when clean" property.

```sql
SELECT service,
       COALESCE(NULLIF(status_message, ''), 'error') AS message,
       COUNT(*) AS cnt
FROM spans
WHERE start_time >= now() - INTERVAL <min(window,5)> MINUTE
  AND (? = '' OR namespace = ?)
  AND status IN ('STATUS_CODE_ERROR', 'ERROR')
GROUP BY service, message
ORDER BY cnt DESC
LIMIT 8;
```

Types:

```go
type RecentError struct {
    Service string `json:"service"`
    Message string `json:"message"`
    Count   int64  `json:"count"`
}
```

Add `RecentErrors []RecentError` to `OverviewResult`.

### 3. Wire mapping + handler

- `internal/api/overview_response.go` — add `Activity` and `RecentErrors` to
  `overviewResponse` and map them in `toOverviewResponse`, normalizing to non-nil
  empties like the existing array fields: absent activity serializes as
  `{"buckets":[]}` (not `null`) and recent errors as `[]`.
- `internal/api/ui.go:222` — add `"activity"`, `"recent_errors"` to the Home
  handler's `Include` list. (Leave `"issues"` off — Home doesn't use it. The
  handler already passes `namespace` and `Limit: 200`; no change there.)

## Frontend (Phase 1)

- `web/src/lib/types.ts` — **note:** this file carries a `// Code generated by
  cmd/genblocks; DO NOT EDIT` header, but `genblocks` only reflects the AI block
  registry; the existing `OverviewResponse`/`OverviewIncident`/`TopError`
  interfaces under the hand-written `// ── Overview (Home page) types` section
  (~line 246) are maintained by hand. Add `OverviewActivity`, `ActivityBucket`,
  and `RecentError` in that same section and extend `OverviewResponse` with
  `activity` and `recent_errors`, following that precedent. Do **not** try to
  extend `genblocks` for these — it doesn't generate API wire types. Default both
  safely in `home-page.tsx`, exactly as `alerts` is defaulted today.
- `web/src/pages/home-page.tsx` — restructure into the two-column rail. Keep
  window selector, freshness, fetch/poll logic, and incident handling unchanged;
  move alerts + recent-errors into the rail; replace healthy-rows with the heatmap
  in the main column; render the activity chart under the health strip.
- New components listed above.

## Phase 2 — Fast-follow (designed, not in the first plan)

- **Top movers** — baseline-relative deviation. Compute a trailing-24h per-service
  baseline from `service_rollup` (damped: `dev = (cur - base)/max(base, floor)`,
  floors ~`0.005` err / `50ms` p95), rank by deviation, return top N. New
  `Include: "movers"` section. This is the v2 scoring effort flagged in
  `home-vision.md`; ship after Phase 1 proves the layout.
- **Topology mini-map** — reuse `Service.TopologyWithParams` (nodes carry
  `BlastRadius`, edges carry status). Add a new `Include: "topology_mini"` section
  capped to top-N by blast radius (preferred — there is **no** `/api/topology`
  route today, so the alternative would mean adding one). Frontend SVG graph; no
  force layout needed at this size.

## Out of Scope

- No dashboard builder / draggable widgets — layout is fixed and opinionated.
- No LLM calls on Home — AI stays behind Investigate.
- No new ingest/storage — everything composes existing rollups + spans + topology.
- Recovery arcs and cascade grouping remain `home-vision.md` v2 items, untouched here.

## Known Limitations (pre-existing, not addressed here)

- **Alerts ignore namespace.** `overview_alerts.go` calls
  `ListAlerts("firing", "", "")`, so the firing-alerts panel is global even when
  `?namespace=` is active. This predates the redesign; fixing it (namespace-aware
  alert listing) is a separate change and explicitly out of scope. If it proves
  confusing in a namespaced view, the minimal mitigation is to label the panel as
  showing all namespaces.

## Test Strategy

- **Backend** — extend `internal/service/overview_test.go`: `activity` and
  `recent_errors` populate when requested (sqlmock rows → asserted buckets/feed);
  the empty-data path; **include-off** produces no query for these sections;
  **cache separation** — a snapshot built for an MCP-shaped call (both flags off)
  does not satisfy a Home-shaped call (flags on), i.e. the cache key really
  distinguishes them; the calm gate skips the recent-errors query when no service
  has errors; namespace filtering is applied. `internal/api/overview_response_test.go`
  asserts the new fields serialize as non-nil empties when absent.
- **Frontend** — `web/` has **no test runner** (vite + eslint only), so Phase 1
  frontend verification is `just check` (covers `eslint` + the `tsc -b` typecheck
  in `build`) plus a manual pass in `just up` against the demo across
  empty/calm/incident states. Adding Vitest/RTL is out of scope; don't claim unit
  tests that can't run.

## Verification

`just check` green; then `just up` and confirm at the demo host: calm state shows
activity chart + full heatmap + recent-errors panel; incident state surfaces cards
above the heatmap and lights the rail; empty state unchanged; window selector and
30s auto-refresh still work; clicking a tile navigates to the service page.
