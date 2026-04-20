# Fanout — Requirements (v1.0)

> **Goal:** Single-binary observability platform. Ingest OpenTelemetry (OTLP) **traces, logs, metrics**, store as **partitioned Parquet**, query via **DuckDB**, serve **Web UI** + **MCP** for AI agents.

## 1. Product Scope

### Ingest
- OTLP **gRPC** on :4317 (traces, logs, metrics)
- Single-tenant deployment

### Storage
- **Partitioned Parquet**: `/data/telemetry/parquet/main/{signal}/namespace=<ns>/year=YYYY/month=MM/day=DD/hour=HH/*.parquet`
- **DuckDB** embedded query engine + **minute rollups** (`service_rollup`)
- **Retention**: Automatic pruning (configurable, default 30 days)

### HTTP API (:7520)
- **Health**: `/healthz`, `/readyz`
- **Metrics**: `/-/metrics` (Prometheus)
- **Web UI**: `/` (Templ + HTMX + Vega-Lite)

### MCP Server
- Endpoint: `/mcp`
- Tools: status, diagnose, find, trace, timeline, topology, compare, query, schema, render

## 2. Non-Goals (v1.0)
- External DB service (no ClickHouse/Elasticsearch)
- Long-term cold storage management
- OTLP/HTTP (gRPC only)

## 3. Operational Requirements
- Single binary: **fanout**
- Config via **ENV** (see below)
- Linux/macOS x86_64/arm64 (CGO for DuckDB)

## 4. Performance Targets
- Ingest: thousands of spans/logs/sec
- Freshness: < 30s to dashboard
- Query P95: < 1.5s on rollups, < 5s on raw scans

## 5. Security
- Token auth: `Authorization: Bearer <token>` (optional)
- Rate limiting: Configurable RPS
- TLS via external reverse proxy

## 6. Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_ADDR` | `:7520` | HTTP server address |
| `OTLP_GRPC_ADDR` | `:4317` | OTLP gRPC address |
| `DATA_DIR` | `./data` | Storage root for telemetry, query cache, and control data |
| `FLUSH_SECONDS` | `15` | Batch flush interval |
| `FLUSH_BATCH_SIZE` | `50000` | Max rows per writer flush |
| `ROLLUP_EVERY` | `60` | Rollup interval (seconds) |
| `JWT_SECRET` | - | HS256 signing key for access tokens |
| `JWT_REFRESH_SECRET` | - | HS256 signing key for refresh tokens |
| `MCP_ENABLED` | `true` | Enable MCP server |
| `RETENTION_DAYS` | `30` | Data retention (0 = forever) |
| `DEFAULT_NAMESPACE` | `default` | Default namespace for services |

## 7. Build & Run

```bash
# Build (requires CGO for DuckDB)
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
