# Alert System Design

**Date:** 2026-03-27 (revised 2026-03-28)
**Status:** Approved

## Overview

Rule-based alerting for Fanout. Users define expressions (via expr-lang) that evaluate against rollup and anomaly data. When conditions hold, webhooks fire. Managed entirely through MCP tools.

## Goals

- **Performant**: Single DuckDB query per eval cycle, ~50ns per expression eval
- **Easy to use**: Conversational rule creation via MCP, test-before-save workflow
- **Robust**: Compound conditions, rate-of-change, anomaly-based, absence detection
- **No flapping**: `for` duration, cooldown, repeat intervals
- **Simple**: 2 tables, 3 MCP tools

## Non-Goals

- Built-in Slack/PagerDuty integrations (webhooks cover these)
- UI for alert management (MCP-first)
- Multi-tenant alert isolation (single-tenant product)
- Separate delivery audit table (slog handles this)
- Silence management (use enabled/disabled on rules)

## Architecture

```
┌────────────────────────────────────────────────────────────┐
│                        main.go                             │
│                                                            │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌──────────┐ │
│  │ Ingest   │  │ Lake     │  │ Query     │  │ Intel    │ │
│  │ gRPC     │→ │ Writer   │→ │ DuckDB    │← │ Detector │ │
│  └──────────┘  └──────────┘  └─────┬─────┘  └────┬─────┘ │
│                                    │              │        │
│                              ┌─────▼──────────────▼─────┐  │
│                              │     Alert Engine         │  │
│                              │  ┌────────┐ ┌─────────┐  │  │
│                              │  │ SQLite │ │ expr-   │  │  │
│                              │  │ fanout │ │ lang VM │  │  │
│                              │  │ .db    │ │ eval    │  │  │
│                              │  └────────┘ └─────────┘  │  │
│                              └─────────────┬────────────┘  │
│                                            │               │
│                    ┌───────────┐    ┌──────▼──────┐        │
│                    │ Service   │    │ Webhooks    │        │
│                    │ Layer     │    │ (async)     │        │
│                    └─────┬─────┘    └─────────────┘        │
│                          │                                 │
│                    ┌─────▼─────┐                           │
│                    │ MCP Tools │  ← alert_rules,           │
│                    │           │    alerts, alert_env       │
│                    └───────────┘                           │
└────────────────────────────────────────────────────────────┘
```

Alert engine receives `*query.Duck` (rollup queries) and `*intelligence.Detector` (anomaly z-scores). SQLite lives at `lake/fanout.sqlite`. MCP tools are the sole management interface. Webhook actions fire asynchronously — never block the eval loop.

## Data Model — SQLite Schema

All primary keys are UUID v7 (time-ordered, sortable). Two tables.

```sql
CREATE TABLE alert_rules (
    id                TEXT PRIMARY KEY,  -- uuid v7
    name              TEXT NOT NULL,
    description       TEXT,
    enabled           INTEGER DEFAULT 1,
    service           TEXT,              -- specific service, or '*' for all
    namespace         TEXT DEFAULT '',
    expression        TEXT NOT NULL,     -- expr-lang: "error_rate > 0.05 && p95 > 1000"
    for_seconds       INTEGER DEFAULT 60,
    cooldown_s        INTEGER DEFAULT 600,
    repeat_interval_s INTEGER DEFAULT 3600,
    -- Webhook (inline — one per rule; need two destinations? make two rules)
    webhook_url       TEXT,
    webhook_headers   TEXT,              -- JSON: {"Content-Type": "application/json"}
    webhook_template  TEXT,              -- body template: "{{.Alert.Service}}: {{.Rule.Name}}"
    notify_on_resolve INTEGER DEFAULT 0,
    created_at        TEXT DEFAULT (datetime('now')),
    updated_at        TEXT DEFAULT (datetime('now'))
);

CREATE TABLE alerts (
    id                    TEXT PRIMARY KEY,  -- uuid v7
    rule_id               TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    service               TEXT NOT NULL,
    state                 TEXT NOT NULL,     -- 'pending', 'firing', 'resolved'
    value                 REAL,
    fired_at              TEXT,
    resolved_at           TEXT,
    repeated_at           TEXT,
    last_eval             TEXT,
    -- Delivery status (inline — slog has the full audit trail)
    last_delivery_status  TEXT,             -- 'success', 'failed'
    last_delivery_at      TEXT,
    created_at            TEXT DEFAULT (datetime('now')),
    UNIQUE(rule_id, service)
);
```

**Design decisions:**

- **Webhook inline on rule**: One webhook per rule. Need two destinations for the same condition? Create two rules with the same expression. Eliminates the actions table and the actions MCP tool.
- **No silences table**: Claude disables rules via `enabled=false` and re-enables after deploy. Silences are a UI concept for humans clicking buttons. MCP doesn't need them.
- **No deliveries table**: `slog` logs every webhook attempt with status, URL, and response. `last_delivery_status` + `last_delivery_at` on the alert row covers "did it fire?" without a separate table.
- **UNIQUE(rule_id, service)**: One active alert per rule+service pair. Structural deduplication.

