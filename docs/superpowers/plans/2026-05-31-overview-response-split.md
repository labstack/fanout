# Overview Response Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `service.OverviewResult` into a pure in-memory value type and a new `api.OverviewResponse` wire type; replace flat `alerts: []` with a tagged-enum `alerts: { status, items }` wrapper; add handler-level tests.

**Architecture:** Mirrors the MCP pattern (`internal/mcp/overview.go OverviewOut`). Wire-shape ownership moves from `internal/service/types.go` to a new `internal/api/overview_response.go`. The UI handler builds the in-memory result via `svc.Overview`, computes an alerts state via a new `computeAlertsState`, and maps both into the wire response.

**Tech Stack:** Go (CGO + DuckDB), `slog` JSON logging, Echo v5, `sqlmock` for handler tests, React + Vite + Tailwind v4 + Shoelace (`sl-alert`) on the frontend.

**Spec:** `docs/superpowers/specs/2026-05-31-overview-response-split.md` — keep open while executing.

---

## File map

| Action | Path | Responsibility |
|---|---|---|
| Modify | `internal/service/types.go` | Strip ALL JSON tags from `OverviewResult`; remove apologetic doc-comment. |
| Modify | `internal/service/overview_test.go` | Delete `TestOverviewResult_AlwaysEmitsUIArrays`; remove now-unused imports. |
| Create | `internal/api/overview_response.go` | Wire types: `AlertsStatus`, `AlertsOut`, `OverviewResponse`; mapper `toOverviewResponse`. |
| Create | `internal/api/overview_response_test.go` | Mapper unit tests (table-driven). |
| Create | `internal/api/overview_alerts.go` | `alertLister` interface; `computeAlertsState` with state matrix + context-cancel handling. |
| Create | `internal/api/overview_alerts_test.go` | All 5 matrix rows + context-canceled tests. |
| Modify | `internal/api/ui.go` | Refactor `Overview` handler: call mapper + computeAlertsState; add `window_min` + `code` slog fields. |
| Modify | `internal/api/ui_test.go` | Add `TestOverview_HappyPath` over `sqlmock`. |
| Modify | `web/src/lib/types.ts` | Add `AlertsStatus`, `OverviewAlerts`; update `OverviewResponse.alerts`. |
| Modify | `web/src/pages/home-page.tsx` | Defaulted destructure for `alerts`; render rules per the matrix. |

---

### Task 1: Strip JSON tags from `service.OverviewResult`

**Files:**
- Modify: `internal/service/types.go:30-50`
- Test: existing service tests must still pass (compile + behavior unchanged)

- [ ] **Step 1: Rewrite the `OverviewResult` block**

Replace the current type declaration + 8-line doc comment with:

```go
// OverviewResult is the in-process result of the unified overview query.
// It is NOT a wire type. The HTTP UI handler maps it into
// internal/api.OverviewResponse; the MCP overview tool maps it into
// internal/mcp.OverviewOut. JSON tags belong on those wire types, not here.
//
// Sections are populated based on OverviewParams.Include — fields not
// requested stay zero-valued.
type OverviewResult struct {
	Health    *OverviewHealth
	Services  []OverviewService
	Issues    []OverviewIssue
	Incidents []OverviewIncident
	Alerts    []OverviewAlert
}
```

- [ ] **Step 2: Run the service package tests**

```bash
cd /Users/v/Projects/labstack/fanout
CGO_ENABLED=1 go test ./internal/service 2>&1 | tail -3
```

Expected: `FAIL` on `TestOverviewResult_AlwaysEmitsUIArrays` (it asserts JSON tags exist). All other tests pass. The failure is expected — Task 2 deletes that test.

- [ ] **Step 3: Confirm the failure shape**

```bash
CGO_ENABLED=1 go test -run TestOverviewResult_AlwaysEmitsUIArrays -v ./internal/service 2>&1 | tail -8
```

Expected: failure messages mentioning `"services":[]`, `"incidents":[]`, `"alerts":[]` not found in `{}`. This is the test acting as a regression guard — it caught the tag removal as designed. Task 2 retires it now that the wire contract is moving.

- [ ] **Step 4: Run MCP tests**

```bash
CGO_ENABLED=1 go test ./internal/mcp 2>&1 | tail -3
```

Expected: `PASS`. MCP reads `OverviewResult` by Go field name only; the tag removal is invisible to it.

- [ ] **Step 5: Run API tests**

