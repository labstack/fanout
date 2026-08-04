# Fanout

Single-binary, agent-native OpenTelemetry investigation. One Go process owns
OTLP/gRPC ingest, DuckLake/Parquet storage, DuckDB queries, a Streamable HTTP
MCP server, the model/tool loop behind an AG-UI event stream, and the embedded
React client.

Licensed under [AGPL-3.0](LICENSE).

## Layout

```text
cmd/fanout/        process composition and the single entry point
cmd/bench/         load generator and benchmark reporter
internal/          ingest, storage, query, agent, MCP, auth, alerts
ui/host/           React AG-UI browser host (build-time)
ui/apps/           portable React MCP Apps (build-time)
```

## Build

Requires Go with `CGO_ENABLED=1` (DuckDB), [Bun](https://bun.sh) to compile the
browser assets, and [just](https://just.systems).

```sh
just install   # browser dependencies and git hooks
just build     # browser assets, then the binaries
```

`internal/ui` and `internal/mcp` embed the compiled assets, so the browser
bundles must be built before the Go binary — `just build` enforces that order.
Because those built assets are committed, `just ui-check` fails when they no
longer match a fresh build.

Run `just` to list every recipe. `just check` is the full gate: format, lint,
embedded-asset freshness, Go and browser tests, and spec validation. Lefthook
runs fast checks on commit and the same gate on push; CI runs it too.

## Specs

Product behavior is specified under `openspec/`, managed with
[OpenSpec](https://github.com/fission-ai/openspec). Canonical specs in
`openspec/specs/` describe shipped behavior; proposed behavior belongs in a
change under `openspec/changes/`. Validate with `just spec-check`.

## Status

This repository is being opened up incrementally. Deployment tooling, the
marketing site, and the demo stack are not included.
