---
title: MCP server
description: Use Claude Code (or any MCP client) to investigate your telemetry.
---

Fanout ships an MCP (Model Context Protocol) server at `/mcp`. Point Claude Code — or any MCP-capable assistant — at it, and ten tools become available for investigation.

## Connect Claude Code

```sh
claude mcp add fanout --transport http https://fanout.example.com/mcp
```

Or run against a local dev instance:

```sh
claude mcp add fanout --transport http http://localhost:7520/mcp
```

## Tools

| Tool | Purpose |
|---|---|
| `overview` | System health, scores, top issues. |
| `topology` | Service dependency map with blast radius. |
| `spans` | Search/aggregate trace spans. |
| `logs` | Search/aggregate log entries. |
| `metrics` | Discover and query OTLP metric timeseries. |
| `trace` | Distributed trace with root-cause analysis. |
| `diagnose` | Deep-dive service analysis with baseline comparison. |
| `compare` | Side-by-side: services, time windows, or operations. |
| `attributes` | Discover filterable attribute keys. |
| `query` | Raw SQL against DuckDB. |

## Investigation pattern

1. Start with `overview` — what's broken?
2. `topology` — who depends on the broken service?
3. `diagnose` on the suspect service — latency, errors, saturation vs. baseline.
4. `trace` on a representative trace ID.
5. `logs` around the same window for clues.
6. `query` when you want something the structured tools can't answer.

The loop is **problem → candidates → evidence → root cause** — the tools just make each step one turn instead of one dashboard-hunt.
