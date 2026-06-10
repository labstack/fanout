package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	_ "modernc.org/sqlite" // SQLite driver used to put the DuckLake catalog in WAL mode

	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/metrics"
)

type Duck struct {
	DB              *sql.DB
	cfg             env.Config
	lastMaintenance time.Time
	// rollupLagNanos holds the rollup watermark back from the max ingested
	// timestamp so late/out-of-order commits aren't skipped. Zero disables the
	// lag (no trailing window).
	rollupLagNanos int64
	// writeMu serializes write commits (rollups, maintenance, and ingest appender
	// flushes) so that, when the pool holds more than one connection, two
	// connections never commit to the DuckLake SQLite catalog concurrently.
	writeMu sync.Mutex
}

const (
	serviceRollupStateKey  = "service_rollup_v2"
	serviceRollupRawMaxKey = "service_rollup_v2_rawmax"
	edgeRollupStateKey     = "edge_rollup_v2"
	edgeRollupRawMaxKey    = "edge_rollup_v2_rawmax"
	defaultDuckDBPoolSize  = 1
)

// WriteLock returns the shared write-serialization mutex. The ingest writer must
// hold it around appender flushes so writes never overlap rollup/maintenance
// commits on a multi-connection pool.
func (d *Duck) WriteLock() *sync.Mutex { return &d.writeMu }

// duckDBPoolSize is the effective connection-pool size: the configured value,
// floored at 1.
func duckDBPoolSize(cfg env.Config) int {
	if cfg.DuckDBMaxConns < 1 {
		return 1
	}
	return cfg.DuckDBMaxConns
}

func NewDuck(ctx context.Context, cfg env.Config) (*Duck, error) {
	if err := os.MkdirAll(cfg.QueryDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create query dir: %w", err)
	}
	if err := os.MkdirAll(cfg.TelemetryDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create telemetry dir: %w", err)
	}

	dbPath := cfg.QueryDuckDBPath()
	tempDir := cfg.QueryTempDir()
	metadataPath := cfg.TelemetryDuckLakePath()
	dataPath := cfg.TelemetryParquetDir()
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		return nil, fmt.Errorf("create telemetry parquet dir: %w", err)
	}

	// Put the DuckLake SQLite catalog in WAL mode before DuckDB attaches it, so
	// read queries run concurrently with the single writer instead of failing
	// with "database is locked" (rollback-journal mode), and a crashed writer
	// can't leave the catalog permanently locked. Must run before openDuckDB.
	if err := enableCatalogWAL(metadataPath); err != nil {
		return nil, fmt.Errorf("enable WAL on DuckLake catalog: %w", err)
	}

	mem := cfg.DuckDBMemory
	if mem == "" {
		mem = "512MB"
	}
	if strings.ContainsAny(mem, "&?'\"\\; ") {
		return nil, fmt.Errorf("invalid DUCKDB_MEMORY value: %q", mem)
	}
	dsn := dbPath + "?threads=4&memory_limit=" + mem

	db, err := openDuckDB(ctx, dsn, tempDir, metadataPath, dataPath, duckDBPoolSize(cfg))
	if err != nil {
		return nil, fmt.Errorf(
			"open duckdb catalog: %w (if the local cache catalog is corrupted, remove %s and %s; DuckLake data remains in %s)",
			err,
			dbPath,
			dbPath+".wal",
			dataPath,
		)
	}

	d := &Duck{DB: db, cfg: cfg, rollupLagNanos: rollupLagFromConfig(cfg)}
	if err := CreateTables(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := CreateViews(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create views: %w", err)
	}
	return d, nil
}

