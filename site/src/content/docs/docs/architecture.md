---
title: Architecture
description: How data moves through Fanout from OTLP ingest to query.
---

Fanout is one Go binary. Every subsystem lives in-process; data flows through in-memory channels, not over a network. There is no external database, no message broker, no sidecar.

## Data flow

```
  OTLP gRPC :4317
        │
        ▼
  ┌───────────┐  in-process  ┌──────────────┐  flush (~15 s)  ┌────────────────┐
  │  ingest   │ ───spans────▶│  lake writer │ ───columnar──▶ │  data/telemetry│
  │           │ ───logs─────▶│              │                  │  partitioned   │
  │           │ ───metrics──▶│              │                  └────────────────┘
  └───────────┘              └──────────────┘                         │
                                                                      ▼
                                                              ┌───────────────┐
                                                              │ query engine  │
                                                              │ + rollup ~60s │
                                                              └───────────────┘
                                                                      │
                                                                      ▼
                                                              ┌───────────────┐
                                                              │   HTTP API    │
                                                              └─────┬─────────┘
                                                  ┌───────────────┬─┴──────────────┐
                                                  ▼               ▼                ▼
                                              Web UI           MCP server       Reports
```

## Components

| Component | Path | Responsibility |
| --- | --- | --- |
| ingest | `internal/ingest` | gRPC OTLP server, hands rows to channels |
| lake | `internal/lake` | Batching, columnar writer, retention |
| query | `internal/query` | Query execution, rollups |
| service | `internal/service` | Domain logic — overviews, services, traces, alerts |
| api | `internal/api` | HTTP handlers (Echo) |
| mcp | `internal/mcp` | MCP server and the twelve tools |
| intelligence | `internal/intelligence` | Anomaly scoring, alert evaluation |
| web | `web/` | React admin SPA, embedded in the binary at build time |

## Storage layout

```
data/
├── telemetry/    # Partitioned columnar files for traces, logs, metrics
├── query/        # Catalog and temp files for the query engine
└── control/      # SQLite — application state, bookmarks, saved reports
```

- **Columnar storage** for telemetry — fast scans, compact, queryable directly.
- **SQLite** for application state — small, transactional, no separate server required.
- Files are partitioned by **year / month / day / hour** so the engine can prune entire chunks when filtering by time.

## Performance targets

These are design goals, not SLAs. Real numbers depend on disk speed, memory budget, and dataset size.

- P95 < 1.5 s for queries that hit the rollup table.
- P95 < 5 s for raw scans across recent partitions.
- Flush interval 15 s — the freshness vs. I/O dial. Lower it if the UI feels stale; raise it if disk I/O becomes a bottleneck.

## Why one binary?

Most observability stacks ask you to operate a tracing backend, a log store, a metrics store, dashboards, an alert engine, and the glue between them. Each layer has its own scaling rules and failure modes.

Fanout fuses those layers into one process backed by one storage format. The trade-off: you scale vertically rather than horizontally. For most teams, that's a feature, not a limit.
