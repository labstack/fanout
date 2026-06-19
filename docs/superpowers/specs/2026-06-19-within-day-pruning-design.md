# Within-Day Pruning via Hour Partitioning — Design

**Date:** 2026-06-19
**Status:** Approved (design)
**Author:** v@labstack.com (with Claude)

## Problem

fanout's query-latency-under-load is gated by how much data the rollup and the
Overview error queries scan, and that scan grows with a UTC day's accumulated
data. The throughput benchmark proved it: on cpx32, as `day=19` filled, the
rollup climbed 1.2s → 6.6s → 13s and Overview query p95 hit 5s (>1.5s SLO),
while ingest stayed trivial (drops=0, export p95 ~8ms). The soak never certified
a rated capacity.

Root cause: the lake is partitioned by **`(namespace, day(start_time))`**, so a
recent-window query (`start_time >= now() - N min`) prunes only to the *whole
day* and then scans all of it. `merge_adjacent_files` consolidates a day's
parquet into a few large files whose per-file `start_time`/`ingested_unix_nano`
zonemaps span the entire day — so there is no within-day pruning (a 4-file day
still scanned 13s of data). The already-shipped fixes (snapshot-expiry grace,
read-retry on the file race, rollup scan-bound, decoupled frequent merge) bound
file *count* and stop the race, but none reduce the *data volume* a recent-window
scan reads within a day.

The three within-day scanners:
- **Overview error queries** (`internal/service/overview.go:320,414`) —
  `spans WHERE start_time >= now() - INTERVAL N MINUTE`.
- **Rollup agg scans** (`serviceRollupInsertSQL` / `edgeRollupInsertSQL`) — carry
  a `start_time` bound to the affected bucket range.
- **Rollup `affected` CTE** (`internal/query/duck.go:640+`) — filters
  `ingested_unix_nano` (NOT the partition key); prunes only via per-file
  zonemaps, which merge widens.

## Goal

Make recent-window scans prune *within* a day so query p95 holds < 1.5s under
sustained load through a full UTC day, and the benchmark soak certifies a rated
capacity. Ingest is already fine and must stay fine (no regression: drops=0,
ms-level export).

## Approach: hour-level partitioning

Repartition the lake tables from `(namespace, day(ts))` to
**`(namespace, hour(ts))`**, where `hour` is DuckLake's **date-inclusive** hour
transform (hours-since-epoch, like Iceberg — it uniquely identifies the hour, so
no separate `day` term is needed). Tables/columns:

- `lake.spans` → `(namespace, hour(start_time))`
- `lake.logs` → `(namespace, hour(log_time))`
- `lake.metrics` → `(namespace, hour(metric_time))`

**Mechanism (stated precisely — this is file-level zonemap pruning, NOT partition
pruning):** DuckLake prunes by per-file `start_time` min/max stats
(`ducklake_file_column_stats`). `hour()` is an order-preserving transform whose
only job is to physically segregate rows into per-hour files so those zonemaps
stay ~1 hour wide. A range predicate (`start_time >= now() - INTERVAL N MINUTE`)
then prunes any file whose `start_time` max is below the bound — range pruning is
inherent to min/max, so there is no Iceberg-style "ranges don't prune on a
transform" hazard. Today's day-wide files can't exclude anything within the day;
hour-wide files can.

**Why this fixes all three scanners with almost no query changes:**
- Overview error queries and rollup agg scans already filter `start_time`, so
  their files prune to ≈ the **current 1–2 hours' files** (~1/24 of a day).
- The rollup `affected` CTE (ingested-time filter, no `start_time` bound) prunes
  **indirectly and conditionally**: files written/merged in earlier hours have an
  `ingested_unix_nano` max below the rollup watermark, so they prune out — leaving
  ≈ the current hour's files. **This holds only while old hour partitions are not
  backfilled.** A late-arriving span (old `start_time`, fresh `ingested_unix_nano`)
  lands in an *old* hour partition and re-widens that partition's newest file's
  ingested zonemap up to "now", so it stops pruning for subsequent rollup passes
  (same trade-off already documented for the span_agg scan at duck.go ~711-719).
  Steady state: the affected scan reads ≈ current-hour ingested files (small).
  Heavy late data: partial reversion. The benchmark MUST measure the affected
  scan cost, not assume it is free.

The partition key does the work; the query/rollup SQL is essentially unchanged.

## Components

1. **`internal/query/views.go::configureDuckLake`** — change the three
   `SET PARTITIONED BY` statements to the `hour(...)` transform. Precede with a
   one-time **capability check**: confirm the bundled DuckLake accepts the `hour`
   transform and that it is date-inclusive. If `hour()` is hour-of-day (0–23),
   fall back to `(namespace, day(ts), hour(ts))` so the partition is still unique
   per calendar hour. This check is implementation step 1.
2. **No changes** to the Overview queries or rollup SQL — their existing
   `start_time` filters auto-prune to hour partitions. (Do NOT add a tight
   `start_time` bound to the `affected` CTE: it must keep catching late-arriving
   rows by ingested time regardless of their start_time.)
3. **Keep** all shipped fixes — `runMerge` (now per hour partition), expiry
   grace, read-retry, rollup scan bounds — they remain correct and complementary.
4. **Benchmark** (`scripts/bench-throughput.sh`) — add a mode that spans multiple
   hours so within-day pruning is actually exercised. A 15-min soak stays inside
   one hour partition and would NOT test the fix. Options: (a) pre-seed several
   hours of backdated data before the soak, or (b) backdate ingest timestamps so
   the run crosses hour boundaries. Pre-seed is preferred (deterministic). This
   bench change is part of the work, not optional.

## Data flow

