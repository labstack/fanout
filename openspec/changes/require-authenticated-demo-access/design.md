# Design: Require authenticated demo access

## Context

The browser middleware currently has a conditional branch that manufactures a
viewer principal for selected telemetry routes. The ingest interceptor has a
separate conditional branch that returns before loading or checking the stored
token. Both branches are controlled by startup environment variables and are
off by default.

The normal paths are already usable for demos: first-admin setup creates an
administrator session and an ingest token, administrators can create viewer
accounts, and local or OIDC login establishes browser sessions. Removing the
bypasses therefore simplifies behavior without adding a replacement identity
system.

## Decisions

### Browser requests always use persisted accounts

Delete the synthetic principal, its special capability rules, and its ownership
guard. `/api/auth/me` returns the persisted user directly. The React auth gate
recognizes only `none` and `user`; a missing or rejected session renders login.

This intentionally removes anonymous public dashboards. A public demo creates
a real viewer account and uses the same login flow as any deployment.

### OTLP always uses the ingest credential

Delete the early-success branch from the gRPC interceptor. Before setup, the
missing token hash keeps ingest closed. After setup, collectors must present
the generated token in `x-fanout-ingest-token` or as a bearer token.

A human account is not reused for ingest. Browser sessions and ingest tokens
remain different credential classes with different rotation and audit behavior.

### The benchmark proves the authenticated paths

`cmd/bench` requires `-token` for every run. Query-under-load requests require
`-query-session-cookie`, a complete Cookie header copied from a signed-in viewer
session. The value is sent only to the configured query origin and is omitted
from reports and logs.

The existing published two-vCPU result remains labeled as a historical run that
did not measure token verification. Its runnable instructions use credentials
so future measurements are production-representative.

## Security

The change fails closed: no replacement fallback is introduced. A deployment
that previously depended on a bypass receives HTTP 401 or gRPC Unauthenticated
until an account session or ingest token is supplied. Removing special
principals also removes authorization exceptions based on a magic user ID.

Session and ingest credentials remain secrets. Benchmark documentation uses
shell variables and the benchmark manifest does not serialize either value.

## Migration

1. Complete first-admin setup and save the generated ingest token.
2. Configure every collector and benchmark driver with that token.
3. Create a viewer account for demo visitors or benchmark query load and use
   local or OIDC login to obtain its browser session.
4. Remove the two obsolete environment variables from deployment manifests.

No database migration or token rotation is required.

## Compatibility

This is deliberately breaking for unauthenticated demo deployments. Unknown
environment variables are ignored by the environment parser, so leaving an old
variable in a manifest does not restore access; requests fail closed. The auth
status and current-user response changes affect only the embedded browser client,
which is rebuilt in the same release.