func openDuckDB(ctx context.Context, dsn, tempDir, metadataPath, dataPath string, maxConns int) (*sql.DB, error) {
	// AUTOMATIC_MIGRATION upgrades an older on-disk DuckLake catalog to the format
	// the loaded extension requires. Without it, a fanout build that bundles a
	// newer DuckLake (e.g. the DuckDB 1.5.3 bump, which needs catalog v1.0) fails
	// to attach an existing v0.4 catalog and the server can't boot. Migration is
	// in place and forward-only.
	attach := fmt.Sprintf("ATTACH IF NOT EXISTS %s AS lake (DATA_PATH %s, AUTOMATIC_MIGRATION true)",
		sqlLiteral("ducklake:sqlite:"+metadataPath),
		sqlLiteral(dataPath))

	// temp_directory is an instance-global setting: re-setting it after the temp
	// dir has already been used fails with "Cannot switch temporary directory
	// after the current one has been used". The boot hook runs once per pooled
	// connection, so only the first connection sets it; the rest inherit the
	// instance-wide value. LOAD/ATTACH stay per-connection — they're idempotent.
	var tempDirOnce sync.Once
	var tempDirErr error

	connector, err := duckdb.NewConnector(dsn, func(execer driver.ExecerContext) error {
		for _, stmt := range []string{"LOAD ducklake", "LOAD sqlite"} {
			if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
				return err
			}
		}
		tempDirOnce.Do(func() {
			_, tempDirErr = execer.ExecContext(ctx, "SET temp_directory="+sqlLiteral(tempDir), nil)
		})
		if tempDirErr != nil {
			return tempDirErr
		}
		if _, err := execer.ExecContext(ctx, attach, nil); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	db := sql.OpenDB(connector)
	// DuckLake metadata lives in a SQLite catalog that locks under *concurrent*
	// commits from multiple connections. The default pool of 1 serializes
	// everything through one handle. Larger pools are allowed (read queries then
	// run concurrently), but write commits must still be serialized by the
	// caller's write mutex (Duck.WriteLock) so two connections never commit at
	// once.
	if maxConns < 1 {
		maxConns = 1
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	return db, nil
}

// enableCatalogWAL switches the DuckLake SQLite catalog at metadataPath to WAL
// journal mode before DuckDB attaches it. The default rollback-journal mode
// makes a committing writer take an exclusive lock that fails concurrent readers
// with "database is locked", and a crashed or stuck writer can leave a hot
// journal that locks the catalog until every connection is dropped (observed in
// prod as a multi-day write+query outage). WAL lets readers run concurrently
// with the single writer and recovers cleanly after a crash. journal_mode is
// persisted in the database header, so this is effectively a one-time migration,
// but it is cheap and idempotent to assert on every boot. The DuckDB sqlite
// extension exposes no busy_timeout knob, so the retry timeout is set here on
// the bootstrap connection; the persisted WAL mode is what carries over to the
// connections DuckDB opens.
func enableCatalogWAL(metadataPath string) error {
	db, err := sql.Open("sqlite", metadataPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("catalog journal_mode = %q, want wal", mode)
	}
	return nil
}

func sqlLiteral(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func (d *Duck) Close() error { return d.DB.Close() }

// DefaultNamespace returns empty string so queries search all namespaces.
func (d *Duck) DefaultNamespace() string {
	return ""
}

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
	affected := int64(0)

	n, err := d.refreshServiceRollup(ctx)
	if err != nil {
		return 0, err
	}
	affected += n

	n, err = d.refreshEdgeRollup(ctx)
	if err != nil {
		return 0, err
	}
	affected += n

	if err := d.runMaintenance(ctx); err != nil {
		slog.Warn("ducklake maintenance failed", "err", err)
	}

	return int(affected), nil
}

func (d *Duck) refreshServiceRollup(ctx context.Context) (int64, error) {
	// Serialize against other writers (edge rollup, maintenance, ingest flushes).
	// writeMu is always acquired before a connection to keep lock ordering
	// consistent and deadlock-free.
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	lastWatermark, err := rollupWatermark(ctx, tx, serviceRollupStateKey)
	if err != nil {
		return 0, err
	}
	lastRawMax, err := rollupWatermark(ctx, tx, serviceRollupRawMaxKey)
	if err != nil {
		return 0, err
	}

	rawWatermark, err := maxServiceRollupWatermark(ctx, tx)
	if err != nil {
		return 0, err
	}
	if rawWatermark <= lastWatermark {
		return 0, tx.Commit()
	}

	// The affected scan below uses lastWatermark as its lower bound. Choosing the
	// stored watermark:
	//   - While the max is advancing (new ingest), hold the stored watermark a
	//     lag behind the raw max so the next pass reprocesses a trailing window —
	//     ingested_unix_nano is stamped at ingest but signals flush independently
	//     and the writer retries, so a row can commit below the current max and
	//     must still be picked up (delete+insert is idempotent per bucket).
	//   - Once the max plateaus (no new ingest), we've reprocessed that trailing
	//     window once already, so advance straight to the raw max and let the next
	//     cycle short-circuit. Otherwise a burst's trailing window would be
	//     re-aggregated every cycle forever after ingest goes quiet.
	newWatermark := rawWatermark
	if rawWatermark > lastRawMax {
		newWatermark = rawWatermark - d.rollupSafetyLagNanos()
		if newWatermark < lastWatermark {
			newWatermark = lastWatermark
		}
	}

	args := []any{lastWatermark, lastWatermark, lastWatermark}
	if _, err := tx.ExecContext(ctx, serviceRollupDeleteSQL, args...); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, serviceRollupInsertSQL, args...)
	if err != nil {
		return 0, err
	}

	if err := storeRollupWatermark(ctx, tx, serviceRollupStateKey, newWatermark); err != nil {
		return 0, err
	}
	if err := storeRollupWatermark(ctx, tx, serviceRollupRawMaxKey, rawWatermark); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	if rows, err := res.RowsAffected(); err == nil {
		return rows, nil
	}
	return 0, nil
}

func (d *Duck) refreshEdgeRollup(ctx context.Context) (int64, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	lastWatermark, err := rollupWatermark(ctx, tx, edgeRollupStateKey)
	if err != nil {
		return 0, err
	}
	lastRawMax, err := rollupWatermark(ctx, tx, edgeRollupRawMaxKey)
	if err != nil {
		return 0, err
	}

	rawWatermark, err := maxEdgeRollupWatermark(ctx, tx)
	if err != nil {
		return 0, err
	}
	if rawWatermark <= lastWatermark {
		return 0, tx.Commit()
	}
	// Same trailing-window logic as the service rollup (see refreshServiceRollup):
	// a child span can commit after its parent's bucket watermark advanced, or
	// after a retry, so keep a window open while the max advances, then close it
	// once ingest plateaus to avoid re-aggregating a burst's tail every cycle.
	newWatermark := rawWatermark
	if rawWatermark > lastRawMax {
		newWatermark = rawWatermark - d.rollupSafetyLagNanos()
		if newWatermark < lastWatermark {
			newWatermark = lastWatermark
		}
	}

	if _, err := tx.ExecContext(ctx, edgeRollupDeleteSQL, lastWatermark); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, edgeRollupInsertSQL, lastWatermark)
	if err != nil {
		return 0, err
	}

	if err := storeRollupWatermark(ctx, tx, edgeRollupStateKey, newWatermark); err != nil {
		return 0, err
	}
	if err := storeRollupWatermark(ctx, tx, edgeRollupRawMaxKey, rawWatermark); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	if rows, err := res.RowsAffected(); err == nil {
		return rows, nil
	}
	return 0, nil
}

