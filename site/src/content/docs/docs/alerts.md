---
title: Alerts
description: Define expr-lang rules that fire on rollup signals.
---

Fanout has a built-in alert engine. Rules are written in [expr-lang](https://expr-lang.org) and evaluated every 30 seconds by default (configurable via `ALERT_EVAL_INTERVAL`).

:::caution
Alerts are an active area of development. The UI and notification surfaces are evolving — see the repo for the current state.
:::

## Rule shape

A rule has:

- A **name** (shown on the Alerts page).
- An **expression** that evaluates to a boolean per service.
- An optional **for_seconds** duration (the condition must hold for this many seconds before firing).
- Optional webhook delivery (`webhook_url`, `webhook_headers`, `webhook_template`).

## Examples

```text
name:        "error rate > 5% sustained 5 min"
expression:  error_rate > 0.05
for_seconds: 300
```

```text
name:        "p95 latency > 2s sustained 10 min"
expression:  p95 > 2000
for_seconds: 600
```

```text
name:        "sudden drop in throughput"
expression:  throughput_delta < -0.5 && throughput > 10
for_seconds: 120
```

## Fields available in expressions

| Field | Type | Description |
|---|---|---|
| `service` | string | Service name being evaluated. |
| `error_rate` | float | Error rate in this evaluation window (0.0 – 1.0). |
| `p50` | float | Median latency (ms). |
| `p95` | float | 95th-percentile latency (ms). |
| `throughput` | float | Requests per second. |
| `log_count` | float | Log entries in this evaluation window. |
| `z_score` | float | Anomaly z-score vs. historical baseline. |
| `health_score` | float | Composite health score (lower = worse). |
| `error_rate_delta` | float | Relative change in `error_rate` vs. baseline. |
| `p95_delta` | float | Relative change in `p95` vs. baseline. |
| `throughput_delta` | float | Relative change in `throughput` vs. baseline. |

## Lifecycle

- **Pending**: expression just became true; waiting out `for_seconds`.
- **Firing**: condition has held for `for_seconds`. Webhooks deliver; UI shows it in the nav bar.
- **Resolved**: expression is no longer true. Optional resolve notification fires if `notify_on_resolve` is set.

Manage rules via the Alerts page in the admin UI, or via the `alert_rules` / `alerts` [MCP tools](/docs/mcp/).
