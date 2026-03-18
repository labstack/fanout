# MCP Hardening Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add result envelope, cost guards, better tool descriptions, and log/metric attribute discovery to the MCP tool layer.

**Architecture:** Generic `wrap` function intercepts all tool registrations to add a consistent `{type, data, meta}` envelope. Cost guards are advisory warnings from pattern-matching SQL before execution. Tool descriptions are enriched with sequencing/gotcha hints. Attribute discovery for logs/metrics uses bounded JSON sampling.

**Tech Stack:** Go, DuckDB SQL, MCP go-sdk, sqlmock for tests

**Spec:** `docs/superpowers/specs/2026-03-17-mcp-hardening-design.md`

---

## File Map

| File | Change | Responsibility |
|------|--------|---------------|
| `internal/mcp/result.go` | **Create** | `Result`, `ResultMeta` types + generic `wrap()` |
| `internal/mcp/result_test.go` | **Create** | Tests for wrap: envelope shape, error propagation, callResult bypass |
| `internal/mcp/server.go:91-194` | **Modify** | Wrap all 10 tool registrations + update all descriptions |
| `internal/mcp/query.go:20-27` | **Modify** | Add `Warnings` field to `QueryOut`, call `CheckQueryCost` |
| `internal/query/sql.go` | **Modify** | Add exported `CheckQueryCost()` function |
| `internal/query/sql_test.go` | **Modify** | Table-driven tests for cost check patterns |
| `internal/service/attributes.go:31-38` | **Modify** | Add `DiscoveryMethod` field to `AttributeInfo` |
| `internal/service/attributes.go:78-92` | **Modify** | Replace logs/metrics stub with `attributesFromJSON` call |
| `internal/service/attributes.go` | **Modify** | Add `attributesFromJSON()` method |
| `internal/service/attributes_test.go` | **Create** | Mock-based test for JSON attribute discovery |

---

### Task 1: Result envelope types and wrap function

**Files:**
- Create: `internal/mcp/result.go`
- Create: `internal/mcp/result_test.go`

- [ ] **Step 1: Write the failing test for wrap**

Create `internal/mcp/result_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testIn struct {
	Name string `json:"name"`
}

type testOut struct {
	Value int    `json:"value"`
	Msg   string `json:"msg"`
}

func TestWrap_EnvelopeShape(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return nil, testOut{Value: 42, Msg: "hello"}, nil
	}

	wrapped := wrap("test_tool", handler)
	_, out, err := wrapped(context.Background(), nil, testIn{Name: "x"})
	if err != nil {
		t.Fatalf("wrap() error = %v", err)
	}

	result, ok := out.(Result)
	if !ok {
		t.Fatalf("wrap() returned %T, want Result", out)
	}

	if result.Type != "test_tool" {
		t.Errorf("Type = %q, want %q", result.Type, "test_tool")
	}
	if result.Meta.ExecTimeMs < 0 {
		t.Errorf("ExecTimeMs = %d, want >= 0", result.Meta.ExecTimeMs)
	}

	// Verify Data is the original testOut
	data, ok := result.Data.(testOut)
	if !ok {
		t.Fatalf("Data is %T, want testOut", result.Data)
	}
	if data.Value != 42 || data.Msg != "hello" {
		t.Errorf("Data = %+v, want {Value:42, Msg:hello}", data)
	}
}

func TestWrap_ErrorPropagation(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return nil, testOut{}, fmt.Errorf("something broke")
	}

	wrapped := wrap("test_tool", handler)
	_, out, err := wrapped(context.Background(), nil, testIn{})
	if err == nil {
		t.Fatal("wrap() should propagate error")
	}
	if err.Error() != "something broke" {
		t.Errorf("error = %q, want %q", err.Error(), "something broke")
	}

	// Even on error, Result envelope should be present with type set
	result, ok := out.(Result)
	if !ok {
		t.Fatalf("wrap() returned %T on error, want Result", out)
	}
	if result.Type != "test_tool" {
		t.Errorf("Type = %q on error, want %q", result.Type, "test_tool")
	}
}

func TestWrap_CallToolResultBypass(t *testing.T) {
	ctr := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "custom"}},
	}
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return ctr, testOut{}, nil
	}

	wrapped := wrap("test_tool", handler)
	callResult, out, err := wrapped(context.Background(), nil, testIn{})
	if err != nil {
		t.Fatalf("wrap() error = %v", err)
	}
	if callResult != ctr {
		t.Error("wrap() should pass through CallToolResult")
	}
	if out != nil {
		t.Errorf("out should be nil when CallToolResult is set, got %v", out)
	}
}

func TestWrap_JSONSerialization(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return nil, testOut{Value: 7, Msg: "ok"}, nil
	}

	wrapped := wrap("overview", handler)
	_, out, _ := wrapped(context.Background(), nil, testIn{})

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if m["type"] != "overview" {
		t.Errorf("JSON type = %v, want %q", m["type"], "overview")
	}
	if m["data"] == nil {
		t.Error("JSON data should not be nil")
	}
	if m["meta"] == nil {
		t.Error("JSON meta should not be nil")
	}

	meta, ok := m["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map", m["meta"])
	}
	if _, ok := meta["exec_time_ms"]; !ok {
		t.Error("meta should have exec_time_ms")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -v -run TestWrap`
Expected: FAIL — `Result` and `wrap` not defined

- [ ] **Step 3: Write the implementation**

Create `internal/mcp/result.go`:

```go
package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Result is the uniform envelope for all MCP tool responses.
// Wraps the tool-specific output in a consistent {type, data, meta} shape.
type Result struct {
	Type string     `json:"type"`
	Data any        `json:"data"`
	Meta ResultMeta `json:"meta"`
}

// ResultMeta carries cross-cutting execution metadata.
type ResultMeta struct {
	ExecTimeMs int64 `json:"exec_time_ms"`
}

// wrap returns a new handler that wraps the original handler's output in a Result envelope.
// Returns `any` (not Result) so the MCP SDK skips output schema derivation —
// Result.Data is `any`, which would produce an empty-object JSON Schema.
func wrap[TIn, TOut any](toolType string, fn func(context.Context, *mcp.CallToolRequest, TIn) (*mcp.CallToolResult, TOut, error)) func(context.Context, *mcp.CallToolRequest, TIn) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in TIn) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		callResult, out, err := fn(ctx, req, in)
		if callResult != nil {
			return callResult, nil, err
		}
		return nil, Result{
			Type: toolType,
			Data: out,
			Meta: ResultMeta{ExecTimeMs: time.Since(start).Milliseconds()},
		}, err
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp/ -v -run TestWrap`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/result.go internal/mcp/result_test.go
git commit -m "feat(mcp): add Result envelope type and generic wrap function"
```

---

### Task 2: Wrap all tool registrations

**Files:**
- Modify: `internal/mcp/server.go:91-194` (registerTools function)

- [ ] **Step 1: Wrap all 10 tool registrations**

In `internal/mcp/server.go`, update `registerTools()`. Replace each `}, s.<handler>)` with `}, wrap("<toolname>", s.<handler>))`:

```go
func (s *Server) registerTools() {
	// 1. overview — system health entry point
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "overview",
		Description: `System health overview. Start here. Returns composite health score (0–1), per-service status, and top issues.

Workflow: overview → diagnose (problem service) → spans/logs/trace (specific errors)

Params: window ("15m","1h","7d" or ISO range), include (["health","services","issues"]), sort_services_by ("severity","error_rate","latency","throughput")

Returns: health (score, total_services, by_status, throughput_per_min, global_error_rate, global_p95_ms), services (service, status, requests, error_rate, p50_ms, p95_ms), top_issues (service, issue, value, threshold)`,
	}, wrap("overview", s.overview))

	// 2. topology — service dependency map
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "topology",
		Description: `Service dependency map with health status, blast radius, and critical paths.

Params: window, edge_type (call|messaging|all), depth (BFS hops from service), service (focus node), include_inactive, namespace, tenant

Returns: nodes (service, status, requests, error_rate, p50_ms, p95_ms, blast_radius, upstream_count, downstream_count), edges (source, target, calls, avg_ms, error_rate, edge_type), critical_paths (top 3 weighted paths)`,
	}, wrap("topology", s.topology))

	// 3. spans — span search and aggregation
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "spans",
		Description: `Search, filter, and aggregate trace spans. Supports raw listing or group_by aggregation with percentile latency.

Params: query (substring match), operation (exact), service, status (error|ok|slow|all), kind (server|client|producer|consumer|internal), min_duration_ms, max_duration_ms, attrs (key-value), group_by (service|operation|status|kind|http.method|http.status_code), order_by (time|duration|error_rate|count), include_exemplars, window, namespace, tenant, limit

Returns (ungrouped): spans (trace_id, span_id, service, operation, kind, start_time, duration_ms, status, attributes), total_matched
Returns (grouped): groups (key, count, error_count, error_rate, p50_ms, p95_ms, p99_ms, exemplar_trace_ids), total_groups`,
	}, wrap("spans", s.spans))

	// 4. logs — log search and aggregation
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "logs",
		Description: `Search, filter, and aggregate log entries. Supports raw listing or group_by aggregation with sample bodies and trace correlation.

Params: query (substring on body), severity (TRACE|DEBUG|INFO|WARN|ERROR|FATAL), trace_id (correlate to trace), service, attrs (key-value), group_by (service|severity), order_by (time|count|severity), window, namespace, tenant, limit

Returns (ungrouped): logs (time, service, severity, body, trace_id, span_id, attributes), total_matched
Returns (grouped): groups (key, count, sample_bodies, sample_trace_ids), total_groups`,
	}, wrap("logs", s.logs))

	// 5. metrics — metric discovery and timeseries query
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "metrics",
		Description: `Discover and query OTLP metric timeseries with anomaly detection. Two actions: 'list' discovers metrics; 'query' returns bucketed timeseries.

Params: action (list|query), name, names (overlay multiple), aggregation (avg|sum|min|max|count), group_by, granularity (1m|5m|15m|1h|auto), service, attrs, window, namespace, tenant, limit

Returns (list): metrics (name, type, unit, services, description)
Returns (query): series (labels, metric, aggregation, unit, datapoints), anomalies (time, type, value, expected, deviation_sigma)`,
	}, wrap("metrics", s.metrics))

	// 6. trace — distributed trace with root cause analysis
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "trace",
		Description: `Distributed trace with auto root-cause analysis. Shows spans, correlated logs, critical path, and identifies the likely error or latency cause.

Params: trace_id (required), include_logs (default true), include_metrics (adds service_rollup context around trace time), compare_to (another trace_id for side-by-side diff)

Returns: spans (tree with timing/self_time), logs (correlated), critical_path, root_cause (reason, evidence), comparison (when compare_to set), metric_context (when include_metrics set)`,
	}, wrap("trace", s.trace))

	// 7. diagnose — multi-signal root cause analysis
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "diagnose",
		Description: `Deep-dive into a service with baseline comparison, change point detection, and log correlation.

Params: service (required), symptom (auto|latency|errors|throughput_drop), window, namespace, tenant

Returns: metrics (p50/p95/p99_ms, error_rate, request_count, comparison_to_baseline), top_errors (message, count, example_trace), slow_operations, dependencies, change_points (time, metric, before, after), correlated_log_patterns (pattern, count, severity)`,
	}, wrap("diagnose", s.diagnose))

	// 8. compare — side-by-side comparison (3 modes)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "compare",
		Description: `Side-by-side comparison with 3 modes. Services mode compares 2-4 services. Time mode compares same service across two windows. Operations mode compares two operations within a service.

Params: mode (services|time|operations), services (for services mode), service (for time/operations), left/right (mode-specific config), focus (["latency","errors","throughput"]), window

Returns: comparison (per-metric left/right values, change_pct, direction, statistically_significant), verdict`,
	}, wrap("compare", s.compare))

	// 9. attributes — attribute discovery
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "attributes",
		Description: `Discover what OTel attributes exist in the data. Returns attribute keys with occurrence count, cardinality, and sample values per signal (spans/logs/metrics). Use this before filtering to learn what keys are available.

Params: signal (spans|logs|metrics, default: spans), service, operation (spans only), window (default: 1h), namespace, tenant, limit (default: 50)

Returns: attributes (key, count, cardinality, samples[]), resource_attributes (key, count, cardinality, samples[])

Example: attributes(signal="spans", service="checkout") → discovers http.method (4 values), http.status_code (8 values), db.system (2 values), etc.`,
	}, wrap("attributes", s.attributes))

	// 10. query — raw SQL with DuckDB views
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query",
		Description: queryToolDescription(s.cfg.LakeDir),
	}, wrap("query", s.query))
}
```

- [ ] **Step 2: Run all MCP tests**

Run: `go test ./internal/mcp/ -v`
Expected: All tests PASS. Handler tests call handlers directly (not through `wrap`), so they are unaffected.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat(mcp): wrap all tool registrations with Result envelope"
```

