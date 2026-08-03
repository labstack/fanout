# Design: Evidence-led single-binary data-plane improvements

## Context

Fanout ships as one Go runtime binary. The binary owns OTLP ingestion, a
DuckDB query kernel, DuckLake/Parquet persistence, rollups, HTTP APIs, AG-UI,
MCP, and embedded browser assets. `cmd/bench` is a supported benchmark driver, but
it is not part of the released runtime artifact.

The current storage path already has useful constraints that this change must
preserve:

- DuckLake uses a SQLite catalog and permits one catalog-writing operation at a
  time. Ingest flushes, rollups, merge, and maintenance therefore share a
  process-wide write mutex.
- Ingest uses a bounded asynchronous flush queue and DuckDB appenders. Query
  SQL and `database/sql` row handling stay inside the query kernel; the API,
  MCP, agent, dashboard, and alert layers consume typed observability models.
- Parquet data is compressed with Zstandard, targets 256 MiB files, and is
  partitioned by namespace and hour. No global sort order is currently part of
  the shipped contract.
- Existing rollups use durable watermarks, bounded catch-up chunks, late-data
  lag, raw-query fallback, and provenance metadata.
- The existing local benchmark gate requires a live/healthy server, no drops,
  OOMs, application errors, or benchmark-driver failures, and mixed-query p95 at
  or below 1500 ms. The accelerated soak gate additionally requires no rollup
  sample older than 40 seconds and no more than 800 lake partitions. Those
  thresholds remain release guardrails; they are not proof that a candidate is
  faster.

The earlier M3 Max and rollup measurements are diagnostic evidence only. They
are not the baseline for a retain/reject decision because they were not
captured with the reproducible protocol below on the decision host.

Canonical behavior is defined by `openspec/specs/`. This change deliberately
skips delta specs because measurement infrastructure and behavior-preserving
optimization do not yet change a user-visible requirement. The design includes
explicit checkpoints at which that assumption must be revisited.

## Goals / Non-Goals

**Goals:**

- Establish a reproducible, production-shaped baseline for ingest, mixed
  queries, storage growth, maintenance, rollups, CPU, memory, and allocation.
- Make DuckLake write contention and background work observable without
  changing single-writer semantics.
- Improve the current Go + DuckDB + DuckLake path in independently measurable
  stages, retaining only material wins that pass every guardrail.
- Keep SQL and database-specific types behind the query kernel while preserving
  the typed contracts used by the rest of the product.
- Preserve the one-binary deployment, existing data readability, raw-query
  fallback, security boundaries, and operational behavior.

**Non-Goals:**

- Replacing Go, DuckDB, DuckLake, Parquet, AG-UI, or MCP.
- Adding a second runtime service, sidecar, JVM, Python process, or required
  external database.
- Introducing a hypothetical multi-engine abstraction.
- Replacing the durable lake with a WAL-backed hot store or in-memory primary
  table.
- Changing OTLP, HTTP, MCP, AG-UI, auth, query-result, or raw-SQL compatibility
  contracts without a separate OpenSpec delta.
- Enabling Arrow merely because an integration exists; Arrow remains a
  profile-gated experiment.

## Decisions

### 1. Use a staged evidence protocol

Work proceeds in the following order. A later stage cannot be used to justify
an earlier stage, and measurements from multiple candidate changes cannot be
combined into one acceptance decision.

1. Add measurement-only instrumentation and verify behavior and measurement
   overhead.
2. Capture the immutable baseline.
3. Measure and, if justified, improve write scheduling. This is the one
   optimization the change intends to attempt; the known limit is
   rollup/maintenance contention on the shared catalog write gate.
4. Physical layout, allocation/marshaling, and rollup shapes are conditional
   follow-ups, not planned work. Each starts only if stage 3's profiles
   attribute material cost to it, and each changes one variable at a time.
5. Run final mixed-load validation and the Linux soak on whatever was retained.

A no-go is a successful outcome at any stage. Concluding that contention is not
material, and stopping, is cheaper than an unjustified rewrite.

Instrumentation is an enabling stage, not a performance win. It must preserve
results and stay within the five-percent continuous-metric guardrail. Each
optimization experiment must name exactly one primary target before it runs:

