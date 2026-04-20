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
  -e JWT_SECRET=... -e JWT_REFRESH_SECRET=... \
  -e SMTP_HOST=... -e SMTP_USER=... -e SMTP_PASS=... -e SMTP_FROM=... \
  -e AI_API_KEY=... \
  ghcr.io/labstack/fanout:latest
```

Required on first boot: `JWT_SECRET`, `JWT_REFRESH_SECRET`, the `SMTP_*` block, and `AI_API_KEY`. See the [environment reference](/docs/config/) for the full list.

Open [http://localhost:7520](http://localhost:7520).

## 2. Find the setup token

On first boot Fanout prints a one-time **setup token** to the server logs — it's what authorizes the admin-creation flow:

```
docker logs fanout 2>&1 | grep "setup token"
```

## 3. Create the admin

The UI redirects any unauthenticated request to `/login`, which on first boot shows the setup form. Enter:

- Your email and name.
- The setup token from step 2.

Fanout creates the admin user, issues access/refresh tokens, and generates a **random ingest token** that's shown **once** in the response. Copy it — you'll need it for every OTLP client.

You can rotate the ingest token later from **Settings → Ingest**. The setup window closes after the first admin is created; a restart is required to reopen it.

Login on subsequent visits uses **email + one-time code** delivered via SMTP. No password is ever stored.

## 4. Send telemetry

Point an OpenTelemetry SDK or collector at Fanout. `OTLP_GRPC_ADDR` defaults to `127.0.0.1:4317` (loopback only) — set it to `:4317` (or a specific interface) if your collector is off-host.

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_HEADERS=x-fanout-ingest-token=fo_<YOUR_TOKEN>
export OTEL_SERVICE_NAME=my-service
```

Collector config:

```yaml
exporters:
  otlp/fanout:
    endpoint: fanout.yourdomain.com:4317
    headers:
      x-fanout-ingest-token: fo_<YOUR_TOKEN>
```

Alternatively pass the token as a bearer header: `Authorization: Bearer fo_<YOUR_TOKEN>`.

## 5. Explore

- **Home**: service health grid, top errors, latency trends.
- **Service detail**: operations, dependencies, top spans.
- **Chat**: ask "what's slow?" — the MCP tools run the investigation for you.

## Next

- [Environment reference](/docs/config/)
- [OTLP ingest details](/docs/ingest/)
- [MCP for Claude Code](/docs/mcp/)
