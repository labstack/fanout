# Design

## Context

`env.Load` parses the environment into `Config` and validates it. Nothing
between those two steps looks at the machine, and nothing afterwards reports
what was decided. The only core-aware value anywhere in the server is the gRPC
stream-worker pool added alongside the ingest profiling work.

The failure this change fixes is specific: DuckDB's self-sizing reads total
memory and claims 80% of it, unaware that it is a library inside a Go process.
Measured on a 2 vCPU / 8 GB host, DuckDB's budget resolved to ~6.2 GB and the
process reached 7.5 GB RSS before the kernel killed it. Go's own footprint in
that run was roughly 1.3 GB.

## Goals / Non-Goals

**Goals**

- A default configuration that does not get OOM-killed on a small machine.
- Resolution that is observable: an operator can read what was chosen from
  startup logs or health diagnostics.
- Container-correct detection, since a cgroup limit is what actually binds.

**Non-Goals**

- Tuning for throughput. This is about not dying, not about going faster.
- Changing `FLUSH_BATCH_SIZE`. The appender benchmark shows batch size matters,
  but not how it trades against Parquet file count and query latency.
- Auto-tuning at runtime. Resolution happens once, at startup, from values that
  do not change while the process runs.

## Decisions

**Reserve 40% of detected memory, give DuckDB 60%.** DuckDB's own default of
80% is the number that failed, with ~1.3 GB of Go runtime alongside it. 60%
leaves headroom that scales with the machine rather than a fixed constant that
is generous on a large host and useless on a small one. The rule is arbitrary in
the way any safety margin is arbitrary; it is defensible because it is stated,
logged, and overridable.

**Detect memory from cgroups first, then the host.** A container limit is what
the kernel enforces, and reading the host's total inside a limited container is
exactly the mistake being fixed. Linux resolves the process cgroup from procfs,
walks its ancestors for the tightest v1/v2 limit, and compares that limit with
`/proc/meminfo`. macOS reads `hw.memsize`. A sentinel "max" value means
unlimited and falls through to the host source.

**Do nothing when detection fails.** If no source yields a figure — an
unrecognized platform, an unreadable cgroup — leave `DUCKDB_MEMORY` empty and
DuckDB's existing behavior in place. Guessing a cap for a machine whose size is
unknown risks capping a large host at a small number, which converts a
hypothetical crash into a certain slowdown.

**Scale the connection pool with cores, floor of 2.** `DUCKDB_MAX_CONNS` must
stay above 1 or the lake writer refuses to start without a write gate, so the
floor is an invariant rather than a preference. The ceiling exists because
connections past the point of CPU saturation add contention, not concurrency.

**Signal auto-sizing with 0, not with an absent variable.** `DUCKDB_THREADS`
already uses 0 to mean "leave DuckDB's default", so 0 is the established
spelling for "decide for me". Checking `os.LookupEnv` instead would make the
struct tag's stated default a lie.

**Report the resolved values in logs and health diagnostics.** Without this, an
adaptive default is unfalsifiable in support: nobody can say what the machine
chose. Startup emits one structured log; `/readyz` and `/api/health` return a
non-secret `runtime_sizing` block for later inspection.

## Risks / Trade-offs

- **Existing deployments change behavior on upgrade.** Anyone relying on the
  implicit 4-connection pool or DuckDB's 80% gets different values. The startup
  log is what makes that discoverable rather than mysterious.
- **60% may be conservative on a large host.** A 64 GB machine reserves 25 GB it
  probably does not need. Explicit configuration remains the answer for anyone
  who has measured their own workload.
- **Published benchmarks were run with the value pinned by hand.** They remain
  valid for what they measured, but their reproduction steps now describe a
  configuration the software would have chosen anyway.
- **Container detection is Linux-specific.** macOS can size from physical host
  memory but has no cgroup concept. Unsupported platforms fall through to no
  decision, which is the safe direction.

## Migration Plan

None required. Resolution happens at startup, changes no stored data, and is
overridden by setting the variables explicitly. Rolling back means pinning
`DUCKDB_MEMORY` and `DUCKDB_MAX_CONNS` to their previous effective values, both
of which the new startup log reports.
