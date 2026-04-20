# Service Detail Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a deterministic Service Detail page at `/service/:name` that shows metrics, charts, endpoints, errors, dependencies, and change points for a single service.

**Architecture:** One new `GET /api/service/:name` handler calls `DiagnoseEnhanced()` + `Spans(groupBy=["operation"])` + `QueryRollupBuckets()` and merges results into `ServiceDetailResult`. Frontend renders a vertical-stack page with reusable chart components. All data deterministic — AI only behind Investigate button.

**Tech Stack:** Go/Echo (backend), React/TypeScript/Tailwind (frontend), DuckDB rollups

**Spec:** `docs/service-detail-vision.md`

---

## File Map

### Backend (Go)

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/service/types.go` | Modify | Add `ServiceDetailResult` type, add JSON tags to `DiagnoseResult` fields |
| `internal/service/service_detail.go` | Create | `ServiceDetail()` method: calls DiagnoseEnhanced + Spans + RollupBuckets, merges |
| `internal/service/service_detail_test.go` | Create | Tests for ServiceDetail() |
| `internal/api/ui.go` | Modify | Add `GET /api/service/:name` handler |
| `internal/api/ui_test.go` | Modify | Add handler tests |

### Frontend (React/TypeScript)

| File | Action | Responsibility |
|------|--------|----------------|
| `client/src/lib/types.ts` | Modify | Add ServiceDetailResponse types |
| `client/src/pages/ServicePage.tsx` | Create | Page shell: fetch, time window, layout |
| `client/src/components/service/ServiceHeader.tsx` | Create | Name, badge, back link, Investigate |
| `client/src/components/service/MetricCards.tsx` | Create | 4 metric cards with baseline |
| `client/src/components/service/ServiceChart.tsx` | Create | Timeseries chart (error rate / latency) |
| `client/src/components/service/EndpointTable.tsx` | Create | Endpoint table with sorting |
| `client/src/components/service/ErrorList.tsx` | Create | Top errors with trace links |
| `client/src/components/service/DependencyList.tsx` | Create | Downstream services, clickable |
| `client/src/components/service/ChangePoints.tsx` | Create | Detected metric shifts |
| `client/src/App.tsx` | Modify | Add `/service/:name` route |

---

## Task 1: Add JSON Tags to DiagnoseResult + ServiceDetailResult Type

**Files:**
- Modify: `internal/service/types.go`

- [ ] **Step 1: Add JSON tags to DiagnoseResult fields**

The top-level `DiagnoseResult` fields lack JSON tags (PascalCase by default). Add snake_case tags to match Home's convention. Also add tags to `SlowOp`, `Dependency`, and `ErrorInfo`:

```go
type DiagnoseResult struct {
	Service       string  `json:"service"`
	Status        string  `json:"status"`
	WindowMinutes int     `json:"window_minutes"`
	P50Ms         float64 `json:"p50_ms"`
	P95Ms         float64 `json:"p95_ms"`
	P99Ms         float64 `json:"p99_ms"`
	ErrorRate     float64 `json:"error_rate"`
	SpanCount     int64   `json:"span_count"`
	TopErrors     []ErrorInfo  `json:"top_errors"`
	SlowOps       []SlowOp     `json:"slow_ops"`
	Dependencies  []Dependency `json:"dependencies"`

	// Enhanced fields (populated by DiagnoseEnhanced)
	SymptomDetected       string              `json:"symptom_detected,omitempty"`
	Baseline              *BaselineComparison `json:"comparison_to_baseline,omitempty"`
	ChangePoints          []ChangePoint       `json:"change_points,omitempty"`
	CorrelatedLogPatterns []LogPattern        `json:"correlated_log_patterns,omitempty"`
	SuggestedTraces       []string            `json:"suggested_traces,omitempty"`
}

type SlowOp struct {
	Name      string  `json:"name"`
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
	P99Ms     float64 `json:"p99_ms"`
	ErrorRate float64 `json:"error_rate"`
	Count     int64   `json:"count"`
}

type Dependency struct {
	Service   string  `json:"service"`
	CallCount int64   `json:"call_count"`
	ErrorRate float64 `json:"error_rate"`
	AvgMs     float64 `json:"avg_ms"`
}
```

- [ ] **Step 2: Add ServiceDetailResult type**

Add after the DiagnoseResult types:

```go
// ServiceDetailResult is the response for the Service Detail page.
// It wraps DiagnoseEnhanced output with rollup buckets and endpoint groups.
type ServiceDetailResult struct {
	Diagnose  DiagnoseResult       `json:"diagnose"`
	Endpoints []ServiceEndpoint    `json:"endpoints"`
	Buckets   []ServiceBucket      `json:"buckets"`
}

// ServiceEndpoint is a per-operation summary from span group-by.
type ServiceEndpoint struct {
	Operation  string   `json:"operation"`
	Count      int64    `json:"count"`
	ErrorRate  float64  `json:"error_rate"`
	P50Ms      float64  `json:"p50_ms"`
	P95Ms      float64  `json:"p95_ms"`
	P99Ms      float64  `json:"p99_ms"`
	ExemplarID string   `json:"exemplar_id,omitempty"`
}

