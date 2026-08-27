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

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/queryrows"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

type Duck struct {
	DB         *sql.DB
	cfg        config.Config
	repository *telemetrystore.Repository
	// rollupLagNanos holds the rollup watermark back from the max ingested
	// timestamp so late/out-of-order commits aren't skipped. Zero disables the
	// lag (no trailing window).
	rollupLagNanos int64
	// writeGate serializes writes to the rebuildable DuckDB rollup cache.
	writeGate writegate.WriteGate
	// parquetMu pins immutable files for active DuckDB readers. Its reader-first
	// gate keeps a queued maintenance publish from stalling unrelated new reads.
	parquetMu parquetReadGate
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
	serviceRollupStateKey    = "service_rollup_v2"
	serviceRollupRawMaxKey   = "service_rollup_v2_rawmax"
	edgeRollupStateKey       = "edge_rollup_v2"
	edgeRollupRawMaxKey      = "edge_rollup_v2_rawmax"
	EndpointRollupStateKey   = "endpoint_rollup_v1"
	endpointRollupRawMaxKey  = "endpoint_rollup_v1_rawmax"
	endpointBackfillStateKey = "endpoint_rollup_v1_backfill_started"
	EndpointReadyStateKey    = "endpoint_rollup_v1_ready"
	EndpointDisabledStateKey = "endpoint_rollup_v1_disabled"
	defaultDuckDBPoolSize    = 1
)

// WriteGate returns the gate that serializes writes to DuckDB's rebuildable
// rollup cache.
func (d *Duck) WriteGate() *writegate.WriteGate { return &d.writeGate }

