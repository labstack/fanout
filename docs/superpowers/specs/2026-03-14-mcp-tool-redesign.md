# MCP Tool Redesign Specification

## Goal

Redesign the Fanout MCP tool surface so it can answer any observability question or visualization request. Keep it simple, optimized, and performant. JSON-only responses. UI/chat/report changes deferred.

## Approach

Incremental evolution of the existing 9 tools. No rewrite, no adapter layer. Extend the service layer, split `find`, merge `schema` into `query`, add DuckDB views for clean SQL.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Alerts/SLOs | Design only, implement later | Requires new backend subsystems |
| Rendering | JSON-only from MCP | Report system redesigned with UI later |
| `correlate` tool | Dropped | LLM composes primitives naturally |
| Clean SQL columns | DuckDB views | Native, simple, fast |
| `group_by` fields | Fixed set + `query` escape hatch | Covers 90% of questions, optimizable |
| Implementation approach | Incremental evolution | Smallest diff, existing tests adapt |

---

## Tool Surface: 10 Tools, 3 Tiers (+1 Power)

### Shared Parameters (all tools)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | `string` | — | Filter to a single service |
| `services` | `string[]` | — | Filter to multiple services |
| `window` | `string` | `"15m"` | Time window: `5m`, `1h`, `6h`, `24h`, `7d`, or ISO range |
| `namespace` | `string` | — | OTel namespace filter |
| `tenant` | `string` | — | Tenant ID filter |
| `limit` | `integer` | 100 | Max rows returned |

`attrs` (object, attribute key-value filters) only on `spans`, `logs`, `metrics`, `query`.

---

### Tier 1: Observe

#### 1. `overview` (replaces `status`)

Single-call system health check with health scoring and sortable service list.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `include` | `string[]` | `["health","services","issues"]` | Sections to include |
| `sort_services_by` | `string` | `"severity"` | Sort: `severity`, `error_rate`, `latency`, `throughput` |

**Returns:**

```json
{
  "timestamp": "2026-03-14T17:05:00Z",
  "window": "15m",
  "health": {
    "score": 0.92,
    "total_services": 20,
    "by_status": { "healthy": 16, "degraded": 3, "unhealthy": 1 },
    "throughput_per_min": 4850,
    "global_error_rate": 0.031,
    "global_p95_ms": 18.2
  },
  "services": [
    {
      "service": "frontend",
      "status": "degraded",
      "requests": 4397,
      "error_rate": 0.036,
      "p50_ms": 2.62,
      "p95_ms": 16.17,
      "p99_ms": 21.96
    }
  ],
  "top_issues": [
    {
      "service": "accounting",
      "issue": "p95_latency",
      "value": 9444.36,
      "threshold": 500,
      "since": "2026-03-14T16:30:00Z"
    }
  ]
}
```

**Health score computation:** Weighted average across services — error_rate (40%), latency vs baseline (30%), throughput stability (30%). Per-service status: healthy (score >= 0.9), degraded (>= 0.7), unhealthy (< 0.7).

**Data source:** `service_rollup` table (fast, pre-aggregated).

---

#### 2. `topology` (enhanced)

Service dependency graph with impact analysis.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `edge_type` | `string` | `"all"` | Filter: `call`, `messaging`, `all` |
| `depth` | `integer` | — | BFS depth limit from `service` (requires `service`) |
| `include_inactive` | `boolean` | `false` | Include services with 0 spans |

**Returns:**

```json
{
  "nodes": [
    {
      "service": "frontend",
      "status": "degraded",
      "requests": 4397,
      "error_rate": 0.036,
      "p50_ms": 2.62,
      "p95_ms": 16.17,
      "p99_ms": 21.96,
      "upstream_count": 2,
      "downstream_count": 5,
      "blast_radius": 0.85
    }
  ],
  "edges": [
    {
      "source": "frontend",
      "target": "product-catalog",
      "edge_type": "call",
      "calls": 1770,
      "error_rate": 0.0,
      "avg_ms": 1.86,
      "p95_ms": 4.02
    }
  ],
  "critical_paths": [
    ["load-generator", "frontend-proxy", "frontend", "checkout", "payment"]
  ]
}
```

**`blast_radius`:** Fraction of total traffic flowing through this node, computed from `edge_rollup` call counts.

**`critical_paths`:** Longest weighted paths through the dependency graph (by call volume * latency).

**Data source:** `edge_rollup` + `service_rollup` tables.

---

### Tier 2: Explore

#### 3. `spans` (replaces span portion of `find`)

