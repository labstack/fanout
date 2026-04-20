---
title: Environment
description: Every environment variable Fanout reads at startup.
---

Fanout is configured entirely through environment variables. A `.env` file next to the binary is loaded first (non-destructive), then `.env.${ENV}` overrides it — `ENV` defaults to `development`.

Variables marked **Required** must be set or Fanout will refuse to start. Everything else has a sensible default.

## Network

| Variable | Default | Description |
| --- | --- | --- |
| `HTTP_ADDR` | `:7520` | Address the web UI, API, and MCP endpoint listen on. |
| `OTLP_GRPC_ADDR` | `127.0.0.1:4317` | OTLP gRPC ingest address. Loopback by default — set to `:4317` (or a specific interface) to accept ingest from off-host. The official Docker image overrides this to `:4317`. |
| `DEFAULT_NAMESPACE` | `default` | Namespace assigned to OTLP payloads that don't carry `service.namespace`. |

## Storage

| Variable | Default | Description |
| --- | --- | --- |
| `DATA_DIR` | `./data` | Storage root. Telemetry, query cache, and application state live here. |
| `DUCKDB_MEMORY` | `512MB` | In-memory budget for the embedded query engine. Raise for larger working sets. |
| `RETENTION_DAYS` | `30` | Drop telemetry files older than N days. `0` keeps everything forever. |

## Ingest tuning

Defaults work well for most installs. Tune these only if you observe back-pressure or stale data.

| Variable | Default | Description |
| --- | --- | --- |
| `FLUSH_SECONDS` | `15` | How often pending rows are flushed to disk. Lower = fresher UI; higher = less I/O. |
| `FLUSH_BATCH_SIZE` | `50000` | Cap on rows per flush, regardless of interval. |
| `ROLLUP_EVERY` | `60` | How often per-minute rollups are recomputed. |

## Authentication (required)

Two HMAC secrets sign the access and refresh tokens. They must differ, and each must be at least 32 characters.

| Variable | Default | Description |
| --- | --- | --- |
| `JWT_SECRET` | — | **Required.** HS256 signing key for short-lived access tokens. |
| `JWT_REFRESH_SECRET` | — | **Required.** HS256 signing key for refresh tokens. Must differ from `JWT_SECRET`. |

Generate fresh secrets with `openssl rand -hex 32`.

## Email (required)

Fanout sends login codes and admin invites by email. All four SMTP variables are required at startup.

| Variable | Default | Description |
| --- | --- | --- |
| `SMTP_HOST` | — | **Required.** SMTP server hostname. |
| `SMTP_PORT` | `587` | `465` uses implicit TLS; `587` and `25` use STARTTLS when offered. |
| `SMTP_USER` | — | **Required.** SMTP username. |
| `SMTP_PASS` | — | **Required.** SMTP password or API key. |
| `SMTP_FROM` | — | **Required.** From header — e.g. `"Fanout" <noreply@example.com>`. |

## AI provider (required)

The chat investigator and MCP loop call out to an LLM provider.

| Variable | Default | Description |
| --- | --- | --- |
| `AI_PROVIDER` | `anthropic` | `anthropic` or `openai`. |
| `AI_API_KEY` | — | **Required.** Provider API key. |
| `AI_MODEL` | (provider default) | Override the default model — e.g. `claude-sonnet-4-6`, `gpt-4.1`. |
| `AI_BASE_URL` | (provider default) | Override the API base URL — useful for proxies and gateways. |

## Alerts

| Variable | Default | Description |
| --- | --- | --- |
| `ALERT_ENABLED` | `true` | Set to `false` to disable the alert engine entirely. |
| `ALERT_EVAL_INTERVAL` | `30` | How often (seconds) rules are evaluated against fresh rollups. |
| `ALERT_HISTORY_DAYS` | `7` | How long resolved alerts stay queryable in the UI and via the `alerts` MCP tool. |

## MCP

| Variable | Default | Description |
| --- | --- | --- |
| `MCP_ENABLED` | `true` | Expose the MCP server at `/mcp`. Disable if you don't want it reachable. |

## TLS

Most production deployments terminate TLS at a reverse proxy (Caddy, nginx, Traefik). If you'd rather have Fanout do it directly — for an air-gapped or compliance setup — provide both files. Setting just one is a startup error.

| Variable | Default | Description |
| --- | --- | --- |
| `TLS_CERT_FILE` | — | Path to the server certificate (PEM). |
| `TLS_KEY_FILE` | — | Path to the server private key (PEM). |

When set, both `HTTP_ADDR` and `OTLP_GRPC_ADDR` listen with TLS 1.3.
