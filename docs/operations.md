# Operator runbook

Fanout is one process and one persistent data directory. Operate those two
things as a unit: preserve the directory, pin the binary or image version, and
give ingest credentials only to trusted services and collectors.

## Runtime secret delivery

Runtime configuration uses built-in defaults followed by nonempty process
environment values. Every setting requires a restart. Fanout reads no
configuration document or dotenv file. Generated `.env.example` lists the
settings as commented-out empty entries, with types, defaults, and secret file
forms in comments. Copying the template sets nothing and preserves image ENV;
uncomment only settings you want to supply. For development, `just run`
loads optional ignored `./.env` with Node's native `parseEnv`; inherited values
win, including empty values. Personal development credentials may live there.
Personal environment files and operator profiles are excluded from Git and Docker.

Custom names use `FANOUT_` plus a clear name, adding the area only when it
clarifies. Vendor credentials and endpoints keep their documented names.
Unknown `FANOUT_` names fail startup, except platform service-discovery names;
unrelated names in shared vendor namespaces are ignored.

### Environment and mounted secret files

Deliver credentials through deployment environment injection or managed mounts.
Every secret accepts either its value or its file path:

| Value variable | File path variable |
| --- | --- |
| `FANOUT_AUTH_CODE_SECRET` | `FANOUT_AUTH_CODE_SECRET_FILE` |
| `FANOUT_OIDC_CLIENT_SECRET` | `FANOUT_OIDC_CLIENT_SECRET_FILE` |
| `FANOUT_SMTP_PASSWORD` | `FANOUT_SMTP_PASSWORD_FILE` |
| `FANOUT_METRICS_TOKEN` | `FANOUT_METRICS_TOKEN_FILE` |
| `ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY_FILE` |
| `OPENAI_API_KEY` | `OPENAI_API_KEY_FILE` |

Both forms nonempty fail startup. Fanout strips exactly one trailing LF or CRLF
from file contents, then rejects an empty result. Errors identify the variable
and path, never the contents. Fanout performs no file-mode check; the deployment
controls mount access. Do not bake credentials into images or frontend bundles.
Unselected AI providers' value and file forms are ignored.

For Docker, mount a provisioned secret and inject its path:

```sh
docker run --name fanout -p 7520:7520 \
  -v fanout-data:/var/lib/fanout/data \
  --mount type=bind,src="$AUTH_SECRET_PATH",dst=/run/secrets/auth_code,readonly \
  -e FANOUT_AUTH_CODE_SECRET_FILE=/run/secrets/auth_code \
  labstack/fanout:latest
```

The source secret must be readable by the image's UID 65532. In Compose, grant
only the required secret to the service:

```yaml
services:
  fanout:
    image: labstack/fanout:latest
    ports: ["7520:7520"]
    volumes: ["fanout-data:/var/lib/fanout/data"]
    environment:
      FANOUT_AUTH_CODE_SECRET_FILE: /run/secrets/auth_code
    secrets: [auth_code]
secrets:
  auth_code:
    file: ${AUTH_SECRET_PATH}
volumes:
  fanout-data:
```

For Kubernetes, put ordinary settings in a ConfigMap and credentials in a
Secret provisioned through your platform's secret manager. This Pod fragment
injects both without putting credential values in the manifest:

```yaml
containers:
  - name: fanout
    image: labstack/fanout:latest
    envFrom:
      - configMapRef:
          name: fanout-settings
    env:
      - name: FANOUT_AUTH_CODE_SECRET
        valueFrom:
          secretKeyRef:
            name: fanout-credentials
            key: auth_code_secret
```

Alternatively mount the Secret as files and set the corresponding `_FILE`
paths. Use the existing persistent-volume and access policy for telemetry data.

### SOPS runtime injection

Use one authoritative SOPS-encrypted profile per actual workload/credential
set, such as ignored `secrets/fanout.env`. Select a complete profile explicitly;
never layer different credential sets or fall back on a missing profile.
Platform-owned credentials remain platform-owned. This public repository ships
no operator secrets, recipient policy, or decryption keys.

With a provisioned SOPS key and existing encrypted profile, launch through
[SOPS runtime injection](https://getsops.io/docs/usage/advanced/#passing-secrets-to-other-processes)
without writing decrypted credentials to disk:

```sh
sops exec-env secrets/fanout.env './bin/fanout'
# For local development with optional personal settings:
sops exec-env secrets/fanout.env 'just run'
```

The profile supplies environment values to the child; ordinary settings may
also come from deployment inputs. A dotenv parser neither decrypts SOPS nor
resolves 1Password references. Verify synthetic values containing dollars,
quotes, spaces, and newlines through the actual deployment parser before
using a profile. Keep private age identities out of Git, restrict their
local files to mode `0600`, and document key recovery in the existing 1Password
item. 1Password holds recovery rather than a second maintained copy of
SOPS-owned application secrets.

AI chat is disabled by default. Select exactly `anthropic` or `openai` with
`FANOUT_AI_PROVIDER`, then deliver its required `ANTHROPIC_API_KEY` or
`OPENAI_API_KEY`. A selected provider without its key fails startup. Inherited
keys never enable chat. Only the selected endpoint is validated and used.
Overrides are `ANTHROPIC_BASE_URL` and `OPENAI_BASE_URL`; the OpenAI base
includes `/v1`, while Anthropic's excludes it.

## Network surface

| Default | Purpose | Exposure |
|---|---|---|
| `127.0.0.1:7520` (native), `:7520` (image) | Browser, API, MCP, health, metrics, OTLP/gRPC and OTLP/HTTP | Users, trusted services or a reverse proxy |

Both native and container deployments use one listener. Native installs default
to loopback; set `FANOUT_ADDR=:7520` for remote access. The image supplies
`FANOUT_ADDR=:7520` and `FANOUT_DATA_DIR=/var/lib/fanout/data`. Every OTLP request
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

MCP also validates `Origin` before authentication; see
[Connect an agent over MCP](https://fanout.run/guides/connect-over-mcp#protocol).

## Health and monitoring

`fanout healthcheck` derives `/healthz` from `FANOUT_ADDR` or its built-in
default. Empty, `0.0.0.0`, and `::` bind hosts become `127.0.0.1`.
`FANOUT_TLS_CERT_FILE` selects HTTPS. Derived probes bypass environment proxies.
HTTPS IP-literal probes trust only the configured certificate and verify its
first DNS or IP SAN, which may differ from the bind IP; hostname probes verify
normally. Healthcheck reads only these two settings and does not require valid
secrets. An explicit `fanout healthcheck <url>` uses default proxy and
certificate verification behaviour.

- `/healthz` is the liveness check.
- `/readyz` includes storage readiness and resolved runtime sizing.
- `/metrics` exposes Prometheus metrics. Keep it private or configure
  `FANOUT_METRICS_TOKEN`; do not make it public merely to simplify scraping.

Alert on repeated restarts, readiness failures, ingest authentication failures,
telemetry drops, sustained query latency, and available disk space. Retention
is controlled by `FANOUT_RETENTION_DAYS`; maintenance removes expired data on
its configured cycle rather than immediately when the setting changes.

## Control database migrations

Fanout uses SQLite with the modernc driver, sqlc query bindings, and embedded
Goose SQL migrations. Pending migrations run forward at startup. PostgreSQL
and pgx are not required.

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
fanout login-link user@example.com
```

The resulting link expires after 15 minutes, works once, and is recorded in the
authentication audit history. A configured SMTP relay that fails delivery is
reported as an error rather than silently claiming that an invitation arrived.