---

### Task 3: Cost guards for the query tool

**Files:**
- Modify: `internal/query/sql.go` (add `CheckQueryCost`)
- Modify: `internal/query/sql_test.go` (add cost check tests)
- Modify: `internal/mcp/query.go:20-27` (add `Warnings` field and call)

- [ ] **Step 1: Write the failing tests for CheckQueryCost**

Append to `internal/query/sql_test.go`:

```go
func TestCheckQueryCost(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantWarnings int
	}{
		// Clean queries — no warnings
		{"safe rollup query", "SELECT service, p95_ms FROM service_rollup WHERE bucket > now() - INTERVAL 1 HOUR", 0},
		{"safe spans with time filter", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 15 MINUTE LIMIT 100", 0},
		{"safe CTE", "WITH x AS (SELECT 1) SELECT * FROM x", 0},

		// High-cardinality GROUP BY
		{"group by trace_id", "SELECT trace_id, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY trace_id", 1},
		{"group by span_id", "SELECT span_id, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY span_id", 1},
		{"group by attributes_json", "SELECT attributes_json, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY attributes_json", 1},
		{"group by body", "SELECT body, COUNT(*) FROM logs WHERE time > now() - INTERVAL 1 HOUR GROUP BY body", 1},
		{"group by events_json", "SELECT events_json, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY events_json", 1},
		{"group by resource_json", "SELECT resource_json, COUNT(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY resource_json", 1},

		// Unbounded time range (also triggers SELECT * without LIMIT)
		{"spans no time filter", "SELECT * FROM spans WHERE service = 'foo'", 2},
		{"logs no time filter", "SELECT * FROM logs WHERE severity = 'ERROR'", 2},
		{"metrics no time filter", "SELECT * FROM metrics WHERE name = 'cpu'", 2},
		{"spans with time filter ok", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 1 HOUR LIMIT 100", 0},
		{"rollup no time filter ok", "SELECT * FROM service_rollup", 0},

		// SELECT * without LIMIT
		{"select star no limit", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 1 HOUR", 1},
		{"select star with limit ok", "SELECT * FROM spans WHERE start_time > now() - INTERVAL 1 HOUR LIMIT 100", 0},
		{"select columns no limit ok", "SELECT service, count(*) FROM spans WHERE start_time > now() - INTERVAL 1 HOUR GROUP BY service", 0},

		// CROSS JOIN (also triggers SELECT * without LIMIT)
		{"cross join", "SELECT * FROM spans CROSS JOIN logs WHERE start_time > now() - INTERVAL 1 HOUR", 2},

		// Multiple warnings
		{"group by trace_id no time", "SELECT trace_id, COUNT(*) FROM spans GROUP BY trace_id", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := CheckQueryCost(tt.query)
			if len(warnings) != tt.wantWarnings {
				t.Errorf("CheckQueryCost() returned %d warning(s), want %d: %v", len(warnings), tt.wantWarnings, warnings)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/query/ -v -run TestCheckQueryCost`
Expected: FAIL — `CheckQueryCost` not defined

- [ ] **Step 3: Implement CheckQueryCost**

Append to `internal/query/sql.go`:

```go
// CheckQueryCost performs best-effort pattern analysis on a SQL query and returns
// advisory warnings about potentially expensive operations. These are not errors —
// the query still executes — but the warnings educate the LLM about risks.
func CheckQueryCost(sql string) []string {
	upper := strings.ToUpper(sql)
	var warnings []string

	// 1. High-cardinality GROUP BY
	if idx := strings.Index(upper, "GROUP BY"); idx >= 0 {
		groupByClause := upper[idx:]
		highCardCols := []string{"TRACE_ID", "SPAN_ID", "ATTRIBUTES_JSON", "RESOURCE_JSON", "BODY", "EVENTS_JSON"}
		for _, col := range highCardCols {
			pattern := regexp.MustCompile(`\b` + col + `\b`)
			if pattern.MatchString(groupByClause) {
				warnings = append(warnings, fmt.Sprintf("GROUP BY %s is high-cardinality and may produce millions of groups. Consider aggregating by service, operation, or status instead.", strings.ToLower(col)))
				break // one warning per query is enough
			}
		}
	}

	// 2. Unbounded time range on base views
	baseViews := []struct {
		view      string
		timePreds []string
	}{
		{"SPANS", []string{"START_TIME", "INTERVAL", "NOW()"}},
		{"LOGS", []string{"TIME", "INTERVAL", "NOW()"}},
		{"METRICS", []string{"TIME", "INTERVAL", "NOW()"}},
	}
	for _, bv := range baseViews {
		viewPattern := regexp.MustCompile(`\b` + bv.view + `\b`)
		if viewPattern.MatchString(upper) {
			hasTimePred := false
			for _, pred := range bv.timePreds {
				if strings.Contains(upper, pred) {
					hasTimePred = true
					break
				}
			}
			// Also check for BUCKET (rollup queries that reference views indirectly)
			if strings.Contains(upper, "BUCKET") {
				hasTimePred = true
			}
			if !hasTimePred {
				warnings = append(warnings, fmt.Sprintf("Query references %s without a time filter. This scans all data. Add a WHERE clause with start_time/time > now() - INTERVAL.", strings.ToLower(bv.view)))
				break
			}
		}
	}

	// 3. SELECT * without LIMIT on base views
	if strings.Contains(upper, "SELECT *") && !strings.Contains(upper, "LIMIT") {
		for _, bv := range baseViews {
			viewPattern := regexp.MustCompile(`\b` + bv.view + `\b`)
			if viewPattern.MatchString(upper) {
				warnings = append(warnings, "SELECT * without LIMIT on a base view. Add LIMIT or select specific columns to control result size.")
				break
			}
		}
	}

	// 4. CROSS JOIN
	if strings.Contains(upper, "CROSS JOIN") {
		warnings = append(warnings, "CROSS JOIN produces a cartesian product. This is almost never what you want in observability queries.")
	}

	return warnings
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/query/ -v -run TestCheckQueryCost`
Expected: PASS

- [ ] **Step 5: Add Warnings field to QueryOut and wire it up**

In `internal/mcp/query.go`, add `Warnings` to `QueryOut`:

After line 25 (`QueryPlan  string          ...`), add:
```go
	Warnings   []string        `json:"warnings,omitempty"`
```

In the `query` handler function (line 173), after the `in.SQL == ""` early return (line 179), add the cost check before execution:

After line 187 (`in.MaxRows = 10000`), add:
```go
	// Advisory cost warnings for the LLM
	costWarnings := query.CheckQueryCost(in.SQL)
```

Then in both the explain return (line 202) and the results return (line 208), include warnings:

For the explain return, change to:
```go
	if in.Explain {
		return nil, QueryOut{
			QueryPlan:  resp.QueryPlan,
			ExecTimeMs: resp.ExecutionTimeMs,
			Warnings:   costWarnings,
		}, nil
	}
```

For the results return, change to:
```go
	return nil, QueryOut{
		Results:    resp.Results,
		RowCount:   resp.RowsReturned,
		ExecTimeMs: resp.ExecutionTimeMs,
		Truncated:  resp.RowsReturned >= in.MaxRows,
		Warnings:   costWarnings,
	}, nil
```

Also add warnings to the error return (line 196):
```go
	if resp.Error != "" {
		return nil, QueryOut{
			Schema:   buildSchemaResponse(),
			Warnings: costWarnings,
		}, fmt.Errorf("%s", resp.Error)
	}
```

- [ ] **Step 6: Run all query and MCP tests**

Run: `go test ./internal/query/... ./internal/mcp/... -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/query/sql.go internal/query/sql_test.go internal/mcp/query.go
git commit -m "feat(mcp): add advisory cost guards for query tool"
```

---

### Task 4: Better MCP tool descriptions

**Files:**
- Modify: `internal/mcp/server.go:91-194` (description strings only)

- [ ] **Step 1: Update all 10 tool descriptions**

Replace the description strings in `server.go` `registerTools()`. This is purely text — no code logic changes. Each description gets "When to use", "Workflow", and "Gotchas" sections.

For **overview**:
```
System health overview. Start here for any investigation.

When to use: First tool to call. Gives you the lay of the land — which services exist, which have issues.
Workflow: overview → diagnose(problem service) → trace(suggested_traces) → logs(trace_id)
Gotchas:
- sort_services_by="severity" (default) surfaces problems first; use "throughput" for traffic-based ranking.
- Returns at most 100 services. Use limit parameter to increase.

Params: window ("15m","1h","7d" or ISO range), include (["health","services","issues"]), sort_services_by ("severity","error_rate","latency","throughput"), namespace, tenant, limit (default 100)

Returns: health (score, total_services, by_status, throughput_per_min, global_error_rate, global_p95_ms), services (service, status, requests, error_rate, p50_ms, p95_ms), top_issues (service, issue, value, threshold)
```

