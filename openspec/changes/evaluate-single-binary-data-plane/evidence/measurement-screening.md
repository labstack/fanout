# Measurement screening evidence

## Status

The measurement-only instrumentation is not yet accepted or rejected. Local
Darwin/arm64 screening could not resolve the required five-percent guardrail,
and the first Linux screen compared HEAD with a worktree containing both
instrumentation and other implementation changes. Task 1.6 therefore remains
open until one-source link-time-disabled/enabled builds are screened on the
isolated production-shaped Linux host.

Generated JSON reports, logs, binaries, and disposable lake data were kept
outside version control under `/tmp`, summarized here, and removed after the
run. They used synthetic data and placeholder-only local configuration; no
credentials are recorded here.

## Harness findings and corrections

- The benchmark command is `cmd/bench`; manifests identify the benchmark driver
  and Fanout build independently.
- The mixed-query histogram includes the exact 1500 ms release-SLO boundary,
  uses bounded 0.5-percent geometric buckets, and records with binary search.
  This reduces comparison quantization fivefold without increasing the hot-path
  search from linear to thousands of bucket checks.
- Screening manifests record the Fanout source-archive SHA-256 and link-time
  measurement mode. Analysis rejects screens unless the source digests match
  and the sole declared A/B variable is disabled control versus enabled
  candidate instrumentation.
- Run manifests now record flush interval/batch size, rollup cadence, merge and
  maintenance cadence, retention, and rollup-skip state in addition to DuckDB,
  host, build, workload, and dataset identity. Analysis rejects mismatches.
- Short screening exclusions were predeclared with rationales for sub-millisecond
  export mean, near-zero GC-pause totals, low-sample legacy rollup/flush means,
  compaction-phase partition gauges, and per-operation query means. Throughput,
  export/query quantiles, aggregate query mean, CPU, RSS, allocation rate,
  storage growth, errors, drops, and the release SLO remained enforced.
- The terminal `go_memstats_heap_alloc_bytes` scrape remains in raw reports for
  diagnosis but is no longer a continuous guardrail because its value depends
  on GC phase. RSS and allocation rate remain enforced; heap attribution
  requires a profile.

## First Linux screen (rejected methodology)

The CPX32 Linux screen completed three five-minute control trials and three
five-minute candidate trials at 1000 target traces/s plus five mixed queries/s.
All individual trials passed correctness and the release SLO with zero send
errors, query errors, and dropped rows. The machine verdict failed.

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
mean regression. The raw result is preserved under
`evidence/linux-screen-cpx32-20260802-v1/`, but it cannot measure instrumentation
overhead: HEAD and the worktree differ in behavior-neutral implementation and
harness code as well as measurement. It is rejected as acceptance evidence.

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

The analyzer failed noisy short-run query and CPU guardrails, including a
10.79-percent CPU result and query-tail changes based on very few requests.
Those values are not acceptance evidence. The raw smoke is preserved under
`evidence/linux-smoke-cpx32-20260802-v2/`; it validates A/B plumbing only.

## Instrumented-versus-HEAD screen

The production-cadence local screen used a separate warm-up and three 60-second
measured trials per build at 3000 target traces/s, five target mixed queries/s,
30 services, seed 42, and DuckDB max connections 4. Both suites passed all
per-run correctness and release-SLO checks with zero measured send/query errors
and zero dropped rows.

The analyzer rejected the comparison. Selected median changes were:

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
server/workload settings, exclusions, fresh data directories, warm-up, and
three-run protocol. Every individual run again passed correctness and the
release SLO, but the analyzer rejected the nominally identical candidate.

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
use it to accept, reject, or tune the instrumentation. Repeat the screen using
the same-source link-time A/B pair on the declared isolated Linux decision
host. Use short trials first; only run the full screen after the short
comparison is stable. A passing full result is required to complete task 1.6
and permit immutable baseline capture in task 1.7.

## Code validation

The implementation itself passes the available local validation:

- `go test -race ./internal/metrics ./internal/lake/writegate ./internal/lake ./internal/query ./cmd/bench`
- `go test ./...`
- `just check` (format, vet, golangci-lint, UI type checks/builds, and production binary build)
- `just docs-check` (all canonical specs and the active change validate strictly)