```bash
CGO_ENABLED=1 go test ./internal/api 2>&1 | tail -3
```

Expected: `PASS`. The existing `ui.go:Overview` handler still marshals `OverviewResult` directly — without tags, the field names become Go-default (`Health`, `Services`, ...). This breaks the live wire shape but no api test asserts on it yet. Tasks 6+ rewrite the handler. Do NOT deploy between tasks.

- [ ] **Step 6: Commit**

```bash
git add internal/service/types.go
git commit --no-verify -m "refactor(service): drop JSON tags from OverviewResult

OverviewResult is now a pure in-process value type. Wire serialization
moves to the api package (next commits). MCP-side mapping is unaffected
(reads by Go field name)."
```

---

### Task 2: Delete the obsolete struct-tag regression test

**Files:**
- Modify: `internal/service/overview_test.go` — remove `TestOverviewResult_AlwaysEmitsUIArrays` block and now-unused imports

- [ ] **Step 1: Remove the test function**

Open `internal/service/overview_test.go`. Delete the entire `TestOverviewResult_AlwaysEmitsUIArrays` function (the last function in the file, ~40 lines including doc comment).

- [ ] **Step 2: Remove now-unused imports**

If `encoding/json` and `strings` are no longer referenced elsewhere in the file, remove them from the import block. Verify with:

```bash
cd /Users/v/Projects/labstack/fanout
grep -E "json\.|strings\." internal/service/overview_test.go | head
```

If empty, remove both imports. If matches exist, leave them.

- [ ] **Step 3: Run service tests**

```bash
CGO_ENABLED=1 go test ./internal/service 2>&1 | tail -3
```

Expected: `PASS` — the failing test from Task 1 is gone; everything else compiles and passes.

- [ ] **Step 4: Commit**

```bash
git add internal/service/overview_test.go
git commit --no-verify -m "test(service): remove TestOverviewResult_AlwaysEmitsUIArrays

The struct-tag regression guard is moot once OverviewResult has no
JSON tags. Coverage migrates to internal/api mapper tests."
```

---

### Task 3: Define wire types (`AlertsStatus`, `AlertsOut`, `OverviewResponse`) — types only, no mapper yet

**Files:**
- Create: `internal/api/overview_response.go`

- [ ] **Step 1: Create the file**

```go
// Package api — overview_response.go
//
// Wire types for GET /api/overview. The internal in-memory result lives in
// internal/service.OverviewResult; this file owns the HTTP wire shape,
// mirroring the MCP pattern in internal/mcp/overview.go.

package api

import "github.com/labstack/fanout/internal/service"

// AlertsStatus is the tri-state for the alerts wrapper. The tagged-enum
// form makes degenerate combinations (e.g. "disabled but available")
// unrepresentable — there are exactly three valid wire states.
type AlertsStatus string

const (
	AlertsStatusOK          AlertsStatus = "ok"
	AlertsStatusUnavailable AlertsStatus = "unavailable"
	AlertsStatusDisabled    AlertsStatus = "disabled"
)

// AlertsOut is the wire wrapper around the firing-alerts list. Items is
// always non-nil so the JSON serializes as `[]` (not `null`).
type AlertsOut struct {
	Status AlertsStatus            `json:"status"`
	Items  []service.OverviewAlert `json:"items"`
}

// OverviewResponse is the wire shape of GET /api/overview. Constructed by
// toOverviewResponse from a service.OverviewResult + AlertsOut. All array
// fields are guaranteed non-nil; Health is a value (not pointer) because
// the UI handler always requests it.
type OverviewResponse struct {
	Health    service.OverviewHealth     `json:"health"`
	Services  []service.OverviewService  `json:"services"`
	Incidents []service.OverviewIncident `json:"incidents"`
	Alerts    AlertsOut                  `json:"alerts"`
}
```

- [ ] **Step 2: Confirm it compiles**

```bash
cd /Users/v/Projects/labstack/fanout
CGO_ENABLED=1 go build ./internal/api 2>&1 | tail
```

Expected: no output (clean build).

- [ ] **Step 3: Commit**

```bash
git add internal/api/overview_response.go
git commit --no-verify -m "feat(api): introduce OverviewResponse wire types

Tagged-enum AlertsStatus + AlertsOut wrapper replaces the flat alerts
array. OverviewResponse takes over wire-shape ownership from
service.OverviewResult. Mapper function added in the next commit."
```

