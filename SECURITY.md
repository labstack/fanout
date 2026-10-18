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
- Ingest credential generation, storage, rotation, and enforcement
- Query paths that bypass an explicit namespace filter or dashboard ownership
- Anything that turns telemetry content into executable behavior

Namespaces filter telemetry queries; they do not restrict account access to data.
Every telemetry reader can query every namespace. Credential substitution and
cross-owner dashboard access are authorization findings.

## Supported versions

Fanout uses CalVer and moves quickly. Fixes land on `main`, and only the latest
release is supported. There are no backports to earlier tags.

## Operating Fanout safely

Fanout serves plaintext by default. Configure `FANOUT_TLS_CERT_FILE` and
`FANOUT_TLS_KEY_FILE`, or place it behind a reverse proxy that terminates TLS.
Browser and API reads require an authenticated account, and every OTLP request
requires the separately managed ingest token. Demo instances use the same
credential paths as production instances.
