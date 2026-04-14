# DuckLake Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Fanout's custom Parquet write/glob/compaction/retention infrastructure with DuckLake, eliminating ~1000 lines of infrastructure code and gaining metadata-driven query pruning.

**Architecture:** DuckLake extension with SQLite catalog stores all telemetry (spans, logs, metrics) as managed tables. The lake writer INSERTs into DuckLake via DuckDB SQL instead of writing Parquet directly via parquet-go. Rollups query DuckLake tables directly instead of glob-scanning the filesystem. DuckLake handles compaction, schema evolution, and data file management automatically.

**Tech Stack:** DuckDB v1.5.2+ (duckdb-go/v2), DuckLake extension, SQLite catalog

---

## File Structure

### Files to DELETE entirely
- `internal/lake/compact.go` — replaced by DuckLake's automatic file management
- `internal/lake/compact_test.go`
- `internal/lake/retention.go` — replaced by DuckLake snapshot expiration
- `internal/lake/retention_test.go`
- `internal/query/perf.go` — glob-based file discovery no longer needed
- `internal/query/perf_test.go` (if exists)
- `internal/query/views.go` — views over read_parquet replaced by DuckLake tables
- `internal/query/views_test.go` (if exists)

### Files to REWRITE
- `internal/lake/writer.go` — switch from parquet-go to DuckDB INSERT
- `internal/lake/writer_test.go`
- `internal/query/duck.go` — DuckLake setup, alias views, simplified rollups, remove glob methods
- `internal/query/schema.go` — update schema docs for DuckLake tables

### Files to EXTRACT (move from deleted file)
- `internal/query/cache.go` — extract `Cache`, `InitQueryCache`, `GetCached`, `SetCached` from `perf.go` (used by `status.go`, `topology.go`)

### Files to MODIFY (query pattern updates)
- `internal/query/sql.go` — update SQL validation for DuckLake table names
- `cmd/fanout/main.go` — remove compactor/pruner, simplify wiring, pass `q.DB` to writer
- `internal/config/config.go` — rename `MaxRows` to `FlushBatchSize`, remove `RetentionHours`
- `internal/config/config_test.go` — update tests for renamed/removed config fields
- `internal/service/trace.go` — replace `read_parquet()` with DuckLake table queries
- `internal/service/diagnose.go` — replace `read_parquet()` with DuckLake table queries
- `internal/service/find.go` — replace `read_parquet()` with DuckLake table queries
- `internal/service/ui.go` — replace `read_parquet()`, rewrite `Namespaces()` to query DuckLake
- `internal/service/metrics.go` — remove `decode()` calls on JSON columns (now VARCHAR, not BLOB)
- `internal/service/attributes.go` — remove `decode()` calls on JSON columns
- `internal/service/spans.go` — `FROM spans` already works via alias view (minimal changes)
- `internal/service/logs.go` — `FROM logs` already works via alias view (minimal changes)
- `internal/service/compare.go` — `FROM spans` already works via alias view (minimal changes)
- `internal/service/service.go` — remove glob dependency
- `internal/mcp/query.go` — update schema response for DuckLake
- `internal/intelligence/detector.go` — replace 4 methods with `read_parquet`/`SpansGlob`/`LogsGlob`/`"name=..."` columns; update `isNoDataError()`
- `internal/api/health.go` — update readiness check
- `internal/query/schema_test.go` — update assertion for `read_parquet` in schema output
- `internal/query/duck_test.go` — remove `TestParquetGlob_DayLevel` and related glob tests
- `go.mod` — upgrade duckdb-go, remove parquet-go
- `CLAUDE.md` — update architecture docs
- `ARCHITECTURE.md` — remove `read_parquet` references

### Concurrency note

The writer INSERTs into DuckLake and rollups also INSERT into rollup tables, both via the same `*sql.DB` connection pool. DuckDB's single-writer model means these compete for the write lock. This is safe (connection pool serializes writes) but rollup latency may spike during large batch inserts. Acceptable tradeoff for simplicity.

---

## Task 1: Upgrade duckdb-go and remove parquet-go

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Upgrade duckdb-go to v2.10502.0+ (DuckDB v1.5.2)**

```bash
go get github.com/duckdb/duckdb-go/v2@latest
```

- [ ] **Step 2: Remove parquet-go dependency**

```bash
go get -u github.com/parquet-go/parquet-go@none
```

- [ ] **Step 3: Tidy modules**

```bash
go mod tidy
```

- [ ] **Step 4: Verify it compiles (expect errors in lake/ — that's fine)**

```bash
go build ./cmd/fanout 2>&1 | head -20
```

Expected: Compilation errors in `internal/lake/writer.go` referencing `parquet-go` — this is correct since we haven't rewritten it yet.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: upgrade duckdb-go for DuckLake, remove parquet-go"
```

---

## Task 2: Rewrite the DuckDB initialization with DuckLake

**Files:**
- Rewrite: `internal/query/duck.go`
- Create: `internal/query/cache.go` (extracted from `perf.go`)
- Delete: `internal/query/views.go`
- Delete: `internal/query/perf.go`

This task replaces the entire DuckDB setup. Instead of creating views over `read_parquet()` globs and maintaining a glob cache, we attach a DuckLake database with a SQLite catalog and create managed tables. Alias views (`spans`, `logs`, `metrics`) are created over the DuckLake tables so that existing service layer queries using `FROM spans` continue to work.

- [ ] **Step 1: Extract cache from `perf.go` into `internal/query/cache.go`**

Before deleting `perf.go`, extract the `Cache` struct, `InitQueryCache`, `GetCached`, `SetCached`, and the cleanup goroutine into a new file `internal/query/cache.go`. These are still used by `internal/service/status.go` and `internal/service/topology.go`. Copy lines 82-178 from `perf.go` into the new file with `package query` header.

- [ ] **Step 2: Delete files that are no longer needed**

Delete these files entirely:
- `internal/query/views.go`
- `internal/query/perf.go` (cache was extracted in step 1)

Also delete their test files if they exist:
- `internal/query/views_test.go`
- `internal/query/perf_test.go`

- [ ] **Step 3: Rewrite `internal/query/duck.go`**

Replace the entire file. Key changes:
- Install DuckLake + SQLite extensions in connector init
- Attach DuckLake with SQLite catalog at `{LakeDir}/catalog.sqlite`, data path `{LakeDir}/data/`
- Create tables (`lake.spans`, `lake.logs`, `lake.metrics`) with proper columns if they don't exist
- Create **alias views** (`CREATE VIEW spans AS SELECT * FROM lake.spans`, same for logs/metrics) so existing `FROM spans` queries work unchanged
- Configure DuckLake options (zstd compression, data inlining, sorted tables)
- Create rollup tables in the default DuckDB database (not in DuckLake — they're ephemeral aggregates)
- Create the `attr()` macro
- Remove `SpansGlob()`, `LogsGlob()`, `MetricsGlob()` methods
- Remove `MigrateOldPartitions()` call
- Remove `CreateViews()` call

```go
package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/duckdb/duckdb-go/v2"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
)

type Duck struct {
	DB  *sql.DB
	cfg config.Config
}

