# DuckDB 2 cache review — 2026-10-01

This report addresses the nine comments in [review 5383705777](https://github.com/labstack/fanout/pull/272#pullrequestreview-5383705777) at `cd916d5`. The [previous report](duckdb2-followups-2026-10-01.md) retains the admission-limited ingestion results and failed short saturation diagnostic. Neither fully built-cache reads nor short sweeps establish sustained cache stability. #277 remains open for encoding CPU cost. [#278](https://github.com/labstack/fanout/issues/278) tracks the remaining sustained read-capacity and peak-memory limits.

## Implementation

| Finding | Change |
|---|---|
| Cache worker lag | Separate completed-batch and analytical write gates, with two writer connections over disjoint tables; checkpointing holds both gates. Bind each signal once per pass, rather than one UNION branch per file. Derive candidates from newly aggregated trace parts without a third Parquet scan. |
| Retained trace-index rewrite | One candidate row per trace, incrementally upserted from new contributions; no global DELETE/reinsert on normal append. Temporary trace parts contain only the current pass. |
| Compaction markers disable the index | Transfer complete cached contributions before the namespace swap. Follow replacement lineage across multiple compaction levels. Retention and incomplete replacements repair only affected trace IDs. |
| Cache footprint | Remove full span-event and log-timestamp copies and the four scope-expanded trace indexes. Keep minute counts, scoped per-batch trace parts, and one candidate per trace. |
| Stale severity semantics | A schema/aggregation version atomically discards all private read tables on mismatch and rebuilds from format-3 Parquet. Matching versions preserve acknowledgements. The already-existing endpoint_rollup/watermark cleanup now has regression coverage. |
| Broad file lists | Use a glob at 64+ selected files when at least 80% of active signal files overlap; retain a captured-ID filename filter. Narrow selections retain explicit lists. Only relations actually referenced are bound. |
| Ingestion throughput interpretation | Keep #277 open. The 20→10ms admission change helps closed-loop acknowledgement latency; it does not remove CPU cost per row. |
| Warm read comparison | Explicitly label cache-current reads and add the five-minute mixed run with pending-batch samples. |

A worker pass admits at most 64 files and 64,000 new span/log rows, except that one oversized immutable file must be admitted to make progress. These are work-selection bounds, not a hard elapsed-time limit. Completed files and their aggregates/acknowledgements commit atomically. A pinned read sees active files and cache contributions from one transaction. Uncached format-3 files remain immediately visible through the authoritative Parquet path. Publication preparation releases the cache gate before waiting for readers to drain; prepared output markers are protected until publication finishes. No prior-format reader or schema conversion is introduced.

Mixed namespace/service traces use exact scoped batch parts. Single-scope traces use the one candidate row; a snapshot-local ambiguity probe avoids adding an unused batch-parts aggregation to scoped plans. Clipped event-time windows retain exact boundary reads; a complete file contained entirely within the window can contribute its cached minute counts even when the window clips that minute. Trace candidate CTEs are not materialized into a full temporary relation for every request. Ranking still scans candidates; this is not a claim that DuckDB provides an ordered top-one index.

## Environment and evidence

Native Linux ARM64, Go 1.27.1, pinned DuckDB `v2.0.0-alpha43763`, Docker limits 4 CPUs / 6 GiB except the explicitly labelled one-CPU ingestion runs. Benchmarks run sequentially. Same-host telemetrygen 0.157.0 and the benchmark scheduler consume part of the same CPU allowance. The nominal offered rate is a configured target; accepted Fanout counters measure the actual ingestion rate.

[Measured JSON](duckdb2-review-2026-10-01.json) records binary SHA256 values, samples, limits and failed iterations. Full local data/evidence are retained in `fanout-review272-cache-v6`, under `/runs/optimized`; the preceding failed gate remains in `fanout-review272-cache-v5`. These are development measurements of the worktree following `cd916d5`, not a production capacity guarantee.

## Five-minute mixed workload

The 300.154-second phase accepted **66,086 rows/s**, with Fanout using **3.05 CPU cores** on average. The scheduler completed **16.4/20 reads/s**, with **19 failed** and **1,099 shed** requests. Successful-client latency was **88.12ms p50 / 5,955.36ms p95 / 10,630.47ms p99**. **The sustained read-capacity gate fails.** Generator exits, dropped rows, restarts, and complete authoritative storage verification pass.

| Minute | Pending batches median / sampled max / last | Preceding failed iteration, last pending | Server handler p50 / p95 |
|---|---:|---:|---:|
| 1 | 7 / 40 / 10 | 9 | 32.87 / 203.27ms |
| 2 | 11 / 51 / 11 | 29 | 60.91 / 398.81ms |
| 3 | 12 / 18 / 18 | 293 | 84.08 / 2285.38ms |
| 4 | 16 / 29 / 14 | 871 | 2192.89 / 8187.41ms |
| 5 | 20.5 / 63 / 16 | 1690 | 4448.36 / 13594.37ms |

The cache stays near the incoming batch rate instead of accumulating 1,690 pending batches as the preceding iteration did. A manual cooldown scrape observed zero pending batches. Read latency nevertheless deteriorates late in the run: cache-current ranking/file scans, analytical work and publication scheduling still share finite resources. The gate failure is retained as a separate capacity limitation; cache backlog improvement does not establish that all overload problems are solved. The per-minute latency values above come from server request logs grouped by request start, not client latencies or completed-read rates. Requested two-second metric samples occasionally stall during overloaded scrapes; their actual timestamps and sample counts are retained.

Peak process RSS is **3.32 GiB**, with **0.98 GiB** at the end of cooldown. The preceding failed iteration peaked at **3.03 GiB**. This does not show lower peak process memory, even though the derived catalog is smaller. Different accepted datasets, cache lifecycles, concurrent queries and compaction prevent treating process RSS as cache size. Full logs and failed gates remain inspectable.

The later scoped ambiguity probe is measured separately by the fully built-cache fixture below. It does not change the unscoped sustained workload's query path; the JSON distinguishes the two measured binaries.


The fixed preset has a ten-second warmup, six generator processes (two per signal, eight workers each, 2,500 rows/s per worker), a full 300-second mixed phase, 1,000 rows/export, 20 open-loop reads/s, and a thirty-second cooldown. The unchanged reader scheduler uses 40 workers and a 40-item queue. The pending-batch gauge is sampled every two seconds. The review's shell driver used a different scheduling/in-flight limit, so the runs are not exact matched throughput comparisons.

## Retained-index append and read experiment

Five disjoint 1,000-span files are appended at each retained size, with one trace per two spans. Fixture creation is excluded. These synthetic caches exercise normal append only: no stale cleanup, compaction, publication wait, or concurrent workload. The pass must still bind/aggregate its new file and mutate the keyed index; constant-time behavior is not claimed.

| Retained spans / trace rows | Append median / max | Materialized / non-materialized candidate read median |
|---|---:|---:|
| 200k / 100k | 16.85 / 20.54ms | 4.91 / 4.83ms |
| 800k / 400k | 18.58 / 19.65ms | 7.18 / 5.17ms |
| 2M / 1M | 17.55 / 24.32ms | 13.04 / 8.88ms |
| 8M / 4M | 21.74 / 28.38ms | 43.96 / 26.39ms |

The synthetic read measures the top-one and touched-trace branches over the same candidate table. It does not include file capture, scoped ambiguity, dashboard serialization or concurrent writes. EXPLAIN shows no retained-candidate CTE materialization in the non-materialized form; the table scans remain. Maxima of five samples are not robust tail estimates.

## File binding

One thousand one-row log files span one thousand minutes. Each case warms once and measures ten requests. Counts agree for the captured data.

| Window | Explicit list median | Captured-ID glob median | Unguarded glob median | Selected files |
|---|---:|---:|---:|---:|
| 24h | 57.70ms | 57.63ms | 44.74ms | 1,000 |
| 10m | 2.34ms | 2.25ms (explicit list selected) | 39.95ms | 11 |

The broad statement shrinks from 139,610 to 42,760 bytes. The exact captured-ID glob is effectively flat with the explicit list in this fixture and still slower than an unguarded glob. An unguarded glob can admit new files published after snapshot capture; it is not substituted for the snapshot contract. A regression test publishes a new batch after capture and verifies exclusion, including a Unicode data path. Rollup-only queries no longer construct unused Parquet lists.

## Cache-current dashboard reads and footprint

| Window / method | Raw median / p95 | Completed median / p95 | Completed CPU ms / request |
|---|---:|---:|---:|
| 24h endpoints | 11.53 / 13.53ms | 17.49 / 18.32ms | 23.32 |
| 24h trace | 26.03 / 27.04ms | 20.72 / 22.16ms | 34.44 |
| 24h logs | 33.50 / 36.86ms | 14.14 / 16.06ms | 19.69 |
| 20m endpoints | 11.53 / 13.53ms | 17.49 / 18.32ms | 23.32 |
| 20m trace | 26.03 / 27.04ms | 20.72 / 22.16ms | 34.44 |
| 20m logs | 33.50 / 36.86ms | 14.14 / 16.06ms | 19.69 |

Using the same fixture and footprint instrumentation, the review head's catalog is **180,891,648 bytes** versus **70,004,736 bytes** now (**61.3% smaller**). Parquet is **52,287,828 bytes** for both, and the empty catalog is **274,432 bytes**. The new catalog is still **1.34×** the highly compressible Parquet fixture; caches are not free. Backfill changes from **3.355s to 1.856s**. This is one trial, not a universal storage ratio.

The persisted-cache experiment caught a transient scoped-trace regression (**62.39ms** broad median): an unnecessary mixed-scope batch-parts branch was duplicated by the non-materialized plan even when no ambiguity existed. The snapshot-local ambiguity probe removes that branch in those scopes; the final broad trace median is **17.46ms**, versus **17.93ms** at the review head. The regression diagnostic is retained in JSON. Mixed scopes still use exact active parts.


The fixture contains 1.2 million spans and 1.2 million logs in twelve hourly batches plus 300 old tiny log/metric files. Both paths read identical format-3 data; caches are completely built, no background worker or ingest runs. Ten requests follow one warmup per case. Endpoint production reads use the established histogram estimate rather than the raw kernel's approx_quantile. Narrow endpoint planning cost remains an explicit limitation. Catalog sizes are measured after CHECKPOINT and include the empty cache/catalog baseline; they are not peak memory measurements. The sustained run supplies process RSS.

## CPU-limited ingestion

| Median metric | #270 | Current typed writer |
|---|---:|---:|
| Durable acknowledged rows/s | 414,700 | 321,598 |
| CPU seconds / million rows | 2.405 | 3.115 |
| Go allocated bytes / row | 3,746 | 3,943 |
| Export p50 (ms) | 35.20 | 43.51 |
| Export p95 (ms) | 64.57 | 76.85 |

All six runs have zero export errors and pass storage verification. With CPU saturated at approximately one core, current throughput is **22.5% lower**, normalized CPU cost **29.5% higher**, and Go allocation/row **5.3% higher**. This independently confirms the review's direction. **#277 remains open; the encoding regression is not resolved.** The earlier four-CPU +19% throughput result remains labelled admission-limited in the historical report.


The baseline is #270 merge `30ba9b9ba8a4217cc73637760d7acdcd5f89f15a`, with a measurement-only profiling overlay. The current writer is unchanged by this cache follow-up. Three alternating baseline/current repetitions use one CPU / 6 GiB, durable mode, 16 in-process clients, equal signals, 1,000 rows/export, and ten-second measurement intervals. No dashboard/cache worker runs. CPU profiles cover the interval; heap profiles cover the whole experiment. Every run verifies authoritative storage. These single-host fixture medians do not prove broader capacity.

## Reproduce and validation

Run opt-in fixtures alone with the pinned wrapper:

```sh
bash scripts/with-duckdb.sh go test -tags=filebench ./internal/query \
  -run 'TestSnapshotFileBindingBenchmark|TestReadCacheAppendScalingBenchmark' -v -count=1
FANOUT_READ_BENCH_LARGE=1 bash scripts/with-duckdb.sh go test -tags=readbench \
  ./internal/observability -run TestCompletedReadBenchmark -v -count=1
FANOUT_BENCH_PROFILE_DIR=/tmp/fanout-profiles \
  bash scripts/with-duckdb.sh go test -tags=transportbench ./internal/ingest \
  -run 'TestTransportComparison/durable=true/c=16/rep=1/shared' -v -count=1
```

For CPU-capped measurements, compile native Linux test binaries with `go test -c` through the wrapper, then execute them in otherwise idle `--cpus 4 --memory 6g` containers (one CPU for the CPU-limited ingestion comparison).

For the sustained test, copy `fanout-bench` at `a9843c1fd55b9465d4d77a42fcf8805c467af731` into a temporary workspace and apply [the complete local harness patch](duckdb2-review-2026-10-01-harness.patch) with `git apply --unidiff-zero`. The patch adapts current Fanout addresses/metrics, adds source identity, installs the fixed preset, and records the gauge. Build the harness and Fanout for native Linux, then run `fanout-bench managed --fanout /tools/fanout --telemetrygen /tools/telemetrygen --preset review272 --query-rate 20 --sample-interval 2s --cooldown 30s --output /runs/optimized/evidence` in a 4-CPU/6-GiB container. Sibling repositories are not modified.

Local validation passes: `just check`, the full `just test-race` suite with the documented CGO checkptr workaround, and the full native Linux ARM64 Go suite. New regression coverage exercises schema/semantic rebuild and matching-version preservation, original severity labels, captured-glob isolation with Unicode and relative paths, protected publication preparation and cancellation, cache progress during a disjoint analytical transaction, row-budget/oversized-file progress, two-level compaction lineage, retention scope/error repair, rollback and public-SQL cache rejection. Existing cold/warm, clipped-window, late-publication, trace-ranking and typed-data parity tests continue to pass.
