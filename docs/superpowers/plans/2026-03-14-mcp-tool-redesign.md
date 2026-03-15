# MCP Tool Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the MCP tool surface from 9 tools to 10, with JSON-only output, DuckDB views for clean SQL, signal-specific exploration tools, and server-side aggregation via `group_by`.

**Architecture:** Incremental evolution of existing `internal/mcp/`, `internal/service/`, and `internal/query/` layers. Split `find` into `spans`/`logs`/`metrics`, rename `status` to `overview`, merge `schema` into `query`, add DuckDB views at startup, remove rendering from MCP responses.

**Tech Stack:** Go, DuckDB (CGO), Parquet, gRPC, Echo, MCP SDK

**Spec:** `docs/superpowers/specs/2026-03-14-mcp-tool-redesign.md`

---

## Chunk 1: Foundation — DuckDB Views, Window Parsing, Shared Infrastructure

### Task 1: DuckDB Views and `attr()` Macro

Create clean-name views over Parquet files at startup so the `query` tool and internal queries can use `service` instead of `"name=service_name"`.

**Files:**
- Modify: `internal/query/duck.go` — add view creation in `NewDuck()` after rollup table creation
- Create: `internal/query/views.go` — view SQL definitions
- Create: `internal/query/views_test.go` — verify views are created

- [ ] **Step 1: Write views_test.go**

Test that `NewDuck()` creates the `spans`, `logs`, `metrics` views and the `attr()` macro. Query `information_schema` or run a simple `SELECT * FROM spans LIMIT 0` to verify.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/query/ -run TestViews -v`
Expected: FAIL — views don't exist yet

- [ ] **Step 3: Create views.go with SQL definitions**

Define constants for each view SQL:
- `spanViewSQL` — maps `"name=trace_id"` → `trace_id`, `"name=service_name"` → `service`, etc. Uses `to_timestamp("name=start_unix_nano" / 1e9)` for timestamps, `from_utf8()` for JSON blobs.
- `logViewSQL` — maps log columns similarly.
- `metricViewSQL` — maps metric columns, includes `"name=description"`.
- `attrMacroSQL` — `CREATE OR REPLACE MACRO attr(json_col, key) AS json_extract_string(json_col, '$.' || key)`

All views use `read_parquet('{lake}/{signal}/**/*.parquet', hive_partitioning=true, union_by_name=true)` with the lake dir from config.

- [ ] **Step 4: Wire view creation into NewDuck()**

In `duck.go`, after rollup table creation (line ~65), call `createViews(db, cfg.LakeDir)` from `views.go`. Handle errors gracefully — if no Parquet files exist yet, views should still be created (they'll return empty results).

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/query/ -run TestViews -v`

- [ ] **Step 6: Commit**

```
feat(query): add DuckDB views with clean column names and attr() macro
```

---

### Task 2: Window Parsing — String-Based Windows

Replace integer-minute windows with string-based windows (`"15m"`, `"1h"`, `"7d"`, or ISO range).

**Files:**
- Create: `internal/mcp/window.go` — `parseWindow()` function
- Create: `internal/mcp/window_test.go` — test all formats
- Modify: `internal/mcp/discovery.go` — update shared parameter helpers

- [ ] **Step 1: Write window_test.go**

Test cases:
- `"5m"` → start=now-5min, end=now
- `"1h"` → start=now-1h, end=now
- `"24h"` → start=now-24h, end=now
- `"7d"` → start=now-7d, end=now
- `"2026-03-14T12:00Z/2026-03-14T14:00Z"` → absolute start/end
- `""` → default 15m
- `"invalid"` → error

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestParseWindow -v`

- [ ] **Step 3: Implement parseWindow()**

```go
type TimeWindow struct {
    Start time.Time
    End   time.Time
    Minutes int // for backward compat with service layer
}

func parseWindow(w string) (TimeWindow, error)
```

Parse duration suffixes (`m`, `h`, `d`) and ISO range (split on `/`, parse each side as RFC3339).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestParseWindow -v`

- [ ] **Step 5: Commit**

```
feat(mcp): add string-based window parsing with duration and ISO range support
```

---

### Task 3: Remove Format Parameter and Rendering from MCP Handlers