func NewDuck(ctx context.Context, cfg config.Config) (*Duck, error) {
	mem := cfg.DuckDBMemory
	if mem == "" {
		mem = "512MB"
	}
	if strings.ContainsAny(mem, "&?'\"\\; ") {
		return nil, fmt.Errorf("invalid DUCKDB_MEMORY value: %q", mem)
	}

	catalogPath := filepath.Join(cfg.LakeDir, "catalog.sqlite")
	dataPath := filepath.Join(cfg.LakeDir, "data")
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// Validate paths for SQL injection
	if strings.ContainsAny(catalogPath, "'\"\\;") {
		return nil, fmt.Errorf("catalog path contains unsafe characters: %q", catalogPath)
	}
	if strings.ContainsAny(dataPath, "'\"\\;") {
		return nil, fmt.Errorf("data path contains unsafe characters: %q", dataPath)
	}

	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		boot := []string{
			fmt.Sprintf("SET memory_limit='%s'", mem),
			"SET threads=4",
			"INSTALL ducklake",
			"INSTALL sqlite",
			fmt.Sprintf("ATTACH 'ducklake:sqlite:%s' AS lake (DATA_PATH '%s/')", catalogPath, dataPath),
		}
		for _, stmt := range boot {
			if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("duckdb connector: %w", err)
	}

	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)

	// Enable spill-to-disk
	tmpDir := filepath.Join(cfg.LakeDir, "tmp")
	if mkErr := os.MkdirAll(tmpDir, 0o755); mkErr == nil {
		if !strings.ContainsAny(tmpDir, "'\"\\;") {
			db.Exec(fmt.Sprintf("SET temp_directory='%s'", tmpDir))
		}
	}

	d := &Duck{DB: db, cfg: cfg}

	if err := d.createTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}
	if err := d.configureDuckLake(); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure ducklake: %w", err)
	}
	if err := d.createRollupTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("create rollup tables: %w", err)
	}
	if err := d.createMacros(); err != nil {
		db.Close()
		return nil, fmt.Errorf("create macros: %w", err)
	}
	if err := d.createAliasViews(); err != nil {
		db.Close()
		return nil, fmt.Errorf("create alias views: %w", err)
	}

	return d, nil
}

func (d *Duck) createTables() error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS lake.spans (
			trace_id VARCHAR,
			span_id VARCHAR,
			parent_span_id VARCHAR,
			service VARCHAR,
			operation VARCHAR,
			kind VARCHAR,
			start_time TIMESTAMP,
			end_time TIMESTAMP,
			duration_ms DOUBLE,
			status VARCHAR,
			status_message VARCHAR,
			resource_json VARCHAR,
			attributes_json VARCHAR,
			events_json VARCHAR,
			links_json VARCHAR,
			trace_state VARCHAR,
			flags INTEGER,
			scope_name VARCHAR,
			scope_version VARCHAR,
			ingested_at TIMESTAMP,
			http_method VARCHAR,
			http_status_code VARCHAR,
			http_route VARCHAR,
			db_system VARCHAR,
			rpc_method VARCHAR,
			rpc_service VARCHAR,
			peer_service VARCHAR,
			service_version VARCHAR,
			deployment_env VARCHAR,
			exception_type VARCHAR,
			exception_message VARCHAR,
			namespace VARCHAR,
			tenant VARCHAR
		)`,
		`CREATE TABLE IF NOT EXISTS lake.logs (
			time TIMESTAMP,
			observed_time TIMESTAMP,
			severity VARCHAR,
			severity_number INTEGER,
			body VARCHAR,
			service VARCHAR,
			trace_id VARCHAR,
			span_id VARCHAR,
			flags INTEGER,
			resource_json VARCHAR,
			attributes_json VARCHAR,
			scope_name VARCHAR,
			scope_version VARCHAR,
			ingested_at TIMESTAMP,
			body_template VARCHAR,
			namespace VARCHAR,
			tenant VARCHAR
		)`,
		`CREATE TABLE IF NOT EXISTS lake.metrics (
			time TIMESTAMP,
			name VARCHAR,
			description VARCHAR,
			unit VARCHAR,
			type VARCHAR,
			service VARCHAR,
			value DOUBLE,
			hist_bounds_json VARCHAR,
			hist_counts_json VARCHAR,
			hist_count BIGINT,
			hist_sum DOUBLE,
			exemplars_json VARCHAR,
			attributes_json VARCHAR,
			resource_json VARCHAR,
			scope_name VARCHAR,
			scope_version VARCHAR,
			ingested_at TIMESTAMP,
			namespace VARCHAR,
			tenant VARCHAR
		)`,
	}
	for _, ddl := range tables {
		if _, err := d.DB.Exec(ddl); err != nil {
			return fmt.Errorf("exec %s: %w", ddl[:40], err)
		}
	}
	return nil
}

func (d *Duck) configureDuckLake() error {
	opts := []string{
		"CALL lake.set_option('parquet_compression', 'zstd')",
		"CALL lake.set_option('parquet_compression_level', 3)",
		"CALL lake.set_option('target_file_size', '64MB')",
		// Inlining: buffer up to 5000 rows in SQLite before flushing to Parquet.
		// With 15s flush intervals this accumulates ~4 flushes (60s) of data
		// before writing a Parquet file, reducing small file creation.
		"CALL lake.set_option('data_inlining_row_limit', 5000, table_name => 'spans')",
		"CALL lake.set_option('data_inlining_row_limit', 5000, table_name => 'logs')",
		"CALL lake.set_option('data_inlining_row_limit', 5000, table_name => 'metrics')",
	}
	if d.cfg.RetentionDays > 0 {
		opts = append(opts,
			fmt.Sprintf("CALL lake.set_option('expire_older_than', '%d days')", d.cfg.RetentionDays),
			"CALL lake.set_option('delete_older_than', '1 day')",
		)
	}
	for _, stmt := range opts {
		if _, err := d.DB.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}

	// Sort spans by start_time for optimal time-range pruning
	sortOpts := []string{
		"ALTER TABLE lake.spans SET SORTED BY (start_time ASC)",
		"ALTER TABLE lake.logs SET SORTED BY (time ASC)",
		"ALTER TABLE lake.metrics SET SORTED BY (time ASC)",
	}
	for _, stmt := range sortOpts {
		if _, err := d.DB.Exec(stmt); err != nil {
			slog.Warn("set sorted by failed (may already be set)", "stmt", stmt, "err", err)
		}
	}

	return nil
}

func (d *Duck) createRollupTables() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS service_rollup (
			bucket TIMESTAMP,
			service TEXT,
			spans BIGINT,
			p50_ms DOUBLE,
			p95_ms DOUBLE,
			error_rate DOUBLE,
			log_count BIGINT DEFAULT 0,
			metric_count BIGINT DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS edge_rollup (
			bucket TIMESTAMP,
			caller TEXT,
			callee TEXT,
			calls BIGINT,
			avg_ms DOUBLE,
			error_rate DOUBLE,
			edge_type TEXT DEFAULT 'call'
		)`,
	}
	for _, ddl := range stmts {
		if _, err := d.DB.Exec(ddl); err != nil {
			return err
		}
	}
	return nil
}

func (d *Duck) createMacros() error {
	_, err := d.DB.Exec(`CREATE OR REPLACE MACRO attr(json_col, key) AS
		json_extract_string(json_col, '$.' || key)`)
	return err
}

// createAliasViews creates convenience views so existing queries using
// `FROM spans`, `FROM logs`, `FROM metrics` continue to work unchanged.
// This minimizes changes in the service layer and preserves backward
// compatibility for user SQL queries via the MCP query tool.
func (d *Duck) createAliasViews() error {
	views := []string{
		"CREATE OR REPLACE VIEW spans AS SELECT * FROM lake.spans",
		"CREATE OR REPLACE VIEW logs AS SELECT * FROM lake.logs",
		"CREATE OR REPLACE VIEW metrics AS SELECT * FROM lake.metrics",
	}
	for _, ddl := range views {
		if _, err := d.DB.Exec(ddl); err != nil {
			return fmt.Errorf("%s: %w", ddl, err)
		}
	}
	return nil
}

func (d *Duck) Close() error { return d.DB.Close() }

func (d *Duck) DefaultTenantID() string {
	return d.cfg.TenantID.String()
}