| Stage | Allowed primary target | Required evidence |
|---|---|---|
| Write scheduling | p95 write-gate wait for the targeted operation class | Lock wait/hold distributions and mixed-load run |
| Physical layout | Critical-query p95, lake file count/growth, or merge/maintenance p95 | One layout variable changed, identical logical dataset |
| Allocation/marshaling | Allocated bytes/op, allocation rate, or CPU time in the named path | Before/after profiles plus mixed-load run |
| Rollups | p95 latency or CPU for one named repeated typed query | Raw/rollup equivalence, lag, fallback, and provenance checks |

A candidate is retained only when the median primary target across at least
three comparable runs improves by 10 percent or more and no continuous
guardrail median regresses by more than 5 percent. Direction is normalized:
higher throughput is better; lower latency, CPU, memory, allocation, lag,
storage growth, file count, and lock time are better. Every continuous metric
the harness records is a guardrail; excluding one requires a written rationale
in the evidence before the run.

This comparison is made by reading the three-run medians, not by a program.
An analyzer that renders a verdict is only worth building once the protocol can
demonstrably resolve the difference it claims to measure — and the null screen
below shows it currently cannot. Encoding the rule in code before then would
dress noise as a decision.

The following are per-run, zero-tolerance correctness/SLO failures and are
never averaged away:

- unexpected dropped, duplicated, or silently unacknowledged rows;
- query-result or raw/rollup equivalence mismatch;
- catalog corruption, final flush failure, benchmark-driver failure, OOM,
  unexpected application error, dead process, or failed health check;
- breach of the existing 1500 ms mixed-query p95 release SLO;
- in the accelerated soak profile, any rollup age above 40 seconds or lake file
  count above the configured cap (800 by default);
- authentication, authorization, or tenant-isolation behavior change.

Local developer hardware may reject obviously poor candidates. Retain/reject
evidence must come from the same production-shaped Linux host class with fixed
CPU/memory limits, dataset, seed, configuration, warm-up, workload mix, and
duration. The accepted baseline and every candidate use three 30-minute mixed
runs. After all retained changes, the final stack also runs a two-hour Linux
soak.

### 2. Make benchmark runs self-describing

The benchmark driver emits a run manifest alongside the human report. It records
only what the driver can derive or was told about the workload:

- Git commit and dirty-worktree flag, Go version, and Fanout/bench build
  identifiers;
- OS, architecture, logical CPU count, GOMAXPROCS, and cgroup memory limit;
- workload seed, service/cardinality settings, rate, concurrency, query mix,
  and duration, plus a hash of those parameters identifying the dataset.

It deliberately does not record server configuration such as pool size, flush
cadence, or retention. `bench` cannot observe those, so a manifest field for
them would only restate what the operator typed and would state it with
misplaced confidence when the two disagree. Server settings belong in the run
log next to the evidence.

Manifests must not contain environment variables, endpoints, tokens, query text
containing customer data, or other secrets; a test asserts this. Decision
artifacts contain commands, manifests, summarized metrics, profiles, and
conclusions. Generated lake data, raw run logs, and secrets stay outside version
control — benchmark output is diagnostic and is not evidence merely because it
is large.

### 3. Instrument the existing write gate before changing scheduling

The raw shared mutex will first be wrapped by a small internal write-gate type
that preserves `sync.Mutex` acquisition semantics. Acquiring the gate records
wait time; releasing it records hold time. Operations use a fixed enum-like set
of labels rather than arbitrary strings:

- ingest spans, logs, and metrics;
- startup rollup skip-to-latest watermark initialization;
- service, endpoint, and edge rollups;
- adjacent-file merge;
- DuckLake maintenance.

The initial wrapper does not add priority, fairness, cancellation, parallel
catalog writes, or a new queue. It exists to expose
`fanout_write_gate_wait_seconds` and
`fanout_write_gate_hold_seconds` histograms by bounded operation label.
Merge/maintenance duration and result counters, rollup lag, and rollup
watermark/backlog indicators are added with similarly bounded labels. Metrics
must never use tenant IDs, service names, trace IDs, error messages, or query
text as labels.

Every catalog-writing path will be enumerated in a test-backed inventory. The
required acquisition order is:

