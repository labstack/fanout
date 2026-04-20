---
title: OTLP ingest
description: Send OpenTelemetry traces, logs, and metrics to Fanout — SDK, collector, TLS, and multi-tenant setups.
---

Fanout speaks **OpenTelemetry over gRPC** on port `4317`. Anything that can export OTLP — an SDK, a collector, a sidecar — will work without modification. HTTP/protobuf and HTTP/JSON OTLP are not yet supported; if you need them, run an OpenTelemetry Collector in front and point its `otlp` exporter at Fanout.

## Authentication

Every request must carry a valid ingest token. The token is generated during admin setup (shown once) and rotatable any time from **Settings → Ingest** in the UI.

Two header forms are accepted, equivalently:

```
x-fanout-ingest-token: fo_<token>
Authorization: Bearer fo_<token>
```

A missing or invalid token returns `Unauthenticated`. Tokens are scoped to the install — the same token works for every signal type.

## Direct from an SDK

Set the standard OpenTelemetry env vars in your service. Most SDKs (Go, Python, Node, Java, .NET, Ruby) read them automatically.

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=https://fanout.example.com:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_HEADERS=x-fanout-ingest-token=fo_<token>
export OTEL_SERVICE_NAME=checkout
```

Use `http://` if your endpoint isn't TLS-terminated. The headers env var takes a comma-separated list of `key=value` pairs.

## Through an OpenTelemetry Collector

If you already run a collector (recommended for production — buffering, batching, sampling, and per-tenant routing), add Fanout as another `otlp` exporter:

```yaml
exporters:
  otlp/fanout:
    endpoint: fanout.example.com:4317
    headers:
      x-fanout-ingest-token: fo_<token>

service:
  pipelines:
    traces:  { exporters: [otlp/fanout] }
    logs:    { exporters: [otlp/fanout] }
    metrics: { exporters: [otlp/fanout] }
```

You can fan out to Fanout *and* an existing backend during a migration — exporters are list-typed.

## TLS

You have two options. Pick one based on what you already operate.

**Behind a reverse proxy (recommended).** Caddy, nginx, or Traefik terminates TLS for `fanout.example.com:4317` and proxies plaintext gRPC to Fanout on `127.0.0.1:4317`. Your collector or SDK only ever sees the proxy.

**Direct termination.** Set `TLS_CERT_FILE` and `TLS_KEY_FILE` in Fanout's environment. Both the HTTP and gRPC listeners use the same certificate. TLS 1.3 minimum.

If you set only one of the two TLS variables, Fanout exits at startup — that's a guardrail to catch half-configured deployments.

## Multi-product / multi-tenant: namespaces

If a single Fanout serves more than one product or environment, set `service.namespace` in your OpenTelemetry resource attributes. The UI's namespace picker (top-right of the header) filters every view; MCP tools accept `namespace` as an explicit argument.

```sh
export OTEL_RESOURCE_ATTRIBUTES=service.namespace=product-a,service.name=checkout
```

Payloads without a `service.namespace` land in `DEFAULT_NAMESPACE` (`default` unless you've overridden it in [config](/docs/config/)).

## Verifying ingest

A few quick checks if traces aren't appearing in the UI:

- **Token header.** Confirm the header reaches Fanout. Most reverse proxies forward custom headers by default, but a few strip them.
- **Endpoint scheme.** SDKs treat `OTEL_EXPORTER_OTLP_ENDPOINT` without a scheme as plaintext on the default port. Add `http://` or `https://` explicitly.
- **Reachability.** From your service host: `nc -vz fanout.example.com 4317` should connect.
- **Default flush.** Data takes up to `FLUSH_SECONDS` (default 15) to appear after the SDK exports it. Wait a beat before debugging.
- **Service appears.** If the service shows up in the UI but spans don't, your SDK is only emitting metrics or logs — confirm the trace exporter is enabled.
