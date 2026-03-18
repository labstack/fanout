# MCP Hardening: Result Envelope, Cost Guards, Tool Descriptions, Attribute Filtering

**Date:** 2026-03-17
**Goal:** Four improvements to the MCP tool layer that increase reliability for LLM clients: consistent result shapes, query safety, better tool guidance, and full attribute discovery.

---

## 1. Typed Result Envelope

### Problem

Each MCP tool returns a different JSON shape. The LLM client has no consistent way to know what type of response it received, how long the query took, or whether results were truncated — it must infer this from ad-hoc fields that vary per tool.

### Design

Add a `Result` wrapper that all tool responses pass through. Handler methods stay unchanged — the wrapping happens at registration time via a generic Go function.

**New types** in `internal/mcp/result.go`:

```go
type Result struct {
    Type string     `json:"type"`
    Data any        `json:"data"`
    Meta ResultMeta `json:"meta"`
}

type ResultMeta struct {
    ExecTimeMs int64 `json:"exec_time_ms"`
}
```

Note: `Truncated` and `Window` are already present in per-tool output structs (e.g., `SpansOut.Suggestion`, `OverviewOut.Window`). Duplicating them in `ResultMeta` would create two sources of truth. The meta layer only carries cross-cutting concerns (execution time).

**Generic wrapper** in `internal/mcp/result.go`:

The wrapper returns `any` (not `Result`) to avoid MCP SDK output schema validation issues. The SDK derives a JSON Schema from the return type — with `Result` it would see `Data` as an empty-object schema and potentially reject the actual data. Returning `any` skips schema derivation entirely.

```go
func wrap[TIn, TOut any](toolType string, fn func(context.Context, *mcp.CallToolRequest, TIn) (*mcp.CallToolResult, TOut, error)) func(context.Context, *mcp.CallToolRequest, TIn) (*mcp.CallToolResult, any, error) {
    return func(ctx context.Context, req *mcp.CallToolRequest, in TIn) (*mcp.CallToolResult, any, error) {
        start := time.Now()
        callResult, out, err := fn(ctx, req, in)
        if callResult != nil {
            // Handler already built a CallToolResult (not used by any current handler,
            // but preserved for forward compatibility). Propagate error if present.
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

**Registration change** in `server.go`:

```go
// Before
mcp.AddTool(s.mcp, overviewTool, s.overview)

