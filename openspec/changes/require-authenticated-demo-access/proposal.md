# Require authenticated demo access

## Why

Fanout has two demo-only environment switches that bypass the normal security
model: `PUBLIC_READ` creates a synthetic anonymous viewer, and `PUBLIC_INGEST`
accepts OTLP without the ingest token. They make demos behave differently from
production, expand the authorization state space, and let a configuration
mistake expose telemetry or permit untrusted writes.

Fanout already has the proper primitives for both use cases. A human can use a
normal viewer account through local or OIDC authentication, while a collector
can use the separately scoped ingest token generated during first-admin setup.
The benchmark driver also accepts that token. There is no remaining product
need for an unauthenticated path.

This change affects runtime behavior, public configuration, the auth status
response, benchmark CLI behavior, and security. It requires no data migration.

## What Changes

- Remove `PUBLIC_READ` and the synthetic anonymous viewer. Every telemetry read
  requires a persisted, active account with the appropriate capability.
- Remove `PUBLIC_INGEST`. Every OTLP request requires the current ingest token,
  including demo and benchmark traffic.
- Stop advertising `public_read` from `/api/auth/status` and stop returning an
  `anonymous` marker from `/api/auth/me`.
- Require `cmd/bench -token`. Mixed query load additionally requires a browser
  session Cookie header from an authenticated account.
- Update security guidance, benchmark reproduction instructions, tests, and
  embedded browser assets to describe one authentication model for demos and
  production.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. No canonical specification currently exists for the configuration or
authentication surface.

## Impact

- **Affected**: `internal/env`, `internal/api`, `internal/ingest`, `ui/host`,
  `cmd/bench`, security guidance, and benchmark documentation.
- **Breaking configuration change**: deployments setting either removed
  variable must complete normal account setup and distribute the ingest token.
- **Breaking response change**: the two demo-mode fields are removed from the
  browser auth responses.
- **Not affected**: telemetry storage, query semantics, schemas, existing users,
  sessions, MCP OAuth credentials, and persisted ingest-token hashes.