Strip `format` parameter and render output from all MCP tool handlers. Tools return JSON-only.

**Files:**
- Modify: `internal/mcp/status.go` — remove `Format` from input, remove `Render` from output, delete `renderStatus()`
- Modify: `internal/mcp/diagnose.go` — same pattern
- Modify: `internal/mcp/find.go` — same
- Modify: `internal/mcp/timeline.go` — same
- Modify: `internal/mcp/topology.go` — same
- Modify: `internal/mcp/compare.go` — same
- Modify: `internal/mcp/trace.go` — same
- Modify: `internal/mcp/discovery.go` — remove `parseFormat()` if present
- Modify: All `*_test.go` files — remove format-related assertions

- [ ] **Step 1: Remove format from each handler one at a time**

For each handler file: remove `Format string` from input struct, remove `Render *render.Output` from output struct, delete `renderXxx()` function, delete render import. Update the handler to not call render.

- [ ] **Step 2: Update tests**

Remove format-related test cases. Verify each tool returns plain JSON output.

- [ ] **Step 3: Run all MCP tests**

Run: `go test ./internal/mcp/ -v`

- [ ] **Step 4: Commit**

```
refactor(mcp): remove format parameter and rendering from all MCP tools

JSON-only output. Report system rendering preserved separately for
future UI work.
```

---

## Chunk 2: Tier 1 Tools — Overview and Topology

### Task 4: Rename `status` → `overview` with Health Score

**Files:**
- Rename: `internal/mcp/status.go` → `internal/mcp/overview.go`
- Rename: `internal/mcp/status_test.go` → `internal/mcp/overview_test.go`
- Modify: `internal/mcp/server.go` — update tool registration name
- Modify: `internal/service/status.go` — rename `Status()` → `Overview()`, add health score
- Modify: `internal/service/types.go` — update `StatusResult` → `OverviewResult` with new fields

- [ ] **Step 1: Write test for health score computation**

In `overview_test.go`, test the threshold-based scoring:
- error_rate 0.005, p95 200ms → healthy (score ~1.0)
- error_rate 0.03, p95 1500ms → degraded
- error_rate 0.15, p95 6000ms → unhealthy (score < 0.7)

- [ ] **Step 2: Run test to verify it fails**

- [ ] **Step 3: Update types.go**

New `OverviewResult` struct with:
- `Health` struct: `Score float64`, `TotalServices int`, `ByStatus map[string]int`, `ThroughputPerMin float64`, `GlobalErrorRate float64`, `GlobalP95Ms float64`
- `Services []ServiceInfo` with `Service`, `Status`, `Requests`, `ErrorRate`, `P50Ms`, `P95Ms`
- `TopIssues []TopIssue`
- Add `include` and `sort_services_by` to input params

- [ ] **Step 4: Implement health score in service layer**

Threshold-based per service (from spec):
- `error_score`: 1.0 if <0.01, 0.7 if <0.05, 0.3 if <0.10, 0.0 otherwise
- `latency_score`: 1.0 if p95 <500, 0.7 if <2000, 0.3 if <5000, 0.0 otherwise
- `throughput_score`: 1.0 if spans>0, 0.0 otherwise
- `service_score = error_score*0.4 + latency_score*0.3 + throughput_score*0.3`

- [ ] **Step 5: Rename files and update registration**

Rename status.go → overview.go, update `server.go` tool name from `"status"` to `"overview"`.

- [ ] **Step 6: Run tests and verify**

Run: `go test ./internal/mcp/ -run TestOverview -v && go test ./internal/service/ -v`

- [ ] **Step 7: Commit**

```
feat(mcp): rename status → overview with health score computation
```

---

### Task 5: Enhanced Topology — blast_radius, critical_paths, depth, edge_type

**Files:**
- Modify: `internal/mcp/topology.go` — add `edge_type`, `depth`, `include_inactive` params
- Modify: `internal/mcp/topology_test.go` — test new params
- Modify: `internal/service/topology.go` — add blast_radius, critical_paths computation
- Modify: `internal/service/types.go` — update TopologyResult

- [ ] **Step 1: Write tests for blast_radius and critical_paths**

Test with known edge data: 3 services A→B→C with known call counts. Verify blast_radius for B (the middle node) is highest.