## Rule Engine — expr-lang

### Environment Struct

Every expression evaluates against this struct. One instance per service per eval cycle.

```go
type AlertEnv struct {
    // From service_rollup (latest 5-minute window)
    ErrorRate   float64 `expr:"error_rate"`    // 0.0-1.0
    P50         float64 `expr:"p50"`           // ms
    P95         float64 `expr:"p95"`           // ms
    P99         float64 `expr:"p99"`           // ms
    Throughput  float64 `expr:"throughput"`    // requests/min
    LogCount    float64 `expr:"log_count"`     // logs in window

    // From intelligence detector
    ZScore      float64 `expr:"z_score"`       // max anomaly z-score
    HealthScore float64 `expr:"health_score"`  // 0-100

    // Rate-of-change (current vs previous 5-min window)
    ErrorRateDelta  float64 `expr:"error_rate_delta"`   // percent change
    P95Delta        float64 `expr:"p95_delta"`          // percent change
    ThroughputDelta float64 `expr:"throughput_delta"`   // percent change

    // Metadata
    Service   string `expr:"service"`
    Namespace string `expr:"namespace"`
}
```

### Example Expressions

```python
# Simple threshold
error_rate > 0.05

# Compound
error_rate > 0.05 && p95 > 1000

# Rate of change — P95 jumped 200%+
p95_delta > 200

# Anomaly-based
z_score > 3.0

# Traffic drop (absence)
throughput < 10

# Complex
(error_rate > 0.1 || p95 > 2000) && throughput > 100
```

### Compilation

Rules are compiled once at load time and cached as `*vm.Program`:

```go
program, err := expr.Compile(
    rule.Expression,
    expr.Env(AlertEnv{}),  // type-check against struct fields
    expr.AsBool(),         // reject non-boolean expressions at compile time
)
```

### Eval Loop

Single goroutine, runs every 30s (configurable via `ALERT_EVAL_INTERVAL`):

```go
func (e *Engine) evaluate(ctx context.Context) {
    rules := e.store.ListEnabled()

    // ONE DuckDB query for ALL services — current + previous window
    envs := e.buildEnvs(ctx)  // map[service]AlertEnv

    for _, rule := range rules {
        services := e.resolveServices(rule, envs)
        for _, svc := range services {
            if !rule.Enabled { continue }
            result, err := expr.Run(rule.Program, envs[svc])  // ~50ns
            if err != nil { slog.Error(...); continue }
            e.transition(rule, svc, result.(bool), envs[svc])
        }
    }
}
```

`buildEnvs` runs a single rollup query returning all services with current values and deltas:

```sql
WITH current AS (
    SELECT service,
           avg(error_rate) as error_rate,
           avg(p50_ms) as p50, avg(p95_ms) as p95, avg(p99_ms) as p99,
           sum(spans) as throughput, sum(log_count) as log_count
    FROM service_rollup
    WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
    GROUP BY service
),
previous AS (
    SELECT service,
           avg(error_rate) as error_rate, avg(p95_ms) as p95, sum(spans) as throughput
    FROM service_rollup
    WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '10 minutes'
      AND bucket < (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
    GROUP BY service
)
SELECT c.*,
       ((c.error_rate - p.error_rate) / NULLIF(p.error_rate, 0)) * 100 as error_rate_delta,
       ((c.p95 - p.p95) / NULLIF(p.p95, 0)) * 100 as p95_delta,
       ((c.throughput - p.throughput) / NULLIF(p.throughput, 0)) * 100 as throughput_delta
FROM current c LEFT JOIN previous p ON c.service = p.service
```

Z-scores and health_score are read from `detector.LatestSnapshot()` — no additional query.

**Performance budget:** 1 SQL query (~5-50ms) + N rule evals at ~50ns each. 100 rules x 50 services = 5000 evals = 0.25ms of expr overhead. DuckDB query dominates.

## Alert State Machine

```
                    expr true
    (none) ────────────────────▶ PENDING
                                    │
                                    │ held for `for_seconds` (default 60)
                                    ▼
                               FIRING ──▶ webhook (fire)
                                    │         │
                expr false          │         │ every `repeat_interval_s` (default 3600)
                    │               │         ▼
                    ▼               │    webhook (reminder)
               RESOLVED ──▶ webhook (resolve, if notify_on_resolve)
                    │
                    │ `cooldown_s` elapsed (default 600)
                    ▼
               (pruned from active, kept in history for 7 days)
```

### Transition Logic

| Current State | Condition | Next State | Action |
|---|---|---|---|
| (none) | expr true, for=0 | FIRING | fire webhook |
| (none) | expr true, for>0 | PENDING | — |
| PENDING | still true, for elapsed | FIRING | fire webhook |
| PENDING | expr false | (deleted) | — |
| FIRING | still true | FIRING | reminder if repeat_interval elapsed |
| FIRING | expr false | RESOLVED | resolve webhook (if notify_on_resolve) |
| RESOLVED | cooldown elapsed | (pruned) | — |