func (d *Duck) DefaultNamespace() string {
	return ""
}
```

Note the `import "database/sql/driver"` is needed for the `duckdb.NewConnector` callback. Check the duckdb-go/v2 API — if the callback signature uses `driver.ExecerContext`, import it. If it uses a different type, adjust accordingly.

- [ ] **Step 3: Rewrite `rollupOnce()` in the same file**

The rollup queries now read from `lake.spans`, `lake.logs`, `lake.metrics` instead of `read_parquet()` with glob patterns. This is dramatically simpler:

```go
func (d *Duck) RunRollups(ctx context.Context) {
	start := time.Now()
	rows, err := d.rollupOnce(ctx)
	if err != nil {
		slog.Error("startup rollup failed", "err", err)
	} else if rows > 0 {
		slog.Info("startup rollup complete", "rows", rows, "duration", time.Since(start))
		metrics.RecordRollup(rows, time.Since(start).Seconds())
	}

	ticker := time.NewTicker(time.Duration(d.cfg.RollupEvery) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			start := time.Now()
			rows, err := d.rollupOnce(ctx)
			if err != nil {
				slog.Error("rollup failed", "component", "rollup", "err", err)
				continue
			}
			metrics.RecordRollup(rows, time.Since(start).Seconds())
		case <-ctx.Done():
			return
		}
	}
}

func (d *Duck) rollupOnce(ctx context.Context) (int, error) {
	// Service rollup from spans
	res, err := d.DB.ExecContext(ctx, `
INSERT INTO service_rollup (bucket, service, spans, p50_ms, p95_ms, error_rate)
SELECT
  date_trunc('minute', start_time) AS bucket,
  service,
  COUNT(*) AS spans,
  quantile_cont(duration_ms, 0.50) AS p50_ms,
  quantile_cont(duration_ms, 0.95) AS p95_ms,
  avg(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1 ELSE 0 END) AS error_rate
FROM lake.spans
WHERE date_trunc('minute', start_time) > COALESCE(
  (SELECT max(bucket) FROM service_rollup), TIMESTAMP '1970-01-01')
GROUP BY ALL`)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()

	// Log rollup
	logRes, logErr := d.DB.ExecContext(ctx, `
INSERT INTO service_rollup (bucket, service, spans, p50_ms, p95_ms, error_rate, log_count, metric_count)
SELECT
  date_trunc('minute', time) AS bucket,
  service,
  0, 0, 0, 0,
  COUNT(*) AS log_count,
  0 AS metric_count
FROM lake.logs
WHERE service IS NOT NULL AND service != ''
  AND date_trunc('minute', time) > COALESCE(
    (SELECT max(bucket) FROM service_rollup WHERE log_count > 0), TIMESTAMP '1970-01-01')
GROUP BY ALL`)
	if logErr != nil {
		slog.Error("log rollup failed", "err", logErr)
	} else {
		n, _ := logRes.RowsAffected()
		affected += n
	}

	// Metric rollup
	metricRes, metricErr := d.DB.ExecContext(ctx, `
INSERT INTO service_rollup (bucket, service, spans, p50_ms, p95_ms, error_rate, log_count, metric_count)
SELECT
  date_trunc('minute', time) AS bucket,
  service,
  0, 0, 0, 0,
  0,
  COUNT(DISTINCT name) AS metric_count
FROM lake.metrics
WHERE service IS NOT NULL AND service != ''
  AND date_trunc('minute', time) > COALESCE(
    (SELECT max(bucket) FROM service_rollup WHERE metric_count > 0), TIMESTAMP '1970-01-01')
GROUP BY ALL`)
	if metricErr != nil {
		slog.Error("metric rollup failed", "err", metricErr)
	} else {
		n, _ := metricRes.RowsAffected()
		affected += n
	}

	// Edge rollup (call edges)
	_, edgeErr := d.DB.ExecContext(ctx, `
INSERT INTO edge_rollup (bucket, caller, callee, calls, avg_ms, error_rate)
WITH calls AS (
  SELECT
    date_trunc('minute', child.start_time) AS bucket,
    parent.service AS caller,
    child.service AS callee,
    child.duration_ms,
    child.status
  FROM lake.spans child
  JOIN lake.spans parent
    ON child.parent_span_id = parent.span_id
    AND child.trace_id = parent.trace_id
  WHERE date_trunc('minute', child.start_time) > COALESCE(
    (SELECT max(bucket) FROM edge_rollup), TIMESTAMP '1970-01-01')
    AND parent.service != child.service
)
SELECT bucket, caller, callee,
  COUNT(*) AS calls,
  AVG(duration_ms) AS avg_ms,
  AVG(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate
FROM calls
GROUP BY bucket, caller, callee`)
	if edgeErr != nil {
		slog.Error("edge rollup failed", "err", edgeErr)
	}

	// Messaging edge rollup
	_, msgErr := d.DB.ExecContext(ctx, `
INSERT INTO edge_rollup (bucket, caller, callee, calls, avg_ms, error_rate, edge_type)
WITH producers AS (
  SELECT date_trunc('minute', start_time) AS bucket, service,
    json_extract_string(attributes_json, '$.messaging.destination.name') AS destination,
    json_extract_string(attributes_json, '$.messaging.system') AS msg_system
  FROM lake.spans
  WHERE kind = 'SPAN_KIND_PRODUCER'
    AND json_extract_string(attributes_json, '$.messaging.destination.name') IS NOT NULL
),
consumers AS (
  SELECT date_trunc('minute', start_time) AS bucket, service,
    json_extract_string(attributes_json, '$.messaging.destination.name') AS destination,
    json_extract_string(attributes_json, '$.messaging.system') AS msg_system
  FROM lake.spans
  WHERE kind = 'SPAN_KIND_CONSUMER'
    AND json_extract_string(attributes_json, '$.messaging.destination.name') IS NOT NULL
)
SELECT p.bucket, p.service AS caller, c.service AS callee,
  COUNT(*) AS calls, 0 AS avg_ms, 0 AS error_rate, 'messaging' AS edge_type
FROM producers p
JOIN consumers c ON p.destination = c.destination AND p.msg_system = c.msg_system AND p.bucket = c.bucket
WHERE p.service != c.service
  AND p.bucket > COALESCE(
    (SELECT max(bucket) FROM edge_rollup WHERE edge_type = 'messaging'), TIMESTAMP '1970-01-01')
GROUP BY p.bucket, p.service, c.service`)
	if msgErr != nil {
		slog.Error("messaging edge rollup failed", "err", msgErr)
	}

	// Prune old rollup data
	if d.cfg.RetentionDays > 0 {
		for _, tbl := range []string{"service_rollup", "edge_rollup"} {
			d.DB.ExecContext(ctx, fmt.Sprintf(
				"DELETE FROM %s WHERE bucket < now() - INTERVAL %d DAY", tbl, d.cfg.RetentionDays))
		}
	}

	return int(affected), nil
}
```

- [ ] **Step 4: Add a `RunMaintenance()` method for periodic DuckLake housekeeping**

```go
// RunMaintenance runs periodic DuckLake maintenance (flush inlined data,
// expire snapshots, merge files, cleanup).
func (d *Duck) RunMaintenance(ctx context.Context) {
	// Run every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Initial run after 2 minutes (let writes settle)
	time.Sleep(2 * time.Minute)
	d.maintenance()

	for {
		select {
		case <-ticker.C:
			d.maintenance()
		case <-ctx.Done():
			return
		}
	}
}