func rollupWatermark(ctx context.Context, tx *sql.Tx, key string) (int64, error) {
	var watermark int64
	err := tx.QueryRowContext(ctx, `
SELECT COALESCE(last_ingested_unix_nano, 0)
FROM rollup_state
WHERE cache_key = ?`, key).Scan(&watermark)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return watermark, err
}

func storeRollupWatermark(ctx context.Context, tx *sql.Tx, key string, watermark int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO rollup_state (cache_key, last_ingested_unix_nano, updated_at)
VALUES (?, ?, now())
ON CONFLICT (cache_key) DO UPDATE
SET last_ingested_unix_nano = excluded.last_ingested_unix_nano,
    updated_at = excluded.updated_at`, key, watermark)
	return err
}

// rollupSafetyLagNanos is how far behind the max ingested timestamp the rollup
// watermark is held, covering the worst-case delay between a row being stamped at
// ingest and committed to the lake (normal flush latency plus a retry or two).
func (d *Duck) rollupSafetyLagNanos() int64 {
	return d.rollupLagNanos
}

// rollupLagFromConfig derives the watermark safety lag from the flush interval:
// two flush cycles, with a 30s floor.
func rollupLagFromConfig(cfg env.Config) int64 {
	lag := 2 * time.Duration(cfg.FlushSeconds) * time.Second
	if lag < 30*time.Second {
		lag = 30 * time.Second
	}
	return lag.Nanoseconds()
}

func maxServiceRollupWatermark(ctx context.Context, tx *sql.Tx) (int64, error) {
	var watermark int64
	err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(v), 0)::BIGINT
FROM (
  SELECT COALESCE(MAX(ingested_unix_nano), 0) AS v FROM spans
  UNION ALL
  SELECT COALESCE(MAX(ingested_unix_nano), 0) AS v FROM logs
  UNION ALL
  SELECT COALESCE(MAX(ingested_unix_nano), 0) AS v FROM metrics
) maxes`).Scan(&watermark)
	return watermark, err
}

