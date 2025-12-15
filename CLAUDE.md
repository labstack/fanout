# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Fanout is a single-binary observability platform that ingests OpenTelemetry (OTLP) traces, logs, and metrics via gRPC, stores them as partitioned Parquet files, and queries them using embedded DuckDB. Built with Go and Echo framework.

**Architecture:** OTLP gRPC (:4317) → Lake Writer (Parquet) → DuckDB (query + rollups) → Echo HTTP API (:7520) + MCP Server

## Build & Run

```bash
# Build (requires CGO for DuckDB)
export CGO_ENABLED=1
go build ./cmd/fanout

# Run
./fanout

# Or with just
just build
just run
```

## Configuration

All config via environment variables (see `internal/config/config.go`):

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

## Architecture Details

### Data Flow
1. **Ingest** (`internal/ingest/`): gRPC OTLP services accept traces/logs/metrics, extract tenant from `x-tenant-id` header, push to channels
2. **Lake Writer** (`internal/lake/`): Batches rows, flushes to Parquet partitioned by `year/month/day/hour`
3. **Pruner** (`internal/lake/retention.go`): Automatic data retention
4. **Query Engine** (`internal/query/`): DuckDB reads Parquet, maintains `svc_minute` rollups
5. **Service Layer** (`internal/service/`): Shared business logic for MCP and Web UI
6. **HTTP API** (`internal/api/`): Echo endpoints + Templ UI + Vega-Lite charts
7. **MCP Server** (`internal/mcp/`): Model Context Protocol for AI agents

### Key Components

**Main** (`cmd/fanout/main.go`):
- Initializes all components with shared channels
- Starts gRPC server with tenant interceptor
- Starts DuckDB rollup goroutine
- Configures Echo with optional Bearer auth and rate limiting

**Service Layer** (`internal/service/`):
- `status.go` - System health overview
- `diagnose.go` - Service deep-dive (P50/P95/P99, errors, dependencies)
- `find.go` - Search spans/logs with filters
- `trace.go` - Distributed trace analysis with root-cause detection
- `timeline.go` - Time-bucketed metrics + anomaly detection
- `topology.go` - Service dependency mapping

**MCP Server** (`internal/mcp/`):
- Tools: status, diagnose, find, trace, timeline, topology, query
- Streaming SSE responses

### Data Schema

**Spans**: trace_id, span_id, parent_span_id, service_name, name, kind, start_unix_nano, end_unix_nano, duration_ms, status_code, status_msg, resource_json, attributes_json, tenant_id, ingested_unix_nano

**Logs**: time_unix_nano, severity, body, service_name, trace_id, span_id, resource_json, attributes_json, tenant_id, ingested_unix_nano

**Metrics**: time_unix_nano, name, mtype, service_name, value, hist_bounds_json, hist_counts_json, attributes_json, resource_json, tenant_id, ingested_unix_nano, hist_count, hist_sum

## API Endpoints

**Health & Metrics:**
- `GET /healthz` - Liveness
- `GET /readyz` - Readiness
- `GET /-/metrics` - Prometheus

**UI:**
- `GET /` - Overview dashboard
- `GET /services` - Service list
- `GET /services/:name` - Service detail
- `GET /traces` - Trace list
- `GET /traces/:id` - Trace detail
- `GET /logs` - Logs
- `GET /metrics` - Metrics

**MCP:**
- `POST /mcp` - Model Context Protocol endpoint

## Dependencies

Key libraries (see `go.mod`):
- `github.com/labstack/echo/v4` - HTTP framework
- `github.com/duckdb/duckdb-go/v2` - DuckDB driver (requires CGO)
- `github.com/parquet-go/parquet-go` - Parquet writer
- `github.com/a-h/templ` - Template engine
- `github.com/modelcontextprotocol/go-sdk` - MCP SDK
- `google.golang.org/grpc` - gRPC server
- `go.opentelemetry.io/proto/otlp` - OTLP protocol

## Testing OTLP Ingest

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_SERVICE_NAME=my-service
```

## Performance

- Flush interval controls freshness vs I/O (default 15s)
- Queries scan Parquet files within time window
- Rollups (`svc_minute`) provide fast dashboard queries
- Targets: P95 < 1.5s on rollups, < 5s on raw scans