// ServiceBucket is a per-minute rollup point for charts.
type ServiceBucket struct {
	Time      string  `json:"time"`
	ErrorRate float64 `json:"error_rate"`
	P95Ms     float64 `json:"p95_ms"`
	P50Ms     float64 `json:"p50_ms"`
	Spans     int64   `json:"spans"`
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/v/Projects/labstack/fanout && go build ./internal/service/...`

- [ ] **Step 4: Commit**

```bash
git add internal/service/types.go
git commit -m "feat(service-detail): add ServiceDetailResult type and JSON tags"
```

---

## Task 2: ServiceDetail() Backend Method

**Files:**
- Create: `internal/service/service_detail.go`
- Create: `internal/service/service_detail_test.go`

- [ ] **Step 1: Create service_detail.go**

```go
package service

import (
	"context"
	"fmt"
	"time"
)

// ServiceDetail assembles all data for the Service Detail page.
// It calls DiagnoseEnhanced, Spans(groupBy=operation), and QueryRollupBuckets,
// merging the results into a single ServiceDetailResult.
func (s *Service) ServiceDetail(ctx context.Context, svcName string, window int, namespace, tenantID string) (*ServiceDetailResult, error) {
	if window <= 0 {
		window = 60
	}

	// 1. DiagnoseEnhanced — metrics, errors, deps, change points, baseline
	diag, err := s.DiagnoseEnhanced(ctx, svcName, window, "", namespace, tenantID)
	if err != nil {
		return nil, fmt.Errorf("service detail diagnose: %w", err)
	}

	// 2. Spans grouped by operation — full endpoint list
	spanResult, err := s.Spans(ctx, SpanParams{
		Service:          svcName,
		GroupBy:          []string{"operation"},
		IncludeExemplars: true,
		Window:           window,
		Namespace:        namespace,
		TenantID:         tenantID,
		Limit:            50,
	})
	if err != nil {
		return nil, fmt.Errorf("service detail spans: %w", err)
	}

	endpoints := make([]ServiceEndpoint, 0, len(spanResult.Groups))
	for _, g := range spanResult.Groups {
		ep := ServiceEndpoint{
			Operation: g.Key["operation"],
			Count:     g.Count,
			ErrorRate: g.ErrorRate,
			P50Ms:     g.P50Ms,
			P95Ms:     g.P95Ms,
			P99Ms:     g.P99Ms,
		}
		if len(g.ExemplarTraceIDs) > 0 {
			ep.ExemplarID = g.ExemplarTraceIDs[0]
		}
		endpoints = append(endpoints, ep)
	}

	// 3. Rollup buckets — per-minute timeseries for charts
	now := time.Now().UTC()
	start := now.Add(-time.Duration(window) * time.Minute)
	rollupBuckets, err := s.QueryRollupBuckets(ctx, svcName, start, now)
	if err != nil {
		// Non-fatal: return empty buckets, page still works without charts
		rollupBuckets = nil
	}

	buckets := make([]ServiceBucket, 0, len(rollupBuckets))
	for i, rb := range rollupBuckets {
		t := start.Add(time.Duration(i) * time.Minute)
		buckets = append(buckets, ServiceBucket{
			Time:      t.Format(time.RFC3339),
			ErrorRate: rb.ErrorRate,
			P95Ms:     rb.P95Ms,
			P50Ms:     rb.P50Ms,
			Spans:     rb.Spans,
		})
	}

	return &ServiceDetailResult{
		Diagnose:  *diag,
		Endpoints: endpoints,
		Buckets:   buckets,
	}, nil
}
```

- [ ] **Step 2: Create service_detail_test.go**

```go
package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestServiceDetail_ReturnsResult(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// DiagnoseEnhanced internal queries: main diagnose query
	diagRows := sqlmock.NewRows([]string{"p50_ms", "p95_ms", "p99_ms", "error_rate", "span_count"}).
		AddRow(10.0, 100.0, 200.0, 0.01, int64(1000))
	mock.ExpectQuery("SELECT").WillReturnRows(diagRows)

	// Top errors query
	errRows := sqlmock.NewRows([]string{"operation", "message", "exception_type", "cnt", "trace_id"})
	mock.ExpectQuery("SELECT").WillReturnRows(errRows)

	// Slow ops query
	slowRows := sqlmock.NewRows([]string{"operation", "p50_ms", "p95_ms", "p99_ms", "error_rate", "cnt"})
	mock.ExpectQuery("SELECT").WillReturnRows(slowRows)

	// Dependencies query
	depRows := sqlmock.NewRows([]string{"service", "call_count", "error_rate", "avg_ms"})
	mock.ExpectQuery("SELECT").WillReturnRows(depRows)

	// Baseline query
	baselineRows := sqlmock.NewRows([]string{"p95_ms"})
	mock.ExpectQuery("SELECT").WillReturnRows(baselineRows)

	// Change points query
	cpRows := sqlmock.NewRows([]string{"p95_ms", "p50_ms", "error_rate", "total_spans"})
	mock.ExpectQuery("SELECT").WillReturnRows(cpRows)

	// Spans grouped by operation
	spanGroupRows := sqlmock.NewRows([]string{"operation", "cnt", "error_count", "error_rate", "p50_ms", "p95_ms", "p99_ms", "exemplar_trace_ids"}).
		AddRow("POST /checkout", int64(500), int64(50), 0.1, 45.0, 890.0, 1200.0, `["abc123"]`).
		AddRow("GET /cart", int64(300), int64(1), 0.003, 12.0, 45.0, 80.0, `["def456"]`)
	mock.ExpectQuery("SELECT").WillReturnRows(spanGroupRows)

	// Rollup buckets
	bucketRows := sqlmock.NewRows([]string{"p95_ms", "p50_ms", "error_rate", "total_spans"}).
		AddRow(100.0, 10.0, 0.01, int64(100)).
		AddRow(120.0, 12.0, 0.02, int64(110))
	mock.ExpectQuery("SELECT").WillReturnRows(bucketRows)

	result, err := svc.ServiceDetail(context.Background(), "checkout-service", 60, "", "")
	if err != nil {
		t.Fatalf("ServiceDetail() error = %v", err)
	}

	if result.Diagnose.Service != "checkout-service" {
		t.Errorf("service = %q, want checkout-service", result.Diagnose.Service)
	}
	if len(result.Endpoints) != 2 {
		t.Errorf("endpoints = %d, want 2", len(result.Endpoints))
	}
	if len(result.Buckets) != 2 {
		t.Errorf("buckets = %d, want 2", len(result.Buckets))
	}
}

func TestServiceDetail_DiagnoseError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	_, err := svc.ServiceDetail(context.Background(), "bad-service", 60, "", "")
	if err == nil {
		t.Fatal("expected error from diagnose failure")
	}
}
```

Add `"fmt"` to imports in the test file.

- [ ] **Step 3: Run tests**

Run: `cd /Users/v/Projects/labstack/fanout && go test ./internal/service/ -run TestServiceDetail -v`

Note: The mock expectations may not match DiagnoseEnhanced's exact query pattern. Adjust mock expectations to match what sqlmock sees. The important thing is that the method compiles and the wiring logic works.

- [ ] **Step 4: Commit**

```bash
git add internal/service/service_detail.go internal/service/service_detail_test.go
git commit -m "feat(service-detail): add ServiceDetail() method"
```

---

## Task 3: API Handler

**Files:**
- Modify: `internal/api/ui.go`
- Modify: `internal/api/ui_test.go`

- [ ] **Step 1: Add ServiceDetail handler to ui.go**

Add route registration in `RegisterUIRoutes` after the `e.GET("/api/overview", h.Overview)` line:

```go
e.GET("/api/service/:name", h.ServiceDetail)
```

Add the handler method:

```go
// ServiceDetail returns deterministic data for the Service Detail page.
func (h *UIHandler) ServiceDetail(c *echo.Context) error {
	if h.svc == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "service layer not configured")
	}

	name := c.Param("name")
	if strings.TrimSpace(name) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "service name required")
	}

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

	result, err := h.svc.ServiceDetail(c.Request().Context(), name, window, c.QueryParam("namespace"), "")
	if err != nil {
		slog.Error("service detail failed", "service", name, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load service detail")
	}

	return c.JSON(http.StatusOK, result)
}
```

- [ ] **Step 2: Add handler tests to ui_test.go**

```go
func TestServiceDetail_NilService(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/service/test-svc", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("name")
	c.SetParamValues("test-svc")

	h := &UIHandler{}
	err := h.ServiceDetail(c)
	if err == nil {
		t.Fatal("expected error for nil service")
	}
	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 503 {
		t.Errorf("code = %d, want 503", httpErr.Code)
	}
}