// After
mcp.AddTool(s.mcp, overviewTool, wrap("overview", s.overview))
```

### Why this approach

- **No handler changes**: Handler methods keep their specific return types. Existing handler tests continue to call handlers directly with no changes.
- **No service layer changes**: The `Data any` field is just the existing `*Out` struct, serialized as-is.
- **Works with Go generics**: `wrap` infers `TIn` and `TOut` from the handler signature. Even handlers returning `any` (metrics, attributes) work.
- **SDK-safe**: Returns `any` to skip MCP SDK output schema derivation, avoiding issues with the `Data any` field.
- **Error propagation**: Errors are always passed through — when `callResult != nil`, the error is still returned to the SDK (not swallowed).
- **Uniform JSON contract**: Every tool response is `{"type": "...", "data": {...}, "meta": {...}}`.

### What the LLM sees (before/after)

Before:
```json
{"services": [...], "health": {...}, "window": "15m", "timestamp": "..."}
```

After:
```json
{
  "type": "overview",
  "data": {"services": [...], "health": {...}, "window": "15m", "timestamp": "..."},
  "meta": {"exec_time_ms": 42}
}
```

---

## 2. Cardinality/Cost Guards on Query Tool

### Problem

The `query` tool validates SQL syntax (SELECT/WITH only, no dangerous functions) but doesn't guard against queries that scan too much data or produce enormous result sets. A query like `GROUP BY trace_id` on 30 days of data could OOM DuckDB.

### Design

Add **pattern-based warnings** to `internal/query/sql.go`. These are advisory — they return a warning in the response rather than blocking execution — because the LLM needs to be told _why_ a query is risky, not just rejected.

**New exported function** `CheckQueryCost(sql string) []string` in `internal/query/sql.go` returns warning strings. Pattern detection is best-effort — false positives/negatives are acceptable since these are advisory warnings. Matches against normalized upper-case SQL; only detects simple cases (direct view/table references without obvious time predicates).

1. **High-cardinality GROUP BY**: Warn if GROUP BY references `trace_id`, `span_id`, `attributes_json`, `resource_json`, `body`, or `events_json`.

2. **Unbounded time range**: Warn if query directly references `spans`, `logs`, or `metrics` views without a WHERE clause containing time-related predicates (`start_time`, `time`, `bucket`, `INTERVAL`, `now()`).

3. **SELECT * without LIMIT**: Warn when `SELECT *` appears with a base view and no explicit `LIMIT` keyword (ensureLimit adds one, but the warning educates the LLM about the pattern).

4. **CROSS JOIN**: Warn on explicit `CROSS JOIN` keywords.

**Integration**: The MCP `query` handler calls `query.CheckQueryCost(in.SQL)` before execution and includes warnings in the response:

```go
type QueryOut struct {
    // ... existing fields
    Warnings []string `json:"warnings,omitempty"` // new field
}
```

The handler sets `out.Warnings = query.CheckQueryCost(in.SQL)` before calling `ExecuteSQL`.

**DuckDB memory guard**: Set `memory_limit` in the DuckDB connection config at initialization time (in `internal/query/duck.go`), not per-query. This avoids connection pool issues — `database/sql` doesn't guarantee the same connection across calls, so a per-query `SET` could be lost. A global config applies to all connections from the pool.

### Not doing

- EXPLAIN-based cost estimation: Adds latency (two queries) and EXPLAIN output parsing is fragile. Pattern detection catches the common cases.
- Blocking dangerous queries: The LLM needs to understand _why_ and retry with a better query. Warnings are more useful than rejections.

---

## 3. Better MCP Tool Descriptions

### Problem

Tool descriptions are parameter-reference-style: they list fields and return shapes but don't tell the LLM _when_ to use each tool, _how_ to sequence tools, or _what_ common mistakes to avoid. This leads to suboptimal tool selection and unnecessary retries.

### Design

Enhance each tool's `Description` field in `server.go` with three additions:

1. **When to use**: One sentence positioning the tool relative to alternatives.
2. **Common workflows**: 2-3 short sequencing hints showing tool chains.
3. **Gotchas**: 1-2 pitfalls to avoid.

Example for `diagnose`:

```
Deep-dive into a service with baseline comparison, change point detection, and log correlation.

**When to use:** After overview identifies a problem service. For exploring unknown issues — use spans/logs for specific known errors.

**Workflow:** overview → diagnose(service) → trace(suggested_traces[0]) → logs(trace_id) for full investigation.

**Gotchas:**
- symptom="auto" (default) detects the dominant issue; specify "latency" or "errors" to force focus.
- suggested_traces contains trace IDs ready for the trace tool — use them.

Params: service (required), symptom (auto|latency|errors|throughput_drop), window, namespace, tenant
...
```

### Scope

All 10 tools get updated descriptions. The content is purely string changes in `server.go` — no code logic changes. Estimated ~30 lines of additional description per tool.

### Key sequencing hints across tools

| Starting point | Next tool | When |
|---|---|---|
| overview | diagnose | Service has issues |
| overview | topology | Need dependency context |
| diagnose | trace | Has suggested_traces |
| diagnose | compare(time) | Has change_points |
| spans/logs | trace | Has trace_id |
| attributes | spans/logs/metrics | Has filterable keys |
| metrics(list) | metrics(query) | Found metric name |
| trace | compare(trace) | Need baseline comparison |

---

## 4. Attribute Filtering: Logs/Metrics Discovery

### Problem

The `attributes` tool only supports spans (via UNPIVOT on pre-extracted columns). For logs and metrics, it returns "not yet supported." This means the LLM can't discover what attribute keys exist before filtering, leading to guesswork and failed queries.

The pre-extracted column approach doesn't work for logs/metrics because they have fewer pre-extracted columns. Instead, we need to sample the JSON blob.

### Design

Add JSON-based attribute discovery for logs and metrics using a bounded sample:

```sql
-- Sample-based attribute discovery (logs example)
WITH sample AS (
  SELECT attributes_json
  FROM logs
  WHERE time >= now() - INTERVAL ? MINUTE
    AND attributes_json IS NOT NULL AND attributes_json != ''
  LIMIT 1000
),
kv AS (
  SELECT k AS key, json_extract_string(attributes_json::JSON, '$.' || k) AS val
  FROM sample, UNNEST(json_keys(attributes_json::JSON)) AS t(k)
)
SELECT key,
       COUNT(*) AS count,
       COUNT(DISTINCT val) AS cardinality
