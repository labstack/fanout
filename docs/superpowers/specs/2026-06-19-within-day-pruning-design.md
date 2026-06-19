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

**Why this fixes all three scanners with almost no query changes:**
- Overview error queries and rollup agg scans already filter `start_time`, so
  DuckLake prunes them to the **1–2 relevant hour partitions** (~1/24 of a day).
- The rollup `affected` CTE (ingested-time filter) benefits indirectly: merge
  now runs *per hour partition*, so merged files span ≤1 hour of data → tight
  zonemaps → the ingested-time scan prunes to the current hour's files instead of
  day-spanning files (the exact failure at "4 merged files = 13s").

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
2. **Partition/file proliferation** — 24 partitions/day/namespace × retention;
   low-traffic hours yield tiny partitions. Bounded; `target_file_size` + merge
   mitigate. Revisit if file count balloons (e.g., coarser at low volume).
3. **Range-predicate pruning over a transform** — confirm DuckLake prunes
   `start_time >= X` to hour partitions (it should; verify empirically).
4. **Bench must span multiple hours** or it won't exercise the fix (addressed by
   the multi-hour mode above).

## Out of scope

- Materialized recent-errors index / fully incremental rollup (a larger
  alternative we did not choose).
- Sub-hour partitioning or zonemap clustering (revisit only if hour granularity
  proves insufficient).
- Raising the ingest ceiling / driver sizing (separate; ingest is not the limit).
