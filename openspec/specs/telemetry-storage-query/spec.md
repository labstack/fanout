# telemetry-storage-query Specification

## Purpose

Defines Fanout's durable data layout, telemetry flush and maintenance lifecycle, shared query scoping, and consistency guarantees under concurrent ingest and reads.

## Requirements

### Requirement: One data root contains durable instance state
Fanout SHALL keep telemetry data, query state, and control-plane state under `DATA_DIR`. Telemetry SHALL use a DuckLake catalog with Parquet files, query support SHALL use a local DuckDB catalog and temporary directory, and user-owned application state SHALL use a control SQLite database.

#### Scenario: Operator backs up a stopped instance
- **WHEN** the complete `DATA_DIR` is copied while Fanout is stopped
- **THEN** the backup contains telemetry, users, settings, dashboards, alerts, sessions, OAuth state, and agent history needed to restore the instance

### Requirement: Telemetry is flushed by time or batch size
Fanout SHALL buffer accepted rows and commit them when the configured flush interval elapses or the configured batch limit is reached.

#### Scenario: Low-volume instance receives a few spans
- **WHEN** the batch limit is not reached
- **THEN** the writer commits the buffered spans no later than the next configured flush cycle, subject to storage availability

### Requirement: Graceful shutdown preserves queued telemetry
Fanout MUST stop accepting new OTLP work before cancelling background processing, drain the writer, and report a final-flush failure rather than silently treating it as success.

#### Scenario: Process receives a termination signal
- **WHEN** Fanout begins graceful shutdown with queued rows
- **THEN** in-flight exporters are stopped and queued rows are given a final durable flush before the process exits

### Requirement: Query scope is explicit and bounded
Every deterministic observability query SHALL use one namespace, a positive start-to-end time range, and a bounded result limit. An omitted namespace SHALL resolve to the configured default, an omitted window SHALL resolve to one hour, and invalid windows or limits SHALL be rejected.

#### Scenario: Client omits optional scope fields
- **WHEN** a client requests an observability result without namespace, window, or limit
- **THEN** Fanout queries the default namespace over the previous hour with the domain's safe default limit

### Requirement: Rollups accelerate queries without changing their contract
Fanout SHALL maintain minute-level service, edge, and endpoint aggregates for repeated investigation queries. When an optimization is not ready or does not cover the full selected range, Fanout MUST use the authoritative raw rows for the uncovered work.

#### Scenario: Endpoint rollup is still warming
- **WHEN** a performance query arrives before enough endpoint rollup data is ready
- **THEN** Fanout answers from raw spans rather than returning an incomplete endpoint list

### Requirement: Query results disclose provenance
Fanout SHALL include the selected namespace, time bounds, and data source in typed observability results so consumers can distinguish rollup, raw, and mixed answers.

#### Scenario: Performance query combines cached and recent rows
- **WHEN** an endpoint result uses rollups up to a watermark and raw spans afterward
- **THEN** the result provenance identifies the mixed source

### Requirement: Retention and compaction bound telemetry storage
Fanout SHALL periodically expire telemetry older than `RETENTION_DAYS` and compact small adjacent files. A retention value of zero SHALL preserve telemetry indefinitely.

#### Scenario: Retention is configured to thirty days
- **WHEN** maintenance runs
- **THEN** eligible telemetry older than thirty days is removed and obsolete files are cleaned up

### Requirement: Concurrent reads do not violate single-writer safety
Fanout SHALL allow a configured pool of query connections while serializing telemetry, rollup, and maintenance writes and keeping the DuckLake catalog safe for concurrent readers.

#### Scenario: Dashboard query overlaps an ingest flush
- **WHEN** a read and write execute concurrently
- **THEN** Fanout preserves committed data integrity and retries protected maintenance-race failures rather than exposing a corrupt or permanently locked catalog
