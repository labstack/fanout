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

Queries return complete 35-column traces and a raw service aggregation. The
general-purpose engines also run an endpoint rollup. The mixed test writes
another 200,000 committed rows while full trace reads run at 100 queries per
second.

## Fanout segment design

- immutable segments containing 2,048-row columnar blocks;
- every column compressed independently with Zstandard;
- block min/max event-time metadata;
- a fixed-width on-disk trace index, binary-searched lazily with full-ID
  verification;
- atomic manifest replacement with file and directory `fsync` ordering;
- orphan detection after a crash between segment and manifest publication;
- streaming compaction that copies compressed blocks without materializing
  rows and merges indexes with bounded memory, then atomically replaces the
  input segments;
- direct execution for indexed Fanout operations.

## Normalized result

Collected on Darwin/arm64, Apple M3 Max, 14 logical CPUs. This is a development
comparison, not a published Fanout capacity claim.

| Storage / execution | Write rows/s | Maintenance | Active disk | Endpoint | Full trace | Raw service | Mixed write | Mixed trace p95 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| **Production Fanout + Parquet** | 161,115 | live | 56.7 MiB | n/a | 0.846 ms | 28.63 ms | 162,544/s | 1.79 ms |
| Fanout columnar experiment | **548,714** | **142 ms** | 34.6 MiB | n/a | **0.663 ms** | 34.93 ms | **538,201/s** | **0.984 ms** |
| DuckDB native | 98,307 | 166 ms rollup + 4.59 ms checkpoint | 47.8 MiB | **0.880 ms** | 1.45 ms | **1.21 ms** | 72,420/s | 2.08 ms |
| Zstd Parquet + DuckDB | 95,305 effective | 320 ms export | **21.8 MiB** | 1.21 ms | 9.44 ms | 3.45 ms | n/a | n/a |
| chDB MergeTree | 125,388 | 4.05 s optimize | 38.1 MiB active | 2.91 ms | 7.10 ms | 6.10 ms | 121,775/s | 12.41 ms |

The chDB directory occupied 106.7 MiB after forced merges because inactive and
engine-internal files remain present; the table's active parts occupied 38.1
MiB. Its embedded-engine initialization took 418 ms in the measured run.

Endpoint is `n/a` for the Fanout rows because the span segment deliberately
contains only the production trace-lookup primitive. Production endpoint
dashboards use the rebuildable DuckDB `endpoint_rollup` cache; the DuckDB row
measures that query shape.

Iceberg is not listed as an execution engine. Its data plane is Parquet; table
metadata, snapshots, manifests, deletion vectors, and planning would sit above
the Parquet/DuckDB result and add capabilities plus some overhead.

## Interpretation

The custom span experiment establishes the upper bound behind the earlier
roughly 500k rows/s figure. It is not the production write rate: it omits the
authoritative Parquet projection for logs and metrics and is intentionally not
a general SQL store. Its trace index stays on disk and is searched lazily, so
retention does not create a resident trace-ID or rollup map.

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
