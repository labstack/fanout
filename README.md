# Fanout

Single-binary observability platform. Ingest OTLP, store in DuckLake, query with DuckDB.

<p align="center">
  <img src="docs/architecture.svg" alt="Fanout Architecture" width="800"/>
</p>

**Documentation:**
- [Architecture](ARCHITECTURE.md) - System design, data flow, components
- [Requirements](REQUIREMENTS.md) - Product scope, configuration, build

## Quick Start

```bash
# Configure required secrets and auth settings
cp .env.example .env

# Build
export CGO_ENABLED=1
go build ./cmd/fanout

# Run
./fanout

# Or build and run the Docker image directly
docker build -t fanout .
docker run --rm --env-file .env -p 7520:7520 fanout
```

On an uninitialized instance, Fanout prints a setup token in the server output. Open `http://localhost:7520/login`, use that token to create the first admin account, and then choose whether OTLP stays private or is exposed publicly over TLS with a generated ingest token.

If you want a collector outside the container to reach OTLP, explicitly set `OTLP_GRPC_ADDR=0.0.0.0:4317` and publish `-p 4317:4317`.

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
| `OTLP_GRPC_ADDR` | `127.0.0.1:4317` | OTLP gRPC address (private by default) |
| `DATA_DIR` | `./data` | Storage root for telemetry, query cache, and control data |
| `FLUSH_SECONDS` | `15` | Batch flush interval |
| `FLUSH_BATCH_SIZE` | `50000` | Max rows per writer flush |
| `ROLLUP_EVERY` | `60` | Rollup refresh (seconds) |
| `AI_PROVIDER` | `anthropic` | AI provider (`anthropic` or `openai`) |
| `AI_API_KEY` | - | Required LLM API key |
| `AI_MODEL` | provider default | Optional model override |
| `AI_BASE_URL` | provider default | Optional API base URL override |
| `SMTP_HOST` | - | SMTP server host for web auth |
| `SMTP_PORT` | `587` | SMTP server port |
| `SMTP_USER` | - | SMTP username for web auth |
| `SMTP_PASS` | - | SMTP password for web auth |
| `SMTP_FROM` | - | Sender address for login/setup emails |
| `JWT_SECRET` | - | HS256 signing key for access tokens |
| `JWT_REFRESH_SECRET` | - | HS256 signing key for refresh tokens |
| `TLS_CERT_FILE` | - | Server cert. When set, HTTP serves HTTPS and OTLP gRPC accepts TLS. Plaintext if unset. |
| `TLS_KEY_FILE` | - | Server private key, paired with `TLS_CERT_FILE` |
| `MCP_ENABLED` | `true` | Enable MCP server |
| `RETENTION_DAYS` | `30` | Data retention (0 = forever) |
| `DEFAULT_NAMESPACE` | `default` | Default namespace |

Storage layout under `DATA_DIR`:
- `telemetry/` - DuckLake metadata and parquet data files
- `query/` - DuckDB catalog and temp working files
- `control/` - Fanout SQLite state, bookmarks, and reports

## OTLP Ingest

Point a collector or SDK at `OTLP_GRPC_ADDR` (default `127.0.0.1:4317`):

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=127.0.0.1:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_SERVICE_NAME=my-service
```

Ingest is **unauthenticated by default** — whoever can reach the port can write. Protect it however your network normally protects a service: firewall, private VPC, reverse proxy, or an **ingest token** you generate from the admin UI.

### Ingest token

From the admin UI, rotate a token. Collectors then send it in every request:

```yaml
exporters:
  otlp:
    endpoint: fanout.example.com:4317
    headers:
      x-fanout-ingest-token: <TOKEN>
```

Once set, the token is required on every OTLP request regardless of source IP. Clear it from the UI to go back to unauthenticated.

### TLS

Fanout is designed to run behind a reverse proxy (Caddy, nginx, Traefik) that terminates TLS.

For direct-exposure scenarios (private network with an internal CA, air-gapped environments), set `TLS_CERT_FILE` / `TLS_KEY_FILE` to have Fanout terminate TLS itself — both HTTP and OTLP gRPC use the cert pair.

Auth is web-only:
- first boot uses the setup page plus the setup token printed at startup to create the first admin
- subsequent logins use emailed verification codes
- AI, SMTP, and JWT config are required at startup

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
