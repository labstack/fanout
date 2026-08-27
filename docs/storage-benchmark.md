# Fanout storage benchmark

This benchmark asks whether a storage path designed only for Fanout's
telemetry workload can outperform embedded general-purpose databases while
remaining crash-safe and retaining a path to ad-hoc SQL.

It is isolated from the product and does not define a migration format.

## Workload

The shared generator emits the complete typed span produced by Fanout's OTLP
parser: 32 source fields, including all resource, attribute, event, link,
scope, HTTP, RPC, database, peer, deployment, status, and exception values.
DuckDB and chDB additionally persist three derived timestamp representations,
matching Fanout's current 35-column analytical shape. Each run uses one million
spans, 50,000-row durable commits, 50 services, 20 routes, 200 tenants, five
spans per trace, and 24 hours of event time.

Queries return complete 35-column traces, endpoint rollups, and a raw service
aggregation. The mixed test writes another 200,000 committed rows while full
trace reads run at 100 queries per second.

## Fanout segment design

- immutable segments containing 2,048-row columnar blocks;
- every column compressed independently with Zstandard;
- block min/max event-time metadata;
- compact per-block trace indexes with full-ID verification;
- five-minute endpoint histograms built during ingestion;
- atomic manifest replacement with file and directory `fsync` ordering;
- orphan detection after a crash between segment and manifest publication;
- streaming compaction that copies compressed blocks without materializing
  rows, then atomically replaces the input segments;
- direct execution for indexed Fanout operations.

## Normalized result

Collected on Darwin/arm64, Apple M3 Max, 14 logical CPUs. This is a development
comparison, not a published Fanout capacity claim. Peak RSS was measured in an
isolated process for each embedded engine.

| Storage / execution | Write rows/s | Maintenance | Active disk | Endpoint | Full trace | Raw service | Mixed write | Mixed trace p95 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| **Production Fanout + Parquet** | 154,848 | live | 56.6 MiB | **0.171 ms** | **0.508 ms** | 27.42 ms | 156,547/s | 0.864 ms |
| Fanout columnar experiment | **511,113** | **94 ms** | 34.4 MiB | 0.210 ms | 0.551 ms | 27.99 ms | **513,927/s** | **0.816 ms** |
| DuckDB native | 93,189 | 143 ms rollup + 5 ms checkpoint | 47.5 MiB | 0.899 ms | 1.52 ms | **1.16 ms** | 80,255/s | 2.02 ms |
| Zstd Parquet + DuckDB | 90,215 effective | 354 ms export | **21.8 MiB** | 1.35 ms | 9.40 ms | 3.64 ms | n/a | n/a |
| chDB MergeTree | 78,434 | 4.64 s optimize | 38.1 MiB active | 3.47 ms | 9.47 ms | 6.88 ms | 97,271/s | 12.38 ms |

The chDB directory occupied 106.7 MiB after forced merges because inactive and
engine-internal files remain present; the table's active parts occupied 38.1
MiB. Its embedded-engine initialization took 747 ms in the measured run.

Iceberg is not listed as an execution engine. Its data plane is Parquet; table
metadata, snapshots, manifests, deletion vectors, and planning would sit above
the Parquet/DuckDB result and add capabilities plus some overhead.

## Interpretation

The custom span experiment establishes the upper bound behind the earlier
roughly 500k rows/s figure. It is not the production write rate: it omits the
authoritative Parquet projection for logs and metrics and is intentionally not
a general SQL store.

The production design keeps the useful parts without taking on a home-grown
database:

- Fanout owns request-level WAL durability, a compact commit journal, the
  recent-span index, retention, and compaction;
- Parquet is the single authoritative format for spans, logs, and metrics;
- DuckDB executes SQL, filtering, ordering, and broad analytical scans;
- SQLite stores transactional control and identity state only.

This trades some maximum write throughput for much lower implementation risk,
full telemetry coverage, standard files, and a featureful SQL engine. The
benchmark reports direct durable publication throughput; request acknowledgement
is decoupled through the WAL and should be measured separately under the target
collector concurrency and hardware before publishing a capacity claim.

## Reproduction

Custom, DuckDB, and Parquet:

```sh
go run ./bench/storage \
  -rows 1000000 \
  -batch 50000 \
  -repeats 11 \
  -mixed-rows 200000
```

Run one embedded engine in isolation with `-engine custom` or `-engine duck`.

chDB is a nested experiment module so its embedded C++ library does not enter
Fanout's production dependency graph or binary:

```sh
cd bench/storage/chdb
go run . -rows 1000000 -batch 50000 -repeats 11 -mixed-rows 200000
```
