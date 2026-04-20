---
title: Alerts
description: Define expr-lang rules that fire on rollup signals.
---

Fanout has a built-in alert engine. Rules are written in [expr-lang](https://expr-lang.org) and evaluated every 30 seconds against the `service_rollup` table.

:::caution
Alerts are an active area of development. The UI and notification surfaces are evolving — see the [home](https://github.com/labstack/fanout) for the current state.
:::

## Rule shape

A rule has:

- A **name** (shown on the Alerts page).
- An **expression** that evaluates to a boolean per service.
- A **severity** (`critical`, `warning`, `info`).
- An optional **for** duration (minutes the condition must hold before firing).

## Examples

```text
name:  "error rate > 5% (5m)"
expr:  error_rate > 0.05
for:   5m
severity: critical
```

```text
name:  "p95 latency > 2s (10m)"
expr:  p95_ms > 2000
for:   10m
severity: warning
```

## Fields available in expressions

| Field | Type | Description |
|---|---|---|
| `service` | string | Service name being evaluated. |
| `spans` | int | Request count in the bucket. |
| `error_rate` | float | 0.0 – 1.0 error rate. |
| `p50_ms` | float | P50 latency (ms). |
| `p95_ms` | float | P95 latency (ms). |

## Lifecycle

- **Firing**: condition met for the `for` duration. Shown in the nav bar.
- **Resolved**: condition is no longer met.
- **Acknowledged**: operator ack'd — suppresses noise without clearing the firing state.

See the Alerts page in the admin UI for the current set.