For **topology**:
```
Service dependency map with health status, blast radius, and critical paths.

When to use: To understand which services call which, identify blast radius, or find critical dependency chains.
Workflow: topology → diagnose(unhealthy node) or topology(service=X, depth=2) to zoom in on a subgraph.
Gotchas:
- edge_type="messaging" shows async producer/consumer links; "call" shows synchronous RPC.
- blast_radius indicates how many downstream services are affected if this node fails.

Params: window, edge_type (call|messaging|all), depth (BFS hops from service), service (focus node), include_inactive, namespace, tenant

Returns: nodes (service, status, requests, error_rate, p50_ms, p95_ms, blast_radius, upstream_count, downstream_count), edges (source, target, calls, avg_ms, error_rate, edge_type), critical_paths (top 3 weighted paths)
```

For **spans**:
```
Search, filter, and aggregate trace spans.

When to use: To find specific spans matching criteria, or to get aggregated latency/error stats by service or operation.
Workflow: spans(service=X, status=error) → trace(trace_id) for full context. Use group_by for patterns before drilling in.
Gotchas:
- Without group_by, returns raw spans (use limit to control volume). With group_by, returns aggregated stats with percentiles.
- Use attributes tool first to discover filterable attribute keys for attrs parameter.
- status="slow" filters spans above service P95 baseline.

Params: query (substring match), operation (exact), service, status (error|ok|slow|all), kind (server|client|producer|consumer|internal), min_duration_ms, max_duration_ms, attrs (key-value), group_by (service|operation|status|kind|http.method|http.status_code), order_by (time|duration|error_rate|count), include_exemplars, window, namespace, tenant, limit

Returns (ungrouped): spans (trace_id, span_id, service, operation, kind, start_time, duration_ms, status, attributes), total_matched
Returns (grouped): groups (key, count, error_count, error_rate, p50_ms, p95_ms, p99_ms, exemplar_trace_ids), total_groups
```

For **logs**:
```
Search, filter, and aggregate log entries.

When to use: To find logs by pattern, severity, or trace correlation. Use after trace tool to get logs for a specific request.
Workflow: logs(trace_id=X) for request logs. logs(severity=["ERROR","FATAL"], service=X) → trace(trace_id) for error investigation.
Gotchas:
- severity is an array — pass ["ERROR", "FATAL"] for multiple levels.
- group_by=["service","severity"] gives a heatmap of log volume by service and level.
- Use attributes tool first to discover filterable attribute keys.

Params: query (substring on body), severity (TRACE|DEBUG|INFO|WARN|ERROR|FATAL), trace_id (correlate to trace), service, attrs (key-value), group_by (service|severity), order_by (time|count|severity), window, namespace, tenant, limit

Returns (ungrouped): logs (time, service, severity, body, trace_id, span_id, attributes), total_matched
Returns (grouped): groups (key, count, sample_bodies, sample_trace_ids), total_groups
```

For **metrics**:
```
Discover and query OTLP metric timeseries with anomaly detection.

When to use: For metric-based investigation — CPU, memory, request rates, custom business metrics. Start with action="list" to discover what metrics exist.
Workflow: metrics(action=list) → metrics(action=query, name=X) → anomalies in response highlight spikes/drops.
Gotchas:
- Cumulative sum metrics (type="sum") are auto-converted to per-bucket deltas — you see rates, not raw counters.
- action="histogram" returns bucket distributions; action="exemplars" returns trace links from histogram exemplars.
- names=["metric1","metric2"] overlays multiple metrics in one query for comparison.

Params: action (list|query|histogram|exemplars), name, names (overlay multiple), aggregation (avg|sum|min|max|count), group_by, granularity (1m|5m|15m|1h|auto), service, attrs, window, namespace, tenant, limit

Returns (list): metrics (name, type, unit, services, description)
Returns (query): series (labels, metric, aggregation, unit, datapoints), anomalies (time, type, value, expected, deviation_sigma)
```

For **trace**:
```
Distributed trace with auto root-cause analysis.

When to use: When you have a trace_id from spans, logs, diagnose, or metrics exemplars. Shows the full request journey with timing breakdown.
Workflow: trace(trace_id) → check root_cause → if latency issue, compare with trace(trace_id, compare_to=healthy_trace_id).
Gotchas:
- include_metrics=true adds service_rollup context around the trace's time — useful for seeing if the trace was during a spike.
- critical_path shows spans that consumed the most wall-clock time relative to the trace duration.
- compare_to gives a side-by-side diff highlighting which operations changed.

Params: trace_id (required), include_logs (default true), include_metrics (adds service_rollup context around trace time), compare_to (another trace_id for side-by-side diff), window

Returns: spans (tree with timing/self_time), logs (correlated), critical_path, root_cause (reason, evidence), comparison (when compare_to set), metric_context (when include_metrics set)
```

For **diagnose**:
```
Deep-dive into a service with baseline comparison, change point detection, and log correlation.

When to use: After overview identifies a problem service. For broad "what's wrong?" exploration — use spans/logs for specific known errors.
Workflow: overview → diagnose(service) → trace(suggested_traces[0]) → logs(trace_id) for full investigation.
Gotchas:
- symptom="auto" (default) detects the dominant issue; specify "latency" or "errors" to force focus.
- suggested_traces contains trace IDs ready for the trace tool — always use them for follow-up.
- change_points show when metrics shifted — feed the timestamp to compare(mode=time) for before/after.

Params: service (required), symptom (auto|latency|errors|throughput_drop), window, namespace, tenant

Returns: metrics (p50/p95/p99_ms, error_rate, request_count, comparison_to_baseline), top_errors (operation, message, exception_type, count, example_trace), slow_operations, dependencies, change_points, correlated_log_patterns, suggested_traces
```