func (d *Duck) maintenance() {
	start := time.Now()
	// CHECKPOINT runs all maintenance in order: flush inlined data,
	// expire snapshots, merge files, rewrite deleted, cleanup, delete orphans.
	if _, err := d.DB.Exec("CHECKPOINT lake"); err != nil {
		slog.Error("ducklake maintenance failed", "err", err)
		return
	}
	slog.Info("ducklake maintenance complete", "duration", time.Since(start))
}
```

- [ ] **Step 5: Move existing API query methods to use DuckLake tables**

The methods `LatencyOverview`, `LogsSamples`, `Throughput`, `ServiceThroughput`, `ErrorRoutes`, `ErrorRouteDetails` in `duck.go` that use `read_parquet()` with globs need to query `lake.spans`, `lake.logs` directly. For example:

`LogsSamples` changes from:
```go
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
```
to:
```go
FROM lake.logs
WHERE time >= now() - INTERVAL %d MINUTE
```

And column references change from `"name=body"` to `body`, `"name=service_name"` to `service`, etc.

Apply this pattern to all methods in `duck.go` that use `read_parquet`. Remove the `SpansGlob()`, `LogsGlob()`, `MetricsGlob()` methods entirely.

- [ ] **Step 6: Verify compilation of query package**

```bash
go build ./internal/query/...
```

Expected: Compilation errors in other packages that call removed methods (`SpansGlob`, etc.) — that's expected and handled in later tasks.

- [ ] **Step 7: Commit**

```bash
git add -A internal/query/
git commit -m "feat: replace read_parquet glob infrastructure with DuckLake tables"
```

---

## Task 3: Rewrite the lake writer

**Files:**
- Rewrite: `internal/lake/writer.go`
- Rewrite: `internal/lake/writer_test.go`
- Delete: `internal/lake/compact.go`
- Delete: `internal/lake/compact_test.go`
- Delete: `internal/lake/retention.go`
- Delete: `internal/lake/retention_test.go`

The writer switches from buffering rows and writing Parquet via parquet-go to buffering rows and INSERTing via DuckDB SQL. The row types remain as Go structs but lose their parquet struct tags.

- [ ] **Step 1: Delete compaction and retention files**

Delete:
- `internal/lake/compact.go`
- `internal/lake/compact_test.go`
- `internal/lake/retention.go`
- `internal/lake/retention_test.go`

- [ ] **Step 2: Rewrite `internal/lake/writer.go`**

The row types keep the same fields but drop parquet tags. The writer now takes a `*sql.DB` instead of writing files directly. The flush method builds batch INSERT statements.

```go
package lake

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
)

type SpanRow struct {
	TenantID         string
	Namespace        string
	TraceID          string
	SpanID           string
	ParentSpanID     string
	ServiceName      string
	Name             string
	Kind             string
	StartUnixNanos   int64
	EndUnixNanos     int64
	DurationMs       float64
	StatusCode       string
	StatusMsg        string
	ResourceJSON     []byte
	AttributesJSON   []byte
	EventsJSON       []byte
	LinksJSON        []byte
	TraceState       string
	Flags            uint32
	ScopeName        string
	ScopeVersion     string
	IngestedAt       int64
	HTTPMethod       string
	HTTPStatusCode   string
	HTTPRoute        string
	DBSystem         string
	RPCMethod        string
	RPCService       string
	PeerService      string
	ServiceVersion   string
	DeploymentEnv    string
	ExceptionType    string
	ExceptionMessage string
}

type LogRow struct {
	TenantID          string
	Namespace         string
	TimeUnixNanos     int64
	ObservedTimeNanos int64
	Severity          string
	SeverityNumber    int32
	Body              string
	ServiceName       string
	TraceID           string
	SpanID            string
	Flags             uint32
	ResourceJSON      []byte
	AttributesJSON    []byte
	ScopeName         string
	ScopeVersion      string
	IngestedAt        int64
	BodyTemplate      string
}

type MetricRow struct {
	TenantID       string
	Namespace      string
	TimeUnixNanos  int64
	Name           string
	Description    string
	Unit           string
	MType          string
	ServiceName    string
	Value          float64
	HistBoundsJSON []byte
	HistCountsJSON []byte
	HistCount      int64
	HistSum        float64
	ExemplarsJSON  []byte
	AttributesJSON []byte
	ResourceJSON   []byte
	ScopeName      string
	ScopeVersion   string
	IngestedAt     int64
}

type Writer struct {
	cfg        config.Config
	db         *sql.DB
	chSpans    <-chan SpanRow
	chLogs     <-chan LogRow
	chMetrics  <-chan MetricRow
	mu         sync.Mutex
	bufSpans   []SpanRow
	bufLogs    []LogRow
	bufMetrics []MetricRow
	lastFlush  time.Time
	done       chan struct{}
}

func NewWriter(cfg config.Config, db *sql.DB, spans <-chan SpanRow, logs <-chan LogRow, metrics <-chan MetricRow) *Writer {
	return &Writer{
		cfg: cfg, db: db,
		chSpans: spans, chLogs: logs, chMetrics: metrics,
		lastFlush: time.Now(), done: make(chan struct{}),
	}
}

func (w *Writer) Wait() { <-w.done }

func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)
	ticker := time.NewTicker(time.Duration(w.cfg.FlushSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case r := <-w.chSpans:
			w.mu.Lock()
			w.bufSpans = append(w.bufSpans, r)
			metrics.RecordIngest("spans", 1)
			w.maybeFlush(ctx)
			w.mu.Unlock()
		case r := <-w.chLogs:
			w.mu.Lock()
			w.bufLogs = append(w.bufLogs, r)
			metrics.RecordIngest("logs", 1)
			w.maybeFlush(ctx)
			w.mu.Unlock()
		case r := <-w.chMetrics:
			w.mu.Lock()
			w.bufMetrics = append(w.bufMetrics, r)
			metrics.RecordIngest("metrics", 1)
			w.maybeFlush(ctx)
			w.mu.Unlock()
		case <-ticker.C:
			w.mu.Lock()
			w.flushLocked(ctx)
			w.mu.Unlock()
		case <-ctx.Done():
			w.mu.Lock()
			w.drainChannels()
			w.flushLocked(ctx)
			w.mu.Unlock()
			return nil
		}
	}
}

func (w *Writer) drainChannels() {
	for {
		select {
		case r := <-w.chSpans:
			w.bufSpans = append(w.bufSpans, r)
		case r := <-w.chLogs:
			w.bufLogs = append(w.bufLogs, r)
		case r := <-w.chMetrics:
			w.bufMetrics = append(w.bufMetrics, r)
		default:
			return
		}
	}
}

func (w *Writer) maybeFlush(ctx context.Context) {
	total := len(w.bufSpans) + len(w.bufLogs) + len(w.bufMetrics)
	if total >= w.cfg.FlushBatchSize || time.Since(w.lastFlush) >= time.Duration(w.cfg.FlushSeconds)*time.Second {
		w.flushLocked(ctx)
	}
}

func (w *Writer) flushLocked(ctx context.Context) {
	if len(w.bufSpans) > 0 {
		start := time.Now()
		if err := w.insertSpans(ctx, w.bufSpans); err != nil {
			slog.Error("insert spans failed", "count", len(w.bufSpans), "err", err)
			metrics.FlushErrors.WithLabelValues("spans").Inc()
		}
		metrics.RecordFlush("spans", 0, time.Since(start).Seconds())
		w.bufSpans = w.bufSpans[:0]
	}
	if len(w.bufLogs) > 0 {
		start := time.Now()
		if err := w.insertLogs(ctx, w.bufLogs); err != nil {
			slog.Error("insert logs failed", "count", len(w.bufLogs), "err", err)
			metrics.FlushErrors.WithLabelValues("logs").Inc()
		}
		metrics.RecordFlush("logs", 0, time.Since(start).Seconds())
		w.bufLogs = w.bufLogs[:0]
	}
	if len(w.bufMetrics) > 0 {
		start := time.Now()
		if err := w.insertMetrics(ctx, w.bufMetrics); err != nil {
			slog.Error("insert metrics failed", "count", len(w.bufMetrics), "err", err)
			metrics.FlushErrors.WithLabelValues("metrics").Inc()
		}
		metrics.RecordFlush("metrics", 0, time.Since(start).Seconds())
		w.bufMetrics = w.bufMetrics[:0]
	}
	w.lastFlush = time.Now()
}

// nsToTimestamp converts nanosecond epoch to DuckDB TIMESTAMP literal.
func nsToTimestamp(ns int64) string {
	t := time.Unix(0, ns).UTC()
	return t.Format("2006-01-02 15:04:05.999999")
}