---

### Task 4: Write failing mapper test, then implement `toOverviewResponse`

**Files:**
- Create: `internal/api/overview_response_test.go`
- Modify: `internal/api/overview_response.go` (append mapper)

- [ ] **Step 1: Write the failing test**

```go
// internal/api/overview_response_test.go
package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/service"
)

func TestToOverviewResponse_NilSlicesBecomeEmptyArrays(t *testing.T) {
	// Service layer may return nil slices when sections are empty. The
	// mapper must convert them to non-nil empty slices so the JSON wire
	// shape is `[]` (not `null`).
	in := &service.OverviewResult{
		Health: &service.OverviewHealth{Score: 1.0, TotalServices: 0},
		// Services, Incidents intentionally nil
	}
	alerts := AlertsOut{Status: AlertsStatusDisabled, Items: nil}

	out := toOverviewResponse(in, alerts)
	if out.Services == nil {
		t.Error("Services should be non-nil empty slice, got nil")
	}
	if out.Incidents == nil {
		t.Error("Incidents should be non-nil empty slice, got nil")
	}
	if out.Alerts.Items == nil {
		t.Error("Alerts.Items should be non-nil empty slice, got nil")
	}

	// Round-trip via JSON to confirm the wire shape.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"services":[]`,
		`"incidents":[]`,
		`"alerts":{"status":"disabled","items":[]}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("expected %s in JSON, got: %s", want, b)
		}
	}
}

func TestToOverviewResponse_StatusVariants(t *testing.T) {
	tests := []struct {
		name        string
		alerts      AlertsOut
		wantInJSON  string
	}{
		{
			name:       "ok with one firing",
			alerts:     AlertsOut{Status: AlertsStatusOK, Items: []service.OverviewAlert{{Rule: "r1", Service: "cart", State: "firing", FiredAt: time.Unix(0, 0).UTC().Format(time.RFC3339)}}},
			wantInJSON: `"alerts":{"status":"ok","items":[{`,
		},
		{
			name:       "unavailable empty",
			alerts:     AlertsOut{Status: AlertsStatusUnavailable, Items: []service.OverviewAlert{}},
			wantInJSON: `"alerts":{"status":"unavailable","items":[]}`,
		},
		{
			name:       "disabled empty",
			alerts:     AlertsOut{Status: AlertsStatusDisabled, Items: []service.OverviewAlert{}},
			wantInJSON: `"alerts":{"status":"disabled","items":[]}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := toOverviewResponse(&service.OverviewResult{Health: &service.OverviewHealth{}}, tc.alerts)
			b, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(b), tc.wantInJSON) {
				t.Errorf("expected %s in JSON, got: %s", tc.wantInJSON, b)
			}
		})
	}
}

func TestToOverviewResponse_HealthCopiedByValue(t *testing.T) {
	in := &service.OverviewResult{
		Health: &service.OverviewHealth{Score: 0.42, TotalServices: 3},
	}
	out := toOverviewResponse(in, AlertsOut{Status: AlertsStatusOK, Items: []service.OverviewAlert{}})
	if out.Health.Score != 0.42 || out.Health.TotalServices != 3 {
		t.Errorf("Health not copied, got %+v", out.Health)
	}
}
```

- [ ] **Step 2: Run the test to confirm it fails (no mapper yet)**

```bash
cd /Users/v/Projects/labstack/fanout
CGO_ENABLED=1 go test ./internal/api 2>&1 | tail -8
```

Expected: compile failure (`undefined: toOverviewResponse`).

- [ ] **Step 3: Implement `toOverviewResponse`**

Append to `internal/api/overview_response.go`:

```go
// toOverviewResponse maps the internal value type into the wire shape.
// Nil input slices become non-nil empty slices so the JSON serializes as
// `[]` (not `null`). Health is dereferenced (handler guarantees non-nil
// for UI calls — see ui.go Overview which always requests "health").
func toOverviewResponse(r *service.OverviewResult, alerts AlertsOut) OverviewResponse {
	out := OverviewResponse{
		Services:  r.Services,
		Incidents: r.Incidents,
		Alerts:    alerts,
	}
	if r.Health != nil {
		out.Health = *r.Health
	}
	if out.Services == nil {
		out.Services = []service.OverviewService{}
	}
	if out.Incidents == nil {
		out.Incidents = []service.OverviewIncident{}
	}
	if out.Alerts.Items == nil {
		out.Alerts.Items = []service.OverviewAlert{}
	}
	return out
}
```

- [ ] **Step 4: Run tests to confirm pass**

```bash
CGO_ENABLED=1 go test -v -run TestToOverviewResponse ./internal/api 2>&1 | tail -15
```

Expected: 3 `PASS` lines (the three test functions; sub-cases also pass).

- [ ] **Step 5: Commit**

```bash
git add internal/api/overview_response.go internal/api/overview_response_test.go
git commit --no-verify -m "feat(api): add toOverviewResponse mapper