func TestServiceDetail_EmptyName(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/service/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("name")
	c.SetParamValues("")

	h := &UIHandler{}
	err := h.ServiceDetail(c)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 400 {
		t.Errorf("code = %d, want 400", httpErr.Code)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/v/Projects/labstack/fanout && go build ./... && go test ./internal/api/ -v`

- [ ] **Step 4: Commit**

```bash
git add internal/api/ui.go internal/api/ui_test.go
git commit -m "feat(service-detail): add GET /api/service/:name handler"
```

---

## Task 4: Frontend Types

**Files:**
- Modify: `client/src/lib/types.ts`

- [ ] **Step 1: Add ServiceDetail TypeScript types**

Add at the end of `types.ts`:

```typescript
// ── Service Detail types ────────────────────────────────────

export interface ServiceDetailResponse {
  diagnose: DiagnoseResult;
  endpoints: ServiceEndpoint[];
  buckets: ServiceBucket[];
}

export interface DiagnoseResult {
  service: string;
  status: string;
  window_minutes: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  error_rate: number;
  span_count: number;
  top_errors: ErrorInfo[];
  slow_ops: SlowOp[];
  dependencies: ServiceDependency[];
  symptom_detected?: string;
  comparison_to_baseline?: BaselineComparison;
  change_points?: ChangePoint[];
  correlated_log_patterns?: LogPattern[];
  suggested_traces?: string[];
}

export interface ErrorInfo {
  operation: string;
  message: string;
  exception_type?: string;
  count: number;
  trace_id: string;
}

export interface SlowOp {
  name: string;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  error_rate: number;
  count: number;
}

export interface ServiceDependency {
  service: string;
  call_count: number;
  error_rate: number;
  avg_ms: number;
}

export interface BaselineComparison {
  p95_ratio: number;
  baseline_p95_ms: number;
  baseline_window: string;
}

export interface ChangePoint {
  time: string;
  metric: string;
  before: number;
  after: number;
}

export interface LogPattern {
  pattern: string;
  count: number;
  severity: string;
}

export interface ServiceEndpoint {
  operation: string;
  count: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  exemplar_id?: string;
}

export interface ServiceBucket {
  time: string;
  error_rate: number;
  p95_ms: number;
  p50_ms: number;
  spans: number;
}
```

- [ ] **Step 2: Verify**

Run: `cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit`

- [ ] **Step 3: Commit**

```bash
git add client/src/lib/types.ts
git commit -m "feat(service-detail): add ServiceDetail TypeScript types"
```

---

## Task 5: ServiceHeader Component

**Files:**
- Create: `client/src/components/service/ServiceHeader.tsx`

- [ ] **Step 1: Create ServiceHeader**

```tsx
import { ArrowLeft, Search } from "lucide-react";
import { Link } from "react-router";

interface Props {
  name: string;
  status: string;
  symptom?: string;
  onInvestigate: () => void;
}

export function ServiceHeader({ name, status, symptom, onInvestigate }: Props) {
  const isUnhealthy = status === "unhealthy";
  const isDegraded = status === "degraded";
  const statusCls = isUnhealthy
    ? "text-unhealthy"
    : isDegraded
      ? "text-degraded"
      : "text-healthy";
  const borderCls = isUnhealthy
    ? "border-unhealthy/20 bg-unhealthy/10"
    : isDegraded
      ? "border-degraded/20 bg-degraded/10"
      : "border-healthy/20 bg-healthy/10";

  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
      <div className="flex items-center gap-3 min-w-0 flex-wrap">
        <Link
          to="/"
          className="text-xs text-muted-foreground hover:text-foreground transition-colors mono flex items-center gap-1"
        >
          <ArrowLeft className="h-3 w-3" />
          Home
        </Link>
        <span className="font-heading text-xl font-bold text-foreground truncate">
          {name}
        </span>
        <span
          className={`inline-flex rounded-full border px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${statusCls} ${borderCls}`}
        >
          {status}
        </span>
        {symptom && (
          <span className="text-[11px] text-muted-foreground mono">
            {symptom.replace(/_/g, " ")}
          </span>
        )}
      </div>
      <button
        type="button"
        onClick={onInvestigate}
        className="btn-primary inline-flex items-center gap-1.5 text-xs shrink-0"
      >
        <Search className="h-3 w-3" />
        Investigate
      </button>
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add client/src/components/service/ServiceHeader.tsx
git commit -m "feat(service-detail): add ServiceHeader component"
```

---

## Task 6: MetricCards Component

**Files:**
- Create: `client/src/components/service/MetricCards.tsx`

- [ ] **Step 1: Create MetricCards**

```tsx
import type { BaselineComparison } from "@/lib/types";

function fmtVal(v: number, unit: string): string {
  if (unit === "pct") return `${(v * 100).toFixed(1)}%`;
  if (unit === "ms") return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v.toFixed(0)}ms`;
  if (unit === "count") return v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(0);
  return v.toFixed(1);
}

function colorCls(label: string, v: number): string {
  if (label === "Error Rate") {
    if (v > 0.1) return "text-unhealthy";
    if (v > 0.01) return "text-degraded";
    return "text-foreground";
  }
  if (label === "P95" || label === "P50") {
    if (v > 5000) return "text-unhealthy";
    if (v > 1000) return "text-degraded";
    return "text-foreground";
  }
  return "text-foreground";
}

interface Props {
  errorRate: number;
  p95Ms: number;
  p50Ms: number;
  spanCount: number;
  windowMinutes: number;
  baseline?: BaselineComparison;
}

export function MetricCards({ errorRate, p95Ms, p50Ms, spanCount, windowMinutes, baseline }: Props) {
  const cards = [
    { label: "Error Rate", value: errorRate, unit: "pct" as const },
    { label: "P95", value: p95Ms, unit: "ms" as const },
    { label: "P50", value: p50Ms, unit: "ms" as const },
    { label: "Traffic", value: spanCount / windowMinutes, unit: "count" as const, suffix: "spans/min" },
  ];

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {cards.map((c) => (
        <div
          key={c.label}
          className="rounded-lg border border-border/60 bg-surface-1/80 p-3"
        >
          <div className="detail-label mb-1">{c.label}</div>
          <div className={`font-heading text-xl font-bold ${colorCls(c.label, c.value)}`}>
            {fmtVal(c.value, c.unit)}
          </div>
          {c.suffix && (
            <div className="text-[10px] text-muted-foreground mono">{c.suffix}</div>
          )}
          {c.label === "P95" && baseline && baseline.baseline_p95_ms > 0 && (
            <div className="text-[10px] text-muted-foreground mono mt-1">
              baseline {fmtVal(baseline.baseline_p95_ms, "ms")}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add client/src/components/service/MetricCards.tsx
git commit -m "feat(service-detail): add MetricCards component"
```

---

## Task 7: ServiceChart Component

**Files:**
- Create: `client/src/components/service/ServiceChart.tsx`

- [ ] **Step 1: Create ServiceChart**

A simple SVG timeseries chart with optional change point markers and baseline line.

```tsx
import type { ServiceBucket, ChangePoint } from "@/lib/types";

interface Props {
  title: string;
  buckets: ServiceBucket[];
  metric: "error_rate" | "p95_ms";
  color: string;
  changePoints?: ChangePoint[];
  baselineValue?: number;
}

function fmtVal(v: number, metric: string): string {
  if (metric === "error_rate") return `${(v * 100).toFixed(1)}%`;
  return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v.toFixed(0)}ms`;
}

export function ServiceChart({ title, buckets, metric, color, changePoints, baselineValue }: Props) {
  if (!buckets || buckets.length < 2) {
    return (
      <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
        <div className="detail-label mb-2">{title}</div>
        <div className="text-sm text-muted-foreground">No data</div>
      </div>
    );
  }

  const values = buckets.map((b) => (metric === "error_rate" ? b.error_rate : b.p95_ms));
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;

  const W = 400;
  const H = 100;
  const padY = 8;
  const padX = 4;

  const points = values
    .map((v, i) => {
      const x = (i / (values.length - 1)) * (W - padX * 2) + padX;
      const y = H - padY - ((v - min) / range) * (H - padY * 2);
      return `${x},${y}`;
    })
    .join(" ");

  // Map change points to x positions
  const cpLines: { x: number; label: string }[] = [];
  if (changePoints) {
    const metricName = metric === "error_rate" ? "error_rate" : "p95";
    for (const cp of changePoints) {
      if (!cp.metric.includes(metricName)) continue;
      const cpTime = new Date(cp.time).getTime();
      const startTime = new Date(buckets[0].time).getTime();
      const endTime = new Date(buckets[buckets.length - 1].time).getTime();
      const totalRange = endTime - startTime || 1;
      const frac = (cpTime - startTime) / totalRange;
      if (frac >= 0 && frac <= 1) {
        const x = frac * (W - padX * 2) + padX;
        const ratio = cp.before > 0 ? (cp.after / cp.before).toFixed(1) : "n/a";
        cpLines.push({ x, label: `${ratio}x` });
      }
    }
  }

  // Baseline horizontal line
  let baselineY: number | null = null;
  if (baselineValue !== undefined && baselineValue > 0) {
    baselineY = H - padY - ((baselineValue - min) / range) * (H - padY * 2);
  }

  const lastVal = values[values.length - 1];

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="flex items-center justify-between mb-2">
        <div className="detail-label">{title}</div>
        <div className="text-sm mono" style={{ color }}>
          {fmtVal(lastVal, metric)}
        </div>
      </div>
      <svg width="100%" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="overflow-visible">
        {/* Baseline */}
        {baselineY !== null && (
          <line
            x1={padX}
            y1={baselineY}
            x2={W - padX}
            y2={baselineY}
            stroke={color}
            strokeWidth={0.5}
            strokeDasharray="4,4"
            opacity={0.3}
          />
        )}
        {/* Main line */}
        <polyline
          points={points}
          fill="none"
          stroke={color}
          strokeWidth={2}
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        {/* Change point markers */}
        {cpLines.map((cp, i) => (
          <g key={i}>
            <line
              x1={cp.x}
              y1={0}
              x2={cp.x}
              y2={H}
              stroke="var(--primary)"
              strokeWidth={1}
              strokeDasharray="3,3"
              opacity={0.5}
            />
            <text
              x={cp.x + 3}
              y={12}
              fill="var(--primary)"
              fontSize={9}
              fontFamily="monospace"
            >
              {cp.label}
            </text>
          </g>
        ))}
      </svg>
      <div className="flex justify-between text-[10px] text-muted-foreground mono mt-1">
        <span>-{buckets.length}m</span>
        <span>now</span>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add client/src/components/service/ServiceChart.tsx
git commit -m "feat(service-detail): add ServiceChart timeseries component"
```

---

## Task 8: EndpointTable, ErrorList, DependencyList, ChangePoints

**Files:**
- Create: `client/src/components/service/EndpointTable.tsx`
- Create: `client/src/components/service/ErrorList.tsx`
- Create: `client/src/components/service/DependencyList.tsx`
- Create: `client/src/components/service/ChangePoints.tsx`

- [ ] **Step 1: Create EndpointTable**

```tsx
import type { ServiceEndpoint } from "@/lib/types";

function fmtMs(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}s`;
  return `${v.toFixed(0)}ms`;
}

function fmtPercent(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

function fmtRate(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

interface Props {
  endpoints: ServiceEndpoint[];
  windowMinutes: number;
  onClickEndpoint: (op: string) => void;
}

export function EndpointTable({ endpoints, windowMinutes, onClickEndpoint }: Props) {
  if (endpoints.length === 0) return null;

  const sorted = [...endpoints].sort((a, b) => b.error_rate - a.error_rate);

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Top Endpoints</div>
      <table className="w-full">
        <thead>
          <tr>
            <th className="text-left p-2">Operation</th>
            <th className="text-right p-2">Rate</th>
            <th className="text-right p-2">Errors</th>
            <th className="text-right p-2">P50</th>
            <th className="text-right p-2">P95</th>
          </tr>
        </thead>
        <tbody>
          {sorted.slice(0, 20).map((ep) => (
            <tr
              key={ep.operation}
              className="cursor-pointer hover:bg-surface-2 transition-colors"
              onClick={() => onClickEndpoint(ep.operation)}
            >
              <td className="p-2 text-sm text-foreground/90 mono truncate max-w-[300px]">
                {ep.operation}
              </td>
              <td className="p-2 text-right text-xs text-muted-foreground mono">
                {fmtRate(ep.count / windowMinutes)}/min
              </td>
              <td className={`p-2 text-right text-xs mono ${ep.error_rate > 0.05 ? "text-unhealthy" : ep.error_rate > 0.01 ? "text-degraded" : "text-foreground/70"}`}>
                {fmtPercent(ep.error_rate)}
              </td>
              <td className="p-2 text-right text-xs text-foreground/70 mono">
                {fmtMs(ep.p50_ms)}
              </td>
              <td className={`p-2 text-right text-xs mono ${ep.p95_ms > 1000 ? "text-degraded" : "text-foreground/70"}`}>
                {fmtMs(ep.p95_ms)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

- [ ] **Step 2: Create ErrorList**

```tsx
import type { ErrorInfo } from "@/lib/types";

interface Props {
  errors: ErrorInfo[];
  onClickTrace: (traceID: string) => void;
}

export function ErrorList({ errors, onClickTrace }: Props) {
  if (!errors || errors.length === 0) return null;

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Top Errors</div>
      <div className="space-y-2">
        {errors.slice(0, 10).map((err, i) => (
          <div key={`${err.operation}-${i}`} className="flex items-baseline gap-3 text-sm">
            <span className="mono text-muted-foreground tabular-nums w-12 text-right shrink-0">
              {err.count.toLocaleString()}
            </span>
            <div className="min-w-0 flex-1">
              <div className="text-foreground/80 mono text-xs truncate" title={err.message}>
                {err.message}
              </div>
              <div className="flex items-center gap-2 text-[10px] text-muted-foreground mt-0.5">
                <span>{err.operation}</span>
                {err.trace_id && (
                  <button
                    type="button"
                    onClick={() => onClickTrace(err.trace_id)}
                    className="text-primary hover:underline cursor-pointer"
                  >
                    {err.trace_id.slice(0, 8)}
                  </button>
                )}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Create DependencyList**

```tsx
import { Link } from "react-router";
import type { ServiceDependency } from "@/lib/types";

function fmtMs(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}s`;
  return `${v.toFixed(0)}ms`;
}

function fmtPercent(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

function fmtRate(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

interface Props {
  dependencies: ServiceDependency[];
}

export function DependencyList({ dependencies }: Props) {
  if (!dependencies || dependencies.length === 0) return null;

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Dependencies</div>
      <div className="space-y-1">
        {dependencies.map((dep) => (
          <Link
            key={dep.service}
            to={`/service/${encodeURIComponent(dep.service)}`}
            className="flex items-center justify-between py-2 px-1 rounded-md hover:bg-surface-2 transition-colors group"
          >
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">→</span>
              <span className="text-sm text-foreground/90 group-hover:text-foreground mono">
                {dep.service}
              </span>
            </div>
            <div className="flex items-center gap-4 text-xs mono">
              <span className="text-muted-foreground">
                {fmtRate(dep.call_count)}/min
              </span>
              <span className="text-muted-foreground">{fmtMs(dep.avg_ms)}</span>
              <span className={dep.error_rate > 0.01 ? "text-unhealthy" : "text-foreground/70"}>
                {fmtPercent(dep.error_rate)}
              </span>
            </div>
          </Link>
        ))}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Create ChangePoints**

```tsx
import type { ChangePoint } from "@/lib/types";

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
}

interface Props {
  changePoints: ChangePoint[];
}

export function ChangePointList({ changePoints }: Props) {
  if (!changePoints || changePoints.length === 0) return null;

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Change Points</div>
      <div className="space-y-1">
        {changePoints.map((cp, i) => {
          const ratio = cp.before > 0 ? cp.after / cp.before : 0;
          const direction = ratio > 1 ? "+" : "";
          const color = cp.metric.includes("error") ? "text-unhealthy" : "text-degraded";

          return (
            <div key={`${cp.time}-${cp.metric}-${i}`} className="flex items-center justify-between py-1 text-sm">
              <span className="text-primary mono text-xs">{fmtTime(cp.time)}</span>
              <span className={`mono text-xs ${color}`}>
                {cp.metric.replace(/_/g, " ")} {direction}{ratio.toFixed(1)}x
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
```

- [ ] **Step 5: Verify TypeScript**

Run: `cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit`

- [ ] **Step 6: Commit**

```bash
git add client/src/components/service/
git commit -m "feat(service-detail): add EndpointTable, ErrorList, DependencyList, ChangePoints"
```

---

## Task 9: ServicePage + Router

**Files:**
- Create: `client/src/pages/ServicePage.tsx`
- Modify: `client/src/App.tsx`

- [ ] **Step 1: Create ServicePage**

```tsx
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useLocation, useParams } from "react-router";
import { api, ApiError, setApiToken } from "@/api/client";
import type { ServiceDetailResponse } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";
import { ServiceHeader } from "@/components/service/ServiceHeader";
import { MetricCards } from "@/components/service/MetricCards";
import { ServiceChart } from "@/components/service/ServiceChart";
import { EndpointTable } from "@/components/service/EndpointTable";
import { ErrorList } from "@/components/service/ErrorList";
import { DependencyList } from "@/components/service/DependencyList";
import { ChangePointList } from "@/components/service/ChangePoints";

const REFRESH_INTERVAL = 30_000;

export function ServicePage() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  const [data, setData] = useState<ServiceDetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [timeWindow, setTimeWindow] = useState(60);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [staleSeconds, setStaleSeconds] = useState(0);
  const lastFetch = useRef(0);

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  useEffect(() => {
    if (!name) return;
    let cancelled = false;

    async function load() {
      try {
        const result = await api<ServiceDetailResponse>(
          `/api/service/${encodeURIComponent(name!)}?window=${timeWindow}`,
        );
        if (!cancelled) {
          setData(result);
          setLoading(false);
          setFetchError(null);
          lastFetch.current = Date.now();
          setStaleSeconds(0);
        }
      } catch (err) {
        if (!cancelled) {
          setLoading(false);
          setFetchError(
            err instanceof ApiError ? `Failed to load: ${err.message}` : "Failed to load service data",
          );
          console.error("Service detail fetch failed:", err);
        }
      }
    }

    load();
    const interval = setInterval(load, REFRESH_INTERVAL);
    const staleTick = setInterval(() => {
      if (lastFetch.current > 0) {
        setStaleSeconds(Math.floor((Date.now() - lastFetch.current) / 1000));
      }
    }, 5_000);

    return () => {
      cancelled = true;
      clearInterval(interval);
      clearInterval(staleTick);
    };
  }, [name, timeWindow]);

  const openChat = (prompt: string) => navigate(buildChatPath(prompt, token));

  const windowOptions = [
    { label: "15m", value: 15 },
    { label: "1h", value: 60 },
    { label: "6h", value: 360 },
    { label: "24h", value: 1440 },
  ];

  if (loading && !data) {
    return (
      <div className="px-4 py-6 sm:px-6">
        <div className="mx-auto max-w-5xl space-y-4">
          <div className="h-10 shimmer rounded-lg" />
          <div className="h-24 shimmer rounded-lg" />
          <div className="h-40 shimmer rounded-lg" />
          <div className="h-60 shimmer rounded-lg" />
        </div>
      </div>
    );
  }

  if (!data && fetchError) {
    return (
      <div className="flex items-center justify-center min-h-[60vh]">
        <div className="text-center max-w-md space-y-4 fade-up">
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-5 py-4 text-sm text-unhealthy/90 mono">
            {fetchError}
          </div>
          <button type="button" onClick={() => globalThis.location.reload()} className="btn-ghost text-xs">
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!data) return null;

  const d = data.diagnose;
  const isStale = staleSeconds >= 60;

  return (
    <div className="px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-5xl space-y-4 fade-up">
        {/* Time range + freshness */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1 rounded-full border border-border/60 bg-surface-1/70 p-1">
            {windowOptions.map((opt) => (
              <button
                key={opt.value}
                type="button"
                onClick={() => setTimeWindow(opt.value)}
                className={`rounded-full px-3 py-1 text-[11px] mono transition-colors ${
                  timeWindow === opt.value
                    ? "bg-primary/12 text-foreground"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
          <div className={`flex items-center gap-1.5 text-[11px] mono ${isStale ? "text-degraded/70" : "text-muted-foreground/50"}`}>
            {!isStale && <span className="inline-block w-1.5 h-1.5 rounded-full bg-healthy/60" />}
            {staleSeconds < 10 ? "Live" : staleSeconds < 60 ? `${staleSeconds}s ago` : `${Math.floor(staleSeconds / 60)}m ago`}
          </div>
        </div>

        {fetchError && (
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-sm text-unhealthy/90 mono">
            {fetchError}
          </div>
        )}

        <ServiceHeader
          name={d.service}
          status={d.status}
          symptom={d.symptom_detected}
          onInvestigate={() => openChat(`Investigate ${d.service} — ${d.status}, error rate ${(d.error_rate * 100).toFixed(1)}%, p95 ${d.p95_ms.toFixed(0)}ms. What's the root cause?`)}
        />

        <MetricCards
          errorRate={d.error_rate}
          p95Ms={d.p95_ms}
          p50Ms={d.p50_ms}
          spanCount={d.span_count}
          windowMinutes={d.window_minutes}
          baseline={d.comparison_to_baseline}
        />

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <ServiceChart
            title="Error Rate"
            buckets={data.buckets}
            metric="error_rate"
            color="var(--unhealthy)"
            changePoints={d.change_points}
          />
          <ServiceChart
            title="P95 Latency"
            buckets={data.buckets}
            metric="p95_ms"
            color="var(--degraded)"
            changePoints={d.change_points}
            baselineValue={d.comparison_to_baseline?.baseline_p95_ms}
          />
        </div>

        <EndpointTable
          endpoints={data.endpoints}
          windowMinutes={d.window_minutes}
          onClickEndpoint={(op) => openChat(`Investigate the ${op} endpoint on ${d.service} — show me error traces and latency breakdown.`)}
        />

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <ErrorList
            errors={d.top_errors}
            onClickTrace={(id) => openChat(`Show me trace ${id} for ${d.service}.`)}
          />
          <DependencyList dependencies={d.dependencies} />
        </div>

        <ChangePointList changePoints={d.change_points ?? []} />
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Add route to App.tsx**

Add import:
```tsx
import { ServicePage } from "./pages/ServicePage";
```

Add route after the index route:
```tsx
<Route path="/service/:name" element={<ServicePage />} />
```

- [ ] **Step 3: Verify TypeScript**

Run: `cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit`

- [ ] **Step 4: Commit**

```bash
git add client/src/pages/ServicePage.tsx client/src/App.tsx
git commit -m "feat(service-detail): add ServicePage with router integration"
```

---

## Task 10: Wire Home → Service Detail Navigation

**Files:**
- Modify: `client/src/pages/HomePage.tsx`
- Modify: `client/src/components/home/ServiceRow.tsx`

- [ ] **Step 1: Update ServiceRow to navigate to /service/:name**

In `ServiceRow.tsx`, change the `onClick` handler. The `onClick` prop currently takes `(service: string) => void`. Change it to navigate to the service detail page:

```tsx
import { useNavigate } from "react-router";
import type { OverviewService } from "@/lib/types";
import { Sparkline } from "./Sparkline";

// ... fmtTraffic, fmtPercent, fmtMs unchanged ...

interface Props {
  service: OverviewService;
}

export function ServiceRow({ service }: Props) {
  const navigate = useNavigate();

  return (
    <button
      type="button"
      onClick={() => navigate(`/service/${encodeURIComponent(service.name)}`)}
      className="w-full flex items-center gap-4 px-4 py-2 rounded-lg text-left transition-colors hover:bg-surface-2 group"
    >
      {/* ... same content as before ... */}
    </button>
  );
}
```

- [ ] **Step 2: Update HomePage to remove onClick prop from ServiceRow**

In `HomePage.tsx`, change:
```tsx
<ServiceRow
  key={svc.name}
  service={svc}
  onClick={(name) =>
    investigate(`Give me an overview of ${name} — key metrics, endpoints, and any concerns.`)
  }
/>
```
To:
```tsx
<ServiceRow key={svc.name} service={svc} />
```

- [ ] **Step 3: Verify and commit**

Run: `cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit`

```bash
git add client/src/pages/HomePage.tsx client/src/components/home/ServiceRow.tsx
git commit -m "feat(service-detail): wire Home service rows to /service/:name"
```

---

## Task 11: Full Build Verification

- [ ] **Step 1: Go tests**

Run: `cd /Users/v/Projects/labstack/fanout && go test ./internal/service/ ./internal/api/ -v`

- [ ] **Step 2: TypeScript**

Run: `cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit`

- [ ] **Step 3: Full build**

Run: `cd /Users/v/Projects/labstack/fanout && just check`

- [ ] **Step 4: Browser test**

Start dev server and test at `https://demo.fanout.test`:
1. Home page loads, click any service → navigates to `/service/:name`
2. Service Detail shows header, metric cards, charts, endpoints, errors, deps
3. Time range selector works
4. "Investigate" button opens chat with context
5. Click a dependency → navigates to that service's detail page
6. Click a trace ID → opens chat
7. Back link returns to Home
