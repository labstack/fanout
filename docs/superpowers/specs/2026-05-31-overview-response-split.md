# Overview response: split internal vs wire, add alerts wrapper

**Status:** approved
**Date:** 2026-05-31
**Closes:** #111, #112, #113

## Why

Three findings from the review of [#110](https://github.com/labstack/fanout/pull/110), now consolidated:

1. **`service.OverviewResult` serves two consumers with conflicting needs.** The HTTP UI handler marshals it directly while the MCP tool maps it to a separate `OverviewOut`. JSON tags on the shared type only affect the UI, and an 8-line doc comment exists to apologize for the mixed `omitempty` policy. (#112)
2. **The current `alerts` shape conflates three operator-visible states.** `"alerts":[]` means simultaneously: alerting disabled, alert store errored, and zero rules firing. The home page can't show "all clear" honestly because two of those three are failures. (#111)
3. **The handler-init layer that makes the wire shape correct has no test.** The struct-tag regression guard added in #110 doesn't catch removal of `result.Alerts = []OverviewAlert{}` in `ui.go`. (#113)

No backward compatibility is required: `fanout.labstack.com` is the only deployed instance, freshly bootstrapped, with no external API clients beyond the bundled React UI. MCP wire format is unaffected by this change (verified: `internal/mcp/overview.go` reads `OverviewResult` by Go field name only and emits its own `OverviewOut`).

## Architecture

```
internal/service/
  types.go                  OverviewResult — pure in-memory value, NO JSON tags
                            on ANY field. Used by both UI and MCP code paths.

internal/api/
  overview_response.go      NEW. Wire-shape types (OverviewResponse + AlertsOut)
                            and toOverviewResponse(*service.OverviewResult,
                            AlertsOut) OverviewResponse. Mirrors the pattern
                            in internal/mcp/overview.go.
  overview_response_test.go NEW. Mapper unit tests (no infra needed).
  ui.go                     Handler builds OverviewResult via svc.Overview,
                            calls computeAlertsState, then mapper, then serves.
  ui_test.go                Extended with HTTP-level happy-path test for the
                            new wire shape via httptest + sqlmock.

internal/mcp/overview.go    Unchanged.
internal/mcp/tool_alerts.go Unchanged in this PR; harmonization is a follow-up
                            (separate issue) — its `Message`-string contract
                            diverges from this PR's structured states.
```

`service.NewForTest` is **NOT** introduced. The existing `service.New(duck *query.Duck, cfg env.Config) *Service` at `internal/service/service.go:15-17` is reachable from the `api` package and `query.Duck.DB` is exported, so `&query.Duck{DB: db}` over `sqlmock` is the standard testing pattern. No new helper required.

## Wire shape

`GET /api/overview` returns:

```json
{
  "health":    { "score": 0.95, "total_services": 12, "...": "..." },
  "services":  [ { "service": "...", "...": "..." } ],
  "incidents": [ { "service": "...", "status": "unhealthy", "...": "..." } ],
  "alerts": {
    "status": "ok",
    "items":  [ { "rule": "...", "service": "...", "...": "..." } ]
  }
}
```

The `alerts` wrapper uses a **tagged enum** on `status` rather than independent booleans. This makes degenerate states unrepresentable: there is no `{enabled: false, available: true}` to reason about. The Go type:

```go
// internal/api/overview_response.go
type AlertsStatus string

const (
    AlertsStatusOK          AlertsStatus = "ok"
    AlertsStatusUnavailable AlertsStatus = "unavailable"
    AlertsStatusDisabled    AlertsStatus = "disabled"
)

type AlertsOut struct {
    Status AlertsStatus           `json:"status"`
    Items  []service.OverviewAlert `json:"items"` // always non-nil
}

type OverviewResponse struct {
    Health    service.OverviewHealth     `json:"health"`     // value, not pointer; UI always populates
    Services  []service.OverviewService  `json:"services"`   // always non-nil
    Incidents []service.OverviewIncident `json:"incidents"`  // always non-nil
    Alerts    AlertsOut                  `json:"alerts"`
}
```

Notes:

- `Health` is a value (not `*OverviewHealth`), no `omitempty` — UI handler always passes `"health"` in `svc.Overview`'s `Include`, so the field is always populated. Pushing the invariant into the type kills the nullable hole.
- `Services`, `Incidents`, `Items` are guaranteed non-nil by the mapper (it allocates empty slices if the input is nil).
- `service.OverviewAlert` stays in the service package — it's a leaf value type used only by the UI today; moving it to api isn't worth the churn.

## Alerts state matrix

`computeAlertsState(ctx, alertStore)` returns an `AlertsOut`:

| Condition | `status` | `items` |
|---|---|---|
| `alertStore == nil` (`cfg.AlertEnabled=false` at startup) | `"disabled"` | `[]` |
| `alertStore.ListAlerts` returns `context.Canceled` / `DeadlineExceeded` | (propagated as error) | (response discarded) |
| `alertStore.ListAlerts` returns any other error | `"unavailable"` | `[]` |
| Success, zero firing | `"ok"` | `[]` |
| Success, N firing | `"ok"` | `[{…}, …]` |

Context-cancellation rule: when the underlying error wraps `context.Canceled` or `context.DeadlineExceeded`, `computeAlertsState` returns the error to the caller (handler) **without** logging it as an error and **without** flipping `status` to `"unavailable"`. A canceled request means the client hung up; the response is being abandoned anyway, so the handler propagates the context error (Echo discards). This avoids the silent-failure pattern where a banner shows up because the client kept aborting requests.

Note: in practice, `svc.Overview` is called first with the same context. If the client already canceled, that call will return the context error and the handler returns 500 before reaching `computeAlertsState`. The context check in `computeAlertsState` covers the narrow window where the client cancels between the two calls.

## Data flow in `(*UIHandler).Overview`

```go
func (h *UIHandler) Overview(c *echo.Context) error {
    // ... existing param parsing ...

    result, err := h.svc.Overview(c.Request().Context(), service.OverviewParams{
        Window:    window,
        Namespace: c.QueryParam("namespace"),
        Include:   []string{"health", "services", "sparklines", "incidents"},
        Limit:     200,
        Tracker:   h.incidents,
    })
    if err != nil {
        slog.Error("overview query failed",
            "namespace", c.QueryParam("namespace"),
            "window_min", window,
            "code", "overview.query_failed",
            "err", err)
        return echo.NewHTTPError(http.StatusInternalServerError, "failed to build overview data")
    }

    alerts, err := computeAlertsState(c.Request().Context(), h.alertStore, c.QueryParam("namespace"), window)
    if err != nil {
        return err // context cancellation only; Echo discards
    }

    return c.JSON(http.StatusOK, toOverviewResponse(result, alerts))
}
```

### Why 200 not 503 when `alerts.status == "unavailable"`

The `services`, `incidents`, and `health` sections are independently authoritative — they came from a successful `svc.Overview` call against DuckDB. Failing the entire endpoint because the SQLite-backed alert store hiccuped would lose all that data and force the React home page into its error-state UI for what is, fundamentally, sidecar information. The rest of the codebase uses 503 for **whole-handler** failure (`ui.go:214` nil-service, `health.go:76` health degraded, `users.go:79` etc.); we're following the same rule, just at a finer granularity. The wire's explicit `status: "unavailable"` carries the failure signal that 503 would otherwise carry.

### Logging

The two `slog.Error` calls in this handler share a shape:

```
slog.Error(<one-line-message>,
    "namespace", c.QueryParam("namespace"),
    "window_min", window,
    "code", <stable-id>,
    "err", err)
```

`code` is a new field — a stable, grep-friendly identifier (e.g. `"overview.query_failed"`, `"overview.alerts.list_failed"`) — so production debug sessions can filter the JSON log without parsing free-text messages. There is no project-wide error-ID registry today; this PR introduces the convention organically. (Not in scope to create a constants file.)

## Frontend changes

### TS types (`web/src/lib/types.ts`)

```ts
export type AlertsStatus = "ok" | "unavailable" | "disabled";

export interface OverviewAlerts {
  status: AlertsStatus;
  items:  OverviewAlert[];
}

export interface OverviewResponse {
  health:    OverviewHealth;
  services:  OverviewService[];
  incidents: OverviewIncident[];
  alerts:    OverviewAlerts;
}
```

### `home-page.tsx` rendering

Destructure with a safe default (preserves the #110 defense-in-depth pattern even though the new wire contract makes the field always present):

```tsx
const {
  incidents = [],
  services  = [],
  alerts    = { status: "disabled" as const, items: [] },
} = data;
```

Render rules for the alerts section:

| `alerts.status` | `items.length > 0` | Render |
|---|---|---|
| `"disabled"` | * | nothing |
| `"unavailable"` | * | `<sl-alert variant="warning" open>Alerts data temporarily unavailable. Retrying automatically.</sl-alert>` |
| `"ok"` | false | nothing |
| `"ok"` | true | existing alerts strip (count + rule names) |

The banner uses `sl-alert` (Shoelace, already used elsewhere per CLAUDE.md and `ui.go:23` whitelist). Copy is user-facing — does not say "see server logs" because authenticated users may not be operators. The home page already polls `/api/overview` every `REFRESH_INTERVAL`, so "retrying automatically" is accurate.

Five call-sites in `home-page.tsx` are affected by the type change (verified via review): the destructure (line 139), and four references inside the alerts block (`alerts.length`, two more `.length`, `alerts.map`). All migrate to `alerts.items` and `alerts.status` — `tsc --noEmit` will surface any miss.

## Tests

### `internal/api/overview_response_test.go` (new)

Pure unit tests for the mapper — no HTTP or sqlmock infra required. Table-driven, covering:

1. **Empty input** — nil slices in `OverviewResult` → wire output has `[]` for `services`, `incidents`, `alerts.items` (never `null`).
2. **Populated input** — values flow through correctly (one row each for services / incidents / alerts).
3. **AlertsOut variants** — three rows: `status="ok"` with items, `status="unavailable"` with empty items, `status="disabled"` with empty items. Asserts the JSON serialization for each.

### `internal/api/overview_alerts_test.go` (new)

Unit tests for `computeAlertsState` against a small `alertLister` interface stub (or directly against `*alert.Store` if mocking is cheap). Five tests, one per matrix row:

1. `alertStore == nil` → `{status: "disabled", items: []}`, no log.
2. `ListAlerts` returns `context.Canceled` (or a wrap thereof) → propagates `context.Canceled` as the returned error, no log, no state.
3. `ListAlerts` returns `context.DeadlineExceeded` → same as above with `context.DeadlineExceeded`.
4. `ListAlerts` returns a generic error → `{status: "unavailable", items: []}`, `slog.Error` emitted with `code="overview.alerts.list_failed"` (captured via `slog.SetDefault` in test, or by spy handler).
5. `ListAlerts` succeeds with zero rows → `{status: "ok", items: []}`.
6. `ListAlerts` succeeds with N rows → `{status: "ok", items: [N entries]}`.

If `alertLister` doesn't already exist as an interface, this PR introduces it as a one-method test seam (`ListAlerts(state, service, ruleID string) ([]alert.Alert, error)`); `*alert.Store` already satisfies it implicitly.

### `internal/api/ui_test.go` (extend)

New `TestOverview_HappyPath` calls `(*UIHandler).Overview` via `httptest`:
- Constructs `*service.Service` via the existing `service.New(duck, cfg)` over `&query.Duck{DB: db}` with sqlmock (pattern: `internal/service/status_test.go:12-27`, adapted for cross-package use).
- `alertStore = nil` so the handler exercises the disabled-alerts branch end-to-end.
- Asserts the response body parses as JSON with:
  - `alerts.status == "disabled"`
  - `alerts.items` is `[]` (not `null`, not absent)
  - `services` is a present array
  - `incidents` is a present array
  - `health` is a present object

### Removed: `internal/service/overview_test.go TestOverviewResult_AlwaysEmitsUIArrays`

Once `OverviewResult` has no JSON tags on any field, the test is genuinely moot — it asserts a property of a struct that no longer marshals to the wire. The contract migrates to the api package's mapper test. The deletion is part of this PR; the new home for the regression vector is `overview_response_test.go`.

## Out of scope (explicit)

- **MCP `tool_alerts.go` parity.** `internal/mcp/tool_alerts.go` uses a `Message` string field for alerts state and silently returns query errors to the LLM without `slog.Error` (line 387). This deserves the same treatment but is a separate change; file as a follow-up issue.
- **New endpoint for alerting subsystem health.** `/api/health/alerts` or similar would expose `enabled` / `available` independent of the overview. Not needed yet.
- **Frontend test framework.** No `vitest`/`jest`/`@testing-library` in the repo today; adding one for this PR's render-rules is disproportionate. The Go-side mapper tests + manual smoke test cover the contract.

## Verification before merge

- `go test ./internal/api ./internal/service ./internal/mcp` — clean
- `go vet ./...` — clean
- `cd web && tsc --noEmit` — clean (expected to surface ~5 call-site edits in `home-page.tsx`)
- Manual: hit `https://fanout.labstack.com/api/overview` after deploy, confirm `alerts: {status: "ok"|"disabled", items: []}` appears; load the home page and confirm no crash on the no-firing-alerts state.

## Release plan after merge

Cut `fanout/v2026.05.3`. Bump `FANOUT_VERSION` on the host. Recreate the `fanout` container (~2s downtime, same as recent edge changes). Brief rolling-restart window where new React bundle could hit the old server, but for one user (`v@labstack.com`) refreshing once is acceptable; the contract is not stable enough yet to warrant compatibility tooling.

## Follow-up issues to file after merge

1. MCP `tool_alerts.go` adopts the same `{status, items}` contract as the UI; adds `slog.Error` on `ListAlerts` failure.
