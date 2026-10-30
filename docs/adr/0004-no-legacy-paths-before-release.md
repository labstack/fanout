# 0004. No legacy paths before release

Status: Accepted
Date: 2026-10-30

## Context

Fanout has not had a stable release. Keeping aliases, fallback routes or old
readers for contracts that nobody depends on would double the code to test and
let superseded behaviour linger.

## Decision

Until a stable release, breaking changes replace the old behaviour outright.
Superseded routes, handlers, tool names and readers are removed in the same
change, with no aliases or fallbacks. Registrations, authorization
classification, clients, tests, metrics configuration and generated
documentation change together.

## Consequences

- One code path per behaviour, and less to review.
- Upgrading across a breaking change can require resetting local data or
  clients; release notes say so.
- This decision is revisited at the first stable release.
