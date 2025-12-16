# Fanout Architecture

Single-binary observability platform: **ingest → lake → query → API + MCP**.

## System Overview

```mermaid
graph TB
    subgraph Ingest
        OTLP[OTLP gRPC :4317]
        T[Traces]
        L[Logs]
        M[Metrics]
    end

    subgraph Storage
        LW[Lake Writer]
        PQ[(Parquet Files)]
        PR[Pruner]
    end

    subgraph Query
        DK[DuckDB]
        RL[Rollups]
        SVC[Service Layer]
    end

    subgraph API
        HTTP[Echo :7520]
        UI[Web UI]
        MCP[MCP Server]
        REST[REST API]
    end

    OTLP --> T & L & M
    T & L & M --> LW
    LW --> PQ
    PR -.-> PQ
    PQ --> DK
    DK --> RL
    RL --> SVC
    SVC --> HTTP
    HTTP --> UI & MCP & REST
```

## Data Flow

```mermaid
sequenceDiagram
    participant App as Application
    participant OTLP as OTLP gRPC
    participant Lake as Lake Writer
    participant PQ as Parquet
    participant Duck as DuckDB
    participant API as HTTP API

    App->>OTLP: Export(traces/logs/metrics)
    OTLP->>Lake: Push to channels
    Lake->>PQ: Batch write (15s/50k rows)

    API->>Duck: Query
    Duck->>PQ: Read Parquet
    PQ-->>Duck: Results
    Duck-->>API: Response
```

## Components

### Ingest (`internal/ingest/`)

gRPC OTLP services implementing:
- `collector.trace.v1.TraceService`
- `collector.logs.v1.LogsService`
- `collector.metrics.v1.MetricsService`

Each Export call transforms OTLP data into normalized rows and pushes to in-memory channels.

### Lake Writer (`internal/lake/`)

- Micro-batches rows every 15s (configurable) into Parquet files
- Partition path: `/lake/{spans|logs|metrics}/year=YYYY/month=MM/day=DD/hour=HH/part-<unix>.parquet`
- **Pruner**: Automatic retention cleanup (default 30 days)

### Query Engine (`internal/query/`)

- **DuckDB** reads directly from Parquet files
- Maintains **`svc_minute`** rollups for fast dashboard queries
- In-process via CGO driver

### Service Layer (`internal/service/`)

Shared business logic for MCP tools and Web UI:
- `status.go` - System health overview
- `diagnose.go` - Service deep-dive (P50/P95/P99, errors, dependencies)
- `find.go` - Search spans/logs with filters
- `trace.go` - Distributed trace analysis with root-cause detection
- `timeline.go` - Time-bucketed metrics + anomaly detection
- `topology.go` - Service dependency mapping

### Render System (`internal/render/`)

Composable rendering for ASCII and HTML output:
- `Table`, `Badge`, `Metric`, `Chart` components
- `Compose` for combining multiple renderers
- Dual-format output (ASCII for terminal, HTML for reports)

## MCP Tools

```mermaid
graph LR
    subgraph Observability
        ST[status]
        DG[diagnose]
        TL[timeline]
        TP[topology]
    end

    subgraph Search
        FD[find]
        TR[trace]
        QR[query]
    end

    subgraph Analysis
        CM[compare]
        SC[schema]
    end

    subgraph Output
        RD[render]
    end
```

| Tool | Description |
|------|-------------|
| `status` | System health overview with service counts |
| `diagnose` | Service deep-dive: P50/P95/P99, errors, slow ops, dependencies |
| `find` | Search spans/logs by pattern, service, status, severity |
| `trace` | Full trace with root-cause analysis |
| `timeline` | Time-bucketed metrics with anomaly detection |
| `topology` | Service dependency map with health status |
| `compare` | Side-by-side comparison of 2-4 services |
| `query` | Raw SQL against the data lake |
| `schema` | Database schema reference |
| `render` | Generate HTML reports from declarative sections |

## HTTP API

