---
title: Install
description: Run Fanout via Docker or a pre-built binary.
---

Fanout ships as a single executable. Pick whichever path matches how you already deploy.

| Path | Best for |
| --- | --- |
| **Docker** | Servers you already manage with containers. The fastest way to try it. |
| **Pre-built binary** | Bare-metal hosts, custom service managers, air-gapped environments. |

## Docker

```sh
docker run -d --name fanout \
  -p 7520:7520 -p 4317:4317 \
  -v $PWD/data:/var/lib/fanout/data \
  ghcr.io/labstack/fanout:latest
```

| Port / path | Purpose |
| --- | --- |
| `7520` | HTTP — web UI, API, and the MCP endpoint. |
| `4317` | OTLP gRPC ingest. |
| `./data` | Persistent storage — telemetry, application state, and saved reports. |

The container listens on all interfaces by default (`HTTP_ADDR=:7520`, `OTLP_GRPC_ADDR=:4317`). For a host-only install, add `-e OTLP_GRPC_ADDR=127.0.0.1:4317`.

:::caution
On first boot Fanout requires several environment variables — JWT signing secrets, an SMTP relay (for email login codes), and an LLM API key (for the chat investigator). The snippet above will exit immediately without them. See [Getting started](/docs/getting-started/) for the full first-boot command.
:::

## Pre-built binary

Download the artifact for your platform from the [releases page](https://github.com/labstack/fanout/releases) and run it:

```sh
./fanout
```

Defaults: HTTP on `:7520`, OTLP gRPC on `127.0.0.1:4317`, data under `./data`.

The binary is around 30 MB and self-contained — no runtime dependencies beyond a recent libc.

## System requirements

These are guidelines, not hard limits. The binary is small; the data is what consumes resources.

| Resource | Recommended starting point |
| --- | --- |
| CPU | 2 vCPU |
| Memory | 1 GB (raise via `DUCKDB_MEMORY` for larger workloads) |
| Disk | 20 GB on fast local storage; budget ~1 GB / day per million spans at default retention |

## Next

- [Configure environment variables](/docs/config/) — required secrets and tuning knobs.
- [Getting started](/docs/getting-started/) — first-boot walkthrough.