Search, filter, and aggregate trace spans.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `query` | `string` | — | Substring search on operation name and status message |
| `operation` | `string` | — | Exact operation name filter |
| `status` | `string` | `"all"` | Filter: `error`, `ok`, `slow`, `all` |
| `kind` | `string` | — | Span kind: `server`, `client`, `producer`, `consumer`, `internal` |
| `min_duration_ms` | `number` | — | Minimum span duration |
| `max_duration_ms` | `number` | — | Maximum span duration |
| `attrs` | `object` | — | Attribute filters as key-value pairs |
| `group_by` | `string[]` | — | Aggregate by fixed fields (see below) |
| `order_by` | `string` | `"time"` | Sort: `time`, `duration`, `error_rate`, `count` |
| `include_exemplars` | `boolean` | `false` | Include example trace IDs per group |

**Groupable fields:** `service`, `operation`, `status`, `kind`, `http.method`, `http.status_code`

**Returns (ungrouped):**

```json
{
  "spans": [
    {
      "trace_id": "abc123",
      "span_id": "def456",
      "service": "frontend",
      "operation": "GET /api/products",
      "kind": "server",
      "start_time": "2026-03-14T17:01:23Z",
      "duration_ms": 16.17,
      "status": "ok",
      "attributes": {
        "http.method": "GET",
        "http.status_code": "200"
      }
    }
  ],
  "total_matched": 4397,
  "returned": 100
}
```

**Returns (grouped):**

```json
{
  "groups": [
    {
      "key": { "service": "frontend", "http.method": "GET" },
      "count": 3200,
      "error_count": 85,
      "error_rate": 0.0266,
      "p50_ms": 2.1,
      "p95_ms": 14.8,
      "p99_ms": 22.3,
      "exemplar_trace_ids": ["abc123", "def456"]
    }
  ],
  "total_groups": 12
}
```

**Data source:** Span Parquet files via DuckDB `spans` view.

**`attrs` implementation:** For each key-value in `attrs`, adds `json_extract_string(attributes_json, '$.key') = 'value'` to WHERE clause.

**`http.method` / `http.status_code` group_by:** Extracted from `attributes_json` using `json_extract_string`. These two are special-cased because they're the most commonly grouped span attributes.

---

#### 4. `logs` (replaces log portion of `find`)

Search, filter, and aggregate log entries.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `query` | `string` | — | Substring or regex search on log body |
| `severity` | `string[]` | — | Filter: `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL` |
| `trace_id` | `string` | — | Find logs correlated to a specific trace |
| `attrs` | `object` | — | Attribute filters |
| `group_by` | `string[]` | — | Aggregate by: `service`, `severity` |
| `order_by` | `string` | `"time"` | Sort: `time`, `count`, `severity` |

**Returns (ungrouped):**

```json
{
  "logs": [
    {
      "time": "2026-03-14T17:01:23Z",
      "service": "payment",
      "severity": "ERROR",
      "body": "Connection pool exhausted, all 10 connections in use",
      "trace_id": "abc123",
      "span_id": "span-012",
      "attributes": {}
    }
  ],
  "total_matched": 1200,
  "returned": 100
}
```

**Returns (grouped):**

```json
{
  "groups": [
    {
      "key": { "service": "payment", "severity": "ERROR" },
      "count": 23,
      "sample_bodies": ["Connection pool exhausted..."],
      "sample_trace_ids": ["abc123"]
    }
  ],
  "total_groups": 5
}
```

**Data source:** Log Parquet files via DuckDB `logs` view.

---

#### 5. `metrics` (replaces `timeline` + metric portion of `find`)

Explore and query OTel metrics as time series.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `action` | `string` | `"query"` | Action: `list` (discover metrics), `query` (retrieve timeseries) |
| `name` | `string` | — | Metric name for `query` |
| `names` | `string[]` | — | Multiple metric names for overlay |
| `aggregation` | `string` | `"avg"` | Aggregation: `avg`, `sum`, `min`, `max`, `count`, `p50`, `p95`, `p99`, `rate` |
| `group_by` | `string[]` | — | Group timeseries by: `service` |
| `granularity` | `string` | `"auto"` | Bucket size: `1m`, `5m`, `15m`, `1h`, `auto` |
| `attrs` | `object` | — | Attribute filters |

**Returns (list):**

```json
{
  "metrics": [
    {
      "name": "http.server.duration",
      "type": "histogram",
      "unit": "ms",
      "services": ["frontend", "checkout"],
      "description": "Duration of HTTP server requests"
    }
  ]
}
```

