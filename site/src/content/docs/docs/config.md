---
title: Environment
description: All environment variables Fanout reads at startup.
---

Fanout reads config from environment variables (or a `.env` file next to the binary). `.env` is loaded first (non-destructive), then `.env.${ENV}` overrides it — `ENV` defaults to `development`.

Some variables are **required** — Fanout refuses to start without them. These are called out below.

## Core

| Variable | Default | Description |
|---|---|---|
| `HTTP_ADDR` | `:7520` | HTTP server address. |
| `OTLP_GRPC_ADDR` | `127.0.0.1:4317` | OTLP gRPC address. Bound to loopback by default — set to `:4317` (or a specific interface) to accept remote ingest. |
| `DATA_DIR` | `./data` | Storage root for telemetry, query cache, and control data. |
| `DEFAULT_NAMESPACE` | `default` | Namespace assigned to OTLP payloads that don't carry `service.namespace`. |
| `DUCKDB_MEMORY` | `512MB` | DuckDB memory limit. Raise for larger workloads. |

## Flush & rollups

| Variable | Default | Description |
|---|---|---|
| `FLUSH_SECONDS` | `15` | Flush pending rows at this interval. |
| `FLUSH_BATCH_SIZE` | `50000` | Max rows per writer flush. |
| `ROLLUP_EVERY` | `60` | Rollup interval (seconds). |
| `RETENTION_DAYS` | `30` | Drop Parquet files older than N days. `0` disables retention. |

## JWT (required)

| Variable | Default | Description |
|---|---|---|
| `JWT_SECRET` | — | **Required.** HS256 access-token signing key. Must be ≥ 32 characters. |
| `JWT_REFRESH_SECRET` | — | **Required.** HS256 refresh-token signing key. Must be ≥ 32 characters and different from `JWT_SECRET`. |

## SMTP (required)

Email delivery is used for login codes and admin invites. All four fields are required on startup.

| Variable | Default | Description |
|---|---|---|
| `SMTP_HOST` | — | **Required.** SMTP server hostname. |
| `SMTP_PORT` | `587` | SMTP port. `465` → implicit TLS; `587` / `25` → STARTTLS if advertised. |
| `SMTP_USER` | — | **Required.** SMTP username. |
| `SMTP_PASS` | — | **Required.** SMTP password / API key. |
| `SMTP_FROM` | — | **Required.** From header, e.g. `"Fanout <noreply@yours>"`. |

## AI (required)

| Variable | Default | Description |
|---|---|---|
| `AI_PROVIDER` | `anthropic` | `anthropic` or `openai`. |
| `AI_API_KEY` | — | **Required.** Provider API key. |
| `AI_MODEL` | — | Provider-specific model override. |
| `AI_BASE_URL` | — | Provider-specific base URL override (for proxies). |

## Alerts

| Variable | Default | Description |
|---|---|---|
| `ALERT_ENABLED` | `true` | Toggle the alert engine. |
| `ALERT_EVAL_INTERVAL` | `30` | Evaluation interval (seconds). |
| `ALERT_HISTORY_DAYS` | `7` | How long resolved alerts stay queryable. |

## MCP

| Variable | Default | Description |
|---|---|---|
| `MCP_ENABLED` | `true` | Expose the MCP server at `/mcp`. |

## TLS (optional — only for direct termination)

Typical deployments run behind a reverse proxy that terminates TLS. If you want Fanout to terminate TLS itself (private network / compliance / no proxy):

| Variable | Default | Description |
|---|---|---|
| `TLS_CERT_FILE` | — | Path to server certificate. |
| `TLS_KEY_FILE` | — | Path to server private key. |

Setting both enables TLS on **both** `HTTP_ADDR` and `OTLP_GRPC_ADDR`. TLS 1.3 minimum. Setting only one triggers a startup error.