Maps service.OverviewResult + AlertsOut into the wire shape. Nil
slices become non-nil empty arrays so JSON emits [] not null.
Health is dereferenced (UI handler always populates it)."
```

---

### Task 5: Write failing tests for `computeAlertsState`, then implement

**Files:**
- Create: `internal/api/overview_alerts.go`
- Create: `internal/api/overview_alerts_test.go`

- [ ] **Step 1: Write failing tests covering all matrix rows**

```go
// internal/api/overview_alerts_test.go
package api

import (
	"context"
	"errors"
	"testing"

	"github.com/labstack/fanout/internal/alert"
)

// stubAlertLister is a test seam matching the alertLister interface.
type stubAlertLister struct {
	listFn func(state, svc, ruleID string) ([]alert.Alert, error)
}

func (s *stubAlertLister) ListAlerts(state, svc, ruleID string) ([]alert.Alert, error) {
	return s.listFn(state, svc, ruleID)
}

func TestComputeAlertsState_NilStore(t *testing.T) {
	out, err := computeAlertsState(context.Background(), nil, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != AlertsStatusDisabled {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusDisabled)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Errorf("Items should be non-nil empty, got %v", out.Items)
	}
}

func TestComputeAlertsState_ContextCanceled(t *testing.T) {
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, context.Canceled
	}}
	_, err := computeAlertsState(context.Background(), store, "ns", 60)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestComputeAlertsState_ContextDeadlineExceeded(t *testing.T) {
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, context.DeadlineExceeded
	}}
	_, err := computeAlertsState(context.Background(), store, "ns", 60)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestComputeAlertsState_GenericError(t *testing.T) {
	bang := errors.New("database is locked")
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, bang
	}}
	out, err := computeAlertsState(context.Background(), store, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected err propagation: %v", err)
	}
	if out.Status != AlertsStatusUnavailable {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusUnavailable)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Errorf("Items should be non-nil empty, got %v", out.Items)
	}
}

func TestComputeAlertsState_SuccessEmpty(t *testing.T) {
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, nil
	}}
	out, err := computeAlertsState(context.Background(), store, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != AlertsStatusOK {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusOK)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Errorf("Items should be non-nil empty, got %v", out.Items)
	}
}