- [ ] **Step 2: Run test to verify it fails**

- [ ] **Step 3: Update types**

Add to node: `UpstreamCount`, `DownstreamCount`, `BlastRadius float64`.
Add to result: `CriticalPaths [][]string`.
Add to edge: `EdgeType string`.

- [ ] **Step 4: Implement blast_radius**

`sum(calls on edges where this node is source or target) / sum(all edge calls)`. Computed from edge_rollup query results.

- [ ] **Step 5: Implement critical_paths**

DFS from root nodes (no callers), weight = `calls * avg_ms`, break cycles at first revisit, max 10 hops, return top 3 paths.

- [ ] **Step 6: Add depth and edge_type filtering**

`depth` — BFS from service node, limit hop count. `edge_type` — WHERE filter on `edge_rollup.edge_type`.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/mcp/ -run TestTopology -v && go test ./internal/service/ -run TestTopology -v`

- [ ] **Step 8: Commit**

```
feat(mcp): enhance topology with blast_radius, critical_paths, depth, edge_type
```

---

## Chunk 3: Tier 2 Tools — Spans, Logs, Metrics

### Task 6: Split `find` → `spans` Tool

**Files:**
- Create: `internal/mcp/spans.go` — new handler
- Create: `internal/mcp/spans_test.go` — tests
- Create: `internal/service/spans.go` — new `Spans()` method with `group_by`
- Modify: `internal/service/types.go` — add `SpanParams`, `SpansResult`, `SpanGroup`

- [ ] **Step 1: Write test for ungrouped spans query**

Query spans with service filter, verify return shape matches spec (trace_id, span_id, service, operation, kind, start_time, duration_ms, status, attributes).

- [ ] **Step 2: Write test for grouped spans query**

`group_by=["service","operation"]` — verify groups have count, error_count, error_rate, p50/p95/p99, exemplar_trace_ids.

- [ ] **Step 3: Run tests to verify they fail**

- [ ] **Step 4: Define types**

```go
type SpanParams struct {
    Query        string
    Operation    string
    Service      string
    Status       string   // "error", "ok", "slow", "all"
    Kind         string
    MinDurationMs *float64
    MaxDurationMs *float64
    Attrs        map[string]string
    GroupBy      []string // fixed fields only
    OrderBy      string
    IncludeExemplars bool
    Window       TimeWindow
    Namespace    string
    TenantID     string
    Limit        int
}
```

- [ ] **Step 5: Implement Spans() in service layer**

Two query paths:
1. **Ungrouped**: `SELECT trace_id, span_id, service, operation, kind, start_time, duration_ms, status, attributes_json FROM spans WHERE ... ORDER BY ... LIMIT N`
2. **Grouped**: `SELECT group_keys, count(*), sum(CASE error), approx_quantile(duration_ms, [0.5,0.95,0.99]) FROM spans WHERE ... GROUP BY group_keys`

For `http.method` and `http.status_code` in group_by, use `json_extract_string(attributes_json, '$.http.method')`.

- [ ] **Step 6: Implement MCP handler**

Register `spans` tool in server.go. Parse params, call service, return JSON.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/mcp/ -run TestSpans -v && go test ./internal/service/ -run TestSpans -v`

- [ ] **Step 8: Commit**

```
feat(mcp): add spans tool with group_by aggregation
```

---

### Task 7: Split `find` → `logs` Tool

**Files:**
- Create: `internal/mcp/logs.go` — new handler
- Create: `internal/mcp/logs_test.go`
- Create: `internal/service/logs.go` — new `Logs()` method
- Modify: `internal/service/types.go` — add `LogParams`, `LogsResult`, `LogGroup`

- [ ] **Step 1: Write tests for ungrouped and grouped logs**

- [ ] **Step 2: Run tests to verify they fail**

- [ ] **Step 3: Define types and implement**

Similar to spans but with log-specific fields: severity, body, trace_id correlation. Group_by supports `service`, `severity`. Grouped results include `sample_bodies` and `sample_trace_ids`.

- [ ] **Step 4: Implement MCP handler and register**

- [ ] **Step 5: Run tests**

