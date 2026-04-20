---
title: Alerts
description: Define expr-lang rules that evaluate against rollup data and deliver via webhook.
---

Fanout has a built-in alert engine. Rules are written in [expr-lang](https://expr-lang.org), evaluated every 30 seconds by default (`ALERT_EVAL_INTERVAL`), and delivered by webhook to wherever you already get pages.

:::caution
Alerts are an active area of development. The UI and notification surfaces are evolving — capabilities described here reflect the current release.
:::

## Anatomy of a rule

| Field | Description |
| --- | --- |
| `name` | What you'll see on the Alerts page and in webhook payloads. |
| `expression` | An expr-lang boolean evaluated per service per interval. |
| `for_seconds` | How long the expression must hold before the rule fires. Optional; defaults to `0` (fire immediately). |
| `webhook_url` | Where to POST the alert payload. Optional. |
| `webhook_headers` | Extra HTTP headers — typically auth. Optional. |
| `webhook_template` | Override the default JSON payload. Optional. |
| `notify_on_resolve` | Send a follow-up POST when the condition clears. Optional. |

## Recipes

These are battle-tested starting points. Adjust thresholds to match your service's normal behaviour.

**Sustained error rate.** Catches real degradation while ignoring momentary spikes.

```text
name:        "error rate > 5% for 5 min"
expression:  error_rate > 0.05
for_seconds: 300
```

**Latency regression.** Fires only when 95th-percentile latency stays high — single slow requests don't trigger it.

```text
name:        "p95 latency > 2s for 10 min"
expression:  p95 > 2000
for_seconds: 600
```

**Throughput collapse.** Drops in traffic often signal an upstream outage. The `throughput > 10` clause prevents firing on naturally low-traffic services. Delta values are percentage points — `-50` means traffic halved.

```text
name:        "sudden traffic drop"
expression:  throughput_delta < -50 && throughput > 10
for_seconds: 120
```

**Anomaly score.** Catches "something looks off" without you having to specify what.

```text
name:        "anomaly: z-score > 3"
expression:  z_score > 3
for_seconds: 180
```

**Compound condition.** Fires when both error rate and latency are abnormal — useful for promoting noise into actual pages. Both deltas are in percent (`50` = +50% vs. baseline).

```text
name:        "error + latency regression"
expression:  error_rate_delta > 50 && p95_delta > 30
for_seconds: 300
```

## Available fields

Every rule has these fields in scope.

| Field | Type | Description |
| --- | --- | --- |
| `service` | string | Name of the service being evaluated. Useful for `service == "checkout"`. |
| `error_rate` | float | Error rate in this window, `0.0` – `1.0`. |
| `p50` | float | Median latency, milliseconds. |
| `p95` | float | 95th-percentile latency, milliseconds. |
| `throughput` | float | Requests per second over the window. |
| `log_count` | float | Log entries seen in the window. |
| `z_score` | float | Anomaly score against the historical baseline. |
| `health_score` | float | Composite score, lower is worse. |
| `error_rate_delta` | float | Percentage change in `error_rate` vs. baseline (e.g. `50` = +50%). |
| `p95_delta` | float | Percentage change in `p95` vs. baseline. |
| `throughput_delta` | float | Percentage change in `throughput` vs. baseline (e.g. `-50` = halved). |

`p99` is available in the UI and through the `spans` MCP tool, but **not** in alert expressions today. If you need a 99th-percentile rule, file an issue.

## Lifecycle

A rule moves through three states:

- **Pending** — the expression just became true. The engine waits out `for_seconds`.
- **Firing** — the condition has held long enough. Webhooks deliver, and the alert appears in the UI's nav badge.
- **Resolved** — the expression returned false. If `notify_on_resolve` is set, a final webhook fires.

Resolved alerts stay queryable for `ALERT_HISTORY_DAYS` (default 7) — visible in the UI and via the `alerts` MCP tool.

## Managing rules

Two surfaces:

- **Alerts page in the admin UI** — click-through editor with live preview.
- **MCP tools** — `alert_rules` for management, `alerts` for inspecting state. Useful when you want to edit rules from your editor or have an AI loop tune thresholds based on real history.

## Webhook payload

A firing rule POSTs JSON to `webhook_url`:

```json
{
  "rule": "error rate > 5% for 5 min",
  "service": "checkout",
  "namespace": "default",
  "fired_at": "2026-04-20T14:22:08Z",
  "expression": "error_rate > 0.05",
  "values": {
    "error_rate": 0.082,
    "p50": 94,
    "p95": 412,
    "throughput": 1180
  }
}
```

Override the shape with `webhook_template` if your downstream wants a different schema (PagerDuty, Slack, OpsGenie, etc.).