For **compare**:
```
Side-by-side comparison with 3 modes.

When to use: To quantify differences — between services, time windows, or operations — with statistical significance.
Workflow: diagnose → compare(mode=time, left.window=before_change, right.window=after_change) to confirm a regression.

Modes:
- services: Compare 2-4 services side-by-side. Pass services=["svc1","svc2"].
- time: Compare same service across two ISO time windows. Pass service, left.window, right.window.
- operations: Compare two operations within a service. Pass service, left.operation, right.operation.

Gotchas:
- Time mode requires ISO range windows (e.g., "2026-03-17T10:00:00Z/2026-03-17T11:00:00Z"), not durations.
- statistically_significant=true in comparison means the change is likely real, not noise.

Params: mode (services|time|operations), services, service, left/right, focus (["latency","errors","throughput"]), window
Returns: comparison (per-metric left/right values, change_pct, direction, statistically_significant), verdict
```

For **attributes**:
```
Discover what OTel attributes exist in the data.

When to use: Before using attrs={} filters on spans/logs/metrics tools. Tells you what keys are available and their value distributions.
Workflow: attributes(signal=spans, service=X) → spans(service=X, attrs={"http.status_code":"500"})
Gotchas:
- For spans, uses pre-extracted columns (fast, exact counts). For logs/metrics, samples 1000 rows from JSON (approximate counts).
- Additional span attributes may exist in the JSON blob beyond the pre-extracted columns — use query tool with json_keys(attributes_json) to discover them.

Params: signal (spans|logs|metrics, default: spans), service, operation (spans only), window (default: 1h), namespace, tenant, limit (default: 50)

Returns: attributes (key, count, cardinality, samples[]), resource_attributes (key, count, cardinality, samples[])
```

For **query** — keep using `queryToolDescription()` function, but update it to add workflow and gotchas:

In `server.go`, update `queryToolDescription` to:
```go
func queryToolDescription(lakeDir string) string {
	return strings.ReplaceAll(`Raw SQL against DuckDB. Escape hatch for analysis not covered by other tools.

When to use: Only when other tools can't answer the question. Prefer overview/diagnose/spans/logs/metrics for standard queries.
Workflow: query(sql="") to get schema reference → write query using view/column names from schema → query(sql=...).
Gotchas:
- Use the views (spans, logs, metrics) not raw Parquet. Views have clean column names.
- Always add a time filter (WHERE start_time > now() - INTERVAL ...) to avoid full scans.
- Avoid GROUP BY trace_id, span_id, or attributes_json — these are high-cardinality and will be slow.
- Use attr(attributes_json, 'key') macro to extract JSON attributes.

DuckDB Views (clean column names):
- spans: trace_id, span_id, service, operation, kind, start_time, end_time, duration_ms, status, status_message, attributes_json, resource_json, events_json, namespace, tenant
- logs: time, severity, body, service, trace_id, span_id, attributes_json, resource_json, namespace, tenant
- metrics: time, name, type, value, unit, service, description, attributes_json, resource_json, namespace, tenant

Rollup tables:
- service_rollup: bucket, service, spans, error_rate, p50_ms, p95_ms, log_count, metric_count
- edge_rollup: bucket, caller, callee, calls, avg_ms, error_rate, edge_type

Macro: attr(json_col, 'key') — extracts JSON key from attributes_json

Params: sql, explain (returns query plan), max_rows (default 1000), timeout_ms (default 30000)

Example: SELECT service, approx_quantile(duration_ms, 0.95) as p95 FROM spans WHERE start_time > now() - INTERVAL 15 MINUTE GROUP BY service ORDER BY p95 DESC`, "{LAKE}", lakeDir)
}
```

- [ ] **Step 2: Run all MCP tests**

Run: `go test ./internal/mcp/ -v`
Expected: All PASS (descriptions are strings, no logic changes)

- [ ] **Step 3: Build to verify no syntax errors**

Run: `CGO_ENABLED=1 go build ./cmd/fanout`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat(mcp): enrich tool descriptions with workflow hints and gotchas"
```

---

### Task 5: Attribute discovery for logs and metrics

**Files:**
- Modify: `internal/service/attributes.go:31-38` (add DiscoveryMethod field)
- Modify: `internal/service/attributes.go:78-92` (replace stub with attributesFromJSON)
- Modify: `internal/service/attributes.go` (add attributesFromJSON method)
- Modify: `internal/mcp/attributes.go:22-28` (add DiscoveryMethod to AttributeOut)
- Modify: `internal/mcp/attributes.go:77-84` (pass DiscoveryMethod through)
- Create: `internal/service/attributes_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/service/attributes_test.go`:

```go
package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
)