func nullableStr(s string) string {
	if s == "" {
		return "NULL"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func escStr(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func blobOrNull(b []byte) string {
	if len(b) == 0 {
		return "NULL"
	}
	return "'" + escStr(string(b)) + "'"
}
```

The actual `insertSpans`, `insertLogs`, `insertMetrics` methods use DuckDB's Appender API (via duckdb-go) for maximum throughput. Check duckdb-go/v2 docs for the Appender API. If Appender is not available for attached databases, fall back to multi-row INSERT:

```go
func (w *Writer) insertSpans(ctx context.Context, rows []SpanRow) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO lake.spans (
		trace_id, span_id, parent_span_id, service, operation, kind,
		start_time, end_time, duration_ms, status, status_message,
		resource_json, attributes_json, events_json, links_json,
		trace_state, flags, scope_name, scope_version, ingested_at,
		http_method, http_status_code, http_route, db_system,
		rpc_method, rpc_service, peer_service,
		service_version, deployment_env,
		exception_type, exception_message,
		namespace, tenant
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		ns := r.Namespace
		if ns == "" { ns = "default" }
		tid := r.TenantID
		if tid == "" { tid = "default" }
		startTime := time.Unix(0, r.StartUnixNanos).UTC()
		endTime := time.Unix(0, r.EndUnixNanos).UTC()
		ingestedAt := time.Unix(0, r.IngestedAt).UTC()
		_, err := stmt.ExecContext(ctx,
			r.TraceID, r.SpanID, r.ParentSpanID, r.ServiceName, r.Name, r.Kind,
			startTime, endTime, r.DurationMs, r.StatusCode, r.StatusMsg,
			nullBytes(r.ResourceJSON), nullBytes(r.AttributesJSON),
			nullBytes(r.EventsJSON), nullBytes(r.LinksJSON),
			r.TraceState, r.Flags, r.ScopeName, r.ScopeVersion, ingestedAt,
			r.HTTPMethod, r.HTTPStatusCode, r.HTTPRoute, r.DBSystem,
			r.RPCMethod, r.RPCService, r.PeerService,
			r.ServiceVersion, r.DeploymentEnv,
			r.ExceptionType, r.ExceptionMessage,
			ns, tid,
		)
		if err != nil {
			return fmt.Errorf("insert span: %w", err)
		}
	}
	return tx.Commit()
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
```

Write analogous `insertLogs` and `insertMetrics` methods following the same pattern — prepared statement inside a transaction, iterating over rows.

- [ ] **Step 3: Remove `CleanupTempFiles` function**

Delete the `CleanupTempFiles` function from `writer.go` — no more temp Parquet files to clean up.

- [ ] **Step 4: Verify compilation of lake package**

```bash
go build ./internal/lake/...
```

Expected: Success (the lake package is now self-contained with the new writer).

- [ ] **Step 5: Commit**

```bash
git add -A internal/lake/
git commit -m "feat: rewrite lake writer to INSERT into DuckLake, remove compaction and retention"
```

---

## Task 4: Update the service layer and intelligence detector

**Files:**
- Modify: `internal/service/trace.go`
- Modify: `internal/service/diagnose.go`
- Modify: `internal/service/find.go`
- Modify: `internal/service/ui.go` (including `Namespaces()` rewrite)
- Modify: `internal/service/metrics.go` (remove `decode()` calls)
- Modify: `internal/service/attributes.go` (remove `decode()` calls)
- Modify: `internal/service/spans.go` (minimal — alias views handle `FROM spans`)
- Modify: `internal/service/logs.go` (minimal — alias views handle `FROM logs`)
- Modify: `internal/service/compare.go` (minimal — alias views handle `FROM spans`)
- Modify: `internal/intelligence/detector.go` (4 methods with `read_parquet`)

**Strategy:** Because Task 2 created alias views (`CREATE VIEW spans AS SELECT * FROM lake.spans`), files that only use `FROM spans` / `FROM logs` / `FROM metrics` with clean column names need **no query changes**. Only files that use `read_parquet()` directly with `"name=..."` columns and `SpansGlob`/`LogsGlob`/`MetricsGlob` need rewriting.

- [ ] **Step 1: Update `trace.go` — replace `read_parquet` with DuckLake queries**

The trace query in `trace.go:36-58` changes from:

```go
q := fmt.Sprintf(`
SELECT "name=span_id" as span_id,
       "name=parent_span_id" as parent_span_id,
       "name=service_name" as service,
       ...
FROM read_parquet(%s, union_by_name=true)
WHERE "name=trace_id" = ?
ORDER BY "name=start_unix_nano" ASC
LIMIT 200;
`, s.duck.SpansGlob(tenantID, namespace, window))
```

to:

```go
q := `
SELECT span_id, parent_span_id, service, operation, kind,
       strftime(start_time, '%Y-%m-%dT%H:%M:%SZ') AS start_time,
       duration_ms, status, status_message AS status_msg,
       EXTRACT(EPOCH FROM start_time)::BIGINT * 1000000000 AS start_nano,
       events_json, links_json, trace_state, flags,
       scope_name, scope_version, attributes_json, resource_json
FROM lake.spans
WHERE trace_id = ?
ORDER BY start_time ASC
LIMIT 200`
```

Apply the same pattern to every `read_parquet` call in the file. The correlated logs query similarly changes from `read_parquet` to `FROM lake.logs WHERE trace_id = ?`.

- [ ] **Step 2: Update `diagnose.go`**

This file has ~6 `read_parquet()` calls. Each one follows the same transformation:
- `read_parquet(%s, union_by_name=true)` → `lake.spans` or `lake.logs`
- `"name=service_name"` → `service`
- `"name=duration_ms"` → `duration_ms`
- `"name=status_code"` → `status`
- `"name=start_unix_nano"/1000000` → `start_time` (already a TIMESTAMP)
- Remove `s.duck.SpansGlob(...)` and `s.duck.LogsGlob(...)` calls
- Time filtering: `epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE` → `start_time >= now() - INTERVAL %d MINUTE`

- [ ] **Step 3: Update `find.go`**

This file has 3 `read_parquet()` calls (one each for spans, logs, metrics). Apply the same transformation pattern. Also update `find.go:109`:
```go
// OLD:
filters = append(filters, `json_extract_string(decode("name=attributes_json"), ?) = ?`)
// NEW:
filters = append(filters, `json_extract_string(attributes_json, ?) = ?`)
```

- [ ] **Step 4: Update `ui.go` — replace `read_parquet` and rewrite `Namespaces()`**

This file has ~5 `read_parquet()` calls. Apply the same transformation pattern as trace.go/diagnose.go.

**Critical: Rewrite `Namespaces()` (line 389-416).** This function reads the filesystem directly (`os.ReadDir` on `spans/tenant=.../namespace=...`). DuckLake changes the directory layout, so this must query the database instead:

```go
// Namespaces discovers namespaces from DuckLake data.
func (s *Service) Namespaces(ctx context.Context) []string {
	rows, err := s.duck.DB.QueryContext(ctx, `
		SELECT DISTINCT namespace FROM lake.spans
		WHERE namespace IS NOT NULL AND namespace != '' AND namespace != 'default'
		ORDER BY namespace`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var namespaces []string
	for rows.Next() {
		var ns string
		if rows.Scan(&ns) == nil {
			namespaces = append(namespaces, ns)
		}
	}
	return namespaces
}
```

**Also update the caller** in `internal/ai/orchestrator.go:451`:
```go
// OLD:
namespaces := o.svc.Namespaces(o.cfg.LakeDir, "")
// NEW:
namespaces := o.svc.Namespaces(ctx)
```

The function signature changes from `Namespaces(lakeDir, tenantID string)` to `Namespaces(ctx context.Context)`.

- [ ] **Step 5: Update `metrics.go` — remove `decode()` calls**

DuckLake tables define JSON columns as `VARCHAR`, not `BLOB`. The `decode()` function converts BLOB→VARCHAR, and calling it on a VARCHAR will fail. Remove all `decode()` wrapping:

```go
// OLD (metrics.go:404-406):
CAST(decode(attributes_json) AS VARCHAR) AS attrs,
CAST(decode(hist_bounds_json) AS VARCHAR) AS bounds,
CAST(decode(hist_counts_json) AS VARCHAR) AS counts,

// NEW:
attributes_json AS attrs,
hist_bounds_json AS bounds,
hist_counts_json AS counts,
```

Same for line 499:
```go
// OLD:
CAST(decode(exemplars_json) AS VARCHAR) AS exemplars
// NEW:
exemplars_json AS exemplars
```

- [ ] **Step 6: Update `attributes.go` — remove `decode()` calls**

Same pattern — any `decode(...)` on JSON columns becomes a direct column reference.

- [ ] **Step 7: Verify `spans.go`, `logs.go`, `compare.go` need no changes**

These files use `FROM spans` / `FROM logs` with clean column names. The alias views handle the mapping. Verify no `read_parquet`, `SpansGlob`, `decode()`, or `"name=..."` references exist in these files. If any do, apply the same transformation.

- [ ] **Step 8: Update `internal/intelligence/detector.go`**

This file has **4 methods** that use `read_parquet` + `SpansGlob`/`LogsGlob` + `"name=..."` columns:

- `detectErrorRateAnomalies()` (line 149): `d.duck.SpansGlob(...)` + `read_parquet(%s)` with `"name=service_name"`, `"name=status_code"`, `"name=start_unix_nano"`
- `detectLatencyAnomalies()` (line 226): same pattern
- `detectVolumeAnomalies()` (line 307): same pattern
- `detectLogPatterns()` (line 384): `d.duck.LogsGlob(...)` + `read_parquet(%s)` with `"name=body_template"`, `"name=body"`, `"name=severity"`, `"name=service_name"`, `"name=time_unix_nano"`

Apply the same transformation to all 4 methods:
- `read_parquet(%s, union_by_name=true)` → `lake.spans` or `lake.logs`
- `"name=service_name"` → `service`
- `"name=status_code"` → `status`
- `"name=start_unix_nano"` → `start_time` (TIMESTAMP)
- `"name=time_unix_nano"` → `time` (TIMESTAMP)
- `"name=body_template"` → `body_template`
- `"name=body"` → `body`
- `"name=severity"` → `severity`
- Remove all `SpansGlob` and `LogsGlob` calls
- Time filtering: `epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))` → `start_time`

Also update `isNoDataError()` (line 17): With DuckLake tables, empty tables return zero rows instead of erroring with "No files found". Change to:

```go
func isNoDataError(errMsg string) bool {
	return strings.Contains(errMsg, "No files found that match the pattern") ||
		strings.Contains(errMsg, "does not exist")
}
```

Or better: remove the error check entirely and treat zero rows as the "no data" signal in the calling code.

- [ ] **Step 9: Verify compilation**

```bash
go build ./internal/service/... ./internal/intelligence/... ./internal/ai/...
```

Expected: Success.

- [ ] **Step 10: Commit**

```bash
git add internal/service/
git commit -m "feat: update service layer to query DuckLake tables directly"
```

---

## Task 5: Update MCP, config, main, and remaining consumers

**Files:**
- Modify: `internal/mcp/query.go`
- Modify: `internal/query/schema.go`
- Modify: `internal/query/sql.go`
- Modify: `internal/config/config.go`
- Modify: `internal/intelligence/detector.go`
- Modify: `internal/api/health.go`
- Modify: `cmd/fanout/main.go`

- [ ] **Step 1: Update `internal/mcp/query.go` — schema response**

In `buildSchemaResponse()`, change the Views section to describe DuckLake tables:

```go
func buildSchemaResponse() *SchemaResponse {
	return &SchemaResponse{
		Views: []ViewSchema{
			{
				Name: "lake.spans",
				Columns: []ColumnInfo{
					{Name: "trace_id", Type: "VARCHAR", Description: "Distributed trace identifier"},
					{Name: "span_id", Type: "VARCHAR", Description: "Unique span identifier"},
					{Name: "parent_span_id", Type: "VARCHAR", Description: "Parent span identifier"},
					{Name: "service", Type: "VARCHAR", Description: "Service name"},
					{Name: "namespace", Type: "VARCHAR", Description: "Service namespace"},
					{Name: "operation", Type: "VARCHAR", Description: "Span/operation name"},
					{Name: "kind", Type: "VARCHAR", Description: "Span kind"},
					{Name: "start_time", Type: "TIMESTAMP", Description: "Span start time"},
					{Name: "end_time", Type: "TIMESTAMP", Description: "Span end time"},
					{Name: "duration_ms", Type: "DOUBLE", Description: "Duration in milliseconds"},
					{Name: "status", Type: "VARCHAR", Description: "Status code"},
					{Name: "status_message", Type: "VARCHAR", Description: "Status message"},
					{Name: "attributes_json", Type: "VARCHAR", Description: "Span attributes (JSON)"},
					{Name: "resource_json", Type: "VARCHAR", Description: "Resource attributes (JSON)"},
					{Name: "events_json", Type: "VARCHAR", Description: "Span events (JSON)"},
					{Name: "tenant", Type: "VARCHAR", Description: "Tenant identifier"},
				},
			},
			// ... similar for lake.logs and lake.metrics with clean column names
		},
		// RollupTables stays the same
		RollupTables: []ViewSchema{ /* unchanged */ },
		Macros: []MacroInfo{
			{
				Name:        "attr",
				Signature:   "attr(json_col, key)",
				Description: "Extract JSON key: json_extract_string(json_col, '$.' || key)",
			},
		},
		Examples: []QueryExample{
			{Title: "Error spans", SQL: "SELECT * FROM lake.spans WHERE status = 'STATUS_CODE_ERROR' ORDER BY start_time DESC LIMIT 20"},
			{Title: "Service latency", SQL: "SELECT service, approx_quantile(duration_ms, 0.95) as p95 FROM lake.spans WHERE start_time > now() - INTERVAL 15 MINUTE GROUP BY service"},
			{Title: "Error logs", SQL: "SELECT * FROM lake.logs WHERE severity IN ('ERROR','FATAL') ORDER BY time DESC LIMIT 50"},
			{Title: "Metric names", SQL: "SELECT DISTINCT name, type, unit, service FROM lake.metrics ORDER BY name"},
		},
	}
}
```

- [ ] **Step 2: Update `internal/query/schema.go`**

Replace the entire `schemaTemplate` to describe DuckLake tables with clean column names. Remove all references to `read_parquet`, `hive_partitioning`, `union_by_name`, `"name="` column prefix, and partition columns. The schema becomes much simpler — just list the table columns with their types.

- [ ] **Step 3: Update `internal/query/sql.go` — SQL validation**

In `validateSQL()`:
- Remove the `read_parquet` path validation block (lines 206-221) — users now query `lake.spans` etc., no file paths
- Remove the `fileReaders` block — no file-reading functions needed
- Keep `ATTACH` in the disallowed keywords list (user queries should not attach databases)
- `CheckQueryCost()` patterns `\bSPANS\b`, `\bLOGS\b`, `\bMETRICS\b` still match `lake.spans` etc. (the word boundary catches the substring), so they work without changes. But also add `READ_PARQUET` to the blocked functions list since users should now query tables, not raw files.

- [ ] **Step 4: Update `internal/config/config.go`**

Rename `MaxRows` to `FlushBatchSize` (it now controls the writer's batch flush threshold, not Parquet file size).
Remove `RetentionHours` (DuckLake maintenance handles this via CHECKPOINT).
Keep `FlushSeconds` (still controls the writer's timer-based flush interval).
Keep `RetentionDays` (passed to DuckLake's `expire_older_than` option).

```go
type Config struct {
	HTTPAddr       string
	OTLPGRPCAddr   string
	LakeDir        string
	FlushSeconds   int       // 15 — writer timer-based flush interval
	FlushBatchSize int       // 50000 — writer batch size flush threshold
	APIToken       string
	RollupEvery    int       // seconds
	MCPEnabled     bool
	RetentionDays  int       // passed to DuckLake expire_older_than
	TenantID       uuid.UUID
	DefaultNS      string
	DuckDBMemory   string
	// Alerting
	AlertEnabled      bool
	AlertEvalInterval int
	AlertHistoryDays  int
	// AI chat
	AIProvider string
	AIAPIKey   string
	AIModel    string
	AIBaseURL  string
}
```

Update `Load()`: rename `MAX_ROWS` env var to `FLUSH_BATCH_SIZE` (keep `MAX_ROWS` as a deprecated alias for backward compat), remove `RETENTION_HOURS`.
Update `Validate()`: rename the `MaxRows` check to `FlushBatchSize`, remove `RetentionHours` check.

- [ ] **Step 5: Update `internal/config/config_test.go`**

This file tests `MaxRows` and `RetentionHours` directly:
- Line 111: `cfg.MaxRows != 50000` → `cfg.FlushBatchSize != 50000`
- Line 128: `MaxRows: 50000` → `FlushBatchSize: 50000`
- Line 131: Remove `RetentionHours: 1`
- Lines 143-149: Update validation tests to use `FlushBatchSize` instead of `MaxRows`, remove `RetentionHours` validation tests

- [ ] **Step 6: Update `internal/intelligence/detector.go` — `isNoDataError()` only**

The bulk of `detector.go` changes (4 methods with `read_parquet`) are handled in Task 4 Step 8. Here, only update `isNoDataError()`:

With DuckLake, empty tables return zero rows instead of erroring with "No files found". The function can be simplified or kept as a safety net. Add the DuckLake error pattern:

```go
func isNoDataError(errMsg string) bool {
	return strings.Contains(errMsg, "No files found that match the pattern") ||
		strings.Contains(errMsg, "does not exist")
}
```

- [ ] **Step 6: Update `internal/api/health.go`**

The readiness check verifies DuckDB connectivity and lake directory existence. Update it to also verify DuckLake attachment:

```go
// In the readiness check, run:
var n int
err := q.DB.QueryRow("SELECT COUNT(*) FROM lake.information_schema.tables WHERE table_name = 'spans'").Scan(&n)
```

This confirms the DuckLake catalog is attached and accessible.

- [ ] **Step 7: Rewrite `cmd/fanout/main.go`**

Key changes:
- Remove `lake.CleanupTempFiles(cfg.LakeDir)` call
- Initialize DuckDB first (before the writer, since writer now needs `*sql.DB`)
- Pass `q.DB` to `lake.NewWriter()`
- Remove `lake.NewPruner(cfg)` and `go pruner.Run(ctx)`
- Remove `lake.NewCompactor(cfg, q.DB)` and `go compactor.Run(ctx)`
- Add `go q.RunMaintenance(ctx)` for DuckLake housekeeping

```go
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	cfg := config.Load()

	if err := os.MkdirAll(cfg.LakeDir, 0o755); err != nil {
		slog.Error("create lake dir failed", "err", err)
		os.Exit(1)
	}

	// Channels for ingest -> lake writer
	chSpans := make(chan lake.SpanRow, 10000)
	chLogs := make(chan lake.LogRow, 10000)
	chMetrics := make(chan lake.MetricRow, 10000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize query cache (extracted from deleted perf.go into cache.go)
	query.InitQueryCache(ctx)

	errCh := make(chan error, 3)

	// Start DuckDB + DuckLake (must be before writer — writer needs db)
	q, err := query.NewDuck(ctx, cfg)
	if err != nil {
		slog.Error("duckdb init failed", "err", err)
		os.Exit(1)
	}
	defer q.Close()

	// Start Lake Writer (uses DuckDB for inserts)
	writer := lake.NewWriter(cfg, q.DB, chSpans, chLogs, chMetrics)
	go func() {
		if err := writer.Run(ctx); err != nil {
			errCh <- fmt.Errorf("lake writer: %w", err)
		}
	}()

	go q.RunRollups(ctx)
	go q.RunMaintenance(ctx)

	// ... rest of main.go stays the same (intelligence, alerts, grpc, http, etc.)
	// but remove: lake.CleanupTempFiles(), lake.NewPruner(), lake.NewCompactor()
}
```

- [ ] **Step 8: Verify full compilation**

```bash
go build ./cmd/fanout
```

Expected: Success.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat: update MCP schema, config, main wiring for DuckLake"
```

---

## Task 6: Update tests

**Files:**
- Rewrite: `internal/lake/writer_test.go`
- Modify: `internal/query/duck_test.go` — remove `TestParquetGlob_DayLevel` and glob-related tests
- Modify: `internal/query/schema_test.go` — update assertion from `"read_parquet"` to table names
- Modify: `internal/config/config_test.go` — already handled in Task 5 Step 5
- Modify: service layer test files — INSERT test data into DuckLake instead of writing Parquet files
- Modify: MCP handler tests — verify updated schema response
- Delete: `internal/query/views_test.go`, `internal/query/perf_test.go` (already deleted in Task 2)
- Delete: `internal/lake/compact_test.go`, `internal/lake/retention_test.go` (already deleted in Task 3)

- [ ] **Step 1: Rewrite `internal/lake/writer_test.go`**

Tests should:
- Create a DuckDB with DuckLake attached using a temp directory
- Send rows through channels
- Verify rows appear in DuckLake tables via `SELECT COUNT(*)`
- Test flush-on-threshold and flush-on-timer behaviors
- Reference `cfg.FlushBatchSize` (not `cfg.MaxRows`)

```go
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		stmts := []string{
			"INSTALL ducklake",
			"INSTALL sqlite",
			fmt.Sprintf("ATTACH 'ducklake:sqlite:%s/catalog.sqlite' AS lake (DATA_PATH '%s/data/')", tmpDir, tmpDir),
		}
		for _, s := range stmts {
			if _, err := execer.ExecContext(context.Background(), s, nil); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })

	// Create tables (same DDL as production duck.go createTables)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS lake.spans (trace_id VARCHAR, span_id VARCHAR, ...)`,
		`CREATE TABLE IF NOT EXISTS lake.logs (time TIMESTAMP, severity VARCHAR, ...)`,
		`CREATE TABLE IF NOT EXISTS lake.metrics (time TIMESTAMP, name VARCHAR, ...)`,
	} {
		_, err = db.Exec(ddl)
		require.NoError(t, err)
	}
	return db
}
```

- [ ] **Step 2: Update `internal/query/duck_test.go`**

- Remove `TestParquetGlob_DayLevel` and any other tests that call `ParquetGlob`, `SpansGlob`, `LogsGlob`, `MetricsGlob`
- Update tests that verify rollup behavior to use DuckLake setup
- Add test for `RunMaintenance` not erroring on empty tables

- [ ] **Step 3: Update `internal/query/schema_test.go`**

Line 32 asserts `"read_parquet"` appears in schema output. Change to assert DuckLake table references (e.g., `"lake.spans"` or `"spans"` depending on the new schema template).

- [ ] **Step 4: Update service layer tests**

Service tests that set up test data via Parquet files should instead INSERT test data directly into DuckLake tables:
```sql
INSERT INTO lake.spans (trace_id, span_id, service, ...) VALUES (?, ?, ?, ...)
```
The query assertions stay the same since column names match (alias views ensure `FROM spans` still works).

- [ ] **Step 5: Update MCP handler tests**

Tests for the `query` MCP tool should verify the updated schema response references `lake.spans`, `lake.logs`, `lake.metrics` and uses clean column names (no `"name=..."` prefix).

- [ ] **Step 6: Run all tests**

```bash
go test ./... -count=1
```

Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "test: update all tests for DuckLake migration"
```

