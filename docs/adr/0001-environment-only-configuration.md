# 0001. Environment-only configuration

Status: Accepted
Date: 2026-10-30

## Context

Pitot runs as one binary on laptops, containers and hosted platforms. It
needs one configuration source that works the same way in each, and a way to
deliver secrets without writing them into configuration.

## Decision

Configuration comes only from the process environment, over built-in defaults.
`internal/config/config.go` is the authoritative schema. Custom names use the
`PITOT_` prefix; vendor credentials and endpoints keep their vendor names
(`ANTHROPIC_API_KEY`, `OPENAI_BASE_URL`, …). Every secret also accepts a
`<NAME>_FILE` form. Unknown `PITOT_` names fail startup. The binary does not
load dotenv files.

## Consequences

- One precedence rule and one place to read the schema.
- `.env.example` and the site reference are generated from the schema.
- Secrets can be mounted as files without appearing in the environment.
- A typo in a `PITOT_` name fails fast instead of being ignored.
