## Why

Fanout's measurements do not justify changing languages or query engines:
DuckDB and data layout dominate query work, while measured Go-side query
allocations are small. The next performance and architecture work should improve
the existing Go, DuckDB, and DuckLake data plane while preserving the
single-binary product model.

## What Changes

- Retain Go as Fanout's implementation language and DuckDB/DuckLake as the
  embedded query and storage stack.
- Capture a reproducible pre-change baseline before optimization. The baseline
  covers sustained ingest, mixed typed queries, rollups, merge, and maintenance;
  records correctness, throughput, p50/p95/p99, CPU, RSS, allocation rate,
  write-lock wait and hold time, rollup lag, file growth, and catalog errors; and
  replaces short bursts and historical pre-optimization results as the decision
  reference.
- Stage work so each effect is attributable: first add measurement-only
  instrumentation and verify that it preserves behavior, then capture the
  baseline, and only then measure behavior-neutral boundary changes, write
  scheduling, physical layout, profile-directed allocation work, and bounded
  rollup improvements separately.
- Keep DuckDB SQL and `database/sql` scanning inside the observability query
  kernel. Preserve the existing typed observability contract at API, MCP, agent,
  alert, and dashboard boundaries; do not add an abstraction solely to hide
  `*sql.Rows` or support hypothetical engines.
- Separate the `Duck` lifecycle responsibilities for query execution, rollup
  scheduling, maintenance coordination, and telemetry writing where that makes
  ownership and testing clearer, without changing query semantics or public
  contracts.
- Instrument the shared write critical section before changing it. Preserve one
  DuckLake catalog commit at a time, then reduce avoidable wait and tail latency
  through explicit scheduling, bounded work, and merge/maintenance cadence—not
  through concurrent catalog writers.
- Benchmark DuckLake physical-layout improvements, including sort order,
  partition pruning, file sizing, and sorted compaction, one variable at a time.
  Preserve logical schemas and query results, and verify restart, backup, and
  rollback behavior when existing physical files are compacted or rewritten.
- Profile and reduce remaining Go allocation and copying costs in OTLP
  normalization, JSON materialization, batch handoff, and result decoding.
  Consider Go-native Arrow batches only if profiles identify row representation
  or marshalling as a material remaining bottleneck; Arrow remains an optional
  DuckDB-path optimization, not an engine or architectural requirement.
- Move additional repeated investigation work onto bounded incremental rollups
  only when the new path preserves watermark handling, late-data safety, raw
  fallback, result provenance, and existing typed results.
- Require every performance experiment to predeclare one target metric and its
  applicable guardrails. Guardrails default to every continuous baseline metric,
  including throughput, export and query latency, CPU, RSS, allocation rate,
  write-lock timing, rollup lag, and storage growth; excluding one requires an
  explicit rationale in the evidence. Retain a performance-motivated change only
  when the median of three repeated runs improves the target by at least 10% and
  no guardrail median regresses by more than 5%. No individual run may violate a
  correctness invariant or an existing operational SLO. Treat ingest durability
  and result correctness as zero-tolerance invariants: any unexpected dropped,
  duplicated, or silently unacknowledged row, catalog integrity failure, or
  query-result mismatch fails the change. Validate the combined retained changes
  with at least a 30-minute mixed run and a two-hour production-shaped Linux
  soak.

Non-goals:

- No language or query-engine replacement, sidecar, separate database service,
  distributed execution layer, or multi-engine plugin system.
- No change to the raw DuckDB SQL dialect or to public HTTP, MCP, AG-UI, OTLP,
  authentication, backup, or logical on-disk compatibility contracts.
- No write-ahead log, hot memtable, or new durability promise in this change;
  those would require a separately specified behavior change.
- Benchmark snapshots remain evidence, not canonical shipped behavior.

## Capabilities

### New Capabilities

None. This change is an internal architecture, profiling, and performance
improvement that preserves shipped behavior.

### Modified Capabilities

None. The existing `product-foundation`, `telemetry-ingestion`,
`telemetry-storage-query`, and `operations` requirements remain the correctness
baseline. The change opts out of delta specs with `skip_specs: true`. If rollup
freshness, fallback, provenance, durability, or storage compatibility changes
during design, implementation MUST stop until the affected capability receives a
delta spec. The design MUST include explicit canonical-spec checkpoints before
the write-scheduling, physical-layout, and rollup stages. Physical-layout work
MUST stop for a delta spec if it changes backup, restore, downgrade, restart, or
logical compatibility semantics.

## Impact

The change affects internal Go boundaries, benchmark and profiling tooling,
query and ingest tests, operational metrics, and DuckDB/DuckLake configuration.
Likely touch points include `cmd/bench`, `scripts/`, `internal/ingest`,
`internal/lake`, `internal/observability`, `internal/query`, and
`internal/metrics`.

Runtime semantics, public contracts, security boundaries, logical data format,
and the production deployment model remain unchanged. Physical Parquet files and
DuckLake snapshots MAY be rewritten by tested compaction or sort-order changes,
so the design must include restart, backup, rollback, and mixed-version checks.
The released artifact remains one Go binary with embedded DuckDB/DuckLake and
browser assets.