---

## Task 7: Data migration utility

**Files:**
- Create: `cmd/migrate-to-ducklake/main.go`

For existing deployments that have Parquet data in the old layout, provide a one-shot migration tool.

- [ ] **Step 1: Write migration tool**

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/duckdb/duckdb-go/v2"
)

func main() {
	lakeDir := os.Getenv("LAKE_DIR")
	if lakeDir == "" {
		lakeDir = "./lake"
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	catalogPath := filepath.Join(lakeDir, "catalog.sqlite")
	dataPath := filepath.Join(lakeDir, "data")

	// Check if old data exists
	for _, signal := range []string{"spans", "logs", "metrics"} {
		pattern := filepath.Join(lakeDir, signal, "tenant=*", "namespace=*", "year=*", "month=*", "day=*", "hour=*", "*.parquet")
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			slog.Info("no old data found", "signal", signal)
			continue
		}
		slog.Info("found old data", "signal", signal, "files", len(matches))
	}

	// Connect to DuckDB with DuckLake
	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		stmts := []string{
			"INSTALL ducklake",
			"INSTALL sqlite",
			fmt.Sprintf("ATTACH 'ducklake:sqlite:%s' AS lake (DATA_PATH '%s/')", catalogPath, dataPath),
		}
		for _, s := range stmts {
			if _, err := execer.ExecContext(context.Background(), s, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("connect failed", "err", err)
		os.Exit(1)
	}
	db := sql.OpenDB(connector)
	defer db.Close()

	// Create tables (same DDL as fanout)
	// ... (same CREATE TABLE statements as duck.go)

	// Migrate each signal with full column mapping (old "name=..." → new clean names)
	migrateSpans(db, lakeDir)
	migrateLogs(db, lakeDir)
	migrateMetrics(db, lakeDir)

	slog.Info("migration complete")
}

func migrateSpans(db *sql.DB, lakeDir string) {
	glob := filepath.Join(lakeDir, "spans", "tenant=*", "namespace=*", "year=*", "month=*", "day=*", "hour=*", "*.parquet")
	q := fmt.Sprintf(`
INSERT INTO lake.spans (
  trace_id, span_id, parent_span_id, service, operation, kind,
  start_time, end_time, duration_ms, status, status_message,
  resource_json, attributes_json, events_json, links_json,
  trace_state, flags, scope_name, scope_version, ingested_at,
  http_method, http_status_code, http_route, db_system,
  rpc_method, rpc_service, peer_service,
  service_version, deployment_env, exception_type, exception_message,
  namespace, tenant
)
SELECT
  "name=trace_id", "name=span_id", "name=parent_span_id", "name=service_name", "name=name", "name=kind",
  to_timestamp("name=start_unix_nano" / 1e9),
  to_timestamp("name=end_unix_nano" / 1e9),
  "name=duration_ms", "name=status_code", "name=status_msg",
  decode("name=resource_json"), decode("name=attributes_json"),
  decode("name=events_json"), decode("name=links_json"),
  "name=trace_state", "name=flags", "name=scope_name", "name=scope_version",
  to_timestamp("name=ingested_unix_nano" / 1e9),
  "name=attr_http_method", "name=attr_http_status_code", "name=attr_http_route", "name=attr_db_system",
  "name=attr_rpc_method", "name=attr_rpc_service", "name=attr_peer_service",
  "name=res_service_version", "name=res_deployment_env",
  "name=exc_type", "name=exc_message",
  namespace, tenant
FROM read_parquet('%s', union_by_name=true, hive_partitioning=true)
WHERE tenant != '_schema'`, glob)
	slog.Info("migrating spans")
	res, err := db.Exec(q)
	if err != nil {
		slog.Error("spans migration failed", "err", err)
		return
	}
	n, _ := res.RowsAffected()
	slog.Info("spans migrated", "rows", n)
}

func migrateLogs(db *sql.DB, lakeDir string) {
	glob := filepath.Join(lakeDir, "logs", "tenant=*", "namespace=*", "year=*", "month=*", "day=*", "hour=*", "*.parquet")
	q := fmt.Sprintf(`
INSERT INTO lake.logs (
  time, observed_time, severity, severity_number, body, service,
  trace_id, span_id, flags, resource_json, attributes_json,
  scope_name, scope_version, ingested_at, body_template,
  namespace, tenant
)
SELECT
  to_timestamp("name=time_unix_nano" / 1e9),
  to_timestamp("name=observed_time_unix_nano" / 1e9),
  "name=severity", "name=severity_number", "name=body", "name=service_name",
  "name=trace_id", "name=span_id", "name=flags",
  decode("name=resource_json"), decode("name=attributes_json"),
  "name=scope_name", "name=scope_version",
  to_timestamp("name=ingested_unix_nano" / 1e9),
  "name=body_template",
  namespace, tenant
FROM read_parquet('%s', union_by_name=true, hive_partitioning=true)
WHERE tenant != '_schema'`, glob)
	slog.Info("migrating logs")
	res, err := db.Exec(q)
	if err != nil {
		slog.Error("logs migration failed", "err", err)
		return
	}
	n, _ := res.RowsAffected()
	slog.Info("logs migrated", "rows", n)
}

func migrateMetrics(db *sql.DB, lakeDir string) {
	glob := filepath.Join(lakeDir, "metrics", "tenant=*", "namespace=*", "year=*", "month=*", "day=*", "hour=*", "*.parquet")
	q := fmt.Sprintf(`
INSERT INTO lake.metrics (
  time, name, description, unit, type, service, value,
  hist_bounds_json, hist_counts_json, hist_count, hist_sum,
  exemplars_json, attributes_json, resource_json,
  scope_name, scope_version, ingested_at,
  namespace, tenant
)
SELECT
  to_timestamp("name=time_unix_nano" / 1e9),
  "name=name", "name=description", "name=unit", "name=mtype", "name=service_name", "name=value",
  decode("name=hist_bounds_json"), decode("name=hist_counts_json"),
  "name=hist_count", "name=hist_sum",
  decode("name=exemplars_json"), decode("name=attributes_json"), decode("name=resource_json"),
  "name=scope_name", "name=scope_version",
  to_timestamp("name=ingested_unix_nano" / 1e9),
  namespace, tenant
FROM read_parquet('%s', union_by_name=true, hive_partitioning=true)
WHERE tenant != '_schema'`, glob)
	slog.Info("migrating metrics")
	res, err := db.Exec(q)
	if err != nil {
		slog.Error("metrics migration failed", "err", err)
		return
	}
	n, _ := res.RowsAffected()
	slog.Info("metrics migrated", "rows", n)
}
```

The `WHERE tenant != '_schema'` filter excludes the old sentinel/placeholder rows.

- [ ] **Step 2: Verify migration tool compiles**

```bash
go build ./cmd/migrate-to-ducklake
```

- [ ] **Step 3: Commit**

```bash
git add cmd/migrate-to-ducklake/
git commit -m "feat: add one-shot migration tool for existing Parquet data to DuckLake"
```

---

## Task 8: Cleanup and final verification

**Files:**
- Modify: `CLAUDE.md` (update architecture docs)
- Modify: `ARCHITECTURE.md` (remove `read_parquet` references)
- Modify: `justfile` (update build/run targets if needed)

- [ ] **Step 1: Remove stale references**

Search the entire codebase for any remaining references to removed concepts:

```bash
grep -r "read_parquet\|ParquetGlob\|SpansGlob\|LogsGlob\|MetricsGlob\|union_by_name\|hive_partitioning\|parquet-go\|compact\|Compactor\|Pruner\|CleanupTempFiles\|_placeholder\|_schema.parquet\|MaxRows\|RetentionHours" --include="*.go" .
```

Fix any remaining references. Also check markdown files:

```bash
grep -r "read_parquet\|hive_partitioning\|union_by_name" --include="*.md" .
```

- [ ] **Step 2: Run full test suite**

```bash
go test ./... -count=1 -race
```

Expected: All tests pass with no races.

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/fanout
./fanout &
# Send test data via OTLP and verify it appears in queries
```

- [ ] **Step 4: Update CLAUDE.md architecture**

Update the Architecture section to reflect DuckLake:
- Storage section: "DuckLake (SQLite catalog + Parquet data files)" instead of "Partitioned Parquet Files"
- Remove references to Hive partitioning, glob patterns, compaction, retention pruner
- Update the Data Flow diagram
- Update the Directory Structure (remove `lake/spans/`, `lake/logs/`, `lake/metrics/` — now `lake/data/`)
- Update Configuration table (rename `MAX_ROWS` to `FLUSH_BATCH_SIZE`, remove `RETENTION_HOURS`)
- Update Dependencies table (remove `parquet-go`, add `ducklake` extension)

- [ ] **Step 5: Update ARCHITECTURE.md**

Remove `read_parquet` reference and update any architecture diagrams or descriptions to reference DuckLake tables.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: cleanup stale references, update docs for DuckLake migration"
```
