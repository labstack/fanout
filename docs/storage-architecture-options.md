# Fanout storage architecture options

**Decision date:** August 2026

**Product constraint:** one distributable Fanout binary
**Workload:** high-volume OTLP spans, logs, and metrics with fast dashboards,
trace lookup, attribute filtering, retention, and ad-hoc SQL

## Recommendation

Use a hybrid architecture:

1. **Fanout columnar segments** as a rebuildable recent-span trace index.
2. **Direct Fanout execution** for recent trace lookup.
3. **Parquet** as the authoritative durable telemetry format, written in the
   same commit.
4. **DuckDB** for arbitrary SQL over Parquet.
5. **SQLite** for control-plane data only.
6. **Do not use DuckLake, Iceberg, or chDB initially.**

```text
OTLP ingestion
      │
      ▼
Fanout hot columnar store (.fseg)
      ├── disk-resident trace index
      ├── direct recent-trace execution
      └── atomic manifest + streaming compaction
                         │ same WAL-backed transaction
                         ▼
                   Parquet files
                         │
                         ▼
                 DuckDB ad-hoc SQL

SQLite: users, configuration, sessions, alerts, and other control data
```

This is still a single-binary product. Fanout owns the high-performance hot
path; embedded DuckDB supplies a mature SQL engine without owning ingestion or
table lifecycle.

## First: separate the layers

Several technologies under consideration solve different problems and are not
direct substitutes.

| Layer | Purpose | Candidates |
|---|---|---|
| Physical format | Encodes column values in files | Fanout segments, Parquet, MergeTree parts, DuckDB native pages |
| Table management | Tracks files, commits, snapshots, schema, and deletion | Fanout manifest, DuckLake, Iceberg v3 |
| Query execution | Plans and executes filters, joins, and aggregations | Fanout direct execution, DuckDB, ClickHouse through chDB |
| Control database | Stores small transactional product state | SQLite |

Important distinctions:

- **Parquet is a file format**, not a database or query engine.
- **Iceberg uses Parquet in this proposal** and adds table metadata and commit
  semantics above it.
- **DuckLake is a table-management layer for DuckDB and Parquet.**
- **DuckDB is a query engine and native database.** It can query plain Parquet
  without DuckLake.
- **chDB embeds ClickHouse.** Its primary format is ClickHouse MergeTree parts;
  it can also read and write Parquet.
- **SQLite does not overlap with the telemetry engines.** It remains the right
  database for Fanout's low-volume control plane.

## Measured result

The normalized benchmark used one million complete Fanout-shaped spans,
50,000-row commits, complete trace reads, a raw service aggregation, and
another 200,000 rows under concurrent trace load at 100 queries per second.
General-purpose engines also ran an endpoint-rollup query.

| Storage / execution | Write rows/s | Endpoint | Full trace | Raw scan | Mixed write | Mixed trace p95 | Active disk |
|---|---:|---:|---:|---:|---:|---:|---:|
| **Production repository: Fanout + Parquet** | 161,115 | n/a | 0.846 ms | 28.63 ms | 162,544/s | 1.79 ms | 56.7 MiB |
| **Fanout columnar + direct** | **548,714** | n/a | **0.663 ms** | 34.93 ms | **538,201/s** | **0.984 ms** | 34.6 MiB |
| DuckDB native | 98,307 | **0.880 ms** | 1.45 ms | **1.21 ms** | 72,420/s | 2.08 ms | 47.8 MiB |
| Zstd Parquet + DuckDB | 95,305 effective | 1.21 ms | 9.44 ms | 3.45 ms | n/a | n/a | **21.8 MiB** |
| chDB MergeTree | 125,388 | 2.91 ms | 7.10 ms | 6.10 ms | 121,775/s | 12.41 ms | 38.1 MiB |

Maintenance measurements:

