# Fanout

**Single-binary, agent-native OpenTelemetry investigation.**

[![CI](https://github.com/labstack/fanout/actions/workflows/ci.yml/badge.svg)](https://github.com/labstack/fanout/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/labstack/fanout.svg)](https://pkg.go.dev/github.com/labstack/fanout)

Fanout ingests OpenTelemetry data, durably publishes it as atomic Parquet
batches, and puts an AI agent in front of it — in one Go process with no
external dependencies to operate. Persistent trace indexes serve targeted
reads; embedded DuckDB handles SQL, broad scans, and rebuildable rollups. Point
an SDK or Collector at it, open the browser, and ask questions about your
telemetry in plain language.

There is no separate ingester, query service, metadata database, object store,
or dashboard server to deploy. One binary, one data directory.

## Architecture

One process owns ingest, storage, query, alerting, the agent runtime, and an
MCP server. Everything below the dashed boundary is compiled into a single
executable, including the React client.

![Fanout architecture](docs/diagrams/architecture.svg)

Telemetry lands over OTLP/gRPC or OTLP/HTTP. Concurrent small requests may
share a group-commit batch, while up to four workers independently encode and
durably publish atomic Parquet directories with persistent trace indexes.
Targeted trace reads go through those indexes; DuckDB scans the same Parquet
for SQL and maintains rebuildable service, endpoint, and edge rollups. The
browser client, an in-process agent, and any
external MCP host all reach the same typed observability contract rather than
issuing raw SQL.

Attribute and resource values support up to 128 nested arrays or objects.
An export that exceeds this limit is rejected in full with OTLP
`INVALID_ARGUMENT` (HTTP 400); accepted values retain their original types.

Parquet is authoritative telemetry, DuckDB query state is rebuildable, and
SQLite is reserved for transactional product state. Native compaction
prepares replacements while reads continue and briefly gates readers only for
the crash-safe namespace swap:

![Fanout persistence](docs/diagrams/persistence.svg)

Application state (users, sessions, dashboards, alert rules, agent threads)
lives in the control SQLite database and never sits on the telemetry write
path. The published Parquet directories are self-describing, so startup can
discover the authoritative batch set directly from the filesystem.

## Performance

The independent [Fanout Bench](https://github.com/labstack/fanout-bench)
project measures authenticated ingest and optional dashboard read load against
your hardware. It uses the official OpenTelemetry generator and publishes raw,
reproducible evidence separately from the production binary. Ingest, indexed
reads, DuckDB analytics, and native Parquet maintenance have separate
coordination paths but still compete for the same CPU, memory bandwidth,
filesystem cache, and disk.

The current publication candidate is **296,196 accepted OpenTelemetry items per
second** sustained for five minutes with traces, logs, and metrics arriving
together on a machine with eight logical CPUs and 15.6 GiB of memory. It is a
single run, and the benchmark harness that produced it carried uncommitted
local changes, so it is not Fanout's official headline yet. The [performance methodology](https://fanout.run/explanation/performance)
shows the signal breakdown, quality gates, limitations, and publication bar.

## How it compares

Fanout is a single node holding traces, logs, and metrics for a system you can
reason about from one place. That premise, rather than any single feature, is
what separates it from its neighbours.

| If you use | Where Fanout differs |
| --- | --- |
| **Grafana with Loki, Tempo, and Mimir** | That stack keeps a service and a query language per signal, plus object storage underneath. Fanout keeps one process, one data directory, and one typed contract across all three signals, at the cost of the horizontal scale those components are built for. |
| **SigNoz** | Both are OTLP-native and self-hosted. SigNoz composes a collector, ClickHouse, and query services; Fanout compiles ingest, authoritative Parquet storage, indexed trace reads, DuckDB analytics, alerting, and the browser client into one binary. |
| **Jaeger** | Jaeger covers traces and expects a storage backend you run separately. Fanout ingests traces, logs, and metrics into the same store, with nothing else to deploy. |
| **Prometheus with Grafana** | Prometheus pulls metrics and is excellent at them. Fanout accepts pushed OTLP for all three signals and is built around investigating a specific incident rather than maintaining long-range metric series. |
| **Datadog**, **Honeycomb**, **Grafana Cloud** | Those are managed services: someone else runs the storage, the scaling, and the upgrades, and your telemetry leaves your network to get there. Fanout is a binary you run, on data that stays on your disk. |
| **An OpenTelemetry Collector piped into ClickHouse** | The same shape, assembled by hand: collector, database, dashboards, and the glue between them. Fanout is that assembly as one program, with an agent and an MCP server already wired to the same query contract. |

Fanout is a single node. It has no clustering, no replication, and no object
tier; a deployment that outgrows one machine's disk and CPU has outgrown
Fanout.

## Requirements

- **Go 1.27.2 and a C/C++ toolchain** on native Linux or macOS, amd64 or arm64.
  Build with `just build` or `bash scripts/with-duckdb.sh go build ./cmd/fanout`;
  the wrapper supplies DuckDB's pinned headers, static libraries, and CGO flags.
  The first build needs network access to download the checksum-verified engine.
- **[Bun](https://bun.sh)** — compiles the browser assets
- **[just](https://just.systems)** — task runner
- A 32-character **authentication code secret**

SMTP and an AI provider are optional. Without SMTP, an operator can mint a
short-lived login link from the local Fanout binary. With no AI provider selected, ingest,
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
docker run --name fanout -p 7520:7520 \
  -v fanout-data:/var/lib/fanout/data \
  -e FANOUT_AUTH_CODE_SECRET=$(openssl rand -hex 32) \
  labstack/fanout:latest
```

Open the one-time setup URL printed by the container and create the first
administrator. Fanout displays the ingest token exactly once; save it with
your collector secrets. A standard OTLP/HTTP exporter can then use:

```sh
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:7520
export OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer%20$INGEST_TOKEN"
```

For a Collector on the same private container network, the forwarding side is:

```yaml
exporters:
  otlp_http/fanout:
    endpoint: http://fanout:7520
    headers:
      Authorization: "Bearer ${env:INGEST_TOKEN}"

service:
  pipelines:
    traces:  { receivers: [otlp], exporters: [otlp_http/fanout] }
    metrics: { receivers: [otlp], exporters: [otlp_http/fanout] }
    logs:    { receivers: [otlp], exporters: [otlp_http/fanout] }
```

This assumes the Collector's existing `otlp` receiver and an environment
variable containing the one-time token. Replace `fanout` with the private
hostname reachable from that Collector.

The equivalent OTLP/gRPC endpoint is `localhost:7520` (plaintext locally).
Behind a TLS proxy, both transports use the public origin; gRPC requires
HTTP/2 to Fanout.
For later sign-in
without SMTP, mint a 15-minute, single-use link against the running
container's control database:

```sh
docker exec fanout fanout login-link admin@example.com
```

Select `FANOUT_AI_PROVIDER=anthropic` and inject `ANTHROPIC_API_KEY`, or select
`FANOUT_AI_PROVIDER=openai` and inject `OPENAI_API_KEY`, to enable chat. The
selected provider requires its key at startup. Configure all four SMTP settings
(`FANOUT_SMTP_HOST`, `FANOUT_SMTP_USERNAME`, `FANOUT_SMTP_PASSWORD`, and
`FANOUT_SMTP_FROM`) to enable email-code login.

The distroless image runs unprivileged as UID 65532. A bind-mounted host directory at
`/var/lib/fanout/data` must be writable by that user; a named volume, as above,
needs no such handling.

The image sets `FANOUT_ADDR=:7520` and
`FANOUT_DATA_DIR=/var/lib/fanout/data`. Override settings with deployment
environment values or mounted secret files. Publish port `7520`; the UI, API,
MCP, OTLP/HTTP, and OTLP/gRPC share this listener.

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

Optionally select `FANOUT_AI_PROVIDER=anthropic` with `ANTHROPIC_API_KEY`, or
`FANOUT_AI_PROVIDER=openai` with `OPENAI_API_KEY`, for chat. Set the SMTP variables shown above for
email-code login. Without SMTP, run `./bin/fanout login-link
admin@example.com` from the same configuration and data directory.

Fanout serves the UI, API, MCP, OTLP/gRPC and OTLP/HTTP on
<http://localhost:7520>. The first account
created becomes the administrator and receives the ingest token once.

Point any OpenTelemetry collector or SDK at either OTLP endpoint with the
ingest token. Use the separate
[Fanout Bench](https://github.com/labstack/fanout-bench) project for controlled
capacity tests.

## Configuration

Configuration is resolved once at startup from built-in defaults followed by
nonempty process environment values, and validated before data files or
listeners open. Every change requires a restart. Native installs bind
`127.0.0.1:7520` by default; set `FANOUT_ADDR=:7520` for other hosts to reach it.

Custom settings use `FANOUT_` plus a clear name, adding the area only when it
clarifies. Vendor credentials and endpoints keep their documented names:
`ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, `OPENAI_API_KEY`, and
`OPENAI_BASE_URL`. Unknown `FANOUT_` variables fail startup, except for
platform-injected service-discovery names. Unrelated vendor/tool variables are
ignored. Empty values mean unset. Bools, integers, and Go durations are parsed
as their declared types.

[`.env.example`](.env.example) is generated from
[`internal/config/config.go`](internal/config/config.go), with every value
empty and every entry commented out. Types, defaults, and secret file forms
are documented in comments. Copying it sets nothing and preserves image ENV.
Copy it to ignored `.env` and uncomment personal settings and development
credentials as needed. After building, `just run` uses Node's
`parseEnv` to load optional `./.env`, then lets inherited environment values
win, including empty values. The binary itself reads only process environment;
production launchers and deployments supply their own values.

Every secret also accepts a file path through `<ENV>_FILE`:
`FANOUT_AUTH_CODE_SECRET_FILE`, `FANOUT_OIDC_CLIENT_SECRET_FILE`,
`FANOUT_SMTP_PASSWORD_FILE`, `FANOUT_METRICS_TOKEN_FILE`,
`ANTHROPIC_API_KEY_FILE`, and `OPENAI_API_KEY_FILE`. Set either the value or
its file path. Setting both nonempty fails startup. Fanout strips exactly one
trailing LF or CRLF from a secret file and rejects an empty result.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `FANOUT_ADDR` | `127.0.0.1:7520` | UI, API, MCP, and both OTLP transports |
| `FANOUT_DATA_DIR` | `./data` | Parquet, query state, and control SQLite |
| `FANOUT_AUTH_MODE` | `local` | Login link or SMTP, or `oidc` |
| `FANOUT_AUTH_CODE_SECRET` | — | Required in local mode, 32+ characters |
| `FANOUT_AI_PROVIDER` | — | Empty disables chat; `anthropic` or `openai` enables it |
| `ANTHROPIC_API_KEY` | — | Required for Anthropic chat |
| `OPENAI_API_KEY` | — | Required for OpenAI chat |
| `FANOUT_RETENTION_DAYS` | `30` | Telemetry retention window |
| `FANOUT_MCP_ENABLED` | `true` | Serve the MCP endpoint at `/mcp` |

Chat requires an explicit provider and its credential. Inherited keys never
enable it. Only the selected provider's endpoint is validated and used.
`FANOUT_AI_MODEL` selects the model. `ANTHROPIC_BASE_URL` defaults to
`https://api.anthropic.com`; Fanout appends `/v1/messages`. `OPENAI_BASE_URL`
defaults to `https://api.openai.com/v1`; Fanout appends `/responses`.
Both preserve gateway prefixes and accept a trailing slash. The selected URL
must use HTTP(S) without credentials, a query, or a fragment.

See [runtime secret delivery](docs/operations.md#runtime-secret-delivery) for
deployment examples. This public repository excludes operator secret profiles
and private recipient policy. Run `just docs-generate` after changing the
schema; `just docs-generate-check` verifies `.env.example` and the reference.

### Advanced DuckDB sizing

Most deployments should not set DuckDB variables. Give the process or
container the CPU and memory limits it may use; at startup Fanout reserves
headroom for Go and sizes the DuckDB connection pool from available CPUs. The
resolved values and whether Fanout chose them are available in the
`runtime_sizing` block returned by `/readyz`.

These variables are escape hatches for measured, unusual workloads:

| Variable | Automatic behavior | When to override |
| --- | --- | --- |
| `FANOUT_DUCKDB_MEMORY` | 60% of the container or host memory available to Fanout | A measured co-tenant workload needs a different Go/DuckDB split |
| `FANOUT_DUCKDB_MAX_CONNECTIONS` | Available Go CPUs, bounded to 2–16 connections | Query concurrency has been benchmarked for this machine |
| `FANOUT_DUCKDB_THREADS` | DuckDB chooses its own query worker count | Query-heavy work must leave specific cores free for ingest |

An explicit value always wins. Startup logs and `/readyz` report the detected
host and cgroup limits, the selected source, and whether detection was
incomplete. If Fanout cannot conclusively inspect a container limit, it warns;
set `FANOUT_DUCKDB_MEMORY` for a guaranteed bound.

For TLS and reverse proxies, health checks, backups and restores, upgrades,
retention, and recovery, see the [operator runbook](docs/operations.md).

## Development

```sh
just            # list every recipe
just check      # the full gate: format, lint, asset freshness, and tests
just test       # Go tests
just ui         # rebuild the embedded browser assets
```

The `ui/host` workspace builds the SPA and MCP app **into** `internal/ui/dist` and
`internal/mcp/apps`, and those outputs are committed because `go:embed` needs
them present in a source checkout. The binary is therefore only ever as fresh
as the last UI build. `just ui-check` compares temporary builds of both outputs
and fails if the embedded bytes no longer match. It is part of `just check` and runs in CI.

[Lefthook](https://lefthook.dev) runs formatting and linting on commit and the
full gate on push; `just install` wires it up. The pre-push hook is a
convenience and lefthook may skip it when it detects no changed files — CI runs
the same `just check` unconditionally, and that is what actually enforces it.

## Project layout

```text
cmd/fanout/        process composition and the single entry point
internal/          ingest, storage, query, agent, MCP, auth, alerts
ui/host/           React browser host and shared MCP app renderer (build-time)
docs/diagrams/     d2 sources and rendered SVG
```

## Releases

Versions are CalVer — `v{YYYY.M}.{N}`, numbered from 0 within each month, so
`v2026.8.1` is the second release of August 2026. Pushing a tag publishes the
same release manifest to Docker Hub and GHCR and moves both `latest` tags.
GHCR remains the canonical registry and also carries development images:

| Image tag | Points at |
| --- | --- |
| `labstack/fanout:latest` | the newest release, mirrored on Docker Hub |
| `labstack/fanout:2026.8.0` | that exact release, mirrored on Docker Hub |
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
configuration, uptime monitoring, and public demo instance. Those describe how
we operate Fanout, not what it does.

The documentation site in [`site/`](site) is included, and is the exception that
proves the rule: it is not marketing copy but the product's own reference, and
part of it is generated. `cmd/fanout-docgen` writes every settings page from the
same `internal/config` type the loader binds, and `just check` fails when a
committed page no longer matches it. Documentation that can drift from the
binary is documentation that eventually lies about it, and the only place that
check can run is next to the code it checks.

## License

[Apache-2.0](LICENSE) © LabStack LLC. See [NOTICE](NOTICE) and
[THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES) for attribution, and
[TRADEMARK](TRADEMARK.md) for use of the Fanout name and logo.