func maxEdgeRollupWatermark(ctx context.Context, tx *sql.Tx) (int64, error) {
	var watermark int64
	err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(ingested_unix_nano), 0)::BIGINT
FROM spans`).Scan(&watermark)
	return watermark, err
}

const serviceRollupDeleteSQL = `
WITH affected AS (
  SELECT DISTINCT namespace, date_trunc('minute', start_time) AS bucket, service
  FROM spans
  WHERE ingested_unix_nano > ?
    AND start_time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM logs
  WHERE ingested_unix_nano > ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM metrics
  WHERE ingested_unix_nano > ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
)
DELETE FROM service_rollup
WHERE EXISTS (
  SELECT 1
  FROM affected
  WHERE affected.namespace = service_rollup.namespace
    AND affected.bucket = service_rollup.bucket
    AND affected.service = service_rollup.service
);`

const serviceRollupInsertSQL = `
WITH affected AS (
  SELECT DISTINCT namespace, date_trunc('minute', start_time) AS bucket, service
  FROM spans
  WHERE ingested_unix_nano > ?
    AND start_time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM logs
  WHERE ingested_unix_nano > ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM metrics
  WHERE ingested_unix_nano > ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
),
span_agg AS (
  SELECT
    s.namespace,
    date_trunc('minute', s.start_time) AS bucket,
    s.service,
    COUNT(*) AS spans,
    quantile_cont(s.duration_ms, 0.50) AS p50_ms,
    quantile_cont(s.duration_ms, 0.95) AS p95_ms,
    avg(CASE WHEN s.status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time)
   AND a.service = s.service
  GROUP BY s.namespace, date_trunc('minute', s.start_time), s.service
),
log_agg AS (
  SELECT
    l.namespace,
    date_trunc('minute', l.time) AS bucket,
    l.service,
    COUNT(*) AS log_count
  FROM logs l
  JOIN affected a
    ON a.namespace = l.namespace
   AND a.bucket = date_trunc('minute', l.time)
   AND a.service = l.service
  GROUP BY l.namespace, date_trunc('minute', l.time), l.service
),
metric_agg AS (
  SELECT
    m.namespace,
    date_trunc('minute', m.time) AS bucket,
    m.service,
    COUNT(DISTINCT m.name) AS metric_count
  FROM metrics m
  JOIN affected a
    ON a.namespace = m.namespace
   AND a.bucket = date_trunc('minute', m.time)
   AND a.service = m.service
  GROUP BY m.namespace, date_trunc('minute', m.time), m.service
)
INSERT INTO service_rollup (
  namespace,
  bucket,
  service,
  spans,
  p50_ms,
  p95_ms,
  error_rate,
  log_count,
  metric_count
)
SELECT
  a.namespace,
  a.bucket,
  a.service,
  COALESCE(s.spans, 0),
  COALESCE(s.p50_ms, 0),
  COALESCE(s.p95_ms, 0),
  COALESCE(s.error_rate, 0),
  COALESCE(l.log_count, 0),
  COALESCE(m.metric_count, 0)
