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

## Telemetry engine and format

- Use the pinned DuckDB 2 engine in `internal/duckdb`, built with
  `scripts/with-duckdb.sh`. A Go driver version does not identify its bundled
  native engine; verify `SELECT version()` when changing the pin.
- Build and test on native Linux or macOS, AMD64 or ARM64. The wrapper verifies
  each platform archive's checksum and uses its matching headers and statically
  linked core, JSON, Parquet, and time-zone extensions. Do not add dynamic
  extension downloads or a second engine path.
- Store OTLP attributes and resource attributes as typed maps in canonical rows
  and shredded Parquet VARIANT columns. Preserve integers, booleans, bytes,
  nested values, and nanosecond UTC timestamps. Convert nested SQL results to
  JSON only at the client boundary; attribute keys containing dots are literal.
- Dashboard file snapshots use each signal's actual event-time footer bounds,
  including signed nanoseconds. Unknown statistics include the file. Never
  substitute ingestion time or prune public arbitrary SQL from a dashboard scope.
- Notable-trace candidates acknowledge
  complete immutable batch IDs transactionally. Query cache markers and rows in
  one read transaction under the pinned file snapshot; uncached active files
  remain immediately visible. Compaction and retention must invalidate retired
  contributions without double counting or losing late publications.
- Version disposable read-cache schema and semantics together; mismatches rebuild
  all private read tables. Cache per-batch trace bounds,
  and one incremental candidate per trace, never individual event copies or
  four globally rewritten scope indexes. Mixed trace scopes use batch parts. Exact
  scoped boundaries read footer-pruned Parquet, and compaction derives complete
  output contributions from cached inputs. Body search matches redacted text.
  Analytical service/edge watermark lag is separate. Completed-batch writes
  have their own gate and write-pool slot; analytical writes use disjoint tables.
  Maintenance must hold both gates for checkpointing.
- Batch format 3, including its physical schema, is the only accepted telemetry
  format. Schema changes require a format version change. Disable schema unioning
  and Hive partition inference; use native Parquet binding so VARIANT extracts
  can reach the scan. Pre-project hot messaging fields into scalar read-view
  columns; this preview does not push general VARIANT extracts across views.
  Reject unsupported metadata before cleanup or schema rewriting, preserve those files, and add
  no legacy reader or format fallback.
- Keep native `TIMESTAMPTZ_NS` columns in time predicates. Bind Go window
  parameters as `?::TIMESTAMP_NS::TIMESTAMPTZ_NS` to preserve nanoseconds;
  cast returned timestamps and datetime-function arguments only. Every engine
  connection uses UTC. Do not change Parquet UTC-instant semantics for speed.
- Offline verification reuses one bounded, non-spilling native engine to decode
  all Parquet columns and checks physical schemas, complete span sort tuples,
  and exact index ranges. Operational errors and unsupported formats never
  authorize quarantine. Discarded hashes force decoding; they are not checksums.
- Keep DuckDB 2's memory-governed asynchronous I/O defaults unless measurements
  justify tuning them. Do not force an unbounded read-ahead depth.
- Arbitrary SQL is a single read-only SELECT over approved telemetry relations.
  Parse its AST and describe/project results on the same connection and pinned
  Parquet snapshot. Keep engine file access and configuration locked down.
- Rooted service traversal uses keyed recursion over the complete scoped edge
  rollup, keeps namespaces separate, and enforces hop, accumulated-node, and
  execution-time limits. Report truncation explicitly.

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
