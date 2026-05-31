# Overview response: split internal vs wire, add alerts wrapper

**Status:** approved
**Date:** 2026-05-31
**Closes:** #111, #112, #113

## Why

Three findings from the review of [#110](https://github.com/labstack/fanout/pull/110), now consolidated:

1. **`service.OverviewResult` serves two consumers with conflicting needs.** The HTTP UI handler marshals it directly while the MCP tool maps it to a separate `OverviewOut`. JSON tags on the shared type only affect the UI, and an 8-line doc comment exists to apologize for the mixed `omitempty` policy. (#112)
2. **The current `alerts` shape conflates three operator-visible states.** `"alerts":[]` means simultaneously: alerting disabled, alert store errored, and zero rules firing. The home page can't show "all clear" honestly because two of those three are failures. (#111)
3. **The handler-init layer that makes the wire shape correct has no test.** The struct-tag regression guard added in #110 doesn't catch removal of `result.Alerts = []OverviewAlert{}` in `ui.go`. (#113)

No backward compatibility is required: `fanout.labstack.com` is the only deployed instance, freshly bootstrapped, with no external API clients beyond the bundled React UI. MCP wire format is unaffected by this change.

## Architecture

```
internal/service/
  types.go                  OverviewResult — pure in-memory value, no JSON tags.
                            Used by both UI handler and MCP tool.

internal/api/
  overview_response.go      NEW. OverviewResponse (wire type + JSON tags) and
                            toOverviewResponse(*service.OverviewResult, alertsState).
                            Mirrors the pattern in internal/mcp/overview.go.
  overview_response_test.go NEW. Unit tests for the mapper across the four
                            alert states.
  ui.go                     Handler builds OverviewResult via svc.Overview,
                            computes alerts state, calls mapper, serves.
  ui_test.go                Existing tests preserved. New HTTP-level happy-path
                            test asserts the response body shape via httptest.

internal/service/
  service.go (or new helper) Add NewForTest(duck *query.Duck, cfg env.Config)
                             so the api package can construct a *Service against
                             sqlmock without exporting package-private fields.

internal/mcp/overview.go    Unchanged.
```

## Wire shape

`GET /api/overview` returns:

```json
{
  "health":    { "score": 0.95, "total_services": 12, ... },
  "services":  [ { "service": "...", ... } ],
  "incidents": [ { "service": "...", "status": "unhealthy", ... } ],
  "alerts": {
    "enabled":   true,
    "available": true,
    "items":     [ { "rule": "...", "service": "...", ... } ]
  }
}
```

Fields inside the `alerts` wrapper are unprefixed because they're already namespaced under `alerts:`.

`services`, `incidents`, and `alerts.items` are always non-nil arrays on the wire. The handler-init pattern moves into the mapper; `service.OverviewResult` has no opinion on emptiness vs nil.

## Alerts state matrix

| Condition | `enabled` | `available` | `items` |
|---|---|---|---|
| `alertStore == nil` (`cfg.AlertEnabled=false` at startup) | `false` | `false` | `[]` |
| `alertStore.ListAlerts` returns error | `true` | `false` | `[]` |
| Success, zero firing | `true` | `true` | `[]` |
| Success, N firing | `true` | `true` | `[{...}]` |

Rationale: `enabled` reflects deployment state (does this instance evaluate rules?); `available` reflects runtime health of the alert store. They're independent — alerting may be enabled but temporarily unavailable.

## Data flow in `(*UIHandler).Overview`

```go
result, err := h.svc.Overview(ctx, params)   // unchanged; result has no wire tags
if err != nil { return 500 }

alerts := computeAlertsState(ctx, h.alertStore)  // {enabled, available, items}

resp := toOverviewResponse(result, alerts)
return c.JSON(200, resp)
```

`computeAlertsState` is a small handler-local helper (probably 10 lines) that owns the state matrix above. It also handles the `slog.Error` on `ListAlerts` failure (preserving the contextual logging added in #110).

## Frontend changes (`web/src/pages/home-page.tsx`)

TS type updates in `web/src/lib/types.ts`:

```ts
export interface OverviewAlerts {
  enabled:   boolean;
  available: boolean;
  items:     OverviewAlert[];
}

export interface OverviewResponse {
  health:    OverviewHealth;
  services:  OverviewService[];
  incidents: OverviewIncident[];
  alerts:    OverviewAlerts;
}
```

Home page rendering rules for the alerts section:

| `enabled` | `available` | `items.length > 0` | Render |
|---|---|---|---|
| false | * | * | nothing |
| true | false | * | small banner: "Alert subsystem unavailable — see server logs" |
| true | true | false | nothing |
| true | true | true | existing alerts strip (count + rule names) |

The destructure-with-defaults pattern from #110 (`const { incidents = [], services = [] } = data`) is preserved for `incidents`/`services`. `alerts` is now an object, not an array; the destructure becomes `const { alerts } = data;` and the renderer reads `alerts.enabled`, `alerts.available`, `alerts.items`.

## Tests

### `internal/api/overview_response_test.go` (new, no infra)

Pure unit tests for `toOverviewResponse`. One table-driven test covering the 4-row state matrix above, plus a test asserting `services`/`incidents`/`alerts.items` are emitted as `[]` (not `null`) even when the input has nil slices. This is the new home for the contract that `overview_test.go:TestOverviewResult_AlwaysEmitsUIArrays` currently pins.

### `internal/api/ui_test.go` (extend existing file)

New `TestOverview_HappyPath` calls the actual `(*UIHandler).Overview` via `httptest` against:
- A `*service.Service` constructed via `service.NewForTest` over `sqlmock` returning a minimal rollup row set (zero rows is fine).
- `alertStore = nil` (forces the disabled-alerts branch — exercises both the handler-init paths and the new state computation).

Assertion: response body parses as JSON; `alerts.enabled == false`, `alerts.available == false`, `alerts.items == []`; `services` is a present array (length depends on mock); `incidents` is a present array.

### `internal/service/overview_test.go` (slim down)

`TestOverviewResult_AlwaysEmitsUIArrays` becomes redundant once `OverviewResult` has no JSON tags. Remove it. Coverage moves to the api package (where the wire contract now lives).

### `internal/service/service.go` (add helper)

```go
// NewForTest constructs a *Service for unit tests against sqlmock.
// All other dependencies are nil/zero; only Overview/Status-style flows are exercisable.
func NewForTest(duck *query.Duck, cfg env.Config) *Service {
    return &Service{duck: duck, cfg: cfg}
}
```

Tiny and additive. The api package becomes able to construct a service for handler tests without exporting fields or moving struct definitions.

## Error handling

- `ListAlerts` error: `computeAlertsState` logs with `slog.Error(... namespace, err)`, returns `{enabled: true, available: false, items: []}`. The 200 OK is preserved; the operator sees the new banner.
- `svc.Overview` error: existing 500 behavior preserved.
- `alertStore` nil: `computeAlertsState` returns `{enabled: false, available: false, items: []}` without logging.

## Migration

None. The `alerts` field changes from `OverviewAlert[]` to a wrapper object in one atomic PR. No deprecation shim, no v1 endpoint, no compatibility flag.

## Out of scope

- New endpoint to expose alerting configuration or alert-store health independently (could be `/api/health/alerts` later if other surfaces need it).
- Frontend toast / push notification on `available=false`. Only the home-page banner is in scope.
- Renaming the field (`alerts` stays as the key).

## Verification before merge

- `go test ./...` clean (with new tests + the deleted `overview_test.go` test gone)
- `go vet ./...` clean
- `cd web && tsc --noEmit` clean
- Manual: hit `https://fanout.labstack.com/api/overview` after deploy, confirm the new wrapper appears, and that the home page renders without crash in the no-firing-alerts state.

## Release plan after merge

Cut `fanout/v2026.05.3`. Bump `FANOUT_VERSION` on the host. Recreate the `fanout` container (~2s downtime, same as recent edge changes).
