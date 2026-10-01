# Agent instructions

## Control database

- Keep SQLite with `modernc.org/sqlite` through `database/sql`, sqlc-generated
  query bindings, and Goose SQL migrations. DuckDB owns the telemetry query
  schema separately. Do not introduce pgx or PostgreSQL for control state.
- `internal/db/migrations` is the control schema source for both Goose and
  sqlc. Do not add a separate schema copy or another migration framework.
- Use timestamped Goose SQL migrations with `-- +goose Up`. Published
  migrations are immutable; add a new migration for subsequent changes and
  extend the checksum ledger in `internal/db/migrations_test.go`. Never replace
  a published checksum.
- Run `just db-gen` after schema or query changes and verify the storage tests.
- No Atlas conversion, legacy migration tracker, or schema fallback. A database
  with application tables must have a positive applied Goose version before
  initialization. An empty or zero-only version table is not sufficient.

## Product versioning

Fanout uses CalVer with the format `YYYY.M.N[-alpha|-beta|-rc]`.

- `YYYY` is the full year and `M` is the unpadded month.
- `N` is the release number within that month, starting at `0`.
- Increment `N` for every published release, including alpha, beta, RC, and GA.
- Reset `N` to `0` when the release month changes.
- Use lowercase `alpha`, `beta`, or `rc` to indicate product maturity. GA has no suffix.
- The monthly release number identifies each published build. Do not append an
  additional prerelease counter such as `-beta.1`.
- Git tags include a leading `v`; container tags omit it.
- Published versions are immutable. Changes require a new release number.

Example release sequence:

```text
v2026.9.0-alpha
v2026.9.1-alpha
v2026.9.2-beta
v2026.9.3-beta
v2026.9.4-rc
v2026.9.5
```

## API and MCP naming

Use the same naming conventions as Monk, Goal, Cipher, and ResponseKeeper
where the operations have the same meaning. Product-specific resources and
protocol-defined contracts keep their own semantics.

- Application HTTP routes use `/api`, lowercase words, meaningful slash-separated
  domains/resources/actions, and plural collections. Do not use hyphens in
  product-defined static path segments. Opaque resource IDs may contain hyphens.
  Singular paths are appropriate for genuine
  singleton resources or read views. Protocol-defined paths keep their required
  spelling, including `/.well-known/oauth-authorization-server`.
- GET reads, POST creates or invokes an action, PUT replaces the writable
  representation, PATCH partially modifies it, and DELETE removes it.
  Choose the method from the handler's behavior, not its current name.
- Custom actions use POST and a slash-separated action, for example
  `/api/users/{id}/access/revoke` or `/api/settings/ingest/token/rotate`.
  Each segment should have a domain, resource, or action meaning; do not
  mechanically split compound words. Colon actions are conventions in other
  API ecosystems.
- Equivalent authentication flows use `/api/auth/me`, `/api/auth/code/send`,
  `/api/auth/code/verify`, and `POST /api/auth/logout`. Flow selection, passkeys,
  account linking, and sign-in link verification are distinct operations.
- Operational routes use `/healthz` for liveness, `/readyz` for readiness,
  and `/metrics` for Prometheus exposition. Preserve authorization requirements.
- MCP tool names use verb-first snake_case and describe their behavior:
  `get`, `list`, `search`, `inspect`, `create`, `replace`, and domain actions.
  Full replacement uses `replace`, not `update`. Do not add product prefixes
  solely to avoid collisions in clients that aggregate servers.
- New product-owned JSON fields and query parameters use snake_case. Preserve
  protocol-defined names, including MCP, OAuth, OTLP, and AG-UI fields. Existing
  client-contract exceptions require a deliberate contract change.
- Product CalVer, client-contract versions, and protocol versions are separate.
  Do not put the product release or maturity suffix in API paths or tool names.
- Breaking naming changes update registrations, authorization classification,
  clients, tests, metrics configuration, and generated documentation together.
  Remove superseded routes and handlers; add no aliases or fallback behavior.

HTTP method semantics follow [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html)
and [RFC 5789](https://www.rfc-editor.org/rfc/rfc5789.html). The path and casing
rules above are project conventions, not a universal yearly API standard.
MCP names follow the published
[2026-07-28 tool-name guidance](https://modelcontextprotocol.io/specification/2026-07-28/server/tools#tool-names).
