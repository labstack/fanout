# 0001. Environment-only configuration

Status: Accepted
Date: 2026-10-30

## Context

Fanout runs as one binary on laptops, containers and hosted platforms. Mixing
configuration files, flags and environment variables made precedence hard to
explain and secrets easy to leak into files.

## Decision

Configuration comes only from the process environment, over built-in defaults.
`internal/config/config.go` is the authoritative schema. Custom names use the
`FANOUT_` prefix; vendor credentials and endpoints keep their vendor names
(`ANTHROPIC_API_KEY`, `OPENAI_BASE_URL`, …). Every secret also accepts a
`<NAME>_FILE` form. Unknown `FANOUT_` names fail startup. The binary does not
load dotenv files.

## Consequences

- One precedence rule and one place to read the schema.
- `.env.example` and the site reference are generated from the schema.
- Secrets can be mounted as files without appearing in the environment.
- A typo in a `FANOUT_` name fails fast instead of being ignored.