Run: `go test ./internal/mcp/ -run TestLogs -v && go test ./internal/service/ -run TestLogs -v`

- [ ] **Step 6: Commit**

```
feat(mcp): add logs tool with group_by and trace_id correlation
```

---

### Task 8: Replace `timeline` → `metrics` Tool

**Files:**
- Create: `internal/mcp/metrics.go` — new handler with `list`/`query` actions
- Create: `internal/mcp/metrics_test.go`
- Create: `internal/service/metrics.go` — new `Metrics()` method
- Modify: `internal/service/types.go` — add `MetricParams`, `MetricsListResult`, `MetricsQueryResult`
- Delete: `internal/mcp/timeline.go`, `internal/mcp/timeline_test.go`
- Delete: `internal/service/timeline.go`

- [ ] **Step 1: Write test for `list` action**

Verify it returns distinct metric names with type, unit, services, description.

- [ ] **Step 2: Write test for `query` action**

Verify timeseries output with datapoints, anomaly detection.

- [ ] **Step 3: Run tests to verify they fail**

- [ ] **Step 4: Implement `list` action**

```sql
SELECT DISTINCT name, type, unit, description, list(DISTINCT service) as services
FROM metrics WHERE time >= ? GROUP BY name, type, unit, description
```

- [ ] **Step 5: Implement `query` action**

Bucket by granularity (`auto`: ≤1h→1m, ≤6h→5m, ≤24h→15m, >24h→1h). Aggregate using the specified function. Run anomaly detection from `internal/intelligence/` on resulting buckets.

- [ ] **Step 6: Register handler, delete timeline files**

- [ ] **Step 7: Run tests**

Run: `go test ./internal/mcp/ -run TestMetrics -v && go test ./internal/service/ -run TestMetrics -v`

- [ ] **Step 8: Commit**

```
feat(mcp): add metrics tool replacing timeline with list/query actions
```

---

### Task 9: Delete `find` Tool

**Files:**
- Delete: `internal/mcp/find.go`, `internal/mcp/find_test.go`
- Delete: `internal/service/find.go`
- Modify: `internal/mcp/server.go` — remove `find` registration

- [ ] **Step 1: Remove find registration from server.go**

- [ ] **Step 2: Delete find.go files from mcp and service layers**

- [ ] **Step 3: Run full test suite to verify nothing depends on find**

Run: `go test ./internal/... -v`

- [ ] **Step 4: Commit**

```
refactor(mcp): remove find tool (replaced by spans, logs, metrics)
```

---

## Chunk 4: Tier 2-3 Tools — Trace, Diagnose, Compare Enhancements

### Task 10: Enhanced Trace — compare_to, include_metrics

**Files:**
- Modify: `internal/mcp/trace.go` — add `compare_to`, `include_metrics` params
- Modify: `internal/mcp/trace_test.go`
- Modify: `internal/service/trace.go` — implement comparison and metric context
- Modify: `internal/service/types.go` — add `TraceComparison`, `MetricContext` types

- [ ] **Step 1: Write test for compare_to**

- [ ] **Step 2: Write test for include_metrics**

- [ ] **Step 3: Implement trace comparison** — fetch both traces, align by operation, compute deltas

- [ ] **Step 4: Implement metric_context** — query service_rollup for 5min window around trace start

- [ ] **Step 5: Run tests**

- [ ] **Step 6: Commit**

```
feat(mcp): enhance trace with compare_to and include_metrics
```

---

### Task 11: Enhanced Diagnose — Baseline, Change Points, Log Correlation

**Files:**
- Modify: `internal/mcp/diagnose.go` — add `symptom` param
- Modify: `internal/mcp/diagnose_test.go`
- Modify: `internal/service/diagnose.go` — add baseline comparison, change point detection, log correlation
- Modify: `internal/service/types.go` — update DiagnoseResult

- [ ] **Step 1: Write test for baseline comparison**

- [ ] **Step 2: Write test for change point detection**

- [ ] **Step 3: Implement baseline** — query service_rollup for same time-of-day over 7 days, fallback if <3 days

- [ ] **Step 4: Implement change point** — scan rollup buckets for >2σ jump

- [ ] **Step 5: Implement log correlation** — query logs around change point, group by prefix similarity