1. acquire the shared write gate;
2. acquire a DuckDB connection;
3. begin the transaction or create the appender;
4. commit/close database resources;
5. release the write gate.

No path may wait for the gate while holding a connection or transaction. Tests
must prove at most one catalog-writing critical section is active and must
exercise failure/panic-safe release behavior.

Only after baseline data identifies harmful contention may scheduling change.
Candidate scheduling changes may shorten critical sections, coalesce compatible
work, or bound background work, but must preserve one catalog commit at a time,
the lock order above, bounded memory, shutdown flushing, and rollup progress.

### 4. Keep the query kernel concrete and product boundaries typed

DuckDB SQL, retry rules, `database/sql`, appenders, DuckLake procedures, and
physical-layout operations remain inside `internal/query` and the storage
writer path. The API, MCP, agent, dashboard, and alert packages continue to
depend on typed observability contracts.

The existing `Duck` type may remain the composition facade. Small collaborators
such as the write gate, maintenance runner, or rollup runner are extracted only
when they give a measurable/testability benefit. This change will not add a
generic `Engine`, `DataFrame`, or row-batch interface in anticipation of a
future backend.

Raw SQL compatibility remains intentionally database-specific. It does not
justify leaking `*sql.Rows` into higher product layers, nor does the typed
product boundary require replacing `database/sql` inside the kernel.

### 5. Treat physical layout as a compatibility-sensitive experiment

The shipped Zstandard compression, 256 MiB target file size, and
namespace/hour partitioning are the control. Sort order, partition shape,
target file size, and merge/compaction policy are separate variables and must
be tested separately against an identical logical dataset.

Before the first physical-layout experiment, review
`telemetry-storage-query`, `telemetry-ingestion`, and `operations`. If a
candidate changes restart semantics, backup/restore, downgrade readability,
logical query behavior, or an operator-visible setting, stop and create/update
a delta spec before implementing it.

A physical rewrite cannot be retained until this matrix passes:

| Scenario | Required result |
|---|---|
| Current binary opens current data | Control succeeds |
| Candidate binary opens current data | Same logical results |
| Candidate restarts after writing candidate layout | Same logical results |
| Candidate backup/restore after rewrite | Restore succeeds with same logical results |
| Current binary opens candidate-written data | Must succeed, or the change requires an explicit non-rollback migration spec and cannot proceed here |
| Raw and typed queries before/after rewrite | Results and ordering guarantees remain equivalent |
| Interrupted merge/maintenance followed by restart | Catalog remains healthy and queries succeed |

Every destructive rewrite uses a disposable copy of benchmark data first and a
verified backup/restore procedure. No automatic rewrite or migration is added
to startup in this change.

### 6. Use profiles to gate allocation work and Arrow

CPU, heap, allocation, mutex, and blocking profiles are captured under the
same mixed workload as the baseline. Ordinary Go changes—buffer ownership,
fewer exact-batch copies, reduced row scanning/marshaling, and reuse with clear
lifetime rules—are tested before adding a new columnar interchange dependency.

An Arrow experiment becomes eligible only when profiles attribute at least 15
percent of CPU time or allocated bytes in the targeted workload to a boundary
that Arrow can actually remove, and simpler Go/DuckDB changes have failed the
stage gate or cannot address that boundary. Arrow must remain internal, add no
runtime process or data-format contract, keep the one-binary artifact, and
independently pass the same 10-percent/5-percent acceptance gate. Otherwise it
is not added.

### 7. Preserve rollup semantics and add only evidence-backed shapes

Existing service, endpoint, and edge rollups retain their durable watermarks,
late-data lag, bounded catch-up behavior, continuation after per-rollup
failure, readiness checks, raw fallback, and provenance. Scheduling or SQL
changes require equivalence fixtures covering late arrivals, empty windows,
wide backlogs, restart, partial failure, and disabled/not-ready rollups.

Before adding a new rollup shape, review `telemetry-storage-query` and the
consumer capability spec. A new rollup is allowed only for a named repeated
typed query whose baseline shows material cost and whose raw equivalent remains
available. Any new user-visible result provenance, retention, freshness, or
fallback rule requires a delta spec first.