**Returns (query):**

```json
{
  "series": [
    {
      "labels": { "service": "frontend" },
      "metric": "http.server.duration",
      "aggregation": "p95",
      "unit": "ms",
      "datapoints": [
        { "time": "2026-03-14T16:30:00Z", "value": 14.2 },
        { "time": "2026-03-14T16:35:00Z", "value": 15.8 }
      ]
    }
  ],
  "anomalies": [
    {
      "time": "2026-03-14T16:40:00Z",
      "type": "spike",
      "value": 18.1,
      "expected": 14.5,
      "deviation_sigma": 2.3
    }
  ]
}
```

**Data source:** `list` → metric Parquet files (distinct names). `query` → metric Parquet files for raw metrics, `service_rollup` for service-level latency/error/throughput timeseries.

**`auto` granularity:** `window <= 1h` → 1m, `<= 6h` → 5m, `<= 24h` → 15m, `> 24h` → 1h.

**Anomaly detection:** Reuses existing `internal/intelligence/` anomaly detection on the timeseries buckets (same as current `timeline` tool).

---

#### 6. `trace` (enhanced)

Distributed trace with root cause analysis and optional comparison.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `trace_id` | `string` | **required** | Trace ID to analyze |
| `include_logs` | `boolean` | `true` | Include correlated log entries |
| `include_metrics` | `boolean` | `false` | Include rollup metric snapshots around trace time |
| `compare_to` | `string` | — | Another trace_id for side-by-side comparison |

**Returns:**

```json
{
  "trace_id": "abc123",
  "duration_ms": 142.5,
  "span_count": 18,
  "service_count": 5,
  "has_errors": true,
  "spans": [
    {
      "span_id": "root-001",
      "parent_span_id": null,
      "service": "frontend-proxy",
      "operation": "ingress",
      "start_time": "2026-03-14T17:01:23.000Z",
      "duration_ms": 142.5,
      "status": "error",
      "status_message": "upstream connect error",
      "attributes": { "http.method": "GET", "http.url": "/api/checkout" },
      "children": ["span-002", "span-003"],
      "is_critical_path": true
    }
  ],
  "critical_path": {
    "spans": ["root-001", "span-005", "span-012"],
    "total_ms": 138.2,
    "bottleneck": {
      "span_id": "span-012",
      "service": "payment",
      "operation": "process_payment",
      "duration_ms": 89.3,
      "pct_of_total": 0.627
    }
  },
  "root_cause": {
    "confidence": 0.87,
    "summary": "Payment service timeout during process_payment",
    "evidence": [
      "Span span-012 exceeded p99 by 4.2x",
      "Error log: 'Connection pool exhausted' at payment service",
      "3 retries detected in parent span"
    ]
  },
  "correlated_logs": [
    {
      "time": "2026-03-14T17:01:23.089Z",
      "service": "payment",
      "severity": "ERROR",
      "body": "Connection pool exhausted, all 10 connections in use",
      "span_id": "span-012"
    }
  ]
}
```

**`compare_to`:** Fetches both traces, aligns spans by operation name, returns a `comparison` object with per-operation duration deltas. Added to the response only when `compare_to` is provided.

**`include_metrics`:** Queries `service_rollup` for a 5-minute window around the trace start time. Returns a `metric_context` object showing service health at trace time.

**Data source:** Span + log Parquet files, `service_rollup` for metric context.

---

### Tier 3: Analyze

#### 7. `diagnose` (enhanced)

Multi-signal root cause analysis with baseline comparison.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | `string` | **required** | Service to diagnose |
| `symptom` | `string` | `"auto"` | Focus: `latency`, `errors`, `throughput_drop`, `auto` |

**Returns:**