func TestAttributesFromJSON_Logs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	svc := &Service{
		duck: &query.Duck{DB: db},
		cfg: config.Config{
			TenantID: uuid.MustParse("00000000-0000-0000-0000-000000000000"),
		},
	}

	// Total row count
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(
		sqlmock.NewRows([]string{"count"}).AddRow(int64(5000)))

	// Attribute discovery from attributes_json
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}).
			AddRow("http.method", int64(800), int64(4)).
			AddRow("http.url", int64(750), int64(200)))

	// Resource attribute discovery from resource_json
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}).
			AddRow("service.version", int64(900), int64(3)))

	result, err := svc.Attributes(context.Background(), AttributeParams{
		Signal: "logs",
		Window: 60,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("Attributes() error = %v", err)
	}

	if result.Signal != "logs" {
		t.Errorf("Signal = %q, want %q", result.Signal, "logs")
	}
	if result.TotalRows != 5000 {
		t.Errorf("TotalRows = %d, want 5000", result.TotalRows)
	}
	if len(result.Attributes) != 2 {
		t.Fatalf("Attributes count = %d, want 2", len(result.Attributes))
	}
	if result.Attributes[0].Key != "http.method" {
		t.Errorf("Attributes[0].Key = %q, want %q", result.Attributes[0].Key, "http.method")
	}
	if result.Attributes[0].DiscoveryMethod != "sample" {
		t.Errorf("Attributes[0].DiscoveryMethod = %q, want %q", result.Attributes[0].DiscoveryMethod, "sample")
	}
	if result.Attributes[0].Cardinality != 4 {
		t.Errorf("Attributes[0].Cardinality = %d, want 4", result.Attributes[0].Cardinality)
	}
	if len(result.ResourceAttributes) != 1 {
		t.Fatalf("ResourceAttributes count = %d, want 1", len(result.ResourceAttributes))
	}
	if result.ResourceAttributes[0].Key != "service.version" {
		t.Errorf("ResourceAttributes[0].Key = %q", result.ResourceAttributes[0].Key)
	}
	if len(result.Warnings) == 0 {
		t.Error("Warnings should include approximate counts note")
	}
}

func TestAttributesFromJSON_Metrics(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	svc := &Service{
		duck: &query.Duck{DB: db},
		cfg: config.Config{
			TenantID: uuid.MustParse("00000000-0000-0000-0000-000000000000"),
		},
	}

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(
		sqlmock.NewRows([]string{"count"}).AddRow(int64(1000)))
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}))
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}))

	result, err := svc.Attributes(context.Background(), AttributeParams{
		Signal: "metrics",
		Window: 15,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("Attributes() error = %v", err)
	}

	if result.Signal != "metrics" {
		t.Errorf("Signal = %q, want %q", result.Signal, "metrics")
	}
	// Empty attributes is fine — just verify it didn't return the old "not supported" warning
	for _, w := range result.Warnings {
		if w == "Attribute discovery for metrics is not yet supported. Use the query tool with json_keys() on a small sample." {
			t.Error("Should no longer return 'not yet supported' warning")
		}
	}
}

