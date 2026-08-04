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

Requires Go with `CGO_ENABLED=1` (DuckDB) and [Bun](https://bun.sh) to compile
the browser assets.

```sh
cd ui/apps && bun install && bun run build
cd ../host && bun install && bun run build
cd ../.. && go build -o bin/fanout ./cmd/fanout
```

`internal/ui` embeds the compiled assets, so the browser bundle must be built
before the Go binary.

## Status

This repository is being opened up incrementally. Deployment tooling, the
marketing site, and the demo stack are not included.
