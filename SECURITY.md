# Security Policy

## Reporting a vulnerability

Please do not open a public issue for security vulnerabilities.

Report them privately through
[GitHub Security Advisories](https://github.com/labstack/fanout/security/advisories/new),
or by email to **security@labstack.com**.

Include what you need to make the issue reproducible: affected version or
commit, configuration relevant to the finding, and the steps or request
sequence that triggers it.

We will acknowledge your report and keep you informed as we work on a fix. If
you would like credit in the advisory, say so and tell us how you want to be
named.

## Scope

Fanout stores telemetry and the credentials needed to reach it, so findings in
these areas are especially relevant:

- Authentication and session handling — local email codes, OIDC, MCP OAuth
- Authorization between roles, and between browser sessions and MCP clients
- Ingest credentials, and the `PUBLIC_READ` / `PUBLIC_INGEST` overrides
- Query paths that could read data outside the requesting namespace
- Anything that turns telemetry content into executable behavior

The credential classes above are deliberately separate and are never
interchangeable. A finding that crosses those boundaries is in scope.

## Supported versions

Fanout is pre-1.0 and moves quickly. Fixes land on `main`, and only the latest
release is supported. There are no backports to earlier tags.

## Operating Fanout safely

Fanout terminates no TLS of its own by default and expects to sit behind a
reverse proxy that does. `PUBLIC_READ` and `PUBLIC_INGEST` disable
authentication for reads and ingest respectively; both are intended for
disposable demo instances and should never be enabled on an instance holding
real telemetry.
