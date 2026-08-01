## Context

Fanout has accumulated more than thirty internal Markdown documents from four
architectural generations: direct MCP block builders, a standalone React
client, page-oriented observability views, and the current embedded AG-UI plus
MCP Apps product. Public documentation is newer, but it still refers to some
retired surfaces as if they ship. The repository has no existing canonical
behavioral specification.

The baseline was resolved from current entrypoints, route registration, domain
services, persistence models, tests, build configuration, and embedded UI
routes. CodeGraph supplied the structural map; literal configuration and docs
were read directly. The principal sources are:

- `cmd/fanout/main.go` for runtime composition, routes, credentials, and
  shutdown ordering.
- `internal/env/config.go` for configuration defaults and startup validation.
- `internal/ingest`, `internal/lake`, and `internal/query` for the telemetry
  path, storage maintenance, concurrency, and rollups.
- `internal/observability` for the shared typed investigation kernel.
- `internal/dashboard`, `internal/alert`, `internal/agent`, and `internal/mcp`
  for stateful product capabilities.
- `internal/auth`, `internal/api`, and `internal/db/migrations` for identity,
  authorization, OAuth, audit, and control-state contracts.
- `ui/host/src/routes`, `ui/apps`, `Dockerfile`, and `justfile` for the shipped
  browser information architecture and packaging boundary.

## Goals / Non-Goals

**Goals:**

- Make `openspec/specs/` the only current behavioral contract.
- Remove historical documents once their current contracts are consolidated.
- Keep runbooks and public installation guidance close to operators while
  eliminating duplicated product claims.
- Pin strict OpenSpec validation in local and CI workflows.

**Non-Goals:**

- Change runtime behavior, API schemas, migrations, or deployment topology.
- Turn OpenSpec into generated API reference or copy every implementation
  constant into a requirement.
- Preserve transient task checklists, benchmark snapshots, rendered prototypes,
  or rejected UI layouts as active documentation.

## Decisions

### Canonical specs are organized by durable capability

Nine capabilities align to current ownership boundaries and observable product
contracts. This is preferable to organizing specs by old page names because the
current browser ships only chat and dashboard routes, while the deterministic
domains are shared by HTTP, MCP, apps, dashboards, and the agent.

### Code and tests win conflicts during baseline adoption

The following conflicts are resolved explicitly:

- The tracked production build embeds `ui/host`; its primary routes are Chat
  and Dashboards behind a setup/login gate.
- The runtime owns five typed observability results exposed through standard
  MCP tools and optional MCP Apps.
- Telemetry persistence uses DuckLake/Parquet, a local DuckDB query catalog,
  rollups, maintenance, and a separate SQLite control database.
- Authorization is role/capability based. Namespaces are telemetry query scopes,
  while dashboards and threads are owner-scoped.
- Alert management currently ships as an engine and REST API. A browser alert
  surface requires a new OpenSpec change.
- Throughput measurements are deployment evidence rather than a stable product
  guarantee and remain outside canonical specs.

### Superseded material is deleted after consolidation

Requirements, visions, design documents, execution plans, generated SVG/HTML
prototypes, and superseded references are removed after their shipped contracts
are represented in canonical specs. The OpenSpec adoption change records the
consolidation decisions; Git history remains sufficient when archaeology is
needed.

### Runbooks remain outside behavioral specs

`docs/README.md` becomes the source-of-truth index. The public Astro docs remain
the install and operations runbook, but are shortened and corrected to the
current code. The root README stays the quick-start entrypoint. Both link to
canonical specs instead of restating the full behavioral contract.

### OpenSpec 1.7.0 is pinned

The same version used by `../goal` initializes Codex, Claude Code, and GitHub
Copilot integrations. `just docs-check` and CI run strict, non-interactive
validation so an unpinned global CLI cannot silently reinterpret the schema.

## Risks / Trade-offs

- **Useful detail is removed with old plans** -> current behavior is captured as
  testable requirements, the adoption design records conflict resolution, and
  Git history remains available for exceptional archaeology.
- **Specs overstate implementation** -> every normative statement is grounded
  in current code or tests; unsupported page and tenancy claims are excluded.
- **Public docs drift from specs** -> the docs index assigns ownership and CI
  validates all OpenSpec artifacts on relevant changes.
- **Generated integrations add repository surface** -> they are pinned,
  tool-native, and generated by the same OpenSpec initialization as the
  reference repository.
- **Archiving the baseline hides the adoption delta** -> OpenSpec retains the
  complete proposal, design, tasks, and source deltas under a dated archive.

## Migration Plan

1. Initialize OpenSpec and define repository context and artifact rules.
2. Create and strictly validate the nine capability deltas.
3. Reconcile root, internal, and public docs with the current implementation.
4. Remove superseded plans, visions, requirements, references, and prototypes.
5. Add pinned local and CI validation.
6. Complete evidence-linked tasks, strictly validate all artifacts, and archive
   the adoption change so its deltas become canonical specs.

Rollback is documentation-only: restore the prior docs from Git and remove the
OpenSpec initialization. No runtime or data migration is involved.
