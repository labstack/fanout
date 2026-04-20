---
title: MCP server
description: Investigate your telemetry from Claude Code (or any MCP client).
---

Fanout ships an MCP (Model Context Protocol) server at `/mcp`. Connect Claude Code — or any MCP-capable assistant — and twelve tools become available for investigation. The same server backs the chat investigator inside the Fanout UI.

## Connect Claude Code

Production install:

```sh
claude mcp add fanout --transport http https://fanout.example.com/mcp
```

Local dev install:

```sh
claude mcp add fanout --transport http http://localhost:7520/mcp
```

The MCP endpoint accepts an ingest token in the same way as OTLP — pass `Authorization: Bearer fo_<token>` if your transport supports custom headers, or rely on session-based auth if accessing through a logged-in browser.

## Tools

| Tool | What it does | When you'd reach for it |
| --- | --- | --- |
| `overview` | System health, scores, top issues. | Start of every investigation — *what's broken right now?* |
| `topology` | Service dependency map with blast radius. | *Who depends on the service that's failing?* |
| `diagnose` | Deep-dive on one service: latency, errors, saturation, vs. baseline. | *What changed about service X in the last hour?* |
| `spans` | Search and aggregate trace spans. | Find slow or failing requests by attribute, operation, or service. |
| `trace` | Single distributed trace with root-cause analysis. | Drill into one specific request end-to-end. |
| `logs` | Search and aggregate log entries. | Correlate logs to a window, service, or trace ID. |
| `metrics` | Discover and query OTLP metric timeseries. | Look at the gauges and counters your services emit. |
| `compare` | Side-by-side: two services, two time windows, or two operations. | *How is checkout different from checkout an hour ago?* |
| `attributes` | Discover filterable attribute keys for spans, logs, or metrics. | Figure out what dimensions you can slice by. |
| `alerts` | List firing, pending, or resolved alerts — filterable by service or rule. | Understand what's currently paging. |
| `alert_rules` | Manage alert rules — list, create, update, delete. | Tune alerting from the editor instead of the UI. |
| `query` | Raw SQL against the underlying data. | Anything the structured tools can't express. |

## Investigation pattern

The tools chain naturally. A typical incident loop:

1. **`overview`** — what's broken?
2. **`topology`** — what depends on the broken thing? What's the blast radius?
3. **`diagnose`** the suspect service — latency, errors, saturation against baseline.
4. **`trace`** a representative slow or failing request to see the actual call path.
5. **`logs`** in the same window to catch errors the trace doesn't show.
6. **`query`** when you need something the structured tools can't answer.

The shape is **problem → candidates → evidence → root cause** — the tools just collapse each step into one turn instead of one dashboard hunt.

## Example prompts

To get a feel for how the loop runs, try these against a working install:

- *"What was the highest-latency service in the last 30 minutes?"*
- *"Anything firing right now? If so, what changed?"*
- *"Compare checkout latency now vs. last week, same hour."*
- *"Find a slow trace where the user_id attribute is set."*
- *"What's the error budget burn rate for payment over the last 24h?"*

Claude (or whichever model you use) decides which tools to call. You don't need to memorise the tool list.

## Security

The MCP endpoint shares the auth surface with the rest of the API. Tokens that can ingest can also query — there's no separate read/write split today. If you need stricter isolation, gate the endpoint at your reverse proxy.