FROM kv
GROUP BY key
ORDER BY count DESC
LIMIT ?
```

The UNNEST + json_extract_string pattern is O(keys * rows) but bounded to 1000 sample rows, so it stays fast even with many unique keys. This gives us real cardinality numbers, not the constant-1 that a keys-only GROUP BY would produce.

**Key decisions:**
- **Sample 1000 rows**, not full scan. This bounds memory and execution time regardless of data volume.
- **No sample values** for JSON-discovered attributes (unlike span UNPIVOT which returns `list(DISTINCT val)`). Adding samples would require another round of extraction; the LLM can use the `query` tool with `json_extract_string` to sample specific keys if needed.
- **Add a `DiscoveryMethod` field** to `AttributeInfo`: `"column"` for pre-extracted (spans), `"sample"` for JSON-sampled (logs/metrics). This tells the LLM that sample-based counts are approximate.

**Struct change** in `internal/service/attributes.go`:

```go
type AttributeInfo struct {
    Key             string   `json:"key"`
    Count           int64    `json:"count"`
    Cardinality     int64    `json:"cardinality"`
    Samples         []string `json:"samples"`
    DiscoveryMethod string   `json:"discovery_method,omitempty"` // "column" or "sample"
}
```

For JSON-discovered attributes, `Samples` is `[]string{}` (empty).

**Implementation** in `internal/service/attributes.go`:

Add `attributesFromJSON(ctx context.Context, p AttributeParams)` method (matching existing pattern — signal, table name, and time column are derived internally from `p.Signal`) that:
1. Runs the sample query above for `attributes_json`
2. Runs the same for `resource_json`
3. Returns `AttributesResult` with `Warnings: ["Counts are approximate — based on 1000-row sample"]`

Update the `switch` in `Attributes()`:
```go
case "logs", "metrics":
    return s.attributesFromJSON(ctx, p)
```

### Consistency gap

The `attrs={}` filter on spans/logs/metrics tools uses `json_extract_string(attributes_json, '$.key')` for keys not in the pre-extracted column list. This means users can filter on keys that `attributes` didn't report (because they weren't in the pre-extracted columns). With JSON-based discovery for all signals, this gap closes: `attributes` will report the same keys that `attrs={}` can filter on.

For spans, there's still a minor gap: `attributes` reports pre-extracted columns (fast, exact counts) but `attrs={}` can also filter arbitrary JSON keys. To close this, add a note in the `attributes` response:

```
Suggestion: "Showing pre-extracted attributes. Additional attributes may exist in the JSON blob — use query tool with json_keys(attributes_json) on a sample to discover them."
```

---

## Files Changed

| File | Change |
|------|--------|
| `internal/mcp/result.go` | **New** — `Result`, `ResultMeta`, `wrap()` |
| `internal/mcp/result_test.go` | **New** — Test envelope wrapping |
| `internal/mcp/server.go` | Modify — wrap all tool registrations, update descriptions |
| `internal/mcp/query.go` | Modify — add `Warnings` field, call `checkQueryCost` |
| `internal/query/sql.go` | Modify — add `checkQueryCost()` |
| `internal/query/sql_test.go` | Modify — test cost check patterns |
| `internal/service/attributes.go` | Modify — add `attributesFromJSON()`, update switch |
| `internal/service/attributes_test.go` | Modify/New — test JSON-based discovery |

---

## Testing

- **Envelope**: Unit test that `wrap` produces correct JSON shape with type/data/meta. Test that handler errors flow through. Test that `callResult != nil` bypass works.
- **Cost guards**: Table-driven tests for each pattern (GROUP BY trace_id, missing time filter, SELECT *, CROSS JOIN). Test that valid queries produce no warnings.
- **Descriptions**: No functional tests (string content). Verify by reading tool descriptions via MCP client.
- **Attribute discovery**: Mock-based test for `attributesFromJSON` — mock a sample of JSON rows, verify key discovery.

## Non-goals

- Server-side query planner / NL intent detection (the LLM is the planner)
- Canonical observability schema normalization
- Additional data sources (deploys, feature flags, SLOs)
- Changes to the service layer result types
