---
title: Architecture
description: How Fanout moves data from OTLP to Parquet to DuckDB to UI.
---

Fanout is one Go binary. Every subsystem lives in-process; data flows through channels, not over a network.

## Flow

```
  OTLP gRPC :4317
        │
        ▼
  ┌───────────┐   channels   ┌──────────────┐   flush (15 s)   ┌────────────────┐
  │  ingest   │ ───spans────▶│  lake writer │ ───Parquet────▶ │ data/telemetry │
  │           │ ───logs─────▶│              │                  │ year/mo/day/hr │
  │           │ ───metrics──▶│              │                  └────────────────┘
  └───────────┘              └──────────────┘                         │
                                                                      ▼
                                                              ┌───────────────┐
                                                              │    DuckDB     │
                                                              │ + rollup (60s)│
                                                              └───────────────┘
                                                                      │
                                                                      ▼
                                                              ┌───────────────┐
                                                              │ Echo HTTP API │
                                                              └─────┬─────────┘
                                                  ┌───────────────┬─┴──────────────┐
                                                  ▼               ▼                ▼
                                              Web UI           MCP server       Reports
```

## Components

| Component | Path | Responsibility |
|---|---|---|
| ingest | `internal/ingest` | gRPC OTLP server → channels |
| lake | `internal/lake` | batching, Parquet writer, retention |
| query | `internal/query` | DuckDB queries, rollups |
| service | `internal/service` | domain logic layer |
| api | `internal/api` | Echo HTTP handlers |
| mcp | `internal/mcp` | MCP server + ten tools |
| web | `web/` | React admin SPA, embedded in the binary |
| site | `site/` | Marketing + docs (this website) |

## Storage

```
data/
├── telemetry/    # DuckLake metadata + Parquet partitions (year/month/day/hour)
├── query/        # DuckDB catalog + temp files
└── control/      # SQLite for app state, bookmarks, reports
```

- **Parquet** for telemetry because it's fast to scan, compact, and DuckDB reads it natively.
- **SQLite** for application state because it's small, transactional, and doesn't need a server.

## Performance targets

- P95 < 1.5 s for rollup-backed queries.
- P95 < 5 s for raw Parquet scans.
- Flush interval 15 s — the freshness/IO tradeoff dial.
