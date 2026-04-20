---
title: Environment
description: All environment variables Fanout reads at startup.
---

Fanout reads config from environment variables (or a `.env` file next to the binary). Defaults are sensible for local dev; production deployments should set at least `JWT_SECRET`, `JWT_REFRESH_SECRET`, `FRONTEND_URL`, and the SMTP block.

## Core

| Variable | Default | Description |
|---|---|---|
| `HTTP_ADDR` | `:7520` | HTTP server address. |
| `OTLP_GRPC_ADDR` | `:4317` | OTLP gRPC address. |
| `DATA_DIR` | `./data` | Storage root for telemetry, query cache, and control data. |
| `FRONTEND_URL` | `http://localhost:7520` | Absolute URL of the admin UI, used in outbound email links. |

## Retention & performance

| Variable | Default | Description |
|---|---|---|
| `RETENTION_DAYS` | `30` | Drop Parquet files older than N days. `0` disables retention. |
| `FLUSH_SECONDS` | `15` | Flush pending rows at this interval. |
| `FLUSH_BATCH_SIZE` | `50000` | Max rows per writer flush. |
| `ROLLUP_EVERY` | `60` | Rollup interval (seconds). |

## Auth

| Variable | Default | Description |
|---|---|---|
| `JWT_SECRET` | — | HS256 access-token signing key. **Required in prod.** |
| `JWT_REFRESH_SECRET` | — | HS256 refresh-token signing key. **Required in prod.** |

## TLS (optional — only for direct termination)

Typical deployments run behind a reverse proxy that terminates TLS. If you want Fanout to terminate TLS itself (private network / compliance / no proxy):

| Variable | Default | Description |
|---|---|---|
| `TLS_CERT_FILE` | — | Path to server certificate. |
| `TLS_KEY_FILE` | — | Path to server private key. |

Setting both enables TLS on **both** `HTTP_ADDR` and `OTLP_GRPC_ADDR`. TLS 1.3 minimum.

## SMTP (for login codes & invites)

| Variable | Default | Description |
|---|---|---|
| `SMTP_HOST` | — | SMTP server hostname. |
| `SMTP_PORT` | `587` | SMTP port. `465` → implicit TLS; `587` / `25` → STARTTLS if advertised. |
| `SMTP_USER` | — | SMTP username. |
| `SMTP_PASS` | — | SMTP password / API key. |
| `SMTP_FROM` | — | From header, e.g. `"Fanout <noreply@yours>"`. |

## MCP

| Variable | Default | Description |
|---|---|---|
| `MCP_ENABLED` | `true` | Expose the MCP server at `/mcp`. |
