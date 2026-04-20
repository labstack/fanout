---
title: OTLP ingest
description: Send OpenTelemetry traces, logs, and metrics to Fanout.
---

Fanout accepts OpenTelemetry over **gRPC** on port `4317`. HTTP/JSON OTLP is not supported.

## Authentication

Every request must present a valid ingest token. The token is generated during admin setup (shown once) and rotatable from **Settings → Ingest**.

Two header formats are accepted:

```
x-fanout-ingest-token: fo_<token>
Authorization: Bearer fo_<token>
```

Without a valid token, requests are rejected with `Unauthenticated`.

## OpenTelemetry SDK

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=fanout.example.com:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_EXPORTER_OTLP_HEADERS=x-fanout-ingest-token=fo_xxx
OTEL_SERVICE_NAME=my-service
```

## OpenTelemetry Collector

```yaml
exporters:
  otlp/fanout:
    endpoint: fanout.example.com:4317
    headers:
      x-fanout-ingest-token: fo_xxx

service:
  pipelines:
    traces:  { exporters: [otlp/fanout] }
    logs:    { exporters: [otlp/fanout] }
    metrics: { exporters: [otlp/fanout] }
```

## TLS

- **Behind a reverse proxy** (recommended): Caddy/nginx/Traefik terminates TLS on `fanout.example.com:4317` and proxies gRPC to Fanout on `127.0.0.1:4317`. Your collector/SDK speaks TLS to the proxy.
- **Direct termination**: set `TLS_CERT_FILE` + `TLS_KEY_FILE` in Fanout's environment. Both HTTP and gRPC listeners use the same cert.

## Namespaces (multi-product)

If you run multiple products through one Fanout, set `service.namespace` in your resource attributes. The UI groups services by namespace and the `ns:` filter works across search and MCP tools.

```sh
OTEL_RESOURCE_ATTRIBUTES=service.namespace=product-a,service.name=checkout
```
