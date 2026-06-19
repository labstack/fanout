package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
	// maintHealthMu guards the maintenance health fields below, which the
	// readiness probe reads while the maintenance pass writes them.
	maintHealthMu      sync.Mutex
	lastMaintenanceOK  time.Time
	lastMaintenanceAt  time.Time
	lastMaintenanceErr error
}

// MaintenanceHealth reports the maintenance loop's own health: when it last
// completed cleanly, when a pass last executed at all (throttled calls don't
// count), and the error from the most recent executed pass (nil after a clean
// one). Surfaced via /readyz so an instance whose retention/compaction is
// failing — and therefore re-entering the storage growth spiral — tells its
// operator instead of only logging. lastAt distinguishes "failing right now"
// from a stale error awaiting the hourly retry; a zero lastAt means no pass
// has executed since the process started.
func (d *Duck) MaintenanceHealth() (lastOK, lastAt time.Time, lastErr error) {
	d.maintHealthMu.Lock()
	defer d.maintHealthMu.Unlock()
	return d.lastMaintenanceOK, d.lastMaintenanceAt, d.lastMaintenanceErr
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

// duckDSN builds the DuckDB connection string. Zero values leave DuckDB's own
// defaults in place — memory_limit at 80% of detected RAM (cgroup-aware) and
// threads at the core count — so the caps scale with the deployment instead
// of a constant that's wrong everywhere but the box it was tuned for.
func duckDSN(dbPath, mem string, threads int) (string, error) {
	if strings.ContainsAny(mem, "&?'\"\\; ") {
		return "", fmt.Errorf("invalid DUCKDB_MEMORY value: %q", mem)
	}
	params := make([]string, 0, 2)
	if threads > 0 {
		params = append(params, "threads="+strconv.Itoa(threads))
	}
	if mem != "" {
		params = append(params, "memory_limit="+mem)
	}
	dsn := dbPath
	if len(params) > 0 {
		dsn += "?" + strings.Join(params, "&")
	}
	return dsn, nil
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

	dsn, err := duckDSN(dbPath, cfg.DuckDBMemory, cfg.DuckDBThreads)
	if err != nil {
		return nil, err
	}

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
		// sync.Once synchronizes the closure's completion before this read, so
		// every concurrently-booting connection observes tempDirErr without a race;
		// if the one SET failed, no connection proceeds to ATTACH.
		if tempDirErr != nil {
			return fmt.Errorf("set temp_directory: %w", tempDirErr)
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
// prod as a multi-day write+query outage). In WAL mode readers run concurrently
// with the single writer, and the next connection to open the catalog recovers
// the WAL automatically after a crash instead of leaving a lock-holding hot
// journal. journal_mode is persisted in the database header, so this is
// effectively a one-time migration, but it is cheap and idempotent to assert on
// every boot. WAL is the only lever available: the DuckDB sqlite extension
// exposes no busy_timeout knob, so DuckDB's own catalog connections have no
// lock-retry timeout — WAL is what prevents the reader/writer collisions.
func enableCatalogWAL(metadataPath string) (err error) {
	db, err := sql.Open("sqlite", metadataPath+"?_pragma=journal_mode(wal)")
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	defer func() {
		// Closing the last connection checkpoints the WAL; surface a failure here
		// (e.g. the filesystem can't maintain the -wal/-shm sidecars) instead of
		// discovering it later as a mysterious lock.
		if cerr := db.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close catalog bootstrap conn: %w", cerr)
		}
	}()
	db.SetMaxOpenConns(1)

	var mode string
	if scanErr := db.QueryRow("PRAGMA journal_mode").Scan(&mode); scanErr != nil {
		return fmt.Errorf("read journal_mode: %w", scanErr)
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
	// rollupOnce can return rows AND an error (one rollup committed, the other
	// failed), so telemetry is recorded unconditionally — otherwise the rollup
	// throughput metric flatlines on an instance whose service rollup is
	// advancing fine behind a failing edge rollup.
	start := time.Now()
	rows, err := d.rollupOnce(ctx)
	metrics.RecordRollup(rows, time.Since(start).Seconds())
	if err != nil {
		slog.Error("startup rollup failed", "rows", rows, "err", err)
	} else if rows > 0 {
		slog.Info("startup rollup complete", "rows", rows, "duration", time.Since(start))
	}
	d.updateLakeStats(ctx)

	ticker := time.NewTicker(time.Duration(d.cfg.RollupEvery) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			start := time.Now()
			rows, err := d.rollupOnce(ctx)
			metrics.RecordRollup(rows, time.Since(start).Seconds())
			if err != nil {
				slog.Error("rollup failed", "component", "rollup", "rows", rows, "err", err)
			}
			d.updateLakeStats(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// updateLakeStats refreshes the per-signal file-count and byte-size gauges
// (fanout_lake_partitions / fanout_lake_size_bytes) from the DuckLake catalog.
// This is the signal that surfaces unbounded file/snapshot growth — the failure
// mode that previously OOM'd the rollup engine — so an operator or soak test can
// watch it climb. It's a cheap read; a failure here must not disturb rollups.
func (d *Duck) updateLakeStats(ctx context.Context) {
	// Bound the catalog read so a degraded/bloated DuckLake can't stall the
	// rollup loop (this runs inline after rollupOnce in RunRollups).
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := d.DB.QueryContext(ctx, `SELECT table_name, file_count, file_size_bytes FROM ducklake_table_info('lake')`)
	if err != nil {
		slog.Warn("lake stats query failed", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		var fileCount, sizeBytes int64
		if err := rows.Scan(&table, &fileCount, &sizeBytes); err != nil {
			slog.Warn("lake stats scan failed", "err", err)
			return
		}
		// DuckLake table names (spans/logs/metrics) are the metric signal labels.
		metrics.UpdateLakeStats(table, sizeBytes, int(fileCount))
	}
	if err := rows.Err(); err != nil {
		slog.Warn("lake stats iteration failed", "err", err)
	}
}

func (d *Duck) rollupOnce(ctx context.Context) (int, error) {
	var errs []error
	affected := int64(0)

	if n, err := d.refreshServiceRollup(ctx); err != nil {
		errs = append(errs, fmt.Errorf("service rollup: %w", err))
	} else {
		affected += n
	}

	if n, err := d.refreshEdgeRollup(ctx); err != nil {
		errs = append(errs, fmt.Errorf("edge rollup: %w", err))
	} else {
		affected += n
	}

	// Maintenance (retention + DuckLake compaction) must run even when a
	// rollup fails: a failing rollup is exactly when compaction matters most,
	// since file/snapshot growth makes every retried pass heavier.
	if err := d.runMaintenance(ctx); err != nil {
		slog.Warn("ducklake maintenance failed", "err", err)
	}

	return int(affected), errors.Join(errs...)
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

	minIngested := int64(0)
	if lastWatermark == 0 {
		if minIngested, err = minServiceRollupIngested(ctx, tx); err != nil {
			return 0, err
		}
	}
	windowStart, windowEnd, chunked := rollupWindow(lastWatermark, minIngested, rawWatermark)

	// The affected scan below covers (windowStart, windowEnd]. Choosing the
	// stored watermark:
	//   - While the raw max is ahead of what any prior pass processed, never
	//     advance the watermark into the trailing lag window below it —
	//     ingested_unix_nano is stamped at ingest but signals flush
	//     independently and the writer retries, so a row can commit below the
	//     current max and must still be picked up (delete+insert is idempotent
	//     per bucket). This clamp applies to chunked passes too: a chunk end
	//     can land inside the lag window of the live tip.
	//   - Once the max plateaus (no new ingest since a pass processed up to
	//     it), the trailing window has been reprocessed once already, so
	//     advance straight to the raw max and let the next cycle
	//     short-circuit. Otherwise a burst's trailing window would be
	//     re-aggregated every cycle forever after ingest goes quiet.
	newWatermark := windowEnd
	if rawWatermark > lastRawMax {
		if guarded := rawWatermark - d.rollupSafetyLagNanos(); newWatermark > guarded {
			newWatermark = guarded
		}
		if newWatermark < lastWatermark {
			newWatermark = lastWatermark
		}
	}
	// The rawmax key records the highest tip whose trailing lag window a pass
	// has actually processed — advance it only as far as this pass scanned,
	// or the plateau shortcut above would fire before the catch-up reached
	// the tip and skip the backlog's last lag window.
	rawMaxProcessed := rawWatermark
	if chunked {
		rawMaxProcessed = windowEnd
	}

	args := []any{windowStart, windowEnd, windowStart, windowEnd, windowStart, windowEnd}
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
	if err := storeRollupWatermark(ctx, tx, serviceRollupRawMaxKey, rawMaxProcessed); err != nil {
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
	minIngested := int64(0)
	if lastWatermark == 0 {
		if minIngested, err = minEdgeRollupIngested(ctx, tx); err != nil {
			return 0, err
		}
	}
	windowStart, windowEnd, chunked := rollupWindow(lastWatermark, minIngested, rawWatermark)

	// Same chunking + trailing-window logic as the service rollup (see
	// refreshServiceRollup): never advance into the lag window below an
	// unprocessed tip — chunked or not — and only let the plateau shortcut
	// fire once a pass has actually processed up to the recorded raw max.
	newWatermark := windowEnd
	if rawWatermark > lastRawMax {
		if guarded := rawWatermark - d.rollupSafetyLagNanos(); newWatermark > guarded {
			newWatermark = guarded
		}
		if newWatermark < lastWatermark {
			newWatermark = lastWatermark
		}
	}
	rawMaxProcessed := rawWatermark
	if chunked {
		rawMaxProcessed = windowEnd
	}

	if _, err := tx.ExecContext(ctx, edgeRollupDeleteSQL, windowStart, windowEnd); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, edgeRollupInsertSQL, windowStart, windowEnd)
	if err != nil {
		return 0, err
	}

	if err := storeRollupWatermark(ctx, tx, edgeRollupStateKey, newWatermark); err != nil {
		return 0, err
	}
	if err := storeRollupWatermark(ctx, tx, edgeRollupRawMaxKey, rawMaxProcessed); err != nil {
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

// rollupChunkNanos caps how much ingest history one rollup pass scans. A pass
// that starts far behind — first run on an existing dataset, or recovery after
// an outage — catches up one chunk per tick instead of aggregating the whole
// backlog in a single statement. Unbounded catch-up is what took prod down on
// 2026-06-13 (UTC): the edge rollup's first pass covered 12 days of spans,
// spilled 375 GiB to temp, filled the disk, and never committed.
const rollupChunkNanos = int64(time.Hour)

// rollupWindow bounds one pass's scan to (start, end]. start falls back to
// just before the oldest ingested row when there's no stored watermark, so a
// first pass doesn't open the window at the epoch.
func rollupWindow(lastWatermark, minIngested, rawMax int64) (start, end int64, chunked bool) {
	start = lastWatermark
	if start == 0 && minIngested > 0 {
		start = minIngested - 1
	}
	end = rawMax
	if end > start+rollupChunkNanos {
		end = start + rollupChunkNanos
		chunked = true
	}
	return start, end, chunked
}

func minServiceRollupIngested(ctx context.Context, tx *sql.Tx) (int64, error) {
	var v int64
	err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MIN(v), 0)::BIGINT
FROM (
  SELECT MIN(ingested_unix_nano) AS v FROM spans
  UNION ALL
  SELECT MIN(ingested_unix_nano) AS v FROM logs
  UNION ALL
  SELECT MIN(ingested_unix_nano) AS v FROM metrics
) mins
WHERE v IS NOT NULL`).Scan(&v)
	return v, err
}

func minEdgeRollupIngested(ctx context.Context, tx *sql.Tx) (int64, error) {
	var v int64
	err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MIN(ingested_unix_nano), 0)::BIGINT
FROM spans`).Scan(&v)
	return v, err
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
    AND ingested_unix_nano <= ?
    AND start_time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM logs
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM metrics
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
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
    AND ingested_unix_nano <= ?
    AND start_time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM logs
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time) AS bucket, service
  FROM metrics
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
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
  -- Bound the scan to the affected bucket range so DuckLake prunes parquet by
  -- start_time stats instead of scanning all history (the join on a computed
  -- date_trunc bucket alone does not prune). The range is a provable superset
  -- of the affected buckets — every row in an affected bucket has start_time in
  -- [MIN(bucket), MAX(bucket)+1min) — so it drops no rows. A late span with an
  -- old start_time widens the range (and this scan) only for the pass that
  -- ingests it, same trade-off as the edge rollup's parent bound.
  WHERE s.start_time >= (SELECT MIN(bucket) FROM affected)
    AND s.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
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
  WHERE l.time >= (SELECT MIN(bucket) FROM affected)
    AND l.time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
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
  WHERE m.time >= (SELECT MIN(bucket) FROM affected)
    AND m.time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
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
    AND ingested_unix_nano <= ?
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
    AND ingested_unix_nano <= ?
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
   -- Bound the parent side to the affected BUCKET RANGE ±1h: without this
   -- the hash build covers every span ever ingested. Parents more than 1h
   -- outside [MIN(affected.bucket), MAX(affected.bucket)] are dropped —
   -- acceptable for minute-bucket dependency edges. Caveat: buckets come
   -- from span start_time while the window bounds ingested_unix_nano, so
   -- one late-arriving span with an old start_time widens the range (and
   -- this scan) back to that bucket for the pass that ingests it.
   AND parent.start_time >= (SELECT MIN(bucket) FROM affected) - INTERVAL 1 HOUR
   AND parent.start_time <= (SELECT MAX(bucket) FROM affected) + INTERVAL 1 HOUR
  JOIN affected a
    ON a.namespace = child.namespace
   AND a.bucket = date_trunc('minute', child.start_time)
  WHERE parent.service IS NOT NULL
    AND parent.service != ''
    AND child.service IS NOT NULL
    AND child.service != ''
    AND parent.service != child.service
    AND child.start_time >= (SELECT MIN(bucket) FROM affected)
    AND child.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
  GROUP BY child.namespace, date_trunc('minute', child.start_time), parent.service, child.service
),
-- Producers and consumers are aggregated per (namespace, bucket, service,
-- destination, msg_system) BEFORE the join — joining raw span rows multiplies
-- producer spans by consumer spans per destination, quadratic in per-bucket
-- span count. calls = consumed messages on the destination, attributed to
-- each producer service publishing to it (a message consumed once appears on
-- every producer's edge).
producers AS (
  SELECT DISTINCT
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
    AND s.start_time >= (SELECT MIN(bucket) FROM affected)
    AND s.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
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
    json_extract_string(s.attributes_json, '$."messaging.system"') AS msg_system,
    COUNT(*) AS calls
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time)
  WHERE s.kind = 'SPAN_KIND_CONSUMER'
    AND s.start_time >= (SELECT MIN(bucket) FROM affected)
    AND s.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
    AND s.service IS NOT NULL
    AND s.service != ''
    AND json_extract_string(s.attributes_json, '$."messaging.destination.name"') IS NOT NULL
  GROUP BY s.namespace, date_trunc('minute', s.start_time), s.service,
    json_extract_string(s.attributes_json, '$."messaging.destination.name"'),
    json_extract_string(s.attributes_json, '$."messaging.system"')
),
messaging_edges AS (
  SELECT
    p.namespace,
    p.bucket,
    p.service AS caller,
    c.service AS callee,
    SUM(c.calls) AS calls,
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

// snapshotGraceMinutes is how long a superseded DuckLake snapshot (and the
// parquet files it references) is kept past compaction before expiry/cleanup
// may reclaim it. It must exceed the longest read query — reads don't hold
// writeMu, so a shorter window lets cleanup delete a file mid-scan — while
// staying well under the maintenance interval so file/snapshot growth stays
// bounded. Queries are sub-second to a few seconds; 10 minutes is ample margin.
const snapshotGraceMinutes = 10

func (d *Duck) runMaintenance(ctx context.Context) error {
	every := time.Duration(d.cfg.MaintenanceEverySeconds) * time.Second
	if every <= 0 {
		every = time.Hour
	}
	if !d.lastMaintenance.IsZero() && time.Since(d.lastMaintenance) < every {
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

	// DuckLake compaction. Every flush commits a snapshot and writes new parquet
	// files; without merge + expiry both grow without bound until per-file
	// metadata pins OOM every wide query (prod 2026-06-13: 60k snapshots, 50k
	// files averaging 21KB, rollups dead at any memory_limit). Holding writeMu
	// here quiesces the dataset against WRITES, the precondition for these calls.
	// Order: merge rewrites small files into large ones, expiry releases the
	// snapshots that referenced the small ones, cleanup deletes the files no live
	// snapshot references. DuckLake never expires the newest snapshot, so a
	// low-traffic instance (no new commits within the grace) still keeps a
	// readable one.
	//
	// GRACE WINDOW (now() - snapshotGraceMinutes) on EXPIRY: writeMu does NOT
	// serialize reads, so an Overview/diagnose query can be mid-scan against a
	// snapshot merge just superseded; expiring + deleting its parquet immediately
	// yanks the file out from under the reader ("IO Error: Cannot open file …: No
	// such file or directory"). Sparing recently-superseded snapshots keeps their
	// files referenced (so cleanup won't delete them) until any in-flight reader
	// has finished; they're reclaimed a cycle later. At FLUSH_SECONDS=15 the grace
	// retains ~40 snapshots (4/min × 10min) — bounded, nowhere near the 60k OOM,
	// and far below the (default 1h) maintenance cycle.
	//
	// cleanup stays cleanup_all => true: the bundled DuckLake's
	// ducklake_cleanup_old_files does NOT accept an older_than grace (verified by
	// benchmark — the call errored every cycle), and it isn't needed: expiry's
	// grace already prevents within-grace files from being scheduled for deletion,
	// so cleanup_all only unlinks files no live snapshot references.
	expireSQL := fmt.Sprintf(
		"CALL ducklake_expire_snapshots('lake', older_than => now() - INTERVAL %d MINUTE)",
		snapshotGraceMinutes)
	for _, stmt := range []struct {
		name string
		sql  string
	}{
		{name: "merge_adjacent_files", sql: "CALL ducklake_merge_adjacent_files('lake')"},
		{name: "expire_snapshots", sql: expireSQL},
		{name: "cleanup_old_files", sql: "CALL ducklake_cleanup_old_files('lake', cleanup_all => true)"},
	} {
		if _, err := d.DB.ExecContext(ctx, stmt.sql); err != nil {
			slog.Error("maintenance compaction failed", "step", stmt.name, "err", err)
			errs = append(errs, fmt.Errorf("compaction %s: %w", stmt.name, err))
		}
	}

	// DuckLake's catalog CHECKPOINT (as opposed to the compaction calls above)
	// currently trips an internal error on live datasets with nullable string
	// fields, which invalidates the whole database connection. Checkpoint only
	// the main local cache until the upstream checkpoint path is stable.
	if _, err := d.DB.ExecContext(ctx, "CHECKPOINT"); err != nil {
		slog.Error("maintenance checkpoint failed", "target", "main", "err", err)
		errs = append(errs, fmt.Errorf("checkpoint main: %w", err))
	}
	d.lastMaintenance = time.Now()

	err := errors.Join(errs...)
	d.maintHealthMu.Lock()
	d.lastMaintenanceAt = time.Now()
	if err == nil {
		d.lastMaintenanceOK = d.lastMaintenanceAt
	}
	d.lastMaintenanceErr = err
	d.maintHealthMu.Unlock()
	return err
}

// ---- Read query helpers ----

// isTransientLakeIOError reports whether err is a DuckLake compaction race: a
// concurrent maintenance pass (merge + cleanup, which runs without blocking
// reads) unlinked a parquet file this read had planned against. Re-running the
// query re-plans against the current snapshot (the merged file), which succeeds.
func isTransientLakeIOError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "IO Error") && strings.Contains(msg, "No such file or directory")
}

// QueryContext runs a read query, retrying briefly on the transient DuckLake
// "file deleted mid-scan" race (reads don't take writeMu, so maintenance can
// unlink a just-merged file underneath them). Cleanup is instantaneous, so a
// short backoff lets the retry re-plan against fresh files. Use this for every
// read that scans the lake instead of DB.QueryContext directly.
func (d *Duck) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	const maxAttempts = 3
	var rows *sql.Rows
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		rows, err = d.DB.QueryContext(ctx, query, args...)
		if err == nil || attempt == maxAttempts || !isTransientLakeIOError(err) {
			return rows, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
		}
	}
	return rows, err
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