### Defaults (Opinionated)

| Parameter | Default | Rationale |
|---|---|---|
| `for_seconds` | 60 | One minute filters single-bucket noise |
| `cooldown_s` | 600 | 10 minutes prevents flapping |
| `repeat_interval_s` | 3600 | Hourly reminders for long-firing alerts |
| History retention | 7 days | Enough for "what happened last week" |

## Actions — Webhook Execution

Webhook config lives directly on the rule. Template uses Go `text/template` syntax.

### Template Variables

```go
type ActionContext struct {
    Rule struct {
        ID, Name, Expression, Description string
    }
    Alert struct {
        ID, Service, State string
        Value              float64
        FiredAt, ResolvedAt string
    }
    Env   AlertEnv  // full environment values
    Event string    // "fire", "resolve", "reminder"
}
```

### Execution

- Async goroutine per webhook — never blocks eval loop
- HTTP client: 5s connect timeout, 10s total timeout
- 3 retries with linear backoff (2s, 4s) for 5xx/network errors
- No retry on 4xx (config error, not transient)
- Every attempt logged via `slog`
- `last_delivery_status` + `last_delivery_at` updated on the alert row

## MCP Tools

Three tools registered in `internal/mcp/tool_alerts.go`.

### `alert_rules` — Rule CRUD + test

| Action | Params | Returns |
|---|---|---|
| `create` | name, expression, service, for_seconds, cooldown_s, repeat_interval_s, webhook_url, webhook_headers, webhook_template, notify_on_resolve, description | Rule object with compiled status |
| `list` | — | All rules with last_fired info |
| `get` | rule_id | Rule + active alerts |
| `update` | rule_id, (any field) | Updated rule (recompiles expression if changed) |
| `delete` | rule_id | Confirmation |
| `enable` | rule_id | Enables a disabled rule |
| `disable` | rule_id, duration | Disables rule (optionally for a duration, then auto-re-enables) |
| `test` | expression, service | {triggered, env values} — dry-run against live data |
| `test_webhook` | rule_id | Sends test webhook, returns delivery result |

### `alerts` — View alert state

| Params | Returns |
|---|---|
| state (firing/pending/resolved/all), service, rule_id, window, limit | Alert list with delivery status + summary counts |

### `alert_env` — Expression reference + live values

| Params | Returns |
|---|---|
| (none) | Available fields with types and descriptions |
| service | Live values for that service + example expressions |

### Conversational Flow

```
User: "Alert me if checkout gets slow"

Claude: calls alert_env(service="checkout")
  → sees p95=340, error_rate=0.01

Claude: calls alert_rules(action="test", expression="p95 > 800", service="checkout")
  → triggered: false

Claude: "I'll set a rule for P95 > 800ms sustained for 2 minutes.
         Current P95 is 340ms. Where should I send the alert?"

User: "Slack #ops channel"

Claude: calls alert_rules(action="create",
         name="checkout latency",
         expression="p95 > 800",
         service="checkout",
         for_seconds=120,
         webhook_url="https://hooks.slack.com/...",
         webhook_template="{\"text\": \"{{.Alert.Service}}: {{.Rule.Name}} — P95 {{.Env.P95}}ms\"}")

Claude: calls alert_rules(action="test_webhook", rule_id="...")
  → test webhook delivered successfully

Claude: "Done. Rule is active. Tested the webhook — it delivered."
```

## Package Structure

```
internal/alert/
    store.go       — SQLite schema, migrations, CRUD
    engine.go      — eval loop, buildEnvs, transition logic
    expr.go        — AlertEnv struct, compilation, caching
    webhook.go     — webhook execution, templates, retries
    types.go       — Rule, Alert structs

internal/mcp/
    tool_alerts.go — 3 MCP tool handlers
```

## Dependencies

| Package | Purpose |
|---|---|
| `github.com/expr-lang/expr` | Expression compilation + eval (zero deps, pure Go) |
| `modernc.org/sqlite` | Pure Go SQLite driver (no extra CGO) |
| `github.com/google/uuid` | UUID v7 generation |

## Configuration

| Env Var | Default | Description |
|---|---|---|
| `ALERT_ENABLED` | `true` | Enable alert engine |
| `ALERT_EVAL_INTERVAL` | `30s` | Eval loop frequency |
| `ALERT_HISTORY_DAYS` | `7` | Days to retain resolved alerts |

## Startup Sequence

```
1. Lake Writer         (existing)
2. DuckDB + Rollups    (existing)
3. Intelligence Detector (existing)
4. SQLite Store        (NEW — open fanout.sqlite, run migrations)
5. Alert Engine        (NEW — needs duck + detector + store)
6. Service Layer       (existing)
7. MCP Server          (modified — receives alert engine)
8. HTTP Server         (existing)
```

Graceful shutdown: engine respects `ctx.Done()` from the existing cancellation chain.