FROM affected a
LEFT JOIN span_agg s USING (namespace, bucket, service)
LEFT JOIN log_agg l USING (namespace, bucket, service)
LEFT JOIN metric_agg m USING (namespace, bucket, service);`

const edgeRollupDeleteSQL = `
WITH affected AS (
  SELECT DISTINCT namespace, date_trunc('minute', start_time) AS bucket
  FROM spans
  WHERE ingested_unix_nano > ?
    AND start_time IS NOT NULL
)
DELETE FROM edge_rollup
WHERE EXISTS (
  SELECT 1
  FROM affected
  WHERE affected.namespace = edge_rollup.namespace
    AND affected.bucket = edge_rollup.bucket
);`

const edgeRollupInsertSQL = `
WITH affected AS (
  SELECT DISTINCT namespace, date_trunc('minute', start_time) AS bucket
  FROM spans
  WHERE ingested_unix_nano > ?
    AND start_time IS NOT NULL
),
call_edges AS (
  SELECT
    child.namespace,
    date_trunc('minute', child.start_time) AS bucket,
    parent.service AS caller,
    child.service AS callee,
    COUNT(*) AS calls,
    AVG(child.duration_ms) AS avg_ms,
    AVG(CASE WHEN child.status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate,
    'call' AS edge_type
  FROM spans child
  JOIN spans parent
    ON child.parent_span_id = parent.span_id
   AND child.trace_id = parent.trace_id
   AND child.namespace = parent.namespace
  JOIN affected a
    ON a.namespace = child.namespace
   AND a.bucket = date_trunc('minute', child.start_time)
  WHERE parent.service IS NOT NULL
    AND parent.service != ''
    AND child.service IS NOT NULL
    AND child.service != ''
    AND parent.service != child.service
  GROUP BY child.namespace, date_trunc('minute', child.start_time), parent.service, child.service
),
producers AS (
  SELECT
    s.namespace,
    date_trunc('minute', s.start_time) AS bucket,
    s.service,
    json_extract_string(s.attributes_json, '$."messaging.destination.name"') AS destination,
    json_extract_string(s.attributes_json, '$."messaging.system"') AS msg_system
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time)
  WHERE s.kind = 'SPAN_KIND_PRODUCER'
    AND s.service IS NOT NULL
    AND s.service != ''
    AND json_extract_string(s.attributes_json, '$."messaging.destination.name"') IS NOT NULL
),
consumers AS (
  SELECT
    s.namespace,
    date_trunc('minute', s.start_time) AS bucket,
    s.service,
    json_extract_string(s.attributes_json, '$."messaging.destination.name"') AS destination,
    json_extract_string(s.attributes_json, '$."messaging.system"') AS msg_system
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time)
  WHERE s.kind = 'SPAN_KIND_CONSUMER'
    AND s.service IS NOT NULL
    AND s.service != ''
    AND json_extract_string(s.attributes_json, '$."messaging.destination.name"') IS NOT NULL
),
messaging_edges AS (
  SELECT
    p.namespace,
    p.bucket,
    p.service AS caller,
    c.service AS callee,
    COUNT(*) AS calls,
    0.0 AS avg_ms,
    0.0 AS error_rate,
    'messaging' AS edge_type
  FROM producers p
  JOIN consumers c
    ON c.namespace = p.namespace
   AND c.bucket = p.bucket
   AND c.destination = p.destination
   AND c.msg_system = p.msg_system
  WHERE p.service != c.service
  GROUP BY p.namespace, p.bucket, p.service, c.service
)
INSERT INTO edge_rollup (
  namespace,
  bucket,
  caller,
  callee,
  calls,
  avg_ms,
  error_rate,
  edge_type
)
SELECT namespace, bucket, caller, callee, calls, avg_ms, error_rate, edge_type
FROM call_edges
UNION ALL
SELECT namespace, bucket, caller, callee, calls, avg_ms, error_rate, edge_type
FROM messaging_edges;`

func (d *Duck) runMaintenance(ctx context.Context) error {
	if !d.lastMaintenance.IsZero() && time.Since(d.lastMaintenance) < time.Hour {
		return nil
	}

	// Retention deletes and the checkpoint are writes — serialize them too.
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	var errs []error
	if d.cfg.RetentionDays > 0 {
		stmts := []struct {
			name string
			sql  string
		}{
			{name: "lake.spans", sql: fmt.Sprintf("DELETE FROM lake.spans WHERE start_time < now() - INTERVAL %d DAY", d.cfg.RetentionDays)},
			{name: "lake.logs", sql: fmt.Sprintf("DELETE FROM lake.logs WHERE log_time < now() - INTERVAL %d DAY", d.cfg.RetentionDays)},
			{name: "lake.metrics", sql: fmt.Sprintf("DELETE FROM lake.metrics WHERE metric_time < now() - INTERVAL %d DAY", d.cfg.RetentionDays)},
			{name: "service_rollup", sql: fmt.Sprintf("DELETE FROM service_rollup WHERE bucket < now() - INTERVAL %d DAY", d.cfg.RetentionDays)},
			{name: "edge_rollup", sql: fmt.Sprintf("DELETE FROM edge_rollup WHERE bucket < now() - INTERVAL %d DAY", d.cfg.RetentionDays)},
		}
		for _, stmt := range stmts {
			res, err := d.DB.ExecContext(ctx, stmt.sql)
			if err != nil {
				slog.Error("maintenance delete failed", "table", stmt.name, "err", err)
				errs = append(errs, fmt.Errorf("%s retention delete: %w", stmt.name, err))
				continue
			}
			rows, rowsErr := res.RowsAffected()
			if rowsErr != nil {
				slog.Info("maintenance delete complete", "table", stmt.name)
				continue
			}
			slog.Info("maintenance delete complete", "table", stmt.name, "rows", rows)
		}
	}

	// DuckLake's catalog checkpoint currently trips an internal error on live datasets
	// with nullable string fields, which invalidates the whole database connection.
	// Keep runtime maintenance to TTL deletes and the main-cache checkpoint until the
	// upstream checkpoint path is stable.
	if _, err := d.DB.ExecContext(ctx, "CHECKPOINT"); err != nil {
		slog.Error("maintenance checkpoint failed", "target", "main", "err", err)
		errs = append(errs, fmt.Errorf("checkpoint main: %w", err))
	}
	d.lastMaintenance = time.Now()
	return errors.Join(errs...)
}

// ---- Queries for API ----

type LatencyRow struct {
	ServiceName string  `json:"service_name"`
	P95Ms       float64 `json:"p95_ms"`
	ErrorRate   float64 `json:"error_rate"`
	Spans       int64   `json:"spans"`
}

func (d *Duck) LatencyOverview(ctx context.Context, windowMinutes int) ([]LatencyRow, error) {
	namespace := d.DefaultNamespace()
	q := fmt.Sprintf(`