Unchanged at ingest: the Appender writes rows; DuckLake routes each to its hour
partition by the timestamp column. Reads prune by hour partition automatically
from the existing `start_time` predicates.

## Migration

Pre-release, no back-compat (see [[no_backcompat_prerelease]]): no migration
path. `ALTER ... SET PARTITIONED BY` governs new writes; existing data is wiped.
On a deployed instance this is a one-time `/data` wipe, documented in the change.
`configureDuckLake` runs the new `SET PARTITIONED BY` on boot.

## Verification gate (local experiment — BEFORE building or any paid run)

A few hours of local work that de-risks the two Critical assumptions for far less
than discovering them in a paid cloud soak. Build a local DuckLake with the
bundled duckdb-go v2.10503.1, `SET PARTITIONED BY (namespace, hour(start_time))`,
then:

1. Write ~2 hours of synthetic spans **through the real Appender path** (enough
   that one hour exceeds the 256MB target so merge actively runs). Run
   `ducklake_merge_adjacent_files`.
2. **Gate C1 — merge stays within an hour:** query `ducklake_data_file` /
   `ducklake_file_column_stats` and assert **no merged file's `start_time`
   min/max straddles an hour boundary**. If merge crosses hours, zonemaps
   re-widen and the design fails — stop and reconsider.
3. **Gate #1/#4 — range predicate prunes:** `EXPLAIN`/`EXPLAIN ANALYZE`
   `WHERE start_time >= now() - INTERVAL 15 MINUTE` and assert **files scanned ≈
   current-hour files only** (not all hours). Files scanned is the real pruning
   unit — assert on that, not "partitions".
4. **Gate C2 — backfill impact:** write one backdated late span (old
   `start_time`, fresh ingest) and re-check the old partition's ingested zonemap
   to quantify how much the affected-CTE pruning degrades.

Only proceed to implementation if C1 and #1 pass. If C1 fails, the fallback is
DuckLake `sorted_tables`/clustering on `start_time` (tighten zonemaps without the
partition-count explosion) — see Risk 2.

## Testing

- **Unit:** `configureDuckLake` issues the `hour(...)` partition statements (and
  the capability-check fallback path, if added).
- **Benchmark (the real proof):** with the multi-hour bench mode, query p95 < 1.5s
  through a filling multi-hour dataset, and the soak certifies a rated capacity
  (>0, the goal). Confirm pruning via partitions-scanned / `EXPLAIN` showing the
  recent-window scan touches only recent hour partitions.
- **No regression:** ingest drops=0, export p95 ms-level; rollup correctness
  tests (late-data, watermark, chunked, affected-bucket rebuild) still pass.

## Risks (all verifiable early)

1. **`hour` transform support/semantics** in bundled DuckLake (duckdb-go
   v2.10503.1) — verify first; fall back to `(namespace, day, hour)` if `hour()`
   is hour-of-day.
2. **Partition/file proliferation (take seriously — adjacent to the known
   catalog-bloat problem).** 24× more partitions/day/namespace × retention, and
   low-traffic hours yield many tiny files merge can't coalesce across an hour
   boundary. This is the same family as the prior 60k-snapshot / per-file-metadata
   bloat ([[storage_compaction_gap]]). The benchmark MUST track file/catalog row
   counts over the multi-hour soak, not just query p95. Escape hatch if file count
   balloons: DuckLake `sorted_tables`/clustering on `start_time` to tighten
   zonemaps without the partition explosion (keep day partitioning). Design this
   fallback concretely if the gate experiment shows low-volume proliferation —
   don't defer.
3. **Merge must not straddle hour boundaries** (Critical) — proven by the gate
   experiment (step 2) before building, not asserted.
4. **`SET PARTITIONED BY` is new-writes-only** (confirmed by DuckLake docs: it
   does not rewrite or error; old data keeps its old partitioning until retention
   ages it out). So the `/data` wipe is a clean-slate convenience, not strictly
   required. One check: `configureDuckLake` re-issues `SET PARTITIONED BY` every
   boot — confirm re-issuing the same spec is a no-op and does not churn a new
   partition-spec snapshot each restart (snapshot-bloat history).
5. **Bench must span multiple hours** or it won't exercise the fix (addressed by
   the multi-hour mode above).

## Out of scope

- Materialized recent-errors index / fully incremental rollup (a larger
  alternative we did not choose).
- Sub-hour partitioning or zonemap clustering (revisit only if hour granularity
  proves insufficient).
- Raising the ingest ceiling / driver sizing (separate; ingest is not the limit).

## Gate results (2026-06-19, implemented)

Implemented: `configureDuckLake` now partitions spans/logs/metrics by
`hour(ts)` (`internal/query/views.go`). Verified locally against the real
bundled DuckLake (no cloud run needed):

- **hour() transform accepted** — `NewDuck` boots and all real-DuckLake rollup
  tests pass under hour partitioning (late-data, watermark, chunked,
  affected-bucket, edge). No fallback to `(day, hour)` needed.
- **Gate C1 (merge stays within an hour) — PASS.** Experiment
  (`TestHourPartitionPrunesRecentWindow`): 600k spans across 3 hours,
  `merge_adjacent_files` produced **exactly 3 files (one per hour)** — no
  straddling, zonemaps stay ~1 hour wide.
- **Gate #1 (range predicate prunes) — PASS.** `start_time >= now()-15min`
  scanned **10k of 600k rows** (the recent hour), vs scanning all 600k under
  day partitioning. Kept as a regression test.

Remaining for end-to-end showcase: the cloud throughput bench needs the
multi-hour pre-seed mode (a 40-min run stays within ~1 hour partition and so
doesn't exercise cross-hour pruning). The local experiment already proves the
mechanism; the cloud run would confirm query p95 under sustained load.
