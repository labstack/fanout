---
title: Getting started
description: From a fresh install to your first trace, in five steps.
---

This walks a fresh Fanout install through admin setup, generating an ingest token, and sending the first trace.

You'll need: a host that can run the binary or container, an SMTP relay (for the email-based login codes), and an Anthropic or OpenAI API key (for the chat investigator).

## 1. Start Fanout

```sh
docker run -d --name fanout \
  -p 7520:7520 -p 4317:4317 \
  -v $PWD/data:/var/lib/fanout/data \
  -e JWT_SECRET=$(openssl rand -hex 32) \
  -e JWT_REFRESH_SECRET=$(openssl rand -hex 32) \
  -e SMTP_HOST=smtp.example.com \
  -e SMTP_USER=fanout@example.com \
  -e SMTP_PASS=<smtp-password> \
  -e SMTP_FROM='"Fanout" <fanout@example.com>' \
  -e AI_API_KEY=<anthropic-or-openai-key> \
  ghcr.io/labstack/fanout:latest
```

`JWT_SECRET`, `JWT_REFRESH_SECRET`, the four `SMTP_*` variables, and `AI_API_KEY` are required — Fanout refuses to start without them. See the [environment reference](/docs/config/) for everything else.

Open [http://localhost:7520](http://localhost:7520). You should land on the login page.

## 2. Find the setup token

On first boot Fanout logs a one-time **setup token**. It authorises the admin-creation flow and is valid until the first admin is created.

```sh
docker logs fanout 2>&1 | grep "setup token"
```

If you don't see it, you've already booted before — the token is regenerated only on a fresh data directory.

## 3. Create the admin user

The login page detects the unconfigured state and shows a setup form. Provide:

- Your name and email.
- The setup token from step 2.

Fanout creates the admin, signs you in, and **prints the ingest token once** in the response. Copy it now — it isn't shown again. You can rotate it later from **Settings → Ingest** in the UI.

After this, the setup form is closed for the lifetime of the data directory. New users join via email invites; logins use one-time codes delivered via SMTP. No passwords are ever stored.

## 4. Send telemetry

Point your OpenTelemetry SDK or collector at Fanout. The Docker container listens on all interfaces, so this works from any host that can reach it.

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://<fanout-host>:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_HEADERS=x-fanout-ingest-token=fo_<your-token>
export OTEL_SERVICE_NAME=my-service
```

Or in a Collector config:

```yaml
exporters:
  otlp/fanout:
    endpoint: <fanout-host>:4317
    headers:
      x-fanout-ingest-token: fo_<your-token>

service:
  pipelines:
    traces:  { exporters: [otlp/fanout] }
    logs:    { exporters: [otlp/fanout] }
    metrics: { exporters: [otlp/fanout] }
```

Both `x-fanout-ingest-token` and `Authorization: Bearer fo_<token>` are accepted.

## 5. Verify

Send a request through your instrumented service, then open the UI:

- **Home** — your service appears in the health grid within ~15 seconds (the default flush interval).
- **Service detail** — click the service for operations, dependencies, and top spans.
- **Chat** — ask *"what's the slowest service in the last 15 minutes?"* The investigator runs MCP tools to answer.

If nothing shows up, check the [troubleshooting tips](/docs/ingest/) — most often it's an unset token header or a network rule blocking port `4317`.

## Next

- [Environment reference](/docs/config/) — all the knobs.
- [OTLP ingest details](/docs/ingest/) — SDK and collector specifics, TLS, namespaces.
- [MCP for Claude Code](/docs/mcp/) — bring the investigator into your editor.
