# Operator runbook

Fanout is one process and one persistent data directory. Operate those two
things as a unit: preserve the directory, pin the binary or image version, and
give ingest credentials only to trusted services and collectors.

## Network surface

| Default | Purpose | Exposure |
|---|---|---|
| `:7520` | Browser, API, MCP, health, metrics, OTLP/gRPC and OTLP/HTTP | Users, trusted services or a reverse proxy |

Both native and container deployments use one listener. Set
`FANOUT_ADDR=127.0.0.1:7520` for loopback-only access. Every OTLP request
requires the ingest token created during first-admin setup; browser sessions
and MCP tokens do not authorize ingest. Do not embed the ingest token in a
browser or shipped mobile application.

For OTLP/HTTP, set `http/protobuf`, use the base endpoint
`http://fanout:7520`, and supply `Authorization: Bearer <token>`. Exporters
derive `/v1/traces`, `/v1/metrics`, and `/v1/logs` from the general endpoint.
Signal-specific endpoint variables require the full path. OTLP/gRPC uses the
same address and authorization value, over HTTP/2.

## Mobile boundary

Fanout remains a backend receiver, not a public mobile collector. The supported
path is `mobile SDK -> customer-controlled public Collector or gateway ->
private Fanout`; the gateway owns public-client authentication, rate limits,
privacy policy, and abuse controls. Never compile Fanout's shared ingest token
into a shipped application.

Propagate the standard `traceparent` and `tracestate` headers from mobile HTTP
requests to backend services. When the customer's gateway exports client spans
to Fanout, that context correlates them with the backend trace without a Fanout
mobile SDK or a public Fanout listener.

## TLS and reverse proxies

Setting both `FANOUT_TLS_CERT_FILE` and `FANOUT_TLS_KEY_FILE` enables TLS 1.3 on
the shared listener. A proxy must forward HTTP/2 to Fanout for gRPC: use
h2c for a plaintext backend, or HTTP/2 over TLS when Fanout has a certificate.
HTTP/1.1 forwarding supports OTLP/HTTP, but cannot carry gRPC.

Set `FANOUT_PUBLIC_URL` to the externally reachable HTTPS browser origin. When
using forwarded client addresses, set `FANOUT_TRUSTED_PROXY_CIDRS` to only the
proxy networks; never use `0.0.0.0/0`. The MCP resource URL is the same origin
with `/mcp` appended. Fanout accepts public MCP transport requests only when
their HTTP `Host` matches that origin, so the reverse proxy must preserve the
original host.

## Health and monitoring

- `/healthz` is the liveness check.
- `/readyz` includes storage readiness and resolved runtime sizing.
- `/-/metrics` exposes Prometheus metrics. Keep it private or configure
  `FANOUT_METRICS_TOKEN`; do not make it public merely to simplify scraping.

Alert on repeated restarts, readiness failures, ingest authentication failures,
telemetry drops, sustained query latency, and available disk space. Retention
is controlled by `FANOUT_RETENTION_DAYS`; maintenance removes expired data on
its configured cycle rather than immediately when the setting changes.

## Backup

The supported portable baseline is a **cold backup** of the complete
`FANOUT_DATA_DIR`. It contains atomic telemetry Parquet batches, query state,
and the control SQLite database; copying only one
subdirectory does not produce a recoverable installation.

1. Record the running Fanout version and configuration, excluding secrets from
   ordinary logs or tickets.
2. Stop Fanout cleanly and wait for the process to exit. Shutdown stops the shared
   listener and allows five seconds for active requests before closing remaining
   connections, then drains the telemetry commit workers.
3. Snapshot or copy the complete data directory with ownership and permissions
   preserved.
4. Restart Fanout and confirm `/readyz`.
5. Periodically restore a backup into an isolated directory; an untested backup
   is not a recovery plan.

Do not use a normal recursive copy while Fanout is writing. A storage-level
snapshot is acceptable only when it provides a crash-consistent point-in-time
view of the entire data directory.

## Restore

1. Stop the destination Fanout process.
2. Move the existing data directory aside rather than mixing it with a backup.
3. Restore the complete directory and its original ownership and permissions.
4. Start the **same Fanout version that created the backup** and verify
   `/readyz`, sign-in, and a known telemetry query.
5. Upgrade separately after the restored installation is known healthy.

Never recover an account by deleting its row from the control database; that
cascades to sessions, OAuth grants, and owned dashboards. Use `fanout login-link
<email>` as documented in [authentication.md](authentication.md).

## Single-port migration

This is a breaking configuration change:

1. Remove `FANOUT_OTLP_GRPC_ADDR` and `FANOUT_OTLP_HTTP_ADDR`, or YAML
   `ingest.otlp_grpc_addr` and `ingest.otlp_http_addr`. They are rejected at startup.
2. Rename `FANOUT_HTTP_ADDR` to `FANOUT_ADDR`, or YAML `server.http_addr`
   to `server.addr` (default `:7520`). Publish only that container port.
3. Change exporters from ports `4317`/`4318` to `7520`, or to the public HTTPS
   origin and port. The ingest token and OTLP signal paths do not change.
4. Enable HTTP/2 backend forwarding for gRPC at any proxy. Remove obsolete
   ingest port mappings and proxy upstreams. A separate ingest hostname may
   still route to the same Fanout port if required by the edge configuration.
5. Set `FANOUT_PUBLIC_URL` to the external origin so setup instructions advertise
   the reachable endpoint. An explicit `FANOUT_INGEST_ADVERTISED_ENDPOINT` wins.

Local comparison results are in [the single-port benchmark report](benchmarks/single-port-2026-09-08.md).

## Upgrade and rollback

1. Read the release notes and pin the target image tag or binary version.
2. Take and verify a cold backup.
3. Stop Fanout, replace only the binary/image, and retain the data directory.
4. Start Fanout and check `/readyz`, authentication, both configured OTLP
   transports, and representative queries.

Database migrations run forward during startup. A binary rollback after
migration is not assumed safe. Restore the pre-upgrade backup with the previous
binary when rollback is required.

## Local users without SMTP

SMTP is optional. Creating a local user without it succeeds and returns
`invite_delivery: "not_configured"` with `login_link_required: true`. An
operator with access to the same configuration and data directory then runs:

```sh
fanout --config /etc/fanout/fanout.yaml login-link user@example.com
```

The resulting link expires after 15 minutes, works once, and is recorded in the
authentication audit history. A configured SMTP relay that fails delivery is
reported as an error rather than silently claiming that an invitation arrived.