```json
{
  "service": "accounting",
  "status": "unhealthy",
  "symptom_detected": "latency",
  "metrics": {
    "request_count": 36,
    "error_rate": 0.0,
    "p50_ms": 5200,
    "p95_ms": 9444,
    "p99_ms": 12100,
    "comparison_to_baseline": {
      "p95_ratio": 18.9,
      "baseline_p95_ms": 500,
      "baseline_window": "7d"
    }
  },
  "top_slow_operations": [
    {
      "operation": "process_batch",
      "count": 12,
      "p95_ms": 9800,
      "contributing_pct": 0.89,
      "sample_trace_ids": ["trace-001", "trace-002"]
    }
  ],
  "correlated_log_patterns": [
    {
      "pattern": "Batch size exceeds threshold",
      "count": 12,
      "severity": "WARN"
    }
  ],
  "dependency_impact": [
    {
      "service": "kafka",
      "relationship": "consumer",
      "status": "healthy",
      "contributes_to_symptom": false
    }
  ],
  "change_points": [
    {
      "time": "2026-03-14T16:28:00Z",
      "metric": "p95_ms",
      "before": 480,
      "after": 9200
    }
  ],
  "root_cause": {
    "confidence": 0.91,
    "summary": "process_batch operation regression",
    "evidence": [
      "P95 latency jumped 18.9x at 16:28",
      "process_batch accounts for 89% of total latency",
      "Log pattern 'Batch size exceeds threshold' appeared simultaneously",
      "No dependency degradation detected"
    ]
  }
}
```

**Baseline comparison:** Queries `service_rollup` for the same time-of-day over the past 7 days. Computes ratio of current vs baseline for latency percentiles.

**Change point detection:** Scans `service_rollup` buckets within the window for the largest jump (>2 standard deviations) in the symptom metric.

**Correlated log patterns:** Queries logs around the change point time, groups by body substring similarity (simple prefix matching, not drain-style — full pattern detection deferred).

**Data source:** `service_rollup` for metrics/baseline/change points, `edge_rollup` for dependencies, log Parquet for correlated logs, span Parquet for slow operations.

---

#### 8. `compare` (enhanced, 3 modes now + 1 deferred)

Side-by-side comparison.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `mode` | `string` | `"services"` | Mode: `services`, `time`, `operations` (`deploys` deferred) |
| `left` | `object` | — | Left side (varies by mode) |
| `right` | `object` | — | Right side |
| `focus` | `string[]` | `["latency","errors","throughput"]` | Metrics to compare |

**Mode inputs:**

- `services`: `left: { "service": "A" }`, `right: { "service": "B" }`
- `time`: `service` required, `left: { "window": "ISO/ISO" }`, `right: { "window": "ISO/ISO" }`
- `operations`: `service` required, `left: { "operation": "GET /a" }`, `right: { "operation": "GET /b" }`

**Returns:**

```json
{
  "mode": "time",
  "left_label": "Before (12:00-14:00)",
  "right_label": "After (14:00-16:00)",
  "comparison": {
    "latency": {
      "left_p95_ms": 14.2,
      "right_p95_ms": 18.8,
      "change_pct": 32.4,
      "direction": "regression",
      "statistically_significant": true
    },
    "errors": {
      "left_rate": 0.012,
      "right_rate": 0.036,
      "change_pct": 200.0,
      "direction": "regression",
      "statistically_significant": true
    },
    "throughput": {
      "left_rpm": 4800,
      "right_rpm": 4750,
      "change_pct": -1.0,
      "direction": "stable",
      "statistically_significant": false
    }
  },
  "regressed_operations": [
    { "operation": "GET /_error", "left_p95_ms": 2.1, "right_p95_ms": 8.5, "change_pct": 304.8 }
  ],
  "improved_operations": [],
  "verdict": "Significant regression in latency (+32%) and errors (+200%)"
}
```

**Statistical significance:** Two-sample t-test on per-bucket values. Reports `statistically_significant: true` when p < 0.05.

**Data source:** `services` mode → `service_rollup`. `time` mode → `service_rollup` for the two windows. `operations` mode → span Parquet grouped by operation.

---

### Tier 4: Power

#### 9. `query` (replaces `query` + `schema`)

Raw SQL with clean column names via DuckDB views.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `sql` | `string` | — | SQL query (omit for schema reference) |
| `explain` | `boolean` | `false` | Return query plan |
| `max_rows` | `integer` | 1000 | Max rows |
| `timeout_ms` | `integer` | 30000 | Query timeout |

**DuckDB views (created at startup):**