| Operation | Time |
|---|---:|
| Fanout compressed-block and index compaction | **142 ms** |
| DuckDB endpoint-rollup build | 166 ms |
| DuckDB checkpoint | 4.59 ms |
| Parquet export | 320 ms |
| chDB forced optimization | 4.05 s |

The production-repository row includes the real atomic WAL + hot-segment +
Parquet commit path and was rerun on 2026-08-26. The isolated rows measure each
engine separately. These are development measurements from an Apple M3 Max,
not published capacity claims. The detailed methodology and reproduction commands are in
[storage-benchmark.md](storage-benchmark.md).

## Options at a glance

| Option | Writes | Product reads | Ad-hoc SQL | Open data | Complexity | Verdict |
|---|---|---|---|---|---|---|
| Fanout hot index + Parquet + DuckDB | **Best** | **Best** | Strong | Yes | Medium | **Recommended** |
| DuckDB native | Medium | Strong | **Best** | Export required | Low | Good simpler alternative |
| DuckLake + DuckDB + Parquet | Medium | Strong | Strong | Yes | Medium-high | Remove from new design |
| Iceberg v3 + Parquet + DuckDB | Medium-low | Strong | Strong | **Best** | High | Add only for shared object storage |
| chDB + MergeTree | Strong | Good | Strong | Export required | Medium | Not selected |
| Fully custom database and SQL engine | Potentially best | Potentially best | Weak initially | No | **Extreme** | Do not build |

## Option A: Fanout hot store + Parquet + DuckDB

### Components

- Fanout-owned immutable columnar hot segments.
- Atomic Fanout manifest and crash recovery.
- A fixed-width on-disk trace index, searched lazily without a retention-sized
  resident map.
- Streaming compaction that copies compressed blocks without decoding rows.
- Parquet files committed alongside each hot segment.
- DuckDB for dashboards, broad scans, and ad-hoc SQL over Parquet.
- SQLite for control data.

### Benefits

- Highest measured ingestion throughput.
- Lowest measured indexed-query latency.
- No C++ call in the ingestion hot path.
- Fanout can optimize precisely for append-only telemetry and TTL retention.
- Parquet preserves interoperability for the complete retained dataset.
- DuckDB retains featureful SQL without controlling ingestion.

### Costs and risks

- Fanout owns file-format compatibility, checksums, recovery, retention, and
  compaction correctness.
- Recent spans have a second, rebuildable physical representation.
- Trace queries that cross the hot-retention boundary fall back to the
  authoritative Parquet view.
- The current benchmark's broad scan is much slower than DuckDB.
- Long-run compaction tuning remains workload-driven.

### Decision

**Recommended.** It wins the overall Fanout objective while delegating general
SQL and interoperable cold storage to established components.

## Option B: DuckDB native tables

### Components

- DuckDB native database for raw telemetry and rollups.
- DuckDB for all reads and SQL.
- SQLite for control data.
- Optional Parquet export.

### Benefits

- Simplest analytical architecture.
- Excellent broad scans and small dashboard queries.
- Mature SQL, joins, window functions, extensions, and vectorized execution.
- No separate lakehouse catalog is required.

### Costs and risks

- Ingestion was approximately five times slower than the custom hot store in
  the full-shape benchmark.
- Peak RSS was much higher in a previous isolated comparison; the current
  normalized rerun did not repeat RSS measurement.
- Scheduled rollup work remains outside ingestion.
- Native files are not an interoperable telemetry format.
- Export is required for other engines to consume the data.

### Decision

**Best simpler replacement if owning a hot format becomes too expensive.** It
is preferable to a more complicated DuckLake or Iceberg deployment when
everything remains inside one Fanout process. It is an architecture choice,
not a runtime fallback path.

## Option C: DuckLake + DuckDB + Parquet

### Components

- Parquet data files.
- DuckLake metadata and commits, currently backed by a SQLite catalog.
- DuckDB reads and writes.
- A separate SQLite database for Fanout control data.

