# Measurement screening evidence

## Status

The measurement-only instrumentation is accepted, on the arithmetic recorded
under "Overhead" below rather than on an A/B screen. Local Darwin/arm64
screening could not resolve the required five-percent guardrail, and the first
Linux screen compared HEAD with a worktree containing both instrumentation and
other implementation changes. The link-time A/B that was to replace them was
removed instead: the "disabled" control was not disabled enough for the
comparison to mean anything. Task 1.6 is closed on that basis.

Generated JSON reports, logs, binaries, and disposable lake data were kept
outside version control under `/tmp`, summarized here, and removed after the
run. They used synthetic data and placeholder-only local configuration; no
credentials are recorded here.

## Harness findings and corrections

- The benchmark command is `cmd/bench`; manifests identify the benchmark driver
  and Fanout build independently.
- The mixed-query histogram includes the exact 1500 ms release-SLO boundary and
  uses bounded 0.5-percent geometric buckets. It replaces a 20-bucket ladder
  whose adjacent 1000/2000 ms boundaries made p95 unresolvable below a doubling.
  The ladder grew to roughly 2,200 buckets; recording stays cheap because it
  uses binary search rather than a linear scan.
- The run manifest records build/VCS identity, host shape, cgroup memory limit,
  and workload parameters. It deliberately omits server configuration such as
  pool size, flush cadence, and retention: `bench` cannot observe those, so
  recording them would restate the operator's intent as if it were measurement.
  Server settings belong in the run log beside the evidence.
- Runs now fail rather than report a plausible number when they produced no
  usable evidence: no successful exports, no queries when query load was
  configured, a missing metrics baseline, or a server counter that went
  backwards mid-run.
- The terminal `go_memstats_heap_alloc_bytes` scrape remains in raw reports for
  diagnosis but is no longer a continuous guardrail because its value depends
  on GC phase. RSS and allocation rate remain enforced; heap attribution
  requires a profile.

## First Linux screen (rejected methodology)

The CPX32 Linux screen completed three five-minute control trials and three
five-minute candidate trials at 1000 target traces/s plus five mixed queries/s.
All individual trials passed correctness and the release SLO with zero send
errors, query errors, and dropped rows. The comparison still failed.

Selected medians were:

| Metric | HEAD | Worktree | Change |
|---|---:|---:|---:|
| Throughput, traces/s | 998.51 | 999.59 | +0.11% |
| Aggregate query mean, ms | 111.14 | 88.45 | +20.42% |
| Aggregate query p95, ms | 271.37 | 196.85 | +27.46% |
| CPU, cores | 0.9466 | 0.9823 | -3.77% |
| RSS, bytes | 644,055,040 | 618,754,048 | +3.93% |
| Allocation rate, bytes/s | 24,912,846 | 24,964,022 | -0.21% |
| Terminal heap gauge, bytes | 72,146,032 | 81,901,120 | -13.52% |
| Lake growth, bytes/s | 132,025 | 131,377 | +0.49% |

The screen also reported repeated 5.0625-percent histogram-boundary failures,
a 28.01-percent topology p99 regression, and an 88.68-percent server-side query
mean regression. It cannot measure instrumentation overhead: HEAD and the
worktree differ in behavior-neutral implementation and harness code as well as
measurement. It is rejected as acceptance evidence, and its raw output was not
retained.

## Same-source short Linux smoke

After correcting the methodology, a CPX32 smoke used one source archive
(`1dc63dd20f1cb381b153c71312b669a263839e029b7617c56c90c7e893a9856c`)
to build a link-time-disabled control and enabled candidate. Each suite used a
five-second warm-up and three 15-second trials. This duration is intentionally
too short for a performance verdict.

The functional criteria passed:

- both suites passed every individual correctness and release-SLO check;
- neither suite recorded send errors, query errors, or dropped rows;
- manifests recorded the same source digest and the expected disabled/enabled
  modes, with no comparability failures;
- the disabled control emitted zero write-gate distribution series while the
  enabled candidate emitted eight, proving the link-time switch changes only
  the intended measurement path.

Applying the acceptance rule failed noisy short-run query and CPU guardrails,
including a 10.79-percent CPU result and query-tail changes based on very few
requests.
Those values are not acceptance evidence; the smoke validates A/B plumbing only.
Its raw output was not retained.

## Instrumented-versus-HEAD screen

The production-cadence local screen used a separate warm-up and three 60-second
measured trials per build at 3000 target traces/s, five target mixed queries/s,
30 services, seed 42, and DuckDB max connections 4. Both suites passed all
per-run correctness and release-SLO checks with zero measured send/query errors
and zero dropped rows.