SELECT
  service as service_name,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) as p95_ms,
  AVG(CASE WHEN spans > 0 THEN error_rate END) as error_rate,
  SUM(spans)::BIGINT as spans
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
GROUP BY service
HAVING SUM(spans) > 0
ORDER BY p95_ms DESC
LIMIT 100;
`, windowMinutes)
	rows, err := d.DB.QueryContext(ctx, q, namespace, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LatencyRow{}
	for rows.Next() {
		var r LatencyRow
		if err := rows.Scan(&r.ServiceName, &r.P95Ms, &r.ErrorRate, &r.Spans); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type LogsSample struct {
	TS   string `json:"ts"`
	Body string `json:"body"`
	Svc  string `json:"service_name"`
	Sev  string `json:"severity"`
}

func (d *Duck) LogsSamples(ctx context.Context, windowMinutes, limit int, pattern string) ([]LogsSample, error) {
	namespace := d.DefaultNamespace()
	q := fmt.Sprintf(`
SELECT
  strftime(time, '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
  body,
  service AS service_name,
  severity
FROM logs
WHERE time >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
  AND body ~ ?
ORDER BY time DESC
LIMIT %d;
`, windowMinutes, limit)
	rows, err := d.DB.QueryContext(ctx, q, namespace, namespace, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogsSample{}
	for rows.Next() {
		var r LogsSample
		if err := rows.Scan(&r.TS, &r.Body, &r.Svc, &r.Sev); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ThroughputRow struct {
	Bucket string `json:"bucket"`
	Spans  int64  `json:"spans"`
}

func (d *Duck) Throughput(ctx context.Context, windowMinutes int) ([]ThroughputRow, error) {
	namespace := d.DefaultNamespace()
	q := fmt.Sprintf(`
SELECT strftime(bucket, '%%Y-%%m-%%dT%%H:%%M:00Z') AS bucket,
  (SUM(spans) + SUM(COALESCE(log_count, 0)) + SUM(COALESCE(metric_count, 0))) AS spans
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
GROUP BY bucket
ORDER BY bucket ASC;
`, windowMinutes)
	rows, err := d.DB.QueryContext(ctx, q, namespace, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThroughputRow{}
	for rows.Next() {
		var r ThroughputRow
		if err := rows.Scan(&r.Bucket, &r.Spans); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ServiceThroughputRow struct {
	Service        string `json:"service"`
	SpansPerMinute int64  `json:"spans_per_minute"`
}

func (d *Duck) ServiceThroughput(ctx context.Context, windowMinutes int) ([]ServiceThroughputRow, error) {
	namespace := d.DefaultNamespace()
	q := fmt.Sprintf(`
SELECT service, ROUND((SUM(spans) + SUM(COALESCE(log_count, 0)) + SUM(COALESCE(metric_count, 0)))::DOUBLE / %d)::BIGINT AS spans_per_minute
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
GROUP BY service
ORDER BY spans_per_minute DESC
LIMIT 20;
`, windowMinutes, windowMinutes)
	rows, err := d.DB.QueryContext(ctx, q, namespace, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceThroughputRow{}
	for rows.Next() {
		var r ServiceThroughputRow
		if err := rows.Scan(&r.Service, &r.SpansPerMinute); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ErrorRoute struct {
	Route string `json:"route"`
	Count int64  `json:"count"`
}

func (d *Duck) ErrorRoutes(ctx context.Context, windowMinutes, limit int) ([]ErrorRoute, error) {
	namespace := d.DefaultNamespace()
	q := fmt.Sprintf(`
SELECT body AS route, COUNT(*) AS count
FROM logs
WHERE time >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
  AND severity IN ('ERROR', 'ERR', 'WARN')
GROUP BY body
ORDER BY count DESC
LIMIT %d;
`, windowMinutes, limit)
	rows, err := d.DB.QueryContext(ctx, q, namespace, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ErrorRoute{}
	for rows.Next() {
		var r ErrorRoute
		if err := rows.Scan(&r.Route, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ErrorRouteRow struct {
	ServiceName string  `json:"service_name"`
	Name        string  `json:"name"`
	Errors      int64   `json:"errors"`
	ErrorRate   float64 `json:"error_rate"`
}

func (d *Duck) ErrorRouteDetails(ctx context.Context, windowMinutes, limit int) ([]ErrorRouteRow, error) {
	namespace := d.DefaultNamespace()
	q := fmt.Sprintf(`
WITH spans_with_errors AS (
  SELECT service AS service_name, operation AS name, status AS status_code
  FROM spans
  WHERE start_time >= now() - INTERVAL %d MINUTE
    AND (? = '' OR namespace = ?)
)
SELECT service_name,
       name,
       SUM(CASE WHEN status_code IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1 ELSE 0 END) AS errors,
       AVG(CASE WHEN status_code IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate
FROM spans_with_errors
GROUP BY service_name, name
HAVING errors > 0
ORDER BY errors DESC
LIMIT %d;
`, windowMinutes, limit)
	rows, err := d.DB.QueryContext(ctx, q, namespace, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ErrorRouteRow{}
	for rows.Next() {
		var r ErrorRouteRow
		if err := rows.Scan(&r.ServiceName, &r.Name, &r.Errors, &r.ErrorRate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
