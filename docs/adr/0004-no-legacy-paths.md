# 0004. No legacy paths

Status: Accepted
Date: 2026-10-30

## Context

Keeping aliases, fallback routes, old readers or schema fallbacks next to their
replacements doubles the code to test and lets superseded behaviour linger.

## Decision

Superseded behaviour is removed in the same change that replaces it:

- Breaking naming changes update registrations, authorization classification,
  clients, tests, metrics configuration and generated documentation together,
  and remove superseded routes and handlers with no aliases or fallback.
- The control database has no legacy migration tracker or schema fallback.
- Telemetry storage accepts only the current batch format, with no legacy
  reader or format fallback.

## Consequences

- One code path per behaviour, and less to review.
- A breaking change can require updating clients, or resetting data written in
  an unsupported format.