```mermaid
graph TB
    subgraph Health
        HZ["GET /healthz"]
        RZ["GET /readyz"]
        PM["GET /-/metrics"]
    end

    subgraph "Web UI"
        OV["GET /"]
        SV["GET /services"]
        SD["GET /services/:name"]
        TC["GET /traces"]
        TD["GET /traces/:id"]
        LG["GET /logs"]
        MT["GET /metrics"]
        RP["GET /reports"]
    end

    subgraph MCP
        MC["POST /mcp"]
        VW["GET /view/r/:id"]
    end

    subgraph "Reports API"
        RL["GET /api/reports"]
        RD["DELETE /api/reports/:id"]
    end
```

## Data Model

```mermaid
erDiagram
    SPANS {
        string trace_id PK
        string span_id PK
        string parent_span_id
        string service_name
        string name
        int kind
        bigint start_unix_nano
        bigint end_unix_nano
        float duration_ms
        string status_code
        string status_msg
        json resource_json
        json attributes_json
        string tenant_id
        bigint ingested_unix_nano
    }

    LOGS {
        bigint time_unix_nano PK
        string severity
        string body
        string service_name
        string trace_id FK
        string span_id FK
        json resource_json
        json attributes_json
        string tenant_id
        bigint ingested_unix_nano
    }

    METRICS {
        bigint time_unix_nano PK
        string name
        string mtype
        string service_name
        float value
        json hist_bounds_json
        json hist_counts_json
        json attributes_json
        json resource_json
        string tenant_id
        bigint ingested_unix_nano
        bigint hist_count
        float hist_sum
    }

    SVC_MINUTE {
        timestamp ts_min PK
        string service_name PK
        bigint span_count
        float p50_ms
        float p95_ms
        float p99_ms
        float error_rate
    }

    SPANS ||--o{ LOGS : "trace_id"
    SPANS ||--o{ SPANS : "parent_span_id"
```

## Report System

```mermaid
flowchart LR
    subgraph Input
        MCP[MCP render tool]
        SEC[Sections config]
    end

    subgraph Processing
        RND[Renderer]
        CMP[Components]
    end

    subgraph Storage
        FS[("lake/reports/")]
    end

    subgraph Output
        URL["/view/r/:id"]
        API["/api/reports"]
    end

    MCP --> SEC
    SEC --> RND
    RND --> CMP
    CMP --> FS
    FS --> URL & API
```

Reports support these section types:
- `metric` - Single value with label
- `table` - Rows with headers
- `chart` - Vega-Lite specification
- `text` - Markdown content
- `badge` - Status indicator
- `grid` - Multi-column layout
- `panel` - Grouped content

## Configuration

All config via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_ADDR` | `:7520` | HTTP server address |
| `OTLP_GRPC_ADDR` | `:4317` | OTLP gRPC address |
| `LAKE_DIR` | `./lake` | Parquet storage directory |
| `FLUSH_SECONDS` | `15` | Batch flush interval |
| `MAX_ROWS` | `50000` | Max rows per Parquet file |
| `ROLLUP_EVERY` | `60` | Rollup interval (seconds) |
| `API_TOKEN` | - | Bearer auth token (optional) |
| `MCP_ENABLED` | `true` | Enable MCP server |
| `RETENTION_DAYS` | `30` | Data retention (0 = forever) |

## Directory Structure

```
fanout/
├── cmd/fanout/          # Main entry point
├── internal/
│   ├── api/             # HTTP handlers + Web UI
│   ├── config/          # Configuration
│   ├── ingest/          # OTLP gRPC services
│   ├── lake/            # Parquet writer + pruner
│   ├── mcp/             # MCP tools + server
│   ├── query/           # DuckDB engine
│   ├── render/          # ASCII/HTML rendering
│   ├── service/         # Business logic
│   └── web/             # Templ templates
└── lake/                # Data directory (gitignored)
    ├── spans/
    ├── logs/
    ├── metrics/
    └── reports/
```

## Security & Tenancy

- Tenant isolation via `x-tenant-id` gRPC header
- Tenant ID stored in Parquet for query-time filtering
- Optional API bearer token auth (`API_TOKEN`)
- Rate limiting (`RATE_LIMIT_RPS`)

## Scaling

- **Horizontal**: Multiple instances can read the same lake directory
- **Higher concurrency**: Consider ClickHouse while keeping Parquet layout
- **Report cleanup**: Automatic expiration (24h default)
