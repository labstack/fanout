# Fanout

**Single-binary, agent-native OpenTelemetry investigation.**

[![CI](https://github.com/labstack/fanout/actions/workflows/ci.yml/badge.svg)](https://github.com/labstack/fanout/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/labstack/fanout.svg)](https://pkg.go.dev/github.com/labstack/fanout)

Fanout ingests OpenTelemetry data, stores it as Parquet, queries it with DuckDB,
and puts an AI agent in front of it — in one Go process with no external
dependencies to operate. Point a collector at it, open the browser, and ask
questions about your telemetry in plain language.

There is no separate ingester, query service, metadata database, object store,
or dashboard server to deploy. One binary, one data directory.

## Architecture

One process owns ingest, storage, query, alerting, the agent runtime, and an
MCP server. Everything below the dashed boundary is compiled into a single
executable, including the React client.

![Fanout architecture](docs/diagrams/architecture.svg)

Telemetry lands over OTLP/gRPC or OTLP/HTTP, is batched into DuckLake/Parquet,
and is read back through a DuckDB query kernel that also maintains service,
endpoint, and edge rollups. The browser client, an in-process agent, and any
external MCP host all reach the same typed observability contract rather than
issuing raw SQL.

Every write to the telemetry catalog — ingest flush, rollups, and background
maintenance alike — passes through a single write gate that holds one catalog
write in flight at a time:

![Fanout persistence](docs/diagrams/persistence.svg)

Application state (users, sessions, dashboards, alert rules, agent threads)
lives in a separate SQLite database and never sits on the telemetry write path.

## Performance

The bundled [`cmd/bench`](cmd/bench) driver measures authenticated ingest and
optional dashboard read load against your hardware. Fanout does not publish a
throughput headline until the raw reports and exact driver revision can ship
with it; see [the benchmark publication standard](docs/benchmarking.md).

## Requirements

- **Go and a C compiler** with `CGO_ENABLED=1` — DuckDB is a cgo dependency
- **[Bun](https://bun.sh)** — compiles the browser assets
- **[just](https://just.systems)** — task runner
- A 32-character **authentication code secret**

SMTP and an AI provider are optional. Without SMTP, an operator can mint a
short-lived login link from the local Fanout binary. Without an AI key, ingest,
dashboards, traces, logs, metrics, and MCP continue to work; only investigation
chat and AI-assisted controls are hidden.

## Quick start

### Native binary

Release archives support Linux and macOS on amd64 and arm64. The installer
verifies the selected archive against the release checksum before extracting:

```sh
curl -fsSL https://raw.githubusercontent.com/labstack/fanout/main/scripts/install.sh | sh
```

Set `FANOUT_VERSION=v{YYYY.M}.{N}` to pin a release and `FANOUT_PREFIX` to
choose the installation directory.

### Docker

```sh
docker run --name fanout -p 7520:7520 -p 4317:4317 -p 4318:4318 \
  -v fanout-data:/var/lib/fanout/data \
  -e FANOUT_AUTH_CODE_SECRET=$(openssl rand -hex 32) \
  ghcr.io/labstack/fanout:latest
```

Open the one-time setup URL printed by the container and create the first
administrator. Fanout displays the ingest token exactly once; save it with
your collector secrets. A standard OTLP/HTTP exporter can then use:

```sh
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
export OTEL_EXPORTER_OTLP_HEADERS="x-fanout-ingest-token=$INGEST_TOKEN"
```

For a Collector on the same private container network, the forwarding side is:

```yaml
exporters:
  otlp_http/fanout:
    endpoint: http://fanout:4318
    headers:
      x-fanout-ingest-token: ${env:INGEST_TOKEN}

service:
  pipelines:
    traces:  { receivers: [otlp], exporters: [otlp_http/fanout] }
    metrics: { receivers: [otlp], exporters: [otlp_http/fanout] }
    logs:    { receivers: [otlp], exporters: [otlp_http/fanout] }
```

This assumes the Collector's existing `otlp` receiver and an environment
variable containing the one-time token. Replace `fanout` with the private
hostname reachable from that Collector.

The equivalent OTLP/gRPC endpoint is `localhost:4317`. For later sign-in
without SMTP, mint a 15-minute, single-use link against the running
container's control database:

```sh
docker exec fanout fanout --config /etc/fanout/fanout.yaml \
  login-link admin@example.com
```

Add `FANOUT_AI_API_KEY` to enable chat. Configure all four SMTP settings
(`FANOUT_SMTP_HOST`, `FANOUT_SMTP_USERNAME`, `FANOUT_SMTP_PASSWORD`, and
`FANOUT_SMTP_FROM`) to enable email-code login.

The distroless image runs unprivileged as UID 65532. A bind-mounted host directory at
`/var/lib/fanout/data` must be writable by that user; a named volume, as above,
needs no such handling.

The image selects `/etc/fanout/fanout.yaml` by default. That file contains only
the container listener and data-directory defaults; it does not contain
credentials. Start a container-specific document from that file so it retains
the externally reachable HTTP and OTLP bind addresses:

```sh
cp fanout.docker.yaml fanout.yaml
# Add the remaining settings, then mount it over the image document:
docker run -v ./fanout.yaml:/etc/fanout/fanout.yaml:ro \
  ghcr.io/labstack/fanout:latest
```

A replacement document must set `server.http_addr: ":7520"`,
`ingest.otlp_grpc_addr: ":4317"`, and `ingest.otlp_http_addr: ":4318"`;
omitting either ingest address restores its secure loopback-only built-in
default, which is unreachable through a published container port.

### From source

```sh
git clone https://github.com/labstack/fanout.git
cd fanout
just install   # browser dependencies and git hooks
just build     # browser assets, then the binaries
```

Run it with the minimum configuration:

```sh
export FANOUT_AUTH_CODE_SECRET=$(openssl rand -hex 32) # must be 32+ characters

./bin/fanout
```

Optionally set `FANOUT_AI_API_KEY` for chat and the SMTP variables shown above
for email-code login. Without SMTP, run `./bin/fanout login-link
admin@example.com` from the same configuration and data directory.

Fanout serves the UI on <http://localhost:7520>, accepts OTLP/gRPC on
`127.0.0.1:4317`, and accepts OTLP/HTTP on `127.0.0.1:4318`. The first account
created becomes the administrator and receives the ingest token once.

Point any OpenTelemetry collector or SDK at either OTLP endpoint with the
ingest token, or generate load with the bundled gRPC benchmark driver:

```sh
./bin/bench -endpoint localhost:4317 -token "$INGEST_TOKEN"
```

## Configuration

Configuration is resolved once at startup and validated before Fanout opens
data files or listeners. Sources apply in this order: built-in defaults, an
optional YAML document selected with `--config`, then `FANOUT_` environment
variables. Fanout does not search for configuration or load `.env` files.

[`fanout.example.yaml`](fanout.example.yaml) is the complete commented schema:

```sh
cp fanout.example.yaml fanout.yaml
./bin/fanout --config ./fanout.yaml
```

Environment variables override the corresponding YAML values and are useful
for container injection and secrets. An empty environment value means "no
override"; use YAML for an explicit empty string. Unknown YAML keys and unknown
`FANOUT_` variables are startup errors, except for the service-discovery names
Kubernetes and Docker link-style networking inject for a Service named
`fanout`. All environment variables outside the `FANOUT_` namespace are
ignored.

YAML null values are rejected. Boolean values must use YAML 1.2 `true` or
`false` (not `yes`, `no`, `on`, or `off`). If a YAML document contains a
credential, Fanout requires that the file not be accessible by group or others
(for example, mode `0600`). The definitions live in
[`internal/config/config.go`](internal/config/config.go); the settings most
operators touch are:

| YAML key | Environment override | Default | Purpose |
| --- | --- | --- | --- |
| `server.http_addr` | `FANOUT_HTTP_ADDR` | `:7520` | UI, API, and MCP listener |
| `ingest.otlp_grpc_addr` | `FANOUT_OTLP_GRPC_ADDR` | `127.0.0.1:4317` | OTLP/gRPC ingest listener |
| `ingest.otlp_http_addr` | `FANOUT_OTLP_HTTP_ADDR` | `127.0.0.1:4318` | OTLP/HTTP ingest listener |
| `storage.data_dir` | `FANOUT_DATA_DIR` | `./data` | Parquet, query state, and control SQLite |
| `auth.mode` | `FANOUT_AUTH_MODE` | `local` | `local` (login link or SMTP) or `oidc` |
| `auth.code_secret` | `FANOUT_AUTH_CODE_SECRET` | — | Required in local mode, 32+ characters |
| `ai.provider` | `FANOUT_AI_PROVIDER` | `anthropic` | `anthropic` or `openai` |
| `ai.api_key` | `FANOUT_AI_API_KEY` | — | Enables AI investigation chat |
| `storage.retention_days` | `FANOUT_RETENTION_DAYS` | `30` | Telemetry retention window |
| `mcp.enabled` | `FANOUT_MCP_ENABLED` | `true` | Serve the MCP endpoint at `/mcp` |

### Advanced DuckDB sizing

Most deployments should not set DuckDB variables. Give the process or
container the CPU and memory limits it may use; at startup Fanout reserves
headroom for Go and sizes the DuckDB connection pool from available CPUs. The
resolved values and whether Fanout chose them are available in the
`runtime_sizing` block returned by `/readyz` and `/api/health`.

These variables are escape hatches for measured, unusual workloads:

| Variable | Automatic behavior | When to override |
| --- | --- | --- |
| `storage.duckdb.memory` / `FANOUT_DUCKDB_MEMORY` | 60% of the container or host memory available to Fanout | A measured co-tenant workload needs a different Go/DuckDB split |
| `storage.duckdb.max_connections` / `FANOUT_DUCKDB_MAX_CONNECTIONS` | Available Go CPUs, bounded to 2–16 connections | Query concurrency has been benchmarked for this machine |
| `storage.duckdb.threads` / `FANOUT_DUCKDB_THREADS` | DuckDB chooses its own query worker count | Query-heavy work must leave specific cores free for ingest |

An explicit value always wins. Startup logs and `/readyz` report the detected
host and cgroup limits, the selected source, and whether detection was
incomplete. If Fanout cannot conclusively inspect a container limit, it warns;
set `storage.duckdb.memory` or `FANOUT_DUCKDB_MEMORY` for a guaranteed bound.

For TLS and reverse proxies, health checks, backups and restores, upgrades,
retention, and recovery, see the [operator runbook](docs/operations.md).

## Development

```sh
just            # list every recipe
just check      # the full gate: format, lint, asset freshness, and tests
just test       # Go tests
just ui         # rebuild the embedded browser assets
```

The browser workspaces build **into** `internal/ui/dist` and
`internal/mcp/apps`, and those outputs are committed because `go:embed` needs
them present in a source checkout. The binary is therefore only ever as fresh
as the last UI build, so `just ui-check` rebuilds both workspaces and fails if
the committed bytes no longer match. It is part of `just check` and runs in CI.

[Lefthook](https://lefthook.dev) runs formatting and linting on commit and the
full gate on push; `just install` wires it up. The pre-push hook is a
convenience and lefthook may skip it when it detects no changed files — CI runs
the same `just check` unconditionally, and that is what actually enforces it.

## Project layout

```text
cmd/fanout/        process composition and the single entry point
cmd/bench/         load generator and benchmark reporter
internal/          ingest, storage, query, agent, MCP, auth, alerts
ui/host/           React AG-UI browser host (build-time)
ui/apps/           portable React MCP Apps (build-time)
docs/diagrams/     d2 sources and rendered SVG
```

## Releases

Versions are CalVer — `v{YYYY.M}.{N}`, numbered from 0 within each month, so
`v2026.8.1` is the second release of August 2026. Pushing a tag publishes the
matching image and moves `latest`:

| Image tag | Points at |
| --- | --- |
| `ghcr.io/labstack/fanout:latest` | the newest release |
| `ghcr.io/labstack/fanout:2026.8.0` | that exact release |
| `ghcr.io/labstack/fanout:main` | the tip of `main` |
| `ghcr.io/labstack/fanout:sha-<commit>` | one specific commit |

Release images are multi-architecture for `linux/amd64` and `linux/arm64`.
Release archives provide Linux and macOS binaries for amd64 and arm64; every
artifact is built on a native runner because DuckDB requires cgo.
The complete release and verification contract is in
[docs/release.md](docs/release.md).

## Contributing

Issues and pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).
Run `just check` and `just test-race` before opening a pull request; together
they match CI. For anything security-related, follow [SECURITY.md](SECURITY.md)
instead of opening an issue.

## Scope

This repository is Fanout itself, and it builds to a working binary with no
other repository involved. Not included: LabStack's own deployment
configuration, uptime monitoring, public demo instance, and marketing site.
Those describe how we operate Fanout, not what it does.

## License

[Apache-2.0](LICENSE) © LabStack LLC. See [NOTICE](NOTICE) and
[THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES) for attribution, and
[TRADEMARK](TRADEMARK.md) for use of the Fanout name and logo.
