## Why

Fanout's current product contract is spread across requirements, visions,
designs, implementation plans, generated diagrams, and public docs written at
different stages of the product. Several documents describe a retired browser
architecture or future UI as current, so maintainers and agents cannot reliably
distinguish shipped behavior from intent.

## What Changes

- Adopt OpenSpec 1.7.0 as the documentation lifecycle for Codex, Claude Code,
  and GitHub Copilot.
- Consolidate behavior verified in the current code and tests into nine
  capability-oriented canonical specifications.
- Delete superseded plans, visions, requirements, references, and rendered
  prototypes after their current shipped contracts are consolidated.
- Restrict `docs/` to navigation and operational guidance, and update the
  README and public site documentation to match the current binary, UI, APIs,
  authentication modes, and deployment model.
- Add pinned strict OpenSpec validation to the normal local and CI checks.

## Capabilities

### New Capabilities

- `product-foundation`: Product promise, runtime boundaries, supported signals,
  namespaces, and the distinction between current behavior and proposed work.
- `telemetry-ingestion`: OTLP/gRPC signal intake, ingest authentication,
  namespace assignment, normalization, backpressure, and shutdown behavior.
- `telemetry-storage-query`: DuckLake/Parquet persistence, DuckDB access,
  rollups, retention, compaction, concurrency, and query scope contracts.
- `investigation-experience`: Deterministic overview, topology, performance,
  trace, and log investigations across HTTP, browser, and rich result surfaces.
- `dashboards`: Owner-scoped named dashboards, validated widget layouts,
  shared filters, REST compatibility, and MCP management.
- `alerting`: Expression rules, evaluation lifecycle, persistence, webhook
  delivery, retention, and access control.
- `agent-and-mcp`: AG-UI investigations, persisted threads, internal reuse of
  the MCP tool registry, portable MCP Apps, and remote MCP behavior.
- `identity-and-access`: First-admin setup, local and OIDC login, server-side
  sessions, roles and capabilities, MCP OAuth, auditing, and public demo modes.
- `operations`: Single-binary packaging, configuration validation, health and
  metrics, TLS, data layout, backup/restore, upgrades, and graceful shutdown.

### Modified Capabilities

None. This change establishes the first canonical baseline from already shipped
behavior.

## Impact

This is a documentation and workflow change. It adds `openspec/` and generated
agent integrations, replaces the former internal docs corpus, updates public
documentation, and extends local/CI validation. It does not change runtime
code, APIs, data formats, migrations, or deployment behavior.