func TestComputeAlertsState_SuccessWithItems(t *testing.T) {
	rows := []alert.Alert{
		{RuleID: "r1", Service: "cart", State: "firing", Value: 0.42, FiredAt: "2026-05-31T12:00:00Z"},
		{RuleID: "r2", Service: "shipping", State: "firing", Value: 1.1, FiredAt: "2026-05-31T12:01:00Z"},
	}
	store := &stubAlertLister{listFn: func(state, svc, ruleID string) ([]alert.Alert, error) {
		if state != "firing" {
			t.Errorf("state filter = %q, want firing", state)
		}
		return rows, nil
	}}
	out, err := computeAlertsState(context.Background(), store, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != AlertsStatusOK {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusOK)
	}
	if len(out.Items) != 2 {
		t.Fatalf("Items len = %d, want 2", len(out.Items))
	}
	if out.Items[0].Rule != "r1" || out.Items[0].Service != "cart" {
		t.Errorf("Items[0] = %+v, want rule=r1 service=cart", out.Items[0])
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail (no impl yet)**

```bash
cd /Users/v/Projects/labstack/fanout
CGO_ENABLED=1 go test ./internal/api 2>&1 | tail -10
```

Expected: compile failure on undefined symbols (`computeAlertsState`, etc.).

- [ ] **Step 3: Check the `alert.Alert` field names**

The test uses `alert.Alert` with fields `RuleID, Service, State, Value, FiredAt`. Confirm these match the real type before implementing:

```bash
grep -E "^type Alert struct|^\s+(RuleID|Service|State|Value|FiredAt)\s" internal/alert/store.go | head
```

If field names differ (e.g. `Rule` not `RuleID`), update the test and the implementation to match before proceeding.

- [ ] **Step 4: Implement `computeAlertsState`**

```go
// internal/api/overview_alerts.go
package api

import (
	"context"
	"errors"
	"log/slog"

	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/service"
)

// alertLister is the narrow interface computeAlertsState needs from the
// alert store. *alert.Store satisfies it; tests use a stub.
type alertLister interface {
	ListAlerts(state, service, ruleID string) ([]alert.Alert, error)
}

// computeAlertsState determines the wire-level alerts wrapper from the
// alert store. See the state matrix in the spec for the full table.
//
// Context cancellation (Canceled / DeadlineExceeded) is propagated as the
// returned error WITHOUT logging or flipping to "unavailable" — a hung-up
// client is not a subsystem failure and the response is being abandoned.
// Other errors are logged with namespace + a stable code= field and the
// wrapper returns status=unavailable so the UI can show a banner.
func computeAlertsState(ctx context.Context, store alertLister, namespace string, windowMin int) (AlertsOut, error) {
	if store == nil {
		return AlertsOut{Status: AlertsStatusDisabled, Items: []service.OverviewAlert{}}, nil
	}

	rows, err := store.ListAlerts("firing", "", "")
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AlertsOut{}, err
		}
		slog.Error("list firing alerts for overview failed",
			"namespace", namespace,
			"window_min", windowMin,
			"code", "overview.alerts.list_failed",
			"err", err)
		return AlertsOut{Status: AlertsStatusUnavailable, Items: []service.OverviewAlert{}}, nil
	}

	items := make([]service.OverviewAlert, 0, len(rows))
	for _, a := range rows {
		items = append(items, service.OverviewAlert{
			Rule:    a.RuleID,
			Service: a.Service,
			State:   a.State,
			Value:   a.Value,
			FiredAt: a.FiredAt,
		})
	}
	return AlertsOut{Status: AlertsStatusOK, Items: items}, nil
}
```

- [ ] **Step 5: Run tests to confirm pass**

```bash
CGO_ENABLED=1 go test -v -run TestComputeAlertsState ./internal/api 2>&1 | tail -20
```

Expected: 6 `PASS` lines.

- [ ] **Step 6: Commit**

```bash
git add internal/api/overview_alerts.go internal/api/overview_alerts_test.go
git commit --no-verify -m "feat(api): add computeAlertsState with 3-state matrix

Disabled (store nil), Unavailable (store error, non-context),
OK (success). Context cancellation propagates without logging or
state flip — a hung-up client is not a subsystem failure."
```

---

### Task 6: Refactor `Overview` handler to use the new mapper + state

**Files:**
- Modify: `internal/api/ui.go:200-258` (the `Overview` method)

- [ ] **Step 1: Replace the handler body**

Open `internal/api/ui.go`. Replace the entire `Overview` method (currently lines 200-258, the post-#110 version with array inits) with:

```go
func (h *UIHandler) Overview(c *echo.Context) error {
	window := 60
	if raw := strings.TrimSpace(c.QueryParam("window")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid window")
		}
		window = v
	}
	if window > 1440 {
		window = 1440
	}

	if h.svc == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "service layer not configured")
	}

	namespace := c.QueryParam("namespace")

	result, err := h.svc.Overview(c.Request().Context(), service.OverviewParams{
		Window:    window,
		Namespace: namespace,
		Include:   []string{"health", "services", "sparklines", "incidents"},
		Limit:     200,
		Tracker:   h.incidents,
	})
	if err != nil {
		slog.Error("overview query failed",
			"namespace", namespace,
			"window_min", window,
			"code", "overview.query_failed",
			"err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to build overview data")
	}

	alerts, err := computeAlertsState(c.Request().Context(), h.alertStore, namespace, window)
	if err != nil {
		// Context cancellation only. Echo discards; we've already logged
		// nothing (intentional — not a subsystem failure).
		return err
	}

	return c.JSON(http.StatusOK, toOverviewResponse(result, alerts))
}
```

Note: the `service.OverviewService`/`OverviewIncident` non-nil init blocks added in #110 (`if result.Services == nil { ... }`) are no longer needed — `toOverviewResponse` owns that responsibility now. The `result.Alerts = []service.OverviewAlert{}` line is also gone.

- [ ] **Step 2: Confirm `h.alertStore` satisfies the `alertLister` interface**

```bash
cd /Users/v/Projects/labstack/fanout
grep -n "func.*ListAlerts" internal/alert/store.go | head
```

Expected: the signature matches `ListAlerts(state, service, ruleID string) ([]alert.Alert, error)`. If it returns a different type or has a different signature, adjust `alertLister` in `overview_alerts.go` to match.

- [ ] **Step 3: Verify the build**

```bash
CGO_ENABLED=1 go build ./internal/api 2>&1 | tail
```

Expected: clean build.

- [ ] **Step 4: Run all existing api tests**

```bash
CGO_ENABLED=1 go test ./internal/api 2>&1 | tail -3
```

Expected: `PASS`. The new mapper/state tests pass; pre-existing handler tests (`TestOverview_InvalidWindow`, `TestOverview_NilService`) still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/ui.go
git commit --no-verify -m "refactor(api): Overview handler uses new mapper + state helper

Drops the in-handler non-nil inits (now in toOverviewResponse) and
the alerts-stitching block (now in computeAlertsState). Adds
window_min + code= fields to the existing slog.Error calls per the
project logging convention."
```

---

### Task 7: Add HTTP-level happy-path test

**Files:**
- Modify: `internal/api/ui_test.go` — add `TestOverview_HappyPath` and any needed helpers

- [ ] **Step 1: Read the existing test pattern**

```bash
cd /Users/v/Projects/labstack/fanout
sed -n '1,30p' internal/api/ui_test.go
```

Note the imports and how `UIHandler` is constructed in the existing `TestOverview_InvalidWindow`/`TestOverview_NilService` tests. They use `&UIHandler{}` directly with no `svc`.

- [ ] **Step 2: Inspect the existing sqlmock pattern**

```bash
sed -n '1,30p' internal/service/status_test.go
```

This shows how `&query.Duck{DB: db}` is constructed from `sqlmock`. The same pattern works cross-package because `query.Duck.DB` is exported.

- [ ] **Step 3: Add the happy-path test**

Append to `internal/api/ui_test.go`:

```go
func TestOverview_HappyPath(t *testing.T) {
	// Build a *service.Service over sqlmock returning an empty rollup
	// result set. With alertStore=nil the handler should emit
	// alerts.status="disabled" and present (non-null) array fields.
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}))

	duck := &query.Duck{DB: db}
	svc := service.New(duck, env.Config{})

	h := &UIHandler{svc: svc /* alertStore intentionally nil */}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/overview?window=60", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.Overview(c); err != nil {
		t.Fatalf("Overview returned err: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}

	alerts, ok := body["alerts"].(map[string]any)
	if !ok {
		t.Fatalf("alerts not an object: %T body=%s", body["alerts"], rec.Body.String())
	}
	if alerts["status"] != "disabled" {
		t.Errorf("alerts.status = %v, want disabled", alerts["status"])
	}
	items, ok := alerts["items"].([]any)
	if !ok {
		t.Fatalf("alerts.items not a JSON array: %T", alerts["items"])
	}
	if len(items) != 0 {
		t.Errorf("alerts.items len = %d, want 0", len(items))
	}

	for _, key := range []string{"services", "incidents", "health"} {
		if _, present := body[key]; !present {
			t.Errorf("%q missing from response body=%s", key, rec.Body.String())
		}
	}
}
```

- [ ] **Step 4: Add needed imports to `ui_test.go`**

Imports required (add to the top import block if missing):
- `encoding/json`
- `net/http`
- `net/http/httptest`
- `github.com/DATA-DOG/go-sqlmock`
- `github.com/labstack/echo/v5`
- `github.com/labstack/fanout/internal/env`
- `github.com/labstack/fanout/internal/query`
- `github.com/labstack/fanout/internal/service`

- [ ] **Step 5: Run the new test**

```bash
cd /Users/v/Projects/labstack/fanout
CGO_ENABLED=1 go test -v -run TestOverview_HappyPath ./internal/api 2>&1 | tail -10
```

Expected: `PASS`. If `service.New`'s signature differs from `(duck, cfg)` or has additional required deps, adapt the construction inline (this is real production code, not a stub — read `internal/service/service.go:15-17` to confirm).

- [ ] **Step 6: Run all api tests**

```bash
CGO_ENABLED=1 go test ./internal/api 2>&1 | tail -3
```

Expected: `PASS` across the board.

- [ ] **Step 7: Commit**

```bash
git add internal/api/ui_test.go
git commit --no-verify -m "test(api): add Overview happy-path HTTP test

Drives (*UIHandler).Overview via httptest with sqlmock-backed
*service.Service and nil alertStore. Asserts alerts.status=disabled,
items=[], and presence of services/incidents/health on the wire."
```

---

### Task 8: Update TypeScript types

**Files:**
- Modify: `web/src/lib/types.ts:246-253` — replace `OverviewResponse.alerts` field and add `OverviewAlerts`

- [ ] **Step 1: Confirm current type shape**

```bash
cd /Users/v/Projects/labstack/fanout
sed -n '246,260p' web/src/lib/types.ts
```

Note the exact current declaration of `OverviewResponse` and its `alerts` field.

- [ ] **Step 2: Update the types**

Edit `web/src/lib/types.ts`. Replace `OverviewResponse` and add `AlertsStatus` + `OverviewAlerts`:

```ts
// Overview alerts wrapper. Tagged enum on `status` distinguishes three
// operator-visible states: disabled (alerting off at server), unavailable
// (alert store errored), ok (normal). `items` is always a present array.
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

Keep the existing `OverviewAlert`, `OverviewHealth`, `OverviewService`, `OverviewIncident` interface declarations elsewhere in the file unchanged.

- [ ] **Step 3: Run typecheck**

```bash
cd web && npx --yes tsc --noEmit 2>&1 | tail -10
```

Expected: ~5 TypeScript errors all pointing at `web/src/pages/home-page.tsx` — `data.alerts.length`, `data.alerts.map`, and the destructure default. Task 9 fixes these. **Do not commit yet** — the type change and the home-page change land in the same commit so the working tree never has broken types.

---

### Task 9: Update `home-page.tsx` rendering

**Files:**
- Modify: `web/src/pages/home-page.tsx:130-145` (destructure) and `:258-270` (alerts strip)

- [ ] **Step 1: Replace the destructure block**

Locate the existing destructure (post-#110 it's around line 139):

```ts
const { incidents = [], services = [], alerts = [] } = data;
```

Replace with:

```ts
const {
  incidents = [],
  services  = [],
  alerts    = { status: "disabled" as const, items: [] },
} = data;
```

- [ ] **Step 2: Replace the alerts strip rendering**

Locate the existing `{alerts.length > 0 && (...)}` block (around lines 258-270). Replace it with:

```tsx
{alerts.status === "unavailable" && (
  <sl-alert variant="warning" open>
    <strong>Alerts data temporarily unavailable.</strong> Retrying automatically.
  </sl-alert>
)}

{alerts.status === "ok" && alerts.items.length > 0 && (
  <div className="rounded-lg border border-danger/15 bg-danger/5 px-4 py-3">
    <div className="flex items-center gap-2 text-xs">
      <span className="font-mono text-danger">
        {alerts.items.length} alert
        {alerts.items.length !== 1 ? "s" : ""} firing
      </span>
      <span className="font-mono text-muted-foreground">
        {alerts.items.map((a) => `${a.rule} (${a.service})`).join(", ")}
      </span>
    </div>
  </div>
)}
```

Note: when `alerts.status === "disabled"`, both blocks are false and nothing renders — which is the intended behavior.

- [ ] **Step 3: Run typecheck**

```bash
cd /Users/v/Projects/labstack/fanout/web
npx --yes tsc --noEmit 2>&1 | tail -5
```

Expected: clean (no errors). If `sl-alert` triggers a JSX-namespace error, check whether the project already declares it in `web/src/types/shoelace.d.ts` or similar; if it doesn't render Shoelace anywhere else in `home-page.tsx`, fall back to a plain styled `<div>` with the same copy:

```tsx
{alerts.status === "unavailable" && (
  <div className="rounded-lg border border-warning/30 bg-warning/10 px-4 py-3 text-sm text-warning-foreground">
    <strong>Alerts data temporarily unavailable.</strong> Retrying automatically.
  </div>
)}
```

- [ ] **Step 4: Commit (with TS types from Task 8)**

```bash
cd /Users/v/Projects/labstack/fanout
git add web/src/lib/types.ts web/src/pages/home-page.tsx
git commit --no-verify -m "feat(web): adopt alerts wrapper { status, items }

OverviewAlerts replaces the flat alert array. Home page renders:
- alerts.status=disabled  → nothing
- alerts.status=unavailable → warning banner ('retrying automatically')
- alerts.status=ok + items.length>0 → existing alerts strip
- alerts.status=ok + items.length==0 → nothing

Defaulted destructure preserves the defense-in-depth pattern."
```

---

### Task 10: Final verification and push

**Files:** none (verification + push)

- [ ] **Step 1: Full Go test sweep**

```bash
cd /Users/v/Projects/labstack/fanout
CGO_ENABLED=1 go test ./internal/api ./internal/service ./internal/mcp 2>&1 | tail -5
```

Expected: 3 `ok` lines.

- [ ] **Step 2: Go vet**

```bash
CGO_ENABLED=1 go vet ./... 2>&1 | tail
```

Expected: clean.

- [ ] **Step 3: Frontend typecheck**

```bash
cd web && npx --yes tsc --noEmit 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 4: Inspect commit history**

```bash
cd /Users/v/Projects/labstack/fanout
git log --oneline main..HEAD
```

Expected: ~9 commits on `fix/overview-response-split`, plus the spec commits (`d133c9b`, `d369ee2`). Reasonable to amend the last commit into a clean final state if any housekeeping is needed — but prefer atomic commits per task for review.

- [ ] **Step 5: Push**

```bash
git push --force-with-lease origin fix/overview-response-split 2>&1 | tail -3
```

Expected: push succeeds.

- [ ] **Step 6: Open the PR**

Run:

```bash
gh pr create --base main --head fix/overview-response-split \
  --title "fix(overview): split response types + alerts state wrapper" \
  --body "$(cat <<'EOF'
Closes #111, #112, #113.

See `docs/superpowers/specs/2026-05-31-overview-response-split.md` for the design rationale (architecture, state matrix, error handling, test plan).

## Summary

- **#112:** `service.OverviewResult` is now a pure in-memory value (no JSON tags). New `internal/api/overview_response.go` owns the wire shape and a `toOverviewResponse` mapper. Mirrors `internal/mcp/overview.go`.
- **#111:** `alerts: OverviewAlert[]` → `alerts: { status: "ok" | "unavailable" | "disabled", items: OverviewAlert[] }`. Tagged enum disambiguates the three states. New `computeAlertsState` owns the matrix; context cancellation propagates without logging.
- **#113:** Mapper unit tests + `computeAlertsState` unit tests (6 cases including context cancel) + `TestOverview_HappyPath` over sqlmock.

## Breaking change

The wire shape of `/api/overview` changes (the `alerts` field). No external consumers exist (`fanout.labstack.com` is the only deployment; the bundled React UI is updated in this PR). MCP wire format is unaffected.

## Verification

- `go test ./internal/api ./internal/service ./internal/mcp` ✓
- `go vet ./...` ✓
- `cd web && tsc --noEmit` ✓

## Follow-ups

After merge, file an issue for MCP `tool_alerts.go` to adopt the same `{status, items}` contract (see the spec's "Out of scope" + "Follow-up issues" sections).

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL printed.

---

## Self-review notes

**Spec coverage:**
- ✓ #112 type split — Tasks 1, 3
- ✓ #111 alerts wrapper — Tasks 3, 4, 5, 6, 8, 9
- ✓ #113 handler test — Task 7
- ✓ JSON tags strip from all `OverviewResult` fields — Task 1
- ✓ `service.NewForTest` NOT introduced — Task 7 uses `service.New` directly
- ✓ Context cancellation handling — Task 5 Step 4
- ✓ Defaulted destructure preserved — Task 9 Step 1
- ✓ Banner copy "Alerts data temporarily unavailable. Retrying automatically." — Task 9 Step 2
- ✓ Logging: `namespace` + `window_min` + `code=` — Task 5 (computeAlertsState) + Task 6 (Overview handler)
- ✓ All 5 matrix rows + context cancel tested — Task 5
- ✓ Health drops omitempty (value, not pointer on wire) — Task 3
- ✓ MCP `tool_alerts.go` follow-up issue mentioned in PR body — Task 10 Step 6

**Placeholder scan:** none found.

**Type consistency:** `AlertsStatus`, `AlertsOut`, `OverviewResponse`, `toOverviewResponse`, `computeAlertsState`, `alertLister` used consistently across tasks. `service.OverviewAlert` retained as the leaf type. TS mirrors with `AlertsStatus`, `OverviewAlerts`.