### 8. Keep security and operational surfaces stable

No authentication domain is merged or weakened. Ingest tokens, browser
sessions, MCP OAuth tokens, and metrics credentials remain separate. Benchmark
scripts may use isolated loopback ports and temporary data, but production
metrics exposure continues to follow existing authorization configuration.

New metrics are additive and bounded. Diagnostic endpoints and profiles must
not be made publicly reachable by this work. Logs and evidence artifacts redact
secrets and avoid telemetry payloads. Temporary benchmark data is removed after
the run and is never committed.

### 9. Migration, deployment, and rollback

Instrumentation is additive and ships independently once it passes tests. Its
cost is bounded by construction (task 1.6) rather than by an A/B screen, so
there is no separate overhead gate to clear. Each retained optimization is a
separate commit or
otherwise independently revertible unit. Deployment keeps the same single Go
binary and existing configuration defaults.

Behavior-neutral code changes roll back by deploying the previous binary.
Layout candidates stay disabled/unapplied outside disposable benchmark data
until the compatibility matrix passes. If a future accepted layout requires a
catalog or data migration, that work moves to a separate spec with explicit
backup, restart, rollout, and downgrade procedures.

The final release decision requires:

1. strict OpenSpec validation;
2. focused and full Go tests, race tests for touched concurrent paths, UI tests
   only if a UI contract is touched, and a clean production build;
3. three accepted 30-minute Linux mixed-load runs for the final stack;
4. one accepted two-hour Linux soak;
5. synchronized canonical specs/public docs if any checkpoint produced a
   behavioral change.

## Risks / Trade-offs

- **Instrumentation distorts the baseline.** Build the disabled control and
  enabled candidate from one source archive, record the shared source digest
  and link-time measurement mode, and reject or simplify instrumentation that
  exceeds the five-percent guardrail. A HEAD-versus-worktree comparison is
  diagnostic only because it cannot attribute a regression to measurement.
- **Endpoint heap gauges follow GC phase.** Retain the end-of-run heap gauge as
  diagnostic evidence, but use allocation rate and RSS as continuous screening
  guardrails; attribute heap growth from captured profiles rather than one
  terminal scrape.
- **Prometheus label growth consumes memory.** Use compile-time operation/result
  sets only; never label by tenant, service, query, path parameter, or error
  text.
- **A shared gate can hide starvation.** Measure wait and hold distributions by
  operation, retain the single-writer invariant, and test progress for ingest,
  rollups, merge, and maintenance under contention.
- **The harness can report changes it cannot measure.** Comparing the same
  binary against itself on Darwin produced a 19-percent CPU swing and a
  98 ms → 1055 ms p95 swing. Two causes: a coarse 20-bucket latency ladder that
  could not resolve anything finer than a doubling (since replaced with a 0.5%
  geometric ladder), and a shared developer machine. Before trusting any A/B,
  run the same-binary null comparison on that host and confirm the spread is
  well inside the effect you intend to detect. A protocol that has not passed
  its own null test produces numbers, not evidence.
- **Short benchmarks reward burst behavior.** Use fixed 30-minute decision runs
  and a two-hour soak; report warm-up separately and reject non-steady-state
  comparisons.
- **Physical rewrites can remove rollback.** Require disposable copies,
  backup/restore, restart, logical-equivalence checks, and previous-binary
  readability before retention.
- **Buffer reuse can corrupt asynchronous batches.** Preserve explicit ownership
  and lifetime rules and run race tests plus content-equivalence tests.
- **Rollups can be fast but stale or semantically incomplete.** Treat lag,
  watermark progress, raw fallback, late data, and provenance as correctness
  requirements rather than optional performance metrics.
- **Stage scope can grow without evidence.** Predeclare one target, one changed
  variable, and stop conditions. Failed candidates are documented and removed,
  not carried into the next stage.

## Open Questions

- The exact production-shaped Linux host class and resource limits must be
  recorded in the first accepted baseline manifest. Results from another host
  class are screening evidence only.
- Physical-layout and additional-rollup experiments remain conditional. If the
  preceding profiles show no relevant bottleneck or a spec checkpoint finds a
  contract change, the corresponding tasks conclude with a documented no-go
  decision rather than an implementation.
