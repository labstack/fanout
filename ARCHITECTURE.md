# Fanout — Architecture (v1.0)

Single-binary observability platform: **ingest → lake → query → API + MCP**.

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│                                   fanout                                      │
│                                                                              │
│  OTLP gRPC :4317         Lake Writer          DuckDB            Echo :7520   │
│  (traces/logs/metrics)──►Parquet Partitions──►Query + Rollups──►HTTP API     │
│                              │                   │                  │        │
│                              │                   │          ┌───────┴───────┐│
│                           Pruner              Service      │  Web UI       ││
│                          (retention)          Layer        │  MCP Server   ││
│                                                            │  NLQ/SQL API  ││
│                                                            │  Prometheus   ││
│                                                            └───────────────┘│
└──────────────────────────────────────────────────────────────────────────────┘
```

## Components

### Ingest (`internal/ingest/`)
gRPC OTLP services:
- `collector.trace.v1.TraceService`
- `collector.logs.v1.LogsService`
- `collector.metrics.v1.MetricsService`

Each Export call transforms data into normalized rows, pushed to in-memory channels.

### Lake Writer (`internal/lake/`)
- Micro-batches rows every 15s (configurable) into **Parquet** files
- Partition path: `/lake/{spans|logs|metrics}/year=YYYY/month=MM/day=DD/hour=HH/part-<unix>.parquet`
- **Pruner**: Automatic retention (default 30 days)

### Query Engine (`internal/query/`)
- **DuckDB** provides external reads from Parquet
- Maintains **`svc_minute`** rollups for fast dashboards
- Lives in-process (via CGO driver)

### Service Layer (`internal/service/`)
Shared business logic for MCP tools and Web UI:
- Status overview
- Service diagnostics (P50/P95/P99 latency, error rates)
- Span/log search with filters
- Distributed trace analysis with root-cause detection
- Timeline with anomaly detection
- Service topology/dependency mapping

### HTTP API (`internal/api/`)
- **Web UI**: Templ + HTMX + Vega-Lite charts
- **Health**: Liveness (`/healthz`) and readiness (`/readyz`)
- **Metrics**: Prometheus (`/-/metrics`)

### MCP Server (`internal/mcp/`)
Model Context Protocol server at `/mcp`:
- `status` - System health overview
- `diagnose` - Service deep-dive
- `find` - Search spans/logs
- `trace` - Distributed trace analysis
- `timeline` - Time-bucketed metrics + anomaly detection
- `topology` - Service dependency map
- `query` - Raw SQL

### Intelligence (`internal/intelligence/`)
- Anomaly detection for latency spikes, error rates, traffic drops
- Root-cause analysis for trace errors

## Data Model (Parquet)

**Spans** (flattened from OTLP)
- `trace_id, span_id, parent_span_id, service_name, name, kind, start_unix_nano, end_unix_nano, duration_ms, status_code, status_msg, resource_json, attributes_json, tenant_id, ingested_unix_nano`

**Logs**
- `time_unix_nano, severity, body, service_name, trace_id, span_id, resource_json, attributes_json, tenant_id, ingested_unix_nano`

**Metrics**
- `time_unix_nano, name, mtype, service_name, value, hist_bounds_json, hist_counts_json, attributes_json, resource_json, tenant_id, ingested_unix_nano, hist_count, hist_sum`

## Partitions & Freshness
- Files rotated on **time** (15s default) and/or **row count** (50k default)
- Queries filter by timestamp and prune columns for performance

## Security & Tenancy
- Tenant via `x-tenant-id` gRPC header, stored in Parquet for query-time filtering
- API bearer token auth (optional via `API_TOKEN`)
- Rate limiting (configurable via `RATE_LIMIT_RPS`)

## Scaling
- Horizontal: Multiple instances can read the same lake directory
- For higher concurrency: Consider ClickHouse while keeping Parquet layout