### Benefits

- Transactional table semantics over Parquet.
- DuckDB-native integration.
- Schema evolution, snapshots, and managed file lifecycle.
- Parquet remains externally readable.

### Costs and overlap

- DuckLake and the proposed Fanout manifest both manage file commits,
  compaction, retention, and visibility.
- Catalog writes require serialization in the current single-process design.
- More maintenance paths exist than with DuckDB native tables.
- It does not improve the measured hot-path advantage of custom segments.
- The SQLite DuckLake catalog is separate from Fanout's control SQLite and
  should never be conflated with it.

### Decision

**Remove from the new architecture.** DuckLake makes sense when DuckDB owns the
authoritative Parquet table. In the recommended design, Fanout owns the hot
table lifecycle and DuckDB is a secondary SQL executor.

## Option D: Iceberg v3 + Parquet + DuckDB

### Components

- Parquet data files.
- Iceberg v3 table metadata, manifests, snapshots, schema and partition
  evolution, and row-level change mechanisms.
- An Iceberg catalog.
- DuckDB and potentially other engines as readers.
- `iceberg-go` for Fanout writes and metadata commits.

### Benefits

- Strongest open, multi-engine table contract.
- Appropriate for S3/R2 and large long-lived datasets.
- Supports snapshot history, time travel, schema evolution, and multiple
  independent consumers.
- Avoids binding the cold table to DuckDB.

### Costs and overlap

- Iceberg does not replace Parquet or the query engine.
- Snapshot and manifest planning add work above direct Parquet reads.
- It introduces a catalog and a more complex commit protocol.
- Small-file management becomes a first-class operational responsibility.
- It solves multi-engine and object-storage coordination that a single-process
  Fanout appliance does not initially have.

### Add Iceberg when

- S3 or R2 becomes primary durable storage.
- Multiple Fanout writers commit to the same table.
- Spark, Trino, Flink, or another external engine must share authoritative
  tables.
- Snapshot history and time travel become product requirements.
- Cold data outlives individual Fanout installations.

### Decision

**Do not include initially.** Keep the Parquet layout compatible with a later
Iceberg adoption, but do not pay its catalog and metadata cost before those
requirements exist.

## Option E: chDB + ClickHouse MergeTree

### Components

- Embedded ClickHouse through `chdb-go` bindings.
- MergeTree raw tables.
- Materialized views and AggregatingMergeTree rollups.
- Projections and data-skipping indexes.
- SQLite for control data.

### Benefits

- One analytical engine handles ingestion, raw storage, rollups, TTL, indexes,
  and SQL.
- Strong ClickHouse feature set.
- Better measured ingestion and memory than DuckDB in some tests.
- No Fanout-owned analytical file format is required.

### Costs and risks

- It was substantially slower than the custom path for ingestion, endpoint
  reads, full trace reads, mixed load, and maintenance.
- The Go package is a binding to a large C++ engine, not a native-Go database.
- The embedded library is extracted and dynamically loaded at runtime.
- Cold initialization and extracted-library size are meaningful appliance
  concerns.
- The current Go result and bulk-ingestion APIs are less mature than DuckDB's
  appender path.
- MergeTree files are engine-specific; Parquet export is required for open
  storage.

### Decision

**Not selected.** It is a credible one-engine architecture, but the normalized
benchmark no longer justifies its footprint and binding complexity for Fanout.

## Option F: fully custom database

This would include a custom storage format, WAL, catalog, indexes, compaction,
query planner, vectorized execution engine, SQL parser, joins, memory manager,
and transaction system.

### Potential benefit

- Complete control and the theoretical maximum performance for Fanout-specific
  operations.

### Why not

- The benchmark already demonstrates that custom **storage and fixed execution**
  provide most of the useful advantage.
- Building general SQL would duplicate years of DuckDB work.
- Correct recovery, concurrency, query planning, joins, spilling, and schema
  evolution would dominate product development.

