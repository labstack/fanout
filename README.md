# Fanout

Single-binary observability platform. Ingest OTLP, store as Parquet, query with DuckDB.

<p align="center">
  <img src="docs/architecture.svg" alt="Fanout Architecture" width="800"/>
</p>

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

Connect Claude Code or any MCP client to `https://fanout.test/mcp`

| Tool | Description |
|------|-------------|
| `overview` | System health, scores, top issues |
| `topology` | Service dependency map with blast radius |
| `spans` | Search/aggregate trace spans |
| `logs` | Search/aggregate log entries |
| `metrics` | Discover and query OTLP metric timeseries |
| `trace` | Distributed trace with root-cause analysis |
| `diagnose` | Deep-dive service analysis with baseline comparison |
| `compare` | Side-by-side: services, time windows, or operations |
| `attributes` | Discover filterable attribute keys |
| `query` | Raw SQL against DuckDB |

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

## AI Chat

The web UI is a React SPA with a single chat interface powered by an AI observability assistant. The LLM calls MCP tools to gather data, then produces structured blocks (15 visual types) streamed over WebSocket. Visit `/demo` for a component showcase.

## HTTP API

**Health:**
- `GET /healthz` - Liveness check
- `GET /readyz` - Readiness check
- `GET /-/metrics` - Prometheus metrics

**Chat:**
- `GET /ws/chat` - WebSocket chat interface

**Bookmarks:**
- `GET /api/bookmarks` - List bookmarks
- `POST /api/bookmarks` - Create bookmark
- `DELETE /api/bookmarks/:id` - Delete bookmark

**Suggestions:**
- `GET /api/suggestions` - Get suggestions

**MCP:**
- `ANY /mcp` - MCP endpoint (streamable HTTP)

**Reports:**
- `GET /reports` - Report list
- `GET /view/r/:id` - View report
- `GET /api/reports` - List reports (JSON)
- `DELETE /api/reports/:id` - Delete report

**Demo:**
- `GET /demo` - Component demo page

**SPA:**
- `GET /*` - React catch-all (serves index.html)
