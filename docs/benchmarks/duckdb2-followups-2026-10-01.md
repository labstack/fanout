# DuckDB 2 review follow-ups — 2026-10-01

This report retains the measurements before the nine-comment review at
`cd916d5`. They are historical results, not measurements of the subsequent
aggregate-only cache. See [the subsequent nine-comment review report](duckdb2-review-2026-10-01.md). #277 remains open for the encoding CPU regression.
These results do not establish general production capacity.
The complete measured values and binary identities are in
[the accompanying JSON](duckdb2-followups-2026-10-01.json).

## Changes measured

| Finding | Implementation |
|---|---|
| #273: file binding | Explicit immutable-file lists from actual span, log, and metric event-time footer bounds; unknown statistics include the file; newest event bounds first. |
| #274: endpoint tail | Completed-batch minute histograms plus exact partial minutes and uncached active files, without the five-minute endpoint watermark. |
| #275: notable traces | Exact candidate index for all namespace/service scope combinations; merge uncached contributions and recompute partial-window traces. |
| #276: log histogram | Scoped severity minute counts and exact boundary counts; text searches retain redacted-body matching. |
| #277: ingest regression | Reuse scalar buffers, dispatch by column, encode shared canonical resources once with a bounded identity cache, and shorten group admission from 20 to 10ms. |

All derived contributions and the completed-batch marker commit atomically.
Dashboard reads pin immutable files and use one DuckDB transaction for cache
markers and rows. Compaction/retention invalidate retired contributions.
Narrow scalar projections answer partial windows without decoding attributes or
bodies. They consume additional rebuildable DuckDB storage. The cache worker
yields between bounded transactions and is independent of analytical rollup lag.
Format 3 and its physical schema are unchanged. No earlier-format reader exists.

## Environment and method

Native Linux ARM64, Go 1.27.1, pinned DuckDB `v2.0.0-alpha43763`, Docker limit
4 CPUs / 6 GiB. No concurrent benchmark containers. The baseline is the #270
merge, `30ba9b9ba8a4217cc73637760d7acdcd5f89f15a`.

The durable ingestion experiment uses 16 concurrent clients, 1,000 rows/export,
equal spans/logs/metrics, and no dashboard/background query work. Client and
server share the measured process. Three baseline and three final ten-second
measurement intervals alternate in the order baseline/final/final/baseline/
baseline/final. CPU profiles cover the measurement intervals; post-load heap
profiles cover the whole experiment and must not be treated as interval-only
allocations. Every run waits for durable publication and then verifies storage.

| Median metric | #270 | Final DuckDB 2 |
|---|---:|---:|
| Durable acknowledged rows/s | 493,050 | 586,813 |
| Go allocated bytes/row | 3,828 | 4,018 |
| Client + server CPU cores | 1.342 | 2.017 |
| CPU seconds per million rows | 2.722 | 3.437 |
| Export p50 | 32.27ms | 26.79ms |
| Export p95 | 35.68ms | 31.75ms |

All six runs have zero export errors and pass durable storage verification.
Throughput increases **19%**, and p95 decreases **11%**. This closed-loop fixture is admission-limited: halving the admission window
accounts for most of the throughput gain. It does not demonstrate that the
encoding regression is resolved. Typed ingestion still costs
**26% more CPU per row** and **5% more Go allocation per row** than #270.
The admission change contributes to the throughput gain; this comparison does
not establish faster VARIANT encoding. Independent fixed-one-CPU trials from
the review measured 395k/373k rows/s for #270 versus 322k/298k for `cd916d5`,
about 19% slower. Keep #277 open; reducing acknowledgement latency does not
remove the normalized CPU regression.

## Identical-file dashboard comparison

The opt-in read fixture has 1.2 million spans and 1.2 million logs across twelve
hourly batches, plus 300 old tiny log/metric batches. Both paths read the same
format-3 files. The raw path runs the existing query kernels; the completed path
runs the actual production dashboard methods after cache backfill (3.59s).
No compaction or background rollup runs during measurement. Each case has a warmup
and ten measured requests. These p95 values are the maximum of ten samples, so
they are not a robust estimate of production tail latency.

| Window / method | Raw median / p95 | Completed median / p95 | Raw / completed CPU ms per request |
|---|---:|---:|---:|
| 24h endpoints | 47.13 / 50.55ms | 32.38 / 35.04ms | 165.2 / 49.1 |
| 24h notable trace | 60.81 / 66.32ms | 20.04 / 31.82ms | 189.1 / 42.2 |
| 24h logs | 61.01 / 63.73ms | 18.21 / 19.20ms | 193.1 / 26.7 |
| 20m endpoints | 12.84 / 15.07ms | 23.98 / 25.48ms | 23.1 / 36.6 |
| 20m notable trace | 30.05 / 30.70ms | 29.03 / 30.26ms | 72.0 / 50.8 |
| 20m logs | 39.63 / 42.29ms | 15.44 / 17.12ms | 106.1 / 21.0 |