### Decision

**Do not build a general database.** Build a Fanout storage engine and use
DuckDB where general SQL is valuable.

## Why Parquet remains

Parquet is the one lakehouse component retained in the initial architecture.

It provides:

- the smallest measured representation;
- an open, documented columnar format;
- direct DuckDB reads;
- compatibility with future Iceberg adoption;
- straightforward export and backup;
- independence from Fanout's hot-format evolution.

Parquet is published in every durable repository commit. Background compaction
combines small files without changing the authoritative format.

## Proposed data lifecycle

```text
1. Receive OTLP batch
2. Normalize and promote indexed attributes once
3. Durably stage the WAL and authoritative Parquet
4. Atomically publish Parquet under the reader gate
5. Publish the hot segment and commit journal, then remove the WAL
6. Answer recent complete traces through the hot index
7. Query dashboards, broad scans, and ad-hoc SQL with DuckDB
8. Stream-compact small Parquet and hot-segment files
9. Delete expired whole files through manifest commits
```

## Query routing

| Query | Recent path | Authoritative path |
|---|---|---|
| Trace by ID | Fanout trace index | DuckDB over Parquet |
| Service/endpoint dashboard | DuckDB rollup cache | DuckDB rollup cache |
| Promoted attribute filter | DuckDB predicate pushdown | DuckDB predicate pushdown |
| Log text search | DuckDB scan | DuckDB scan |
| Arbitrary SQL | DuckDB over Parquet | DuckDB over Parquet |
| Export | Parquet writer | Existing Parquet files |

## Single-binary implications

| Choice | Distribution consequence |
|---|---|
| Fanout hot store | Native Go code inside the existing binary |
| DuckDB | Embedded native dependency already used by Fanout |
| Parquet | Library code; no separate server |
| SQLite | Embedded control database already used by Fanout |
| chDB | Adds and extracts a large ClickHouse native library |
| Iceberg | Adds Go metadata/catalog logic but still needs storage and execution |

No external database daemon is required by the recommended design.

## Production gates

The implementation should remain gated on these production checks:

- [ ] Add per-block and per-file checksums.
- [ ] Test torn writes and corruption at every commit boundary.
- [ ] Run continuous kill/restart recovery tests.
- [ ] Prove retention and compaction are safe under active readers.
- [ ] Bound memory during multi-day compaction.
- [ ] Benchmark on the target Linux 4-vCPU/8-GB host.
- [ ] Run a long concurrent ingest/query/retention soak.
- [ ] Benchmark realistic high-cardinality attributes and large exception data.

## Final decision table

| Component | Initial decision | Revisit when |
|---|---|---|
| Fanout hot columnar format | **Use** | If ownership cost exceeds its measured advantage |
| Fanout direct trace path | **Use** | Always retain benchmarks against DuckDB |
| Parquet authoritative format | **Use** | No expected replacement |
| DuckDB query engine | **Use** | If another embedded engine wins normalized SQL tests materially |
| SQLite control database | **Use** | No overlap with telemetry storage |
| DuckDB native telemetry tables | Do not use | Reconsider only as a deliberate architecture replacement |
| DuckLake | **Remove** | If DuckDB again becomes authoritative over mutable Parquet tables |
| Iceberg v3 | Not initially | Shared object storage, multiple writers, or multi-engine tables |
| chDB | **Remove** | Only if its binding, footprint, and normalized results improve materially |
| Custom general SQL engine | **Do not build** | No planned revisit |

## Bottom line

Fanout does not need every lakehouse layer.

The smallest architecture that satisfies the product is:

```text
Fanout hot trace index + authoritative Parquet + DuckDB SQL + SQLite control
```

DuckLake and Iceberg overlap with lifecycle management that Fanout already must
own for the hot store. Iceberg remains a clean future option for shared object
storage; it is not a prerequisite for a fast, featureful single-binary Fanout.
