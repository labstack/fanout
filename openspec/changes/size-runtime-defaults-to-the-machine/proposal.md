# Size runtime defaults to the machine

## Why

Fanout's default configuration can be killed by the kernel. Benchmarking on a
2 vCPU / 8 GB host, the process was OOM-killed at 7.5 GB RSS:

```
Out of memory: Killed process (fanout) anon-rss:7566436kB
```

`DUCKDB_MEMORY` is unset by default, which leaves DuckDB's own self-sizing in
place — 80% of detected memory, about 6.2 GB here. That budget is calculated as
though DuckDB owned the machine, but it is embedded in a Go process whose heap,
goroutine stacks, and GC headroom sit on top of it. 80% plus the runtime exceeds
100%, and the kernel resolves the difference.

This is not a tuning preference. The README's Docker quick start reproduces it
on any host where 80% of memory plus the Go runtime exceeds the box.

A second constant misses in both directions. `DUCKDB_MAX_CONNS` is fixed at 4
regardless of the machine: on two cores that is four concurrent queries
oversubscribing the CPU — measured, 5 queries/s cost 47% of ingest capacity —
and on a 32-core host it is an artificial ceiling on query concurrency.

Adaptive defaults have a cost of their own. Two machines silently run different
configurations, benchmarks stop being comparable, and "works on my machine"
becomes unfalsifiable. Fanout logs no resolved configuration at startup today,
so that cost would be paid blind. Logging comes first.

This change affects runtime behavior: every deployment that has not pinned these
variables will resolve different values than before. It adds resolved sizing to
the existing health response, but adds no data migration or security surface.

## What Changes

- Log the resolved runtime configuration at startup, so an adaptive default is
  still one an operator can read back, and expose the same non-secret values in
  health diagnostics.
- Size `DUCKDB_MEMORY` from detected memory, reserving headroom for the Go
  runtime, when the operator has not set it. Detection is cgroup-aware, because
  a container limit — not the host's total — is what the kernel enforces, and
  uses the native host-memory source on supported non-Linux platforms.
- Scale `DUCKDB_MAX_CONNS` with available cores, with a floor that keeps the
  write gate's invariant (`> 1`) intact.
- Leave `FLUSH_BATCH_SIZE` alone. It is a memory, latency, and Parquet
  file-size tradeoff rather than a parallelism one, and the measurement that
  would justify changing it — the effect of batch size on file count and query
  performance — has not been made.

Explicit environment variables continue to win over every resolved value.

## Capabilities

### New Capabilities

None. This changes what unset configuration resolves to.

### Modified Capabilities

None. No specs exist for the configuration surface yet.

## Impact

- **Affected**: `internal/env` (resolution and reporting), `internal/api`
  (additive health diagnostics), `cmd/fanout` startup.
- **Not affected**: the ingest, query, storage, and auth paths. Nothing changes
  about what Fanout does — only about what it reserves before doing it.
- **Benchmarks**: `docs/benchmarks/two-vcpu.md` ran with `DUCKDB_MEMORY=3GB` set
  by hand, precisely because the default was unsafe. Its reproduction steps
  should note that the default now resolves on its own.
