---
title: Install
description: Binaries, Docker images, and build-from-source.
---

## Docker

```sh
docker run -d --name fanout \
  -p 7520:7520 -p 4317:4317 \
  -v $PWD/data:/var/lib/fanout/data \
  ghcr.io/labstack/fanout:latest
```

- `7520` — HTTP (web UI + API + MCP).
- `4317` — OTLP gRPC ingest.
- `./data` — everything persistent (Parquet, SQLite, reports).

## Binary (from a release)

Grab the latest artifact from [GitHub Releases](https://github.com/labstack/fanout/releases) and run it:

```sh
./fanout
```

Default: HTTP on `:7520`, OTLP gRPC on `:4317`, data under `./data`.

## Build from source

Requires Go 1.26+, Bun, and `CGO_ENABLED=1` (DuckDB).

```sh
git clone https://github.com/labstack/fanout
cd fanout
just install
just build
./fanout
```

## Next

- Configure [environment variables](/docs/config/).
- Point a collector at [OTLP ingest](/docs/ingest/).