```sql
CREATE OR REPLACE VIEW spans AS
SELECT
  "name=trace_id" AS trace_id,
  "name=span_id" AS span_id,
  "name=parent_span_id" AS parent_span_id,
  "name=service_name" AS service,
  "name=name" AS operation,
  "name=kind" AS kind,
  to_timestamp("name=start_unix_nano" / 1e9) AS start_time,
  to_timestamp("name=end_unix_nano" / 1e9) AS end_time,
  "name=duration_ms" AS duration_ms,
  "name=status_code" AS status,
  "name=status_msg" AS status_message,
  from_utf8("name=attributes_json") AS attributes_json,
  from_utf8("name=resource_json") AS resource_json,
  from_utf8("name=events_json") AS events_json,
  namespace, tenant
FROM read_parquet('{lake}/spans/**/*.parquet',
     hive_partitioning=true, union_by_name=true);

CREATE OR REPLACE VIEW logs AS
SELECT
  to_timestamp("name=time_unix_nano" / 1e9) AS time,
  "name=severity" AS severity,
  "name=body" AS body,
  "name=service_name" AS service,
  "name=trace_id" AS trace_id,
  "name=span_id" AS span_id,
  from_utf8("name=attributes_json") AS attributes_json,
  from_utf8("name=resource_json") AS resource_json,
  namespace, tenant
FROM read_parquet('{lake}/logs/**/*.parquet',
     hive_partitioning=true, union_by_name=true);

CREATE OR REPLACE VIEW metrics AS
SELECT
  to_timestamp("name=time_unix_nano" / 1e9) AS time,
  "name=name" AS name,
  "name=mtype" AS type,
  "name=value" AS value,
  "name=unit" AS unit,
  "name=service_name" AS service,
  from_utf8("name=attributes_json") AS attributes_json,
  from_utf8("name=resource_json") AS resource_json,
  namespace, tenant
FROM read_parquet('{lake}/metrics/**/*.parquet',
     hive_partitioning=true, union_by_name=true);
```

**`attr()` macro:**

```sql
CREATE OR REPLACE MACRO attr(json_col, key) AS
  json_extract_string(json_col, '$.' || key);
```

**No custom aggregate UDFs.** DuckDB natively supports `approx_quantile(duration_ms, 0.95)` and `avg(CASE WHEN status = 'STATUS_CODE_ERROR' THEN 1.0 ELSE 0.0 END)`. Schema examples show these patterns.

**When `sql` omitted, returns schema:**

```json
{
  "views": [
    {
      "name": "spans",
      "columns": [
        { "name": "trace_id", "type": "VARCHAR", "description": "Distributed trace identifier" },
        { "name": "service", "type": "VARCHAR", "description": "Service name" },
        { "name": "operation", "type": "VARCHAR", "description": "Span/operation name" },
        { "name": "duration_ms", "type": "DOUBLE", "description": "Span duration in milliseconds" },
        { "name": "status", "type": "VARCHAR", "description": "STATUS_CODE_OK, STATUS_CODE_ERROR, STATUS_CODE_UNSET" },
        { "name": "start_time", "type": "TIMESTAMP", "description": "Span start time" },
        { "name": "attributes_json", "type": "VARCHAR", "description": "Span attributes as JSON string" }
      ]
    }
  ],
  "rollup_tables": [
    {
      "name": "service_rollup",
      "columns": [
        { "name": "bucket", "type": "TIMESTAMP" },
        { "name": "service", "type": "VARCHAR" },
        { "name": "spans", "type": "BIGINT" },
        { "name": "error_rate", "type": "DOUBLE" },
        { "name": "p50_ms", "type": "DOUBLE" },
        { "name": "p95_ms", "type": "DOUBLE" }
      ]
    },
    {
      "name": "edge_rollup",
      "columns": [
        { "name": "bucket", "type": "TIMESTAMP" },
        { "name": "caller", "type": "VARCHAR" },
        { "name": "callee", "type": "VARCHAR" },
        { "name": "calls", "type": "BIGINT" },
        { "name": "error_rate", "type": "DOUBLE" },
        { "name": "avg_ms", "type": "DOUBLE" }
      ]
    }
  ],
  "examples": [
    "SELECT service, operation, count(*) as cnt, approx_quantile(duration_ms, 0.95) as p95 FROM spans WHERE start_time >= now() - interval '1 hour' AND status = 'STATUS_CODE_ERROR' GROUP BY service, operation ORDER BY p95 DESC LIMIT 10",
    "SELECT service, severity, count(*) FROM logs WHERE time >= now() - interval '1 hour' GROUP BY service, severity ORDER BY count(*) DESC",
    "SELECT name, service, avg(value) FROM metrics WHERE time >= now() - interval '1 hour' GROUP BY name, service",
    "SELECT bucket, service, spans, error_rate, p95_ms FROM service_rollup WHERE bucket >= now() - interval '6 hours' ORDER BY bucket",
    "SELECT attr(attributes_json, 'http.method') as method, count(*) FROM spans WHERE start_time >= now() - interval '1 hour' GROUP BY method"
  ]
}
```

**Returns (with sql):**

