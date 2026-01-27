# Fanout

Single-binary observability platform. Ingest OTLP, store as Parquet, query with DuckDB.

**Documentation:**
- [Architecture](ARCHITECTURE.md) - System design, data flow, components
- [Requirements](REQUIREMENTS.md) - Product scope, configuration, build

## Quick Start

```bash
# Build
export CGO_ENABLED=1
go build ./cmd/fanout

# Run
./fanout

# Or with Docker
docker compose up -d
```

**Ports:**
- `7520` - HTTP API + MCP (`/mcp`)
- `4317` - OTLP gRPC ingest

## MCP Tools

Connect Claude Code or any MCP client to `http://localhost:7520/mcp`

| Tool | Description |
|------|-------------|
| `status` | System health overview. Start here. |
| `diagnose` | Deep-dive into a service (P50/P95/P99, errors, slow ops) |
| `find` | Search spans and logs by pattern, service, status |
| `trace` | Distributed trace with auto root-cause analysis |
| `timeline` | Time-bucketed metrics with anomaly detection |
| `topology` | Service dependency map with health status |
| `compare` | Side-by-side comparison of 2-4 services |
| `query` | Raw SQL against DuckDB |
| `schema` | Database schema reference for writing queries |
| `render` | Generate HTML reports with charts |

MCP clients receive full JSON Schema via `tools/list`.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_ADDR` | `:7520` | HTTP server address |
| `OTLP_GRPC_ADDR` | `:4317` | OTLP gRPC address |
| `LAKE_DIR` | `./lake` | Parquet storage directory |
| `FLUSH_SECONDS` | `15` | Batch flush interval |
| `MAX_ROWS` | `50000` | Max rows per Parquet file |
| `ROLLUP_EVERY` | `60` | Rollup refresh (seconds) |
| `API_TOKEN` | - | Bearer auth token (optional) |
| `MCP_ENABLED` | `true` | Enable MCP server |
| `RETENTION_DAYS` | `30` | Data retention (0 = forever) |
| `RETENTION_HOURS` | `1` | Retention check interval |
| `DEFAULT_NAMESPACE` | `default` | Default namespace |
| `TENANT_ID` | - | Tenant UUID (optional) |

## OTLP Ingest

Point OpenTelemetry SDK at:
```bash
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_SERVICE_NAME=my-service
```

## HTTP API

**Health:**
- `GET /healthz` - Liveness check
- `GET /readyz` - Readiness check
- `GET /-/metrics` - Prometheus metrics

**Web UI:**
- `GET /` - Overview dashboard
- `GET /services` - Service list
- `GET /services/:name` - Service detail
- `GET /traces` - Trace search
- `GET /traces/:id` - Trace detail
- `GET /logs` - Log search
- `GET /metrics` - Metrics explorer
- `GET /topology` - Service map
- `GET /unified` - Unified timeline
- `GET /reports` - Report list

**MCP:**
- `POST /mcp` - MCP endpoint

**Reports:**
- `GET /view/r/:id` - View report
- `GET /api/reports` - List reports (JSON)
- `DELETE /api/reports/:id` - Delete report
