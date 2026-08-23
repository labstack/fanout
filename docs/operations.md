# Operator runbook

Fanout is one process and one persistent data directory. Operate those two
things as a unit: preserve the directory, pin the binary or image version, and
keep the ingest listeners private to trusted services and collectors.

## Network surface

| Default | Purpose | Exposure |
|---|---|---|
| `:7520` | Browser, API, MCP, health and metrics | Users or a trusted reverse proxy |
| `127.0.0.1:4317` | OTLP/gRPC | Trusted collectors and backend services |
| `127.0.0.1:4318` | OTLP/HTTP | Trusted collectors and backend services |

The native ingest defaults are loopback-only. The container configuration uses
`:4317` and `:4318` so explicitly published container ports are reachable.
Every OTLP request still requires the ingest token created during first-admin
setup. Do not embed that shared backend secret in a browser or shipped mobile
application; put a customer-controlled Collector or gateway in front of Fanout
when public clients are involved.

For OTLP/HTTP, set the exporter protocol to `http/protobuf`, use the base
endpoint `http://fanout:4318`, and supply the ingest token as
`Authorization: Bearer <token>`. Exporters derive the standard `/v1/traces`,
`/v1/metrics`, and `/v1/logs` paths from the base endpoint. OTLP/gRPC uses the
same authorization value as gRPC metadata on port `4317`.

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
the browser, OTLP/gRPC, and OTLP/HTTP listeners. If a reverse proxy terminates
TLS only for the browser listener, keep the ingest listeners on a trusted
private network or terminate their protocols separately.

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
`FANOUT_DATA_DIR`. It contains the telemetry catalog and Parquet files, query
state, and the control SQLite database; copying only one subdirectory does not
produce a recoverable installation.

1. Record the running Fanout version and configuration, excluding secrets from
   ordinary logs or tickets.
2. Stop Fanout cleanly and wait for the process to exit. Shutdown stops both
   OTLP listeners before draining the lake writer.
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
