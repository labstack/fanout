# Fanout-native storage POC

This experiment asks whether a storage path designed only for Fanout's
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

| Storage / execution | Write rows/s | Maintenance | Active disk | Endpoint | Full trace | Raw service | Mixed write | Mixed trace p95 | Peak RSS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Fanout columnar + direct | **520,879** | **135 ms** | 34.4 MiB | **0.224 ms** | **0.507 ms** | 26.98 ms | **528,668/s** | **1.33 ms** | **197 MiB** |
| DuckDB native | 98,030 | 274 ms rollup + 4 ms checkpoint | 47.5 MiB | 0.905 ms | 1.54 ms | **1.44 ms** | 85,514/s | 2.14 ms | 1,693 MiB |
| Zstd Parquet + DuckDB | 94,824 effective | 345 ms export | **21.8 MiB** | 1.35 ms | 10.53 ms | 3.88 ms | n/a | n/a | included in DuckDB process |
| chDB MergeTree | 129,724 | 4.13 s optimize | 38.1 MiB active | 2.81 ms | 7.39 ms | 6.06 ms | 118,722/s | 9.61 ms | 699 MiB |

The chDB directory occupied 106.7 MiB after forced merges because inactive and
engine-internal files remain present; the table's active parts occupied 38.1
MiB. Its embedded-engine initialization took 412 ms in the measured run.

Iceberg is not listed as an execution engine. Its data plane is Parquet; table
metadata, snapshots, manifests, deletion vectors, and planning would sit above
the Parquet/DuckDB result and add capabilities plus some overhead.

## Interpretation

The custom path wins Fanout's fixed ingestion, endpoint, trace, concurrency,
maintenance, and memory objectives. DuckDB remains about 19 times faster for
the broad raw aggregation, and Parquet remains about 37% smaller than the
custom durable format.

This supports a hybrid architecture rather than a home-grown general SQL
database:

- Fanout owns the hot WAL/manifest, columnar segments, indexes, retention,
  compaction, and ingestion-time rollups;
- known product queries use direct vectorized execution;
- cold segments use Parquet when interoperability and density matter;
- an established vectorized SQL engine handles arbitrary scans over cold data;
- SQLite remains control/configuration storage only.

Before production replacement, the POC still needs logs and metrics, promoted
attribute indexes, retention under active readers, corruption checksums,
bounded-memory multi-day compaction, Linux 4-vCPU/8-GB measurements, and a
long-running kill/restart soak.

## Reproduction

Custom, DuckDB, and Parquet:

```sh
go run ./cmd/storage-poc \
  -rows 1000000 \
  -batch 50000 \
  -repeats 11 \
  -mixed-rows 200000
```

Run one embedded engine in isolation with `-engine custom` or `-engine duck`.

chDB is a nested experiment module so its embedded C++ library does not enter
Fanout's production dependency graph or binary:

```sh
cd experiments/storage-poc-chdb
go run . -rows 1000000 -batch 50000 -repeats 11 -mixed-rows 200000
```
