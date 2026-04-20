---
title: Introduction
description: What Fanout is, what problem it solves, and when to use it.
---

Fanout is a **single-binary observability platform** for self-hosters. It accepts OpenTelemetry (OTLP) traces, logs, and metrics over gRPC, writes them as partitioned Parquet files, and queries them with embedded DuckDB. A built-in web UI, an MCP server, and an alert engine ship in the same binary.

## What you get

- **OTLP ingest** on port `4317` (gRPC).
- **Storage** as hourly-partitioned Parquet under `data/telemetry/`.
- **Query** via embedded DuckDB — rollups at 60 s, raw scans P95 under 5 s.
- **Web UI** for service overviews, trace waterfalls, logs, and alert rules.
- **MCP server** with twelve tools for Claude Code and any MCP client.
- **Alerts** expressed as expr-lang rules, evaluated every 30 s.

## What it is not

- Not a SaaS. You run it.
- Not a cluster. One binary, one process. Scale vertically.
- Not a collector replacement. Point your existing OpenTelemetry Collector at Fanout, or send directly from an SDK — whichever you already do.

## When to choose it

- You want traces, logs, and metrics in one place without standing up Jaeger + Loki + Prometheus + Grafana.
- You want the data to live on your own disk.
- You want an AI-assisted investigation loop (MCP tools drive deep analyses from a chat prompt).

Next: [Install](/docs/install/) and [Getting started](/docs/getting-started/).
