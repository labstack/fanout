# Fanout

Single-binary observability platform. Ingest OTLP, store as Parquet, query with DuckDB.

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

MCP clients automatically receive JSON Schema via `tools/list`. Parameters below for reference:

### status
System health overview. Start here.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| window | int | 15 | Time window in minutes |

### diagnose
Deep-dive into a service. P50/P95/P99 latency, error rate, top errors, slow ops, dependencies.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| service | string | required | Service name to diagnose |
| window | int | 15 | Time window in minutes |

### find
Search spans and logs. Filter by pattern, service, status, severity.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| query | string | - | Search pattern (regex for logs, substring for spans) |
| service | string | - | Filter by service |
| type | string | both | Signal type: spans\|logs\|both |
| status | string | all | Filter: error\|slow\|all |
| window | int | 15 | Time window in minutes |
| severity | []string | - | Log severity: DEBUG,INFO,WARN,ERROR,FATAL |
| limit | int | 50 | Max results per type |

### trace
Distributed trace with auto root-cause analysis. Shows spans, logs, critical path.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| trace_id | string | required | Trace ID to analyze |
| include_logs | bool | true | Include correlated logs |

### timeline
Time-bucketed metrics with anomaly detection.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| service | string | - | Filter by service |
| window | int | 60 | Time window in minutes |
| granularity | int | 5 | Bucket size in minutes |

### topology
Service dependency map. Nodes with health, edges with call counts.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| window | int | 60 | Time window in minutes |

### query
Raw SQL against the data lake. Call with empty sql for schema reference.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| sql | string | - | SQL query (SELECT/WITH only) |
| max_rows | int | 1000 | Max rows to return |

## Configuration

Environment variables:
- `HTTP_ADDR` (default `:7520`)
- `OTLP_GRPC_ADDR` (default `:4317`)
- `LAKE_DIR` (default `./lake`)
- `FLUSH_SECONDS` (default `15`)
- `MAX_ROWS` (default `50000`)
- `ROLLUP_EVERY` (default `60`)
- `API_TOKEN` (optional Bearer auth)
- `MCP_ENABLED` (default `true`)
- `RETENTION_DAYS` (default `30`)

## OTLP Ingest

Point OpenTelemetry SDK at:
```bash
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_SERVICE_NAME=my-service
```

## HTTP API

- `GET /healthz` - Health check
- `GET /` - Dashboard UI
- `GET /services` - Service list
- `GET /traces` - Trace list
- `GET /logs` - Logs view
- `POST /mcp` - MCP endpoint