func TestAttributes_SpansDiscoveryMethod(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	svc := &Service{
		duck: &query.Duck{DB: db},
		cfg: config.Config{
			TenantID: uuid.MustParse("00000000-0000-0000-0000-000000000000"),
		},
	}

	// Count query
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(
		sqlmock.NewRows([]string{"count"}).AddRow(int64(100)))

	// UNPIVOT span attributes
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality", "samples"}).
			AddRow("http_method", int64(90), int64(3), `["GET","POST","PUT"]`))

	// UNPIVOT resource attributes
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality", "samples"}))

	result, err := svc.Attributes(context.Background(), AttributeParams{
		Signal: "spans",
		Window: 15,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("Attributes() error = %v", err)
	}

	if len(result.Attributes) != 1 {
		t.Fatalf("Attributes count = %d, want 1", len(result.Attributes))
	}
	if result.Attributes[0].DiscoveryMethod != "column" {
		t.Errorf("DiscoveryMethod = %q, want %q", result.Attributes[0].DiscoveryMethod, "column")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -v -run TestAttributes`
Expected: FAIL — `DiscoveryMethod` field doesn't exist, `attributesFromJSON` not defined

- [ ] **Step 3: Add DiscoveryMethod to AttributeInfo**

In `internal/service/attributes.go`, update the struct at line 33:

```go
type AttributeInfo struct {
	Key             string   `json:"key"`
	Count           int64    `json:"count"`
	Cardinality     int64    `json:"cardinality"`
	Samples         []string `json:"samples"`
	DiscoveryMethod string   `json:"discovery_method,omitempty"`
}
```

- [ ] **Step 4: Set DiscoveryMethod="column" in attributesFromColumns**

In `internal/service/attributes.go`, in the `attributesFromColumns` method, after the key-mapping `if` block (after line 173), add `info.DiscoveryMethod = "column"` so it applies unconditionally to all span attributes:

After the block:
```go
		if otelKey, ok := colToKey[info.Key]; ok {
			info.Key = otelKey
		}
```
Add:
```go
		info.DiscoveryMethod = "column"
```

And for resource attributes, after the similar key-mapping block (after line 225):
```go
			if otelKey, ok := resColToKey[info.Key]; ok {
				info.Key = otelKey
			}
```
Add:
```go
			info.DiscoveryMethod = "column"
```

- [ ] **Step 5: Implement attributesFromJSON**

In `internal/service/attributes.go`, replace the `case "logs", "metrics":` block (lines 81-89) with:

```go
	case "logs", "metrics":
		return s.attributesFromJSON(ctx, p)
```

Then append the `attributesFromJSON` method to the file:

```go
// attributesFromJSON discovers attributes by sampling the JSON blob.
// Used for logs and metrics which don't have pre-extracted columns.
func (s *Service) attributesFromJSON(ctx context.Context, p AttributeParams) (*AttributesResult, error) {
	// Derive table and time column from signal
	var table, timeCol string
	switch p.Signal {
	case "logs":
		table, timeCol = "logs", "time"
	case "metrics":
		table, timeCol = "metrics", "time"
	default:
		return nil, fmt.Errorf("attributesFromJSON: unsupported signal %q", p.Signal)
	}

	out := &AttributesResult{
		Signal:             p.Signal,
		Attributes:         []AttributeInfo{},
		ResourceAttributes: []AttributeInfo{},
		Warnings:           []string{"Counts are approximate — based on 1000-row sample"},
	}

	// Build WHERE clause
	var clauses []string
	var args []any
	clauses = append(clauses, fmt.Sprintf("%s >= now() - INTERVAL %d MINUTE", timeCol, p.Window))
	if p.Service != "" {
		clauses = append(clauses, "service = ?")
		args = append(args, p.Service)
	}
	if p.Namespace != "" {
		clauses = append(clauses, "namespace = ?")
		args = append(args, p.Namespace)
	}
	if p.TenantID != "" {
		clauses = append(clauses, "tenant = ?")
		args = append(args, p.TenantID)
	}
	where := "WHERE " + strings.Join(clauses, " AND ")

	// Get total row count
	countQ := fmt.Sprintf("SELECT COUNT(*) FROM %s %s", table, where)
	var totalRows int64
	if err := s.duck.DB.QueryRowContext(ctx, countQ, args...).Scan(&totalRows); err != nil {
		slog.Warn("attributes count query failed", "signal", p.Signal, "err", err)
	}
	out.TotalRows = totalRows

	// Discover attributes from attributes_json
	out.Attributes = s.discoverJSONKeys(ctx, table, "attributes_json", where, args, p.Limit)

	// Discover resource attributes from resource_json
	out.ResourceAttributes = s.discoverJSONKeys(ctx, table, "resource_json", where, args, p.Limit)

	return out, nil
}

// discoverJSONKeys samples a JSON column and returns discovered attribute keys with counts.
func (s *Service) discoverJSONKeys(ctx context.Context, table, jsonCol, where string, args []any, limit int) []AttributeInfo {
	q := fmt.Sprintf(`
WITH sample AS (
  SELECT %s FROM %s %s AND %s IS NOT NULL AND %s != '' LIMIT 1000
),
kv AS (
  SELECT k AS key, json_extract_string(%s::JSON, '$.' || k) AS val
  FROM sample, UNNEST(json_keys(%s::JSON)) AS t(k)
)
SELECT key, COUNT(*) AS count, COUNT(DISTINCT val) AS cardinality
FROM kv
GROUP BY key
ORDER BY count DESC
LIMIT %d`, jsonCol, table, where, jsonCol, jsonCol, jsonCol, jsonCol, limit)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("JSON attribute discovery failed", "table", table, "col", jsonCol, "err", err)
		return []AttributeInfo{}
	}
	defer rows.Close()

	var attrs []AttributeInfo
	for rows.Next() {
		var info AttributeInfo
		if err := rows.Scan(&info.Key, &info.Count, &info.Cardinality); err != nil {
			slog.Warn("JSON attribute scan failed", "err", err)
			continue
		}
		info.Samples = []string{}
		info.DiscoveryMethod = "sample"
		attrs = append(attrs, info)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("JSON attribute iteration error", "err", err)
	}
	if attrs == nil {
		attrs = []AttributeInfo{}
	}
	return attrs
}
```

- [ ] **Step 6: Update MCP attributes handler to pass through DiscoveryMethod**

In `internal/mcp/attributes.go`, add `DiscoveryMethod` to `AttributeOut` (after line 27):

```go
type AttributeOut struct {
	Key             string   `json:"key"`
	Count           int64    `json:"count"`
	Cardinality     int64    `json:"cardinality"`
	Samples         []string `json:"samples"`
	DiscoveryMethod string   `json:"discovery_method,omitempty"`
}
```

Then in the mapping loops (lines 77-84 and 85-92), add the field:

```go
	for _, a := range result.Attributes {
		out.Attributes = append(out.Attributes, AttributeOut{
			Key:             a.Key,
			Count:           a.Count,
			Cardinality:     a.Cardinality,
			Samples:         a.Samples,
			DiscoveryMethod: a.DiscoveryMethod,
		})
	}
	for _, a := range result.ResourceAttributes {
		out.ResourceAttributes = append(out.ResourceAttributes, AttributeOut{
			Key:             a.Key,
			Count:           a.Count,
			Cardinality:     a.Cardinality,
			Samples:         a.Samples,
			DiscoveryMethod: a.DiscoveryMethod,
		})
	}
```

Also update the spans suggestion (line 96-98) to note the gap:

```go
	if len(out.Attributes) == 0 && len(out.ResourceAttributes) == 0 {
		out.Suggestion = "No attributes found. Try widening the time window or removing service/operation filters."
	} else if result.Signal == "spans" {
		out.Suggestion = fmt.Sprintf("Found %d attribute(s) and %d resource attribute(s) from pre-extracted columns. Additional attributes may exist in attributes_json — use query tool with json_keys() to discover them. Use attrs={\"key\":\"value\"} on spans/logs/metrics tools to filter.", len(out.Attributes), len(out.ResourceAttributes))
	} else {
		out.Suggestion = fmt.Sprintf("Found %d attribute(s) and %d resource attribute(s) from 1000-row sample (counts are approximate). Use attrs={\"key\":\"value\"} on spans/logs/metrics tools to filter.", len(out.Attributes), len(out.ResourceAttributes))
	}
```

- [ ] **Step 7: Run all tests**

Run: `go test ./internal/service/... ./internal/mcp/... -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/service/attributes.go internal/service/attributes_test.go internal/mcp/attributes.go
git commit -m "feat(mcp): add JSON-based attribute discovery for logs and metrics"
```

---

### Task 6: Full build and integration test

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`
Expected: All PASS

- [ ] **Step 2: Build**

Run: `CGO_ENABLED=1 go build ./cmd/fanout`
Expected: Builds cleanly

- [ ] **Step 3: Commit any remaining fixes**

If any tests required fixes, commit them here.

- [ ] **Step 4: Squash into a single PR commit (optional)**

If creating a PR, squash the task commits into a clean history.