- [ ] **Step 6: Run tests**

- [ ] **Step 7: Commit**

```
feat(mcp): enhance diagnose with baseline comparison, change points, log correlation
```

---

### Task 12: Enhanced Compare — Time and Operations Modes

**Files:**
- Modify: `internal/mcp/compare.go` — add `mode`, `left`, `right` params
- Modify: `internal/mcp/compare_test.go`
- Modify: `internal/service/compare.go` — add time and operations modes (if service file exists, else create)
- Modify: `internal/service/types.go` — update CompareResult

- [ ] **Step 1: Write tests for time mode and operations mode**

- [ ] **Step 2: Implement time mode** — query service_rollup for two windows, compute deltas and statistical significance

- [ ] **Step 3: Implement operations mode** — query span Parquet grouped by operation for two operations

- [ ] **Step 4: Add statistical significance heuristic** — change > 2x std dev AND >= 5 buckets

- [ ] **Step 5: Run tests**

- [ ] **Step 6: Commit**

```
feat(mcp): enhance compare with time and operations modes
```

---

## Chunk 5: Query Tool and Cleanup

### Task 13: Merge Schema into Query, Add Explain and Timeout

**Files:**
- Modify: `internal/mcp/query.go` — add `explain`, `timeout_ms` params, return schema when `sql` omitted
- Modify: `internal/query/sql.go` — add explain support, timeout support
- Modify: `internal/query/schema.go` — return structured JSON schema (not markdown)
- Delete: `internal/mcp/schema.go`
- Modify: `internal/mcp/server.go` — remove `schema` registration

- [ ] **Step 1: Write test for schema-via-query (sql omitted)**

Verify returns `views` array with column definitions for spans/logs/metrics, `rollup_tables`, and `examples`.

- [ ] **Step 2: Write test for explain mode**

- [ ] **Step 3: Implement structured schema output**

Convert current markdown schema to JSON structure with views, columns, types, descriptions, rollup tables, and example queries using clean column names.

- [ ] **Step 4: Implement explain mode**

`EXPLAIN` prefix to query, return plan in `query_plan` field.

- [ ] **Step 5: Implement timeout_ms**

Use `context.WithTimeout()` on the query context.

- [ ] **Step 6: Delete schema.go, update server registration**

- [ ] **Step 7: Run tests**

Run: `go test ./internal/mcp/ -run TestQuery -v && go test ./internal/query/ -v`

- [ ] **Step 8: Commit**

```
feat(mcp): merge schema into query tool, add explain and timeout
```

---

### Task 14: Update Tool Descriptions and Server Registration

**Files:**
- Modify: `internal/mcp/server.go` — update all tool descriptions for the new surface

- [ ] **Step 1: Update tool descriptions**

Each tool's MCP `Description` field should clearly explain what it does, when to use it, and key parameters. These descriptions guide the LLM's tool selection.

- [ ] **Step 2: Verify tool count is 10** (overview, topology, spans, logs, metrics, trace, diagnose, compare, query — wait, that's 9. The 10th was render which we removed. So 9 tools.)

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -count=1`

- [ ] **Step 4: Build**

Run: `just build`

- [ ] **Step 5: Commit**

```
feat(mcp): update tool descriptions for redesigned surface
```

---

### Task 15: Integration Smoke Test

**Files:**
- No new files — manual verification

- [ ] **Step 1: Start fanout with test data**

Run fanout, send some OTLP data via otel-demo or a test script.

- [ ] **Step 2: Test each tool via MCP**

Verify each of the 9 tools returns valid JSON responses:
- `overview` — health score, services, issues
- `topology` — nodes with blast_radius, edges, critical_paths
- `spans` — ungrouped and grouped results
- `logs` — ungrouped and grouped results
- `metrics` — list and query actions
- `trace` — with include_logs
- `diagnose` — with baseline and change points
- `compare` — services mode and time mode
- `query` — with and without sql, with explain

- [ ] **Step 3: Test query tool with clean SQL**

```sql
SELECT service, operation, count(*) FROM spans
WHERE start_time >= now() - interval '1 hour'
GROUP BY service, operation
LIMIT 10
```

Verify clean column names work.

- [ ] **Step 4: Final commit if any fixes needed**