Both windows end one second after the fixture's hour boundary and exercise exact
partial minutes. Broad reads improve with the caches fully built. This fixture measures
cache-current reads only; it contains no concurrent ingest or cache lag. Small endpoint windows still
pay additional histogram/union planning overhead, and narrow trace latency is
approximately flat. No claim of uniform latency improvement is made. Endpoints use
the established fixed-boundary histogram estimate rather than the raw kernel's
`approx_quantile`; cold and completed production reads use the same estimator.
The JSON also retains the prior identical-file trial; both show broad gains
and extra planning cost for narrow endpoint windows.

## Mixed-load saturation diagnostic

The unchanged adapted `../fanout-bench` harness at
`a9843c1fd55b9465d4d77a42fcf8805c467af731` runs the standard sweep with
telemetrygen 0.157.0 on the same host, 20 requested reads/s, fifteen-second sweep
phases, sixty-second sustained load, and fifteen-second cooldown. Sibling
repositories were not changed. The native server binary SHA256 is recorded in
JSON. Data and full logs are retained locally in the `fanout-review272-final`
Docker volume, under `/optimized`.

| Diagnostic | Final result |
|---|---:|
| Mixed p1 accepted rows/s / completed reads/s | 34,025 / 20.0 |
| Mixed p2 accepted rows/s / completed reads/s | 57,542 / 20.2 |
| Mixed p8 accepted rows/s / completed reads/s | 141,715 / 9.9 |
| Sustained offered / accepted rows/s | 120,000 / 58,963 |
| Sustained completed reads/s / p95 | 6.5 / 12.47s |
| Sustained failed / shed reads | 47 / 768 |
| Cooldown RSS | 1.48 GiB |

The p1 and p2 sweeps have zero failed or shed reads. The standard harness chooses
its sustained phase adaptively: this build selects **120,000** offered rows/s,
where the historical #270 and pre-follow-up runs selected **60,000**. Sustained
results therefore are **not a matched comparison**. Mixed p8 offered settings
match the historical baseline, which accepted 145,941 rows/s; the final
141,715 is within 3% in these single trials, with different accepted datasets.

**The sustained 20-read/s capacity gate fails.** This is a failed saturation
diagnostic, not a publishable capacity result. Generator exits, server-dropped
rows, server restarts, and full authoritative storage verification all pass.
Same-host generators, adaptive sustained selection, short sweeps, and one trial
limit interpretation. CPU per row, narrow endpoint planning cost, and saturated
read capacity remain measured limitations. The independent review reproduced
cache backlog growth from 51 to 843 batches and read shedding under sustained
load; short sweeps and fully built-cache reads do not establish cache stability.

## Reproduce

On native Linux or macOS, build with the pinned wrapper:

```sh
bash scripts/with-duckdb.sh go test -tags=transportbench ./internal/ingest \
  -run 'TestTransportComparison/durable=true/c=16/rep=1/shared' -v -count=1
FANOUT_BENCH_PROFILE_DIR=/tmp/fanout-profiles \
  bash scripts/with-duckdb.sh go test -tags=transportbench ./internal/ingest \
  -run 'TestTransportComparison/durable=true/c=16/rep=1/shared' -v -count=1
FANOUT_READ_BENCH_LARGE=1 \
  bash scripts/with-duckdb.sh go test -tags=readbench ./internal/observability \
  -run TestCompletedReadBenchmark -v -count=1
```

Run one benchmark at a time. For the reported container limits, compile native
Linux test binaries with `go test -c` through the wrapper, then run each binary
in an otherwise idle `--cpus 4 --memory 6g` container. Repeat the ingestion
selector for repetitions 1, 2, and 3 on both builds. The baseline profiling overlay
changes only measurement collection, not the #270 writer.

Local validation covers cold/warm parity, late publications, partial minutes,
trace scopes and ranking, duplicate log timestamps, compaction/retention,
transaction failure rollback, prompt worker draining and cancellation, public
SQL rejection of private caches, optional footer statistics and signed times,
and mixed shared/unique/null resources across chunks and row groups. The normal
`just check` gate and targeted query/observability/telemetry/storage race suite
pass; native Linux tests exercise the same pinned engine.