// duckDBPoolSize is the effective connection-pool size: the configured value,
// floored at 1.
func duckDBPoolSize(cfg config.Config) int {
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
		return "", fmt.Errorf("invalid storage.duckdb.memory value: %q", mem)
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

// memoryHeadroomBytes is the absolute RAM left free below DuckDB's memory_limit
// for the Go heap, the ingest appender, and untracked allocations. On a small
// box DuckDB's default (80% of RAM) leaves too little — observed: kernel
// OOM-kill at RSS 7.6 GB on an 8 GB box under a heavy rollup+query+ingest load.
const memoryHeadroomBytes = int64(2) << 30 // 2 GiB

// applyMemoryHeadroom caps DuckDB's auto memory_limit to (RAM - headroom) on
// small boxes, keeping the default on large ones. It reads DuckDB's own
// cgroup-aware auto value (so containers stay correct) and only lowers it when
// 80% would leave less than the headroom — converting a kernel OOM-kill into a
// graceful DuckDB memory-limit error. No-op (and non-fatal) if it can't parse.
func applyMemoryHeadroom(ctx context.Context, db *sql.DB) error {
	var raw string
	if err := db.QueryRowContext(ctx, "SELECT current_setting('memory_limit')").Scan(&raw); err != nil {
		return err
	}
	limit, ok := parseDuckBytes(raw)
	if !ok || limit <= 0 {
		return nil // unparseable — leave DuckDB's default in place
	}
	// Invert DuckDB's default memory_limit (80% of RAM) to recover total RAM.
	// The 0.8 is coupled to that default — re-check on DuckDB upgrades — and is
	// only valid because the caller runs this with an unpinned limit (cfg.DuckDBMemory == "").
	total := int64(float64(limit) / 0.8)
	capped := total - memoryHeadroomBytes
	if capped <= 0 || capped >= limit {
		return nil // big box (80% already leaves >= headroom) — keep the default
	}
	_, err := db.ExecContext(ctx, fmt.Sprintf("SET memory_limit='%dB'", capped))
	return err
}

// parseDuckBytes parses a DuckDB byte-size string ("6.4 GiB", "512.0 MiB",
// "1024 bytes") into bytes. Returns false if unparseable.
func parseDuckBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && ((s[i] >= '0' && s[i] <= '9') || s[i] == '.') {
		i++
	}
	num, err := strconv.ParseFloat(strings.TrimSpace(s[:i]), 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(strings.TrimSpace(s[i:])) {
	case "", "b", "byte", "bytes":
		return int64(num), true
	case "kib":
		return int64(num * (1 << 10)), true
	case "mib":
		return int64(num * (1 << 20)), true
	case "gib":
		return int64(num * (1 << 30)), true
	case "tib":
		return int64(num * (1 << 40)), true
	case "kb":
		return int64(num * 1e3), true
	case "mb":
		return int64(num * 1e6), true
	case "gb":
		return int64(num * 1e9), true
	case "tb":
		return int64(num * 1e12), true
	default:
		return 0, false
	}
}

func NewDuck(ctx context.Context, cfg config.Config, repository *telemetrystore.Repository) (*Duck, error) {
	if repository == nil {
		return nil, errors.New("telemetry repository is required")
	}
	if err := os.MkdirAll(cfg.QueryDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create query dir: %w", err)
	}
	if err := os.MkdirAll(cfg.TelemetryDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create telemetry dir: %w", err)
	}

	dbPath := cfg.QueryDuckDBPath()
	tempDir := cfg.QueryTempDir()
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	dsn, err := duckDSN(dbPath, cfg.DuckDBMemory, cfg.DuckDBThreads)
	if err != nil {
		return nil, err
	}

	db, err := openDuckDB(ctx, dsn, tempDir, duckDBPoolSize(cfg))
	if err != nil {
		return nil, fmt.Errorf("open DuckDB query cache: %w (the cache at %s is rebuildable from Parquet)", err, dbPath)
	}

	d := &Duck{DB: db, cfg: cfg, repository: repository, rollupLagNanos: int64(30 * time.Second)}
	repository.SetParquetPublishLock(&d.parquetMu)
	if cfg.DuckDBMemory == "" {
		// Only when the operator hasn't pinned storage.duckdb.memory: keep DuckDB's
		// cgroup-aware auto limit on big boxes but leave absolute RAM headroom on
		// small ones, so the kernel doesn't OOM-kill the whole process.
		if err := applyMemoryHeadroom(ctx, db); err != nil {
			slog.Warn("apply memory headroom failed; using DuckDB default memory_limit", "err", err)
		}
	}
	if err := CreateCacheTables(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := CreateParquetViews(db, repository.Parquet.Dir()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := CreateViews(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create views: %w", err)
	}
	if cfg.RollupSkipToLatest {
		// Best-effort: on failure the rollup just catches up normally.
		if err := d.skipRollupToLatest(ctx); err != nil {
			slog.Warn("rollup skip-to-latest failed; rollup will catch up normally", "err", err)
		} else {
			slog.Info("rollup skip-to-latest: existing data marked already-rolled-up")
		}
	}
	return d, nil
}

// skipRollupToLatest advances every rollup watermark to the current max ingested
// timestamp, so existing data is treated as already-processed rather than
// aggregated as a backlog. This avoids a multi-minute first-rollup catch-up
// (a wide-start_time backlog holds the write gate and starves ingest) when standing up
// a large pre-seeded historical dataset. Runs once at boot before RunRollups, so
// taking the write gate here is uncontended.
func (d *Duck) skipRollupToLatest(ctx context.Context) error {
	unlock := d.writeGate.Lock(writegate.WriteRollupSkip)
	defer unlock()

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	svc, err := maxServiceRollupWatermark(ctx, tx)
	if err != nil {
		return err
	}
	edge, err := maxEdgeRollupWatermark(ctx, tx)
	if err != nil {
		return err
	}
	// A skipped endpoint cache is never queryable: clear any cache left by a
	// previous normal backfill as well as its persisted readiness bit.
	if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_rollup`); err != nil {
		return err
	}
	for _, w := range []struct {
		key       string
		watermark int64
	}{
		{serviceRollupStateKey, svc},
		{serviceRollupRawMaxKey, svc},
		{edgeRollupStateKey, edge},
		{edgeRollupRawMaxKey, edge},
		// Endpoint queries remain on their raw-span fallback when the operator
		// explicitly skips historical rollups. Mark this cache disabled instead of
		// later declaring a new-only, incomplete endpoint cache ready.
		{EndpointReadyStateKey, 0},
		{EndpointDisabledStateKey, 1},
	} {
		if err := storeRollupWatermark(ctx, tx, w.key, w.watermark); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func openDuckDB(ctx context.Context, dsn, tempDir string, maxConns int) (*sql.DB, error) {
	// temp_directory is an instance-global setting: re-setting it after the temp
	// dir has already been used fails with "Cannot switch temporary directory
	// after the current one has been used". The boot hook runs once per pooled
	// connection, so only the first connection sets it; the rest inherit the
	// instance-wide value. LOAD/ATTACH stay per-connection — they're idempotent.
	var tempDirOnce sync.Once
	var tempDirErr error

	connector, err := duckdb.NewConnector(dsn, func(execer driver.ExecerContext) error {
		tempDirOnce.Do(func() {
			_, tempDirErr = execer.ExecContext(ctx, "SET temp_directory="+sqlLiteral(tempDir), nil)
		})
		// sync.Once synchronizes the closure's completion before this read, so
		// every concurrently-booting connection observes tempDirErr without a race;
		// if the one SET failed, no connection proceeds to ATTACH.
		if tempDirErr != nil {
			return fmt.Errorf("set temp_directory: %w", tempDirErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	db := sql.OpenDB(connector)
	// The on-disk DuckDB file contains only rebuildable rollups and views; Parquet
	// scans may run concurrently across the machine-sized pool.
	if maxConns < 1 {
		maxConns = 1
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	return db, nil
}

func sqlLiteral(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func (d *Duck) Close() error {
	return d.DB.Close()
}

// DefaultNamespace returns empty string so queries search all namespaces.
func (d *Duck) DefaultNamespace() string {
	return ""
}

func (d *Duck) RunRollups(ctx context.Context) {
	// rollupOnce can return rows AND an error (one cache committed while another
	// failed), so telemetry is recorded unconditionally — otherwise the rollup
	// throughput metric flatlines on an instance whose service rollup is
	// advancing fine behind a failing edge rollup.
	start := time.Now()
	rows, err := d.rollupOnce(ctx)
	metrics.RecordRollup(rows, time.Since(start).Seconds(), err == nil)
	if err != nil {
		slog.Error("startup rollup failed", "rows", rows, "err", err)
	} else if rows > 0 {
		slog.Info("startup rollup complete", "rows", rows, "duration", time.Since(start))
	}
	d.updateParquetStats()
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		d.runMaintenanceLoop(ctx)
	}()
	defer func() { <-maintenanceDone }()

	ticker := time.NewTicker(d.cfg.RollupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			start := time.Now()
			rows, err := d.rollupOnce(ctx)
			metrics.RecordRollup(rows, time.Since(start).Seconds(), err == nil)
			if err != nil {
				slog.Error("rollup failed", "component", "rollup", "rows", rows, "err", err)
			}
			d.updateParquetStats()
		case <-ctx.Done():
			return
		}
	}
}

// updateParquetStats refreshes the per-signal file-count and byte-size gauges.
func (d *Duck) updateParquetStats() {
	stats, err := d.repository.Parquet.Stats()
	if err != nil {
		slog.Warn("parquet stats failed", "err", err)
		return
	}
	for signal, stat := range stats {
		metrics.UpdateParquetStats(signal, stat.Bytes, stat.Files)
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

	if n, err := d.refreshEndpointRollup(ctx); err != nil {
		errs = append(errs, fmt.Errorf("endpoint rollup: %w", err))
	} else {
		affected += n
	}

	if n, err := d.refreshEdgeRollup(ctx); err != nil {
		errs = append(errs, fmt.Errorf("edge rollup: %w", err))
	} else {
		affected += n
	}

	return int(affected), errors.Join(errs...)
}

func (d *Duck) runMaintenanceLoop(ctx context.Context) {
	every := d.cfg.MaintenanceInterval
	if every <= 0 {
		every = time.Hour
	}
	run := func() {
		if err := d.runRepositoryMaintenance(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("telemetry maintenance failed", "err", err)
		}
		d.updateParquetStats()
	}
	run()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			run()
		case <-ctx.Done():
			return
		}
	}
}

func (d *Duck) runRepositoryMaintenance(ctx context.Context) error {
	start := time.Now()
	cutoff := time.Now().Add(-d.cfg.HotRetention).UnixNano()
	var pruneErr error
	if d.repository != nil {
		_, pruneErr = d.repository.PruneHot(cutoff)
		_, hotCompactErr := d.repository.CompactHot(64)
		var parquetErr error
		if d.cfg.RetentionDays > 0 {
			d.parquetMu.Lock()
			_, parquetErr = d.repository.PruneParquet(time.Now().Add(-time.Duration(d.cfg.RetentionDays) * 24 * time.Hour).UnixNano())
			d.parquetMu.Unlock()
		}
		compactStart := time.Now()
		compacted, compactErr := d.repository.CompactParquetBacklog(ctx, d.DB, 64, &d.parquetMu)
		compactResult := metrics.TelemetryNoop
		if compactErr != nil {
			compactResult = metrics.TelemetryError
		} else if compacted > 0 {
			compactResult = metrics.TelemetrySuccess
		}
		metrics.RecordTelemetryOperation(metrics.TelemetryCompaction, compactResult, time.Since(compactStart).Seconds())
		pruneErr = errors.Join(pruneErr, hotCompactErr, parquetErr, compactErr)
	}
	var cacheErr error
	unlockMaintenance := d.writeGate.Lock(writegate.WriteMaintenance)
	if d.cfg.RetentionDays > 0 {
		for _, table := range []string{"service_rollup", "endpoint_rollup", "edge_rollup"} {
			if _, err := d.DB.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE bucket < now() - INTERVAL %d DAY", table, d.cfg.RetentionDays)); err != nil {
				cacheErr = errors.Join(cacheErr, fmt.Errorf("prune %s: %w", table, err))
			}
		}
	}
	_, checkpointErr := d.DB.ExecContext(ctx, "CHECKPOINT")
	unlockMaintenance()
	err := errors.Join(pruneErr, cacheErr, checkpointErr)
	maintenanceResult := metrics.TelemetrySuccess
	if err != nil {
		maintenanceResult = metrics.TelemetryError
	}
	metrics.RecordTelemetryOperation(metrics.TelemetryMaintenance, maintenanceResult, time.Since(start).Seconds())
	finished := time.Now()
	d.maintHealthMu.Lock()
	d.lastMaintenanceAt = finished
	if err == nil {
		d.lastMaintenanceOK = finished
	}
	d.lastMaintenanceErr = err
	d.maintHealthMu.Unlock()
	if err == nil {
		slog.Info("telemetry maintenance complete", "duration", time.Since(start))
	}
	return err
}

func (d *Duck) refreshServiceRollup(ctx context.Context) (int64, error) {
	start := time.Now()
	result := metrics.RollupError
	var recordedRows int64
	// Progress is reported even when the rollup fails. Otherwise the lag gauge
	// freezes at its last healthy value during exactly the outage it exists to
	// expose. A zero sourceMax means the failure happened before the source tip
	// was read, so there is nothing newer to report.
	var watermark, sourceMax int64
	defer func() {
		metrics.RecordRollupComponent(metrics.RollupService, result, recordedRows, time.Since(start).Seconds())
		if result == metrics.RollupError && sourceMax > 0 {
			updateRollupProgress(metrics.RollupService, true, watermark, sourceMax)
		}
	}()
	if err := d.lockParquetRead(ctx); err != nil {
		return 0, err
	}
	defer d.parquetMu.RUnlock()

	// Serialize against other writers (edge rollup, maintenance, ingest flushes).
	// The write gate is always acquired before a connection to keep lock ordering
	// consistent and deadlock-free.
	unlock := d.writeGate.Lock(writegate.WriteRollupService)
	defer unlock()

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	lastWatermark, err := rollupWatermark(ctx, tx, serviceRollupStateKey)
	if err != nil {
		return 0, err
	}
	watermark = lastWatermark
	lastRawMax, err := rollupWatermark(ctx, tx, serviceRollupRawMaxKey)
	if err != nil {
		return 0, err
	}

	rawWatermark, err := maxServiceRollupWatermark(ctx, tx)
	if err != nil {
		return 0, err
	}
	sourceMax = rawWatermark
	if rawWatermark <= lastWatermark {
		err := tx.Commit()
		if err == nil {
			result = metrics.RollupNoop
			updateRollupProgress(metrics.RollupService, true, lastWatermark, rawWatermark)
		}
		return 0, err
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
	result = metrics.RollupSuccess
	updateRollupProgress(metrics.RollupService, true, newWatermark, rawWatermark)

	rows, err := res.RowsAffected()
	if err != nil {
		// Warn, not Debug: recordedRows feeds fanout_rollup_component_rows_total,
		// so a silent zero here reads in benchmark evidence as "the rollup ran and
		// matched nothing" when it actually means "we do not know".
		slog.Warn("service rollup rows affected unavailable", "err", err)
		return 0, nil
	}
	recordedRows = rows
	return rows, nil
}

func (d *Duck) refreshEndpointRollup(ctx context.Context) (int64, error) {
	start := time.Now()
	result := metrics.RollupError
	var recordedRows int64
	var watermark, sourceMax int64 // see refreshServiceRollup
	defer func() {
		metrics.RecordRollupComponent(metrics.RollupEndpoint, result, recordedRows, time.Since(start).Seconds())
		if result == metrics.RollupError && sourceMax > 0 {
			updateRollupProgress(metrics.RollupEndpoint, true, watermark, sourceMax)
		}
	}()
	if err := d.lockParquetRead(ctx); err != nil {
		return 0, err
	}
	defer d.parquetMu.RUnlock()

	unlock := d.writeGate.Lock(writegate.WriteRollupEndpoint)
	defer unlock()

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	disabled, err := rollupWatermark(ctx, tx, EndpointDisabledStateKey)
	if err != nil {
		return 0, err
	}
	if disabled != 0 {
		err := tx.Commit()
		if err == nil {
			result = metrics.RollupDisabled
			updateRollupProgress(metrics.RollupEndpoint, false, 0, 0)
		}
		return 0, err
	}
	lastWatermark, err := rollupWatermark(ctx, tx, EndpointRollupStateKey)
	if err != nil {
		return 0, err
	}
	watermark = lastWatermark
	lastRawMax, err := rollupWatermark(ctx, tx, endpointRollupRawMaxKey)
	if err != nil {
		return 0, err
	}
	backfillStarted, err := rollupWatermark(ctx, tx, endpointBackfillStateKey)
	if err != nil {
		return 0, err
	}

	rawWatermark, err := maxEdgeRollupWatermark(ctx, tx)
	if err != nil {
		return 0, err
	}
	sourceMax = rawWatermark
	if rawWatermark <= lastWatermark {
		err := tx.Commit()
		if err == nil {
			result = metrics.RollupNoop
			updateRollupProgress(metrics.RollupEndpoint, true, lastWatermark, rawWatermark)
		}
		return 0, err
	}
	minIngested := int64(0)
	if lastWatermark == 0 {
		if minIngested, err = minEdgeRollupIngested(ctx, tx); err != nil {
			return 0, err
		}
		if err := storeRollupWatermark(ctx, tx, endpointBackfillStateKey, 1); err != nil {
			return 0, err
		}
		backfillStarted = 1
	}
	windowStart, windowEnd, chunked := rollupWindow(lastWatermark, minIngested, rawWatermark)

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

	if _, err := tx.ExecContext(ctx, endpointRollupDeleteSQL, windowStart, windowEnd); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, endpointRollupInsertSQL, windowStart, windowEnd)
	if err != nil {
		return 0, err
	}
	if err := storeRollupWatermark(ctx, tx, EndpointRollupStateKey, newWatermark); err != nil {
		return 0, err
	}
	if err := storeRollupWatermark(ctx, tx, endpointRollupRawMaxKey, rawMaxProcessed); err != nil {
		return 0, err
	}
	// The product query remains on raw spans until a normal (non-skip) backfill
	// reaches the live tip. This prevents partial endpoint history on upgrade.
	if backfillStarted != 0 && !chunked && windowEnd == rawWatermark {
		if err := storeRollupWatermark(ctx, tx, EndpointReadyStateKey, 1); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	result = metrics.RollupSuccess
	updateRollupProgress(metrics.RollupEndpoint, true, newWatermark, rawWatermark)
	rows, err := res.RowsAffected()
	if err != nil {
		slog.Warn("endpoint rollup rows affected unavailable", "err", err)
		return 0, nil
	}
	recordedRows = rows
	return rows, nil
}

func (d *Duck) refreshEdgeRollup(ctx context.Context) (int64, error) {
	start := time.Now()
	result := metrics.RollupError
	var recordedRows int64
	var watermark, sourceMax int64 // see refreshServiceRollup
	defer func() {
		metrics.RecordRollupComponent(metrics.RollupEdge, result, recordedRows, time.Since(start).Seconds())
		if result == metrics.RollupError && sourceMax > 0 {
			updateRollupProgress(metrics.RollupEdge, true, watermark, sourceMax)
		}
	}()
	if err := d.lockParquetRead(ctx); err != nil {
		return 0, err
	}
	defer d.parquetMu.RUnlock()

	unlock := d.writeGate.Lock(writegate.WriteRollupEdge)
	defer unlock()

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	lastWatermark, err := rollupWatermark(ctx, tx, edgeRollupStateKey)
	if err != nil {
		return 0, err
	}
	watermark = lastWatermark
	lastRawMax, err := rollupWatermark(ctx, tx, edgeRollupRawMaxKey)
	if err != nil {
		return 0, err
	}

	rawWatermark, err := maxEdgeRollupWatermark(ctx, tx)
	if err != nil {
		return 0, err
	}
	sourceMax = rawWatermark
	if rawWatermark <= lastWatermark {
		err := tx.Commit()
		if err == nil {
			result = metrics.RollupNoop
			updateRollupProgress(metrics.RollupEdge, true, lastWatermark, rawWatermark)
		}
		return 0, err
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

	// Query the start_time range that the affected ingested-window spans cover.
	// We sub-window this range in edgeStartChunkNanos increments so the
	// call_edges self-join never fans out over more than 30 min of start_time,
	// even when a burst catches up hours of backlog in a single ingested window.
	var minStartT, maxStartT sql.NullTime
	err = tx.QueryRowContext(ctx, `
SELECT
  MIN(date_trunc('minute', start_time)),
  MAX(date_trunc('minute', start_time))
FROM spans
WHERE ingested_unix_nano > ?
  AND ingested_unix_nano <= ?
  AND start_time IS NOT NULL`, windowStart, windowEnd).Scan(&minStartT, &maxStartT)
	if err != nil {
		return 0, err
	}

	var totalAffected int64
	if minStartT.Valid {
		subLo := minStartT.Time
		maxT := maxStartT.Time
		for !subLo.After(maxT) {
			subHi := subLo.Add(time.Duration(edgeStartChunkNanos))
			if _, err := tx.ExecContext(ctx, edgeRollupDeleteSQL, windowStart, windowEnd, subLo, subHi); err != nil {
				return 0, err
			}
			res, err := tx.ExecContext(ctx, edgeRollupInsertSQL, windowStart, windowEnd, subLo, subHi)
			if err != nil {
				return 0, err
			}
			rows, err := res.RowsAffected()
			if err != nil {
				slog.Warn("edge rollup rows affected unavailable", "err", err)
			} else {
				totalAffected += rows
			}
			subLo = subHi
		}
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

	result = metrics.RollupSuccess
	recordedRows = totalAffected
	updateRollupProgress(metrics.RollupEdge, true, newWatermark, rawWatermark)
	return totalAffected, nil
}

// rollupChunkNanos caps how much ingest history one rollup pass scans. A pass
// that starts far behind — first run on an existing dataset, or recovery after
// an outage — catches up one chunk per tick instead of aggregating the whole
// backlog in a single statement. Unbounded catch-up is what took prod down on
// 2026-06-13 (UTC): the edge rollup's first pass covered 12 days of spans,
// spilled 375 GiB to temp, filled the disk, and never committed.
const rollupChunkNanos = int64(time.Hour)

// edgeStartChunkNanos bounds how wide a start_time range one edge-rollup
// DELETE+INSERT processes, so the call_edges self-join over a wide backlog
// (catch-up/bulk-load) can't exhaust memory. The pass loops over sub-windows.
const edgeStartChunkNanos = int64(30 * time.Minute)

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

func updateRollupProgress(component metrics.RollupComponent, enabled bool, watermark, sourceMax int64) {
	backlog := sourceMax - watermark
	backlogChunks := 0
	if backlog > 0 {
		backlogChunks = int(backlog / rollupChunkNanos)
		if backlog%rollupChunkNanos != 0 {
			backlogChunks++
		}
	}
	metrics.UpdateRollupProgress(component, enabled, watermark, sourceMax, backlogChunks)
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
// ingest and committed to Parquet (the bounded commit retry window plus queueing).
func (d *Duck) rollupSafetyLagNanos() int64 {
	return d.rollupLagNanos
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
  -- Bound the scan to the affected bucket range so Telemetry prunes parquet by
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

// Endpoint latency is stored as a mergeable fixed-boundary histogram. Unlike
// averaging minute p95 values, summing these bin counts preserves the latency
// distribution across arbitrary query windows. Bounds include Fanout's health
// thresholds (750ms and 2s) and cap the overflow bucket at five minutes.
const endpointRollupDeleteSQL = `
WITH affected AS (
  SELECT DISTINCT
    namespace,
    date_trunc('minute', start_time) AS bucket,
    COALESCE(service, '') AS service,
    COALESCE(NULLIF(http_method, ''), 'CALL') AS method,
    COALESCE(NULLIF(http_route, ''), NULLIF(operation, ''), 'unknown') AS path
  FROM spans
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND start_time IS NOT NULL
)
DELETE FROM endpoint_rollup
WHERE EXISTS (
  SELECT 1
  FROM affected
  WHERE affected.namespace = endpoint_rollup.namespace
    AND affected.bucket = endpoint_rollup.bucket
    AND affected.service = endpoint_rollup.service
    AND affected.method = endpoint_rollup.method
    AND affected.path = endpoint_rollup.path
);`

const endpointRollupInsertSQL = `
WITH affected AS (
  SELECT DISTINCT
    namespace,
    date_trunc('minute', start_time) AS bucket,
    COALESCE(service, '') AS service,
    COALESCE(NULLIF(http_method, ''), 'CALL') AS method,
    COALESCE(NULLIF(http_route, ''), NULLIF(operation, ''), 'unknown') AS path
  FROM spans
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND start_time IS NOT NULL
)
INSERT INTO endpoint_rollup (
  namespace, bucket, service, method, path, calls, error_count, duration_count, duration_buckets
)
SELECT
  s.namespace,
  date_trunc('minute', s.start_time) AS bucket,
  COALESCE(s.service, '') AS service,
  COALESCE(NULLIF(s.http_method, ''), 'CALL') AS method,
  COALESCE(NULLIF(s.http_route, ''), NULLIF(s.operation, ''), 'unknown') AS path,
  COUNT(*) AS calls,
  COUNT(*) FILTER (WHERE upper(s.status) IN ('ERROR', 'STATUS_CODE_ERROR')) AS error_count,
  COUNT(s.duration_ms) AS duration_count,
  struct_pack(
    le_0_1 := COUNT(*) FILTER (WHERE s.duration_ms <= 0.1),
    le_0_5 := COUNT(*) FILTER (WHERE s.duration_ms <= 0.5),
    le_1 := COUNT(*) FILTER (WHERE s.duration_ms <= 1),
    le_2_5 := COUNT(*) FILTER (WHERE s.duration_ms <= 2.5),
    le_5 := COUNT(*) FILTER (WHERE s.duration_ms <= 5),
    le_10 := COUNT(*) FILTER (WHERE s.duration_ms <= 10),
    le_25 := COUNT(*) FILTER (WHERE s.duration_ms <= 25),
    le_50 := COUNT(*) FILTER (WHERE s.duration_ms <= 50),
    le_100 := COUNT(*) FILTER (WHERE s.duration_ms <= 100),
    le_250 := COUNT(*) FILTER (WHERE s.duration_ms <= 250),
    le_500 := COUNT(*) FILTER (WHERE s.duration_ms <= 500),
    le_750 := COUNT(*) FILTER (WHERE s.duration_ms <= 750),
    le_1000 := COUNT(*) FILTER (WHERE s.duration_ms <= 1000),
    le_2000 := COUNT(*) FILTER (WHERE s.duration_ms <= 2000),
    le_5000 := COUNT(*) FILTER (WHERE s.duration_ms <= 5000),
    le_30000 := COUNT(*) FILTER (WHERE s.duration_ms <= 30000),
    le_300000 := COUNT(*) FILTER (WHERE s.duration_ms <= 300000)
  ) AS duration_buckets
FROM spans s
JOIN affected a
  ON a.namespace = s.namespace
 AND a.bucket = date_trunc('minute', s.start_time)
 AND a.service = COALESCE(s.service, '')
 AND a.method = COALESCE(NULLIF(s.http_method, ''), 'CALL')
 AND a.path = COALESCE(NULLIF(s.http_route, ''), NULLIF(s.operation, ''), 'unknown')
WHERE s.start_time >= (SELECT MIN(bucket) FROM affected)
  AND s.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
GROUP BY
  s.namespace,
  date_trunc('minute', s.start_time),
  COALESCE(s.service, ''),
  COALESCE(NULLIF(s.http_method, ''), 'CALL'),
  COALESCE(NULLIF(s.http_route, ''), NULLIF(s.operation, ''), 'unknown');`

const edgeRollupDeleteSQL = `
WITH affected AS (
  SELECT DISTINCT namespace, date_trunc('minute', start_time) AS bucket
  FROM spans
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND start_time IS NOT NULL
    AND start_time >= ?
    AND start_time < ?
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
    AND start_time >= ?
    AND start_time < ?
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

// ---- Read query helpers ----

// QueryContext executes a read against immutable Parquet files and DuckDB's
// local rollup cache.
func (d *Duck) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	if err := d.lockParquetRead(ctx); err != nil {
		return nil, err
	}
	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		d.parquetMu.RUnlock()
		return nil, err
	}
	return &lockedRows{Rows: rows, unlock: d.parquetMu.RUnlock}, nil
}

type lockedRows struct {
	*sql.Rows
	unlockOnce sync.Once
	unlock     func()
}

func (r *lockedRows) Close() error {
	err := r.Rows.Close()
	r.release()
	return err
}

func (r *lockedRows) Next() bool {
	ok := r.Rows.Next()
	if !ok {
		r.release()
	}
	return ok
}

func (r *lockedRows) release() { r.unlockOnce.Do(r.unlock) }

// QueryRowScan executes a single-row query against immutable Parquet files and
// DuckDB's local rollup cache.
func (d *Duck) QueryRowScan(ctx context.Context, dest []any, query string, args ...any) error {
	if err := d.lockParquetRead(ctx); err != nil {
		return err
	}
	defer d.parquetMu.RUnlock()
	return d.DB.QueryRowContext(ctx, query, args...).Scan(dest...)
}

func (d *Duck) lockParquetRead(ctx context.Context) error {
	for {
		if d.parquetMu.TryRLock() {
			return nil
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
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
	rows, err := d.QueryContext(ctx, q, namespace, namespace)
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
	rows, err := d.QueryContext(ctx, q, namespace, namespace, pattern)
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
	rows, err := d.QueryContext(ctx, q, namespace, namespace)
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
	rows, err := d.QueryContext(ctx, q, namespace, namespace)
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
	rows, err := d.QueryContext(ctx, q, namespace, namespace)
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
	rows, err := d.QueryContext(ctx, q, namespace, namespace)
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
