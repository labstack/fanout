# 0003. Pinned DuckDB engine and batch format 4

Status: Accepted
Date: 2026-10-30

## Context

Telemetry queries depend on exact engine behaviour: built-in functions, Parquet
VARIANT handling and nanosecond timestamps. A Go driver version does not
identify the native engine it bundles, and silent engine or format drift would
change query results.

## Decision

Fanout builds against one pinned DuckDB 2 engine (`internal/duckdb`, built with
`scripts/with-duckdb.sh`) with statically linked extensions and no dynamic
extension downloads. Telemetry is stored only as batch format 4 Parquet with
typed attribute maps. A schema change requires a new format version; there is
no legacy reader or format fallback.

## Consequences

- Query results are reproducible across platforms and releases.
- Changing the pin means verifying `SELECT version()` and reviewing every
  changed built-in against the telemetry SQL blocklist.
- Data written in an unsupported format is rejected rather than reinterpreted.