```json
{
  "columns": ["service", "operation", "cnt", "p95"],
  "rows": [
    ["checkout", "POST /api/orders", 156, 89.3],
    ["payment", "POST /api/charge", 142, 45.1]
  ],
  "row_count": 2,
  "execution_time_ms": 42,
  "query_plan": null
}
```

---

## Service Layer Changes

### Modified Methods

| Current | New | Changes |
|---------|-----|---------|
| `Status()` | `Overview()` | Health score computation, `include` sections, `sort_services_by` |
| `Diagnose()` | `Diagnose()` | Baseline comparison (7d rollup history), change point detection (>2σ jump), log correlation |
| `Topology()` | `Topology()` | `depth` (BFS), `edge_type` filter, `blast_radius`, `critical_paths` |
| `Trace()` | `Trace()` | `compare_to` (align spans by operation, compute deltas), `include_metrics` (rollup snapshot) |
| `Compare()` | `Compare()` | 3 modes: `services`, `time`, `operations` |

### New Methods (split from Find)

| Method | Description |
|--------|-------------|
| `Spans(ctx, SpanParams)` | Query span Parquet, optional `group_by` over fixed fields |
| `Logs(ctx, LogParams)` | Query log Parquet, optional `group_by`, `trace_id` correlation |
| `Metrics(ctx, MetricParams)` | `list` → distinct metric names; `query` → timeseries with anomaly detection |

### Removed Methods

| Method | Replaced by |
|--------|-------------|
| `Find()` | `Spans()`, `Logs()`, `Metrics()` |
| `Timeline()` | `Metrics()` (timeseries with anomaly detection) |

### group_by Fixed Fields

| Signal | Groupable fields |
|--------|-----------------|
| spans | `service`, `operation`, `status`, `kind`, `http.method`, `http.status_code` |
| logs | `service`, `severity` |

When `group_by` is provided, SQL switches from `SELECT columns ... LIMIT N` to `SELECT group_keys, count(*), percentiles ... GROUP BY group_keys`. `http.method` and `http.status_code` are extracted from `attributes_json` via `json_extract_string`.

---

## Migration: Current → New

| Current tool | New tool | Migration |
|-------------|----------|-----------|
| `status` | `overview` | Rename handler, add health score, restructure JSON |
| `topology` | `topology` | Add params and return fields |
| `diagnose` | `diagnose` | Add baseline/changepoint/log sections |
| `find` | `spans` + `logs` + `metrics` | Split into 3 handlers, delete `find` |
| `trace` | `trace` | Add params and return fields |
| `timeline` | `metrics` | Rewrite as metrics tool with `list`/`query` actions |
| `compare` | `compare` | Add mode param and 2 new modes |
| `query` | `query` | Add DuckDB views, merge schema, add explain/timeout |
| `schema` | `query` (no sql) | Merge into query tool |

**Breaking change:** All tools switch to JSON-only. `format` parameter removed. Clients expecting ASCII/HTML must update.

---

## Deferred (Future Phases)

### Phase 2: Alerts & SLOs

| Tool | Requirements |
|------|-------------|
| `alerts` | Rule storage, continuous evaluation loop, state machine (firing/ack/resolved/silenced), history |
| `slo` | SLO definition storage (config or DB), rolling SLI computation, error budget tracking, multi-window burn rates |

### Phase 3: Advanced Features

| Feature | Requirements |
|---------|-------------|
| `compare(mode="deploys")` | Deploy event tracking (API or heuristic from `service.version` changes) |
| `logs(detect_patterns=true)` | Incremental drain-style log parser, `log_patterns` rollup table |
| Report system redesign | New rendering pipeline for JSON-to-HTML, tied to UI changes |

---

## Performance Considerations

1. **Rollup-first:** `overview`, `topology`, `metrics` (service-level), `diagnose` baseline/changepoints all query pre-aggregated rollup tables. Sub-100ms.

2. **Parquet scans bounded:** `spans`, `logs`, `metrics` (raw) scan Parquet with time-partition pruning. Queries include `WHERE start_time >= ...` which maps to hive partition filtering on `year/month/day/hour`.

3. **group_by on fixed fields:** Known fields can be indexed or pre-aggregated in future rollup tables. No arbitrary attribute extraction in hot paths.

4. **View overhead:** DuckDB views are query-time macros, not materialized. No storage cost. The `read_parquet(glob)` in each view is re-evaluated per query with partition pruning.

5. **Existing cache:** The 10s TTL cache in `query/perf.go` continues to serve identical requests.
