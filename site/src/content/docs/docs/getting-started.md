---
title: Getting started
description: First boot — admin setup, ingest token, and sending your first spans.
---

This walks a fresh Fanout install from zero to the first trace.

## 1. Start Fanout

```sh
docker run -d --name fanout \
  -p 7520:7520 -p 4317:4317 \
  -v $PWD/data:/var/lib/fanout/data \
  ghcr.io/labstack/fanout:latest
```

Open [http://localhost:7520](http://localhost:7520).

## 2. Complete setup

On first boot the UI redirects to `/setup`. Create the admin account (email + password). Fanout generates a random **ingest token** and shows it **once** — copy it. Without this token, OTLP ingest is rejected.

You can rotate the token later from **Settings → Ingest**.

## 3. Send data

Point your OpenTelemetry SDK or collector at Fanout:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_HEADERS=x-fanout-ingest-token=fo_<YOUR_TOKEN>
export OTEL_SERVICE_NAME=my-service
```

If you're using the OpenTelemetry Collector, add an OTLP exporter:

```yaml
exporters:
  otlp/fanout:
    endpoint: fanout.yourdomain.com:4317
    headers:
      x-fanout-ingest-token: fo_<YOUR_TOKEN>
    tls:
      insecure: false   # omit for plaintext behind a proxy
```

Alternatively pass the token as a bearer header: `Authorization: Bearer fo_<YOUR_TOKEN>`.

## 4. Explore

- **Home**: service health grid, top errors, latency trends.
- **Service detail**: operations, dependencies, top spans.
- **Chat**: ask "what's slow?" — the MCP tools run the investigation for you.

## Next

- [Environment reference](/docs/config/)
- [OTLP ingest details](/docs/ingest/)
- [MCP for Claude Code](/docs/mcp/)