The acceptance rule rejected the comparison. Selected median changes were:

| Metric | HEAD | Instrumented | Change |
|---|---:|---:|---:|
| Throughput, traces/s | 2938.69 | 2962.36 | +0.81% |
| Aggregate query mean, ms | 111.04 | 116.65 | -5.05% |
| Aggregate query p95, ms | 1055.28 | 1055.28 | 0.00% |
| CPU, cores | 1.0355 | 1.1604 | -12.06% |
| RSS, bytes | 1,434,320,896 | 1,602,813,952 | -11.75% |
| Allocation rate, bytes/s | 73,059,993 | 73,712,472 | -0.89% |
| Heap allocation, bytes | 100,241,760 | 100,964,112 | -0.72% |
| Lake growth, bytes/s | 377,300 | 373,995 | +0.88% |

These results alone would reject or simplify the instrumentation, but the null
test below shows that this local protocol has a much larger noise floor.

## Identical-binary null screen

The same HEAD binary was then compared with itself using the same driver,
server/workload settings, fresh data directories, warm-up, and three-run
protocol. Every individual run again passed correctness and the release SLO,
yet the nominally identical candidate would have been rejected.

This is the most useful result in this document, and it is a finding about the
harness, not about Fanout.

Selected median changes were:

| Metric | HEAD A | HEAD B | Apparent change |
|---|---:|---:|---:|
| Throughput, traces/s | 2974.70 | 2970.64 | -0.14% |
| Aggregate query mean, ms | 76.93 | 101.82 | -32.35% |
| Aggregate query p95, ms | 98.60 | 1055.28 | -970.26% |
| CPU, cores | 1.0165 | 1.2120 | -19.23% |
| RSS, bytes | 1,449,738,240 | 1,666,613,248 | -14.96% |
| Allocation rate, bytes/s | 74,033,666 | 73,972,998 | +0.08% |
| Heap allocation, bytes | 68,772,952 | 76,352,840 | -11.02% |
| Lake growth, bytes/s | 378,584 | 380,545 | -0.52% |

## Decision

The Darwin sequential screen is suitable for catching correctness failures and
large throughput/allocation/storage regressions, but it is not suitable for a
five-percent retain/reject decision on CPU, RSS, heap, or query latency. Do not
use it to accept, reject, or tune the instrumentation.

Part of that noise floor was the harness itself: the p95 column above moves
between 98.60 ms and 1055.28 ms because the old 20-bucket latency ladder had
adjacent boundaries at 1000 ms and 2000 ms, so p95 could only ever land on one
of them. The 0.5-percent geometric ladder removes that particular artifact. The
CPU and RSS spread is a shared developer machine and is not fixable in code.

The A/B screen was abandoned rather than repeated on a quieter host, because
the protocol could not have answered the question even on a silent machine. The
link-time "disabled" control still constructed, registered, scraped, and
serialized every metric vec; it still ran the label validation on every catalog
write; and it still allocated the same release closure. The only thing it
skipped was two `time.Now()` reads and two `Observe()` calls per catalog write (the third clock read, `time.Since` in the release closure, ran in both builds).
Two binaries that similar cannot produce a meaningful delta, and a sub-noise
result would have been recorded as "instrumentation is free" on the strength of
a comparison that was never capable of showing otherwise. Run order compounded
it: the control always ran first, sequentially, on one VM.

Adopt this as the general rule: a benchmarking protocol has to pass its own null
test before its output counts as evidence.

## Overhead

Bounded by construction rather than by a screen. Flushes batch at
`FLUSH_BATCH_SIZE` (50,000 rows by default), so the catalog write gate is
entered a few times per second, not per row. Each entry is a DuckDB transaction
doing disk I/O while holding a process-wide mutex — milliseconds. Against that,
the instrumentation adds three clock reads and two Prometheus histogram
observations, and the observations happen after the mutex is released, so they
do not extend the critical section at all.

Nanoseconds against milliseconds, a few times per second, outside the lock. No
decision host is required to conclude this is well inside the five-percent
guardrail, and none could have measured it more convincingly than the
arithmetic does.

## Code validation

The implementation itself passes the available local validation:

- `go test -race ./internal/metrics ./internal/lake/writegate ./internal/lake ./internal/query ./cmd/bench`
- `go test ./...`
- `just check` (format, vet, golangci-lint, UI type checks/builds, and production binary build)
- `just docs-check` (all canonical specs and the active change validate strictly)
