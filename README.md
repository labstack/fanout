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

Telemetry lands over OTLP/gRPC, is batched into DuckLake/Parquet, and is read
back through a DuckDB query kernel that also maintains service, endpoint, and
edge rollups. The browser client, an in-process agent, and any external MCP
host all reach the same typed observability contract rather than issuing raw
SQL.

Every write to the telemetry catalog — ingest flush, rollups, and background
maintenance alike — passes through a single write gate that holds one catalog
write in flight at a time:

![Fanout persistence](docs/diagrams/persistence.svg)

Application state (users, sessions, dashboards, alert rules, agent threads)
lives in a separate SQLite database and never sits on the telemetry write path.

## Performance

Two dedicated vCPUs sustained **5,138 traces/s — 22,612 rows/s** with zero rows
dropped and an export p95 of 3 ms. Adding a dashboard read load of 5 queries/s
cost about half that ingest capacity, with rollup-backed views staying under
120 ms and raw-span queries taking seconds.

Full method, per-operation query latency, and the operational caveats are in
[docs/benchmarks/two-vcpu.md](docs/benchmarks/two-vcpu.md). `cmd/bench` takes no
rate: it sizes itself to the machine, ramps until the server stops keeping up,
and confirms what it found.

## Requirements

- **Go** with `CGO_ENABLED=1` — DuckDB is a cgo dependency
- **[Bun](https://bun.sh)** — compiles the browser assets
- **[just](https://just.systems)** — task runner
- An **SMTP server** and an **AI provider key** (see below)

> **Fanout does not run unconfigured.** Sign-in requires either SMTP (`local`
> auth mode, the default) or an OIDC provider, and the agent requires an API
> key. There is no anonymous or offline mode. For local development, any SMTP
> catcher — [Mailpit](https://mailpit.axllent.org), MailHog — is enough.

## Quick start

### Docker

```sh
docker run -p 7520:7520 -p 4317:4317 \
  -v fanout-data:/var/lib/fanout/data \
  -e AUTH_CODE_SECRET=$(openssl rand -hex 32) \
  -e AI_API_KEY=sk-... \
  -e SMTP_HOST=... -e SMTP_USER=... -e SMTP_PASS=... -e SMTP_FROM=... \
  ghcr.io/labstack/fanout:latest
```

The image runs unprivileged as UID 999. A bind-mounted host directory at
`/var/lib/fanout/data` must be writable by that user; a named volume, as above,
needs no such handling.

### From source

```sh
git clone https://github.com/labstack/fanout.git
cd fanout
just install   # browser dependencies and git hooks
just build     # browser assets, then the binaries
```

Run it with the minimum configuration:

```sh
export AUTH_CODE_SECRET=$(openssl rand -hex 32)   # must be 32+ characters
export AI_API_KEY=sk-...                          # Anthropic by default
export SMTP_HOST=localhost SMTP_PORT=1025
export SMTP_USER=dev SMTP_PASS=dev SMTP_FROM=fanout@example.com

./bin/fanout
```

Fanout serves the UI on <http://localhost:7520> and accepts OTLP/gRPC on
`127.0.0.1:4317`. The first account created becomes the administrator.

Point any OpenTelemetry collector or SDK at `127.0.0.1:4317`, or generate load
with the bundled benchmark driver:

```sh
./bin/bench -endpoint localhost:4317
```

## Configuration

Configuration is entirely environment variables, read once at startup and
validated before anything binds a port. The full set lives in
[`internal/env/config.go`](internal/env/config.go); the ones you are most
likely to touch:

| Variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `:7520` | UI, API, and MCP listener |
| `OTLP_GRPC_ADDR` | `127.0.0.1:4317` | OTLP ingest listener |
| `DATA_DIR` | `./data` | Parquet, query state, and control SQLite |
| `AUTH_MODE` | `local` | `local` (SMTP) or `oidc` |
| `AUTH_CODE_SECRET` | — | Required in `local` mode, 32+ characters |
| `AI_PROVIDER` | `anthropic` | `anthropic` or `openai` |
| `AI_API_KEY` | — | Required |
| `RETENTION_DAYS` | `30` | Telemetry retention window |
| `MCP_ENABLED` | `true` | Serve the MCP endpoint at `/mcp` |

## Development

```sh
just            # list every recipe
just check      # the full gate: format, lint, asset freshness, tests, specs
just test       # Go tests
just ui         # rebuild the embedded browser assets
```

The browser workspaces build **into** `internal/ui/dist` and
`internal/mcp/apps`, and those outputs are committed because `go:embed` needs
them present in a source checkout. The binary is therefore only ever as fresh
as the last UI build, so `just ui-check` rebuilds both workspaces and fails if
the committed bytes no longer match. It is part of `just check` and runs in CI.

[Lefthook](https://lefthook.dev) runs formatting and linting on commit and the
full gate on push; `just install` wires it up. CI runs the same `just check`,
so a green push means a green build.

## Specs

Product behavior is specified under [`openspec/`](openspec/), managed with
[OpenSpec](https://github.com/fission-ai/openspec). Canonical specs in
`openspec/specs/` describe shipped behavior; proposed behavior belongs in a
change under `openspec/changes/`. Validate with `just spec-check`.

## Project layout

```text
cmd/fanout/        process composition and the single entry point
cmd/bench/         load generator and benchmark reporter
internal/          ingest, storage, query, agent, MCP, auth, alerts
ui/host/           React AG-UI browser host (build-time)
ui/apps/           portable React MCP Apps (build-time)
docs/diagrams/     d2 sources and rendered SVG
openspec/          canonical specs and active changes
```

## Releases

Versions are CalVer — `v{YYYY.MM}.{N}`, numbered from 1 within each month, so
`v2026.08.2` is the second release of August 2026. Pushing a tag publishes the
matching image and moves `latest`:

| Image tag | Points at |
| --- | --- |
| `ghcr.io/labstack/fanout:latest` | the newest release |
| `ghcr.io/labstack/fanout:2026.08.1` | that exact release |
| `ghcr.io/labstack/fanout:main` | the tip of `main` |
| `ghcr.io/labstack/fanout:sha-<commit>` | one specific commit |

Images are `linux/amd64`. DuckDB requires cgo and the build has no cross
toolchain, so other architectures need to build from source.

## Contributing

Issues and pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).
Run `just check` before opening a pull request; it is the same gate CI
enforces. For anything security-related, follow [SECURITY.md](SECURITY.md)
instead of opening an issue.

## Scope

This repository is Fanout itself, and it builds to a working binary with no
other repository involved. Not included: LabStack's own deployment
configuration, uptime monitoring, public demo instance, and marketing site.
Those describe how we operate Fanout, not what it does.

## License

[Apache-2.0](LICENSE) © LabStack LLC. See [NOTICE](NOTICE) for attribution.
