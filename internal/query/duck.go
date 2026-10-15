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
	engine "github.com/labstack/fanout/internal/duckdb"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

type Duck struct {
	cachePublishMu  sync.Mutex
	cachePublishing map[string]bool
	snapshotMu      sync.Mutex
	snapshotQueries map[string]compiledSnapshot
	// DB is the read pool. Reads scan immutable Parquet concurrently, so it is
	// sized from the machine.
	DB *sql.DB
	// closeOnce makes Close idempotent. Both handles come from one connector,
	// and database/sql closes a connector that implements io.Closer — so a
	// second Close would call duckdb_close twice on the same instance, which
	// is a double free the runtime cannot recover from.
	closeOnce sync.Once
	closeErr  error
	// releasePoolGauges stops the pool gauges reading this Duck's pool, and
	// only if they are still reading it.
	releasePoolGauges   func()
	releaseMemoryGauges func()
	// writeDB has two connections: analytical rollups share writeGate, while
	// completed-batch aggregates use cacheGate and disjoint tables. Both stay
	// outside the read pool. Maintenance acquires both gates for checkpointing.
	writeDB    *sql.DB
	cfg        config.Config
	repository *telemetrystore.Repository
	// publicationBounds is the repository's exact commit horizon. Tests using
	// mutable fixtures supply their own source; production always uses Repository.
	publicationBounds func() (horizon, published, minimum int64)
	// Cached under writeGate so an idle pass executes no SQL at all.
	rollupWatermarks map[string]int64
	// writeGate serializes writes to the rebuildable DuckDB rollup cache.
	writeGate writegate.WriteGate
	cacheGate writegate.WriteGate
	// Protected by writeGate; controls periodic version-history retirement.
	versionRollupPasses   uint64
	versionRollupFailures map[string]versionRollupFailure
	// parquetMu pins immutable files for active DuckDB readers. Its reader-first
	// gate keeps a queued maintenance publish from stalling unrelated new reads.
	parquetMu parquetReadGate
	// maintHealthMu guards the maintenance health fields below, which the
	// readiness probe reads while the maintenance pass writes them.
	maintHealthMu       sync.Mutex
	lastMaintenanceOK   time.Time
	lastMaintenanceAt   time.Time
	lastMaintenanceErr  error
	maintenanceFailures int
}

// MaintenanceHealth reports the maintenance loop's own health: when it last
// completed cleanly, when a pass last executed at all (throttled calls don't
// count), and the error from the most recent executed pass (nil after a clean
// one). Surfaced via /readyz so an instance whose retention/compaction is
// failing — and therefore re-entering the storage growth spiral — tells its
// operator instead of only logging. lastAt distinguishes "failing right now"
// from a stale error awaiting the hourly retry; a zero lastAt means no pass
// has executed since the process started.
func (d *Duck) MaintenanceHealth() (lastOK, lastAt time.Time, consecutiveFailures int, lastErr error) {
	d.maintHealthMu.Lock()
	defer d.maintHealthMu.Unlock()
	return d.lastMaintenanceOK, d.lastMaintenanceAt, d.maintenanceFailures, d.lastMaintenanceErr
}

const (
	// v3: service_rollup gained served_spans, and its p50/p95 stopped counting
	// the calls a service waits on. ensureCacheTable drops the table when the
	// new column is missing, but the watermark decides what gets rebuilt — left
	// at v2 it would point past every historical bucket, so the dropped rows
	// would never come back and the whole window would read as empty. A new key
	// has no watermark, which is what makes the next pass a full backfill.
	// Bump this whenever the table's shape or the meaning of its columns
	// changes.
	serviceRollupStateKey         = "service_rollup_v3"
	edgeRollupStateKey            = "edge_rollup_v2"
	edgeRollupSubCursorKey        = "edge_rollup_v2_substart"
	edgeRollupSubWindowKey        = "edge_rollup_v2_subwindow_end"
	defaultDuckDBPoolSize         = 1
	parquetCompactionCycle        = 10 * time.Second
	parquetCompactionCycleBudget  = 8 * time.Second
	parquetMaintenanceBatchLimit  = telemetry.CompactionBatchLimit
	parquetMaintenancePhaseBudget = 10 * time.Minute
)

// parquetDrainBudget bounds how long a publisher waits for readers already
// inside the snapshot to leave. parquetSwapBudget bounds the exclusive window
// that follows — the renames and fsync of at most parquetMaintenanceBatchLimit
// directories — which must be allowed to finish rather than be abandoned with
// every reader already excluded.
const (
	parquetDrainBudget = 2 * defaultWriterGrace
	parquetSwapBudget  = 30 * time.Second
)

// parquetStatsWait bounds the gauge refresh, which is the only Parquet reader
// that runs on a loop goroutine rather than behind a request. It is a variable
// so the internal lease can be exercised without a five-second test.
var parquetStatsWait = 5 * time.Second

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
		return "", fmt.Errorf("invalid FANOUT_DUCKDB_MEMORY value: %q", mem)
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

	db, writeDB, err := openDuckDB(ctx, dsn, tempDir, duckDBPoolSize(cfg))
	if err != nil {
		return nil, fmt.Errorf("open DuckDB query cache: %w (the cache at %s is rebuildable from Parquet)", err, dbPath)
	}
	d := &Duck{DB: db, writeDB: writeDB, cfg: cfg, repository: repository, publicationBounds: repository.PublicationBounds}
	// Reads are the pool that can starve, so it is the one worth watching.
	d.releasePoolGauges = metrics.SetDuckDBPoolSource(db.Stats)
	// And what DuckDB admits to holding, so the gap against process RSS is
	// observable rather than inferred.
	d.releaseMemoryGauges = metrics.SetDuckDBMemorySource(func() map[string]int64 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows, err := db.QueryContext(ctx, "SELECT tag, sum(memory_usage_bytes) FROM duckdb_memory() GROUP BY tag")
		if err != nil {
			return nil
		}
		defer rows.Close()
		tags := make(map[string]int64, 16)
		for rows.Next() {
			var tag string
			var bytes int64
			if err := rows.Scan(&tag, &bytes); err != nil {
				return nil
			}
			tags[tag] = bytes
		}
		// A partial read is worse than no read: it publishes a low sum, and
		// the untracked figure derived from it reads correspondingly high.
		if err := rows.Err(); err != nil {
			return nil
		}
		return tags
	})
	if cfg.DuckDBMemory == "" {
		// Only when the operator hasn't pinned FANOUT_DUCKDB_MEMORY: keep DuckDB's
		// cgroup-aware auto limit on big boxes but leave absolute RAM headroom on
		// small ones, so the kernel doesn't OOM-kill the whole process.
		if err := applyMemoryHeadroom(ctx, db); err != nil {
			slog.Warn("apply memory headroom failed; using DuckDB default memory_limit", "err", err)
		}
	}
	// DDL goes through the write handle for the same reason every other write
	// does; the two handles share one DuckDB instance, so the tables and views
	// are the same ones the read pool sees.
	if err := CreateCacheTables(writeDB); err != nil {
		_ = d.Close()
		return nil, err
	}
	if err := d.loadRollupWatermarks(ctx); err != nil {
		_ = d.Close()
		return nil, err
	}
	if err := CreateParquetViews(writeDB, repository.Parquet.Dir()); err != nil {
		_ = d.Close()
		return nil, err
	}
	if err := CreateViews(writeDB); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("create views: %w", err)
	}
	if err := engine.ConfigurePolicy(ctx, writeDB, repository.Parquet.Dir(), cfg.QueryDir()); err != nil {
		_ = d.Close()
		return nil, err
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

// skipRollupToLatest advances every rollup watermark to the publication
// horizon, so existing data is treated as already-processed rather than
// aggregated as a backlog. This avoids a multi-minute first-rollup catch-up
// that monopolizes DuckDB write capacity when standing up a large pre-seeded
// historical dataset. Runs once at boot before RunRollups, so taking the write
// gate here is uncontended.
func (d *Duck) skipRollupToLatest(ctx context.Context) error {
	unlock := d.writeGate.Lock(writegate.WriteRollupSkip)
	defer unlock()

	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	horizon, _, _ := d.publicationBounds()
	for _, w := range []struct {
		key       string
		watermark int64
	}{
		{serviceRollupStateKey, horizon},
		{edgeRollupStateKey, horizon},
	} {
		if err := storeRollupWatermark(ctx, tx, w.key, w.watermark); err != nil {
			return err
		}
	}
	if err := clearEdgeRollupCursor(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.rollupWatermarks = map[string]int64{serviceRollupStateKey: horizon, edgeRollupStateKey: horizon}
	return nil
}

// openDuckDB returns the read pool and the single-connection write handle.
//
// Both come from one connector so they share one DuckDB instance and therefore
// one catalog: a write committed on one handle is visible to the other, and
// DDL run at boot applies to both. (A second connector to the same file would
// also work — DuckDB keeps an instance cache — but two owners of one instance
// is exactly the hazard the wrapper below exists to avoid.)
func openDuckDB(ctx context.Context, dsn, tempDir string, maxConns int) (*sql.DB, *sql.DB, error) {
	// Every pooled connection must use UTC. DuckDB otherwise inherits the host
	// timezone and casts TIMESTAMPTZ rollup buckets into local wall-clock
	// TIMESTAMP values, while API windows arrive in UTC.
	//
	// temp_directory is an instance-global setting: re-setting it after the temp
	// dir has already been used fails with "Cannot switch temporary directory
	// after the current one has been used". The boot hook runs once per pooled
	// connection, so only the first connection sets it; the rest inherit the
	// instance-wide value. LOAD/ATTACH stay per-connection — they're idempotent.
	var tempDirOnce sync.Once
	var tempDirErr error

	connector, err := duckdb.NewConnector(dsn, func(execer driver.ExecerContext) error {
		if _, err := execer.ExecContext(ctx, "SET TimeZone='UTC'", nil); err != nil {
			return fmt.Errorf("set timezone: %w", err)
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
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	db := sql.OpenDB(connector)
	// The on-disk DuckDB file contains only rebuildable rollups and views; Parquet
	// scans may run concurrently across the machine-sized pool.
	if maxConns < 1 {
		maxConns = 1
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

	// Independent analytical and completed-batch writers need two connections.
	// Their tables are disjoint; each domain serializes its own mutations.
	//
	// The write handle gets a connector that cannot close the instance.
	// database/sql closes a connector that implements io.Closer when its DB is
	// closed, and duckdb's Connector.Close calls duckdb_close behind an
	// unsynchronized bool — so two handles over one connector means two
	// duckdb_close calls on one instance, which crashes the process rather
	// than returning an error. The read pool owns the instance; the write
	// handle borrows it.
	writeDB := sql.OpenDB(borrowedConnector{connector})
	writeDB.SetMaxOpenConns(2)
	writeDB.SetMaxIdleConns(2)
	return db, writeDB, nil
}

// borrowedConnector hands out connections from a connector it does not own.
// It deliberately does not implement io.Closer: that is the whole point.
type borrowedConnector struct{ driver.Connector }

func sqlLiteral(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func (d *Duck) Close() error {
	d.closeOnce.Do(func() {
		if d.releasePoolGauges != nil {
			d.releasePoolGauges()
		}
		// Same reason as the pool source: the closure captures this Duck's
		// read pool, so leaving it installed past Close means the next scrape
		// queries a closed pool and the gauges freeze at stale values that
		// still look live.
		if d.releaseMemoryGauges != nil {
			d.releaseMemoryGauges()
		}
		var errs []error
		if d.writeDB != nil {
			// The write handle holds a connector that does not close the
			// instance, so this releases its connection and nothing else.
			errs = append(errs, d.writeDB.Close())
		}
		// A Duck assembled field by field, as tests do, may hold neither
		// handle; closing one is then nothing to do rather than a panic.
		if d.DB != nil {
			errs = append(errs, d.DB.Close())
		}
		d.closeErr = errors.Join(errs...)
	})
	return d.closeErr
}

// writer returns the dedicated write handle initialized by NewDuck.
func (d *Duck) writer() *sql.DB { return d.writeDB }

// checkpoint folds the rollup cache's WAL back into its file, and says how long
// that took. A checkpoint blocks new transactions for its duration, so it is a
// candidate explanation for a stalled query and has to be measurable.
//
// The non-FORCE form refuses while another *write* transaction is open. Every
// domain is serialized by its own gate. Maintenance holds both gates before
// calling this function, so a refusal should not happen — the counter is there
// to say so rather than to be assumed. Only a checkpoint that ran is timed: a
// refusal returns in microseconds and would drag the distribution towards zero,
// which is the opposite of what the histogram is for.
func (d *Duck) checkpoint(ctx context.Context) error {
	start := time.Now()
	if _, err := d.writer().ExecContext(ctx, "CHECKPOINT"); err != nil {
		metrics.DuckDBCheckpointFailures.Inc()
		return err
	}
	metrics.DuckDBCheckpoint.Observe(time.Since(start).Seconds())
	return nil
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
	d.updateParquetStats(ctx)
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		d.runMaintenanceLoop(ctx)
	}()
	defer func() { <-maintenanceDone }()
	readCacheDone := make(chan struct{})
	go func() {
		defer close(readCacheDone)
		d.runReadCacheLoop(ctx)
	}()
	defer func() { <-readCacheDone }()

	versionDone := make(chan struct{})
	go func() {
		defer close(versionDone)
		d.runVersionRollupLoop(ctx)
	}()
	defer func() { <-versionDone }()

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
			d.updateParquetStats(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// updateParquetStats refreshes the per-signal file-count and byte-size gauges.
//
// The wait for the snapshot is bounded. Every other reader carries a request
// deadline, but this one runs on the rollup and maintenance loops under the
// process context, so an unbounded wait here would park those loops for as
// long as a publication stayed stuck. Gauges are refreshed again next tick;
// skipping one refresh costs nothing that waiting would not cost more.
func (d *Duck) updateParquetStats(ctx context.Context) {
	statsCtx, cancel := context.WithTimeout(ctx, parquetStatsWait)
	defer cancel()
	if err := d.lockParquetRead(statsCtx, readerStats); err != nil {
		slog.Warn("parquet stats skipped", "err", err)
		return
	}
	defer d.parquetMu.RUnlock()
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

	if n, err := d.RefreshReadCaches(ctx); err != nil {
		errs = append(errs, fmt.Errorf("completed batch read cache: %w", err))
	} else {
		affected += n
	}

	if n, err := drainEdgeRollup(ctx, d.cfg.RollupInterval/2, d.refreshEdgeRollupPass); err != nil {
		errs = append(errs, fmt.Errorf("edge rollup: %w", err))
	} else {
		affected += n
	}

	return int(affected), errors.Join(errs...)
}

// Completed-file caches do not inherit the minute-based analytical watermark.
// Drain bounded transactions promptly, yielding to other writers between passes.
func (d *Duck) runReadCacheLoop(ctx context.Context) {
	delay := time.Second
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		rows, err := d.RefreshReadCaches(ctx)
		delay = time.Second
		if err != nil && ctx.Err() == nil {
			slog.Warn("completed batch read cache failed", "err", err)
		} else if rows > 0 {
			delay = 100 * time.Millisecond
		}
	}
}

// Version history uses the analytical gate and its own cadence. Its gate wait
// must never delay complete-file caching on the independent cache gate.
func (d *Duck) runVersionRollupLoop(ctx context.Context) {
	delay := time.Second
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = time.Second
		if batches, err := d.DrainVersionRollup(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("version rollup failed", "err", err)
		} else if batches > 0 {
			delay = 100 * time.Millisecond
		}
	}
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
		d.updateParquetStats(ctx)
	}
	runMaintenanceSchedule(ctx, every, run, func() int {
		compacted, err := d.runParquetCompactionCycle(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("telemetry compaction cycle failed", "err", err)
		}
		return compacted
	})
}

// Keep compaction busy while a cycle makes progress, then poll at the normal
// idle cadence. The independent maintenance ticker retains the hourly pass.
func runMaintenanceSchedule(ctx context.Context, every time.Duration, run func(), compact func() int) {
	run()
	maintenanceTicker := time.NewTicker(every)
	defer maintenanceTicker.Stop()
	var compactionTimer *time.Timer
	var compactionTick <-chan time.Time
	if every > parquetCompactionCycle {
		compactionTimer = time.NewTimer(parquetCompactionCycle)
		compactionTick = compactionTimer.C
		defer compactionTimer.Stop()
	}
	for {
		select {
		case <-maintenanceTicker.C:
			run()
		case <-compactionTick:
			compacted := compact()
			delay := parquetCompactionCycle
			if compacted > 0 {
				delay = 100 * time.Millisecond
			}
			compactionTimer.Reset(delay)
		case <-ctx.Done():
			return
		}
	}
}

func (d *Duck) runParquetCompactionCycle(ctx context.Context) (int, error) {
	if d.repository == nil {
		return 0, nil
	}
	started := time.Now()
	if err := d.repository.RecoverParquet(ctx, d); err != nil {
		metrics.RecordTelemetryOperation(metrics.TelemetryCompaction, metrics.TelemetryError, time.Since(started).Seconds())
		return 0, err
	}
	compacted, err := d.repository.CompactParquetPass(ctx, d, parquetMaintenanceBatchLimit, parquetCompactionCycleBudget)
	result := metrics.TelemetryNoop
	if err != nil {
		result = metrics.TelemetryError
	} else if compacted > 0 {
		result = metrics.TelemetrySuccess
	}
	metrics.RecordTelemetryOperation(metrics.TelemetryCompaction, result, time.Since(started).Seconds())
	return compacted, err
}

func (d *Duck) runRepositoryMaintenance(ctx context.Context) error {
	start := time.Now()
	var pruneErr error
	if d.repository != nil {
		recoveryErr := d.repository.RecoverParquet(ctx, d)
		compactStart := time.Now()
		var cleanupErr, parquetErr, compactErr error
		compacted := 0
		if recoveryErr == nil {
			cleanupErr = d.repository.CleanupParquet()
			if d.cfg.RetentionDays > 0 {
				_, parquetErr = d.repository.PruneParquetPass(ctx, d, time.Now().Add(-time.Duration(d.cfg.RetentionDays)*24*time.Hour).UnixNano(), parquetMaintenanceBatchLimit, parquetMaintenancePhaseBudget)
			}
			compacted, compactErr = d.repository.CompactParquetPass(ctx, d, parquetMaintenanceBatchLimit, parquetMaintenancePhaseBudget)
		}
		compactResult := metrics.TelemetryNoop
		if recoveryErr != nil || compactErr != nil {
			compactResult = metrics.TelemetryError
		} else if compacted > 0 {
			compactResult = metrics.TelemetrySuccess
		}
		metrics.RecordTelemetryOperation(metrics.TelemetryCompaction, compactResult, time.Since(compactStart).Seconds())
		pruneErr = errors.Join(pruneErr, recoveryErr, cleanupErr, parquetErr, compactErr)
	}
	cacheErr := func() error {
		unlock, err := d.writeGate.LockContext(ctx, writegate.WriteMaintenance)
		if err != nil {
			return err
		}
		defer unlock()
		unlockCache, err := d.cacheGate.LockContext(ctx, writegate.WriteMaintenance)
		if err != nil {
			return err
		}
		defer unlockCache()
		var errs []error
		if d.cfg.RetentionDays > 0 {
			for _, table := range []string{"service_rollup", "edge_rollup"} {
				if _, err := d.writer().ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE bucket < now() - INTERVAL %d DAY", table, d.cfg.RetentionDays)); err != nil {
					errs = append(errs, fmt.Errorf("prune %s: %w", table, err))
				}
			}
		}
		return errors.Join(errors.Join(errs...), d.checkpoint(ctx))
	}()
	err := errors.Join(pruneErr, cacheErr)
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
		d.maintenanceFailures = 0
	} else {
		d.maintenanceFailures++
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
	// Serialize against other DuckDB writers (edge rollup and maintenance).
	// The write gate is always acquired before a connection to keep lock ordering
	// consistent and deadlock-free.
	unlock := d.writeGate.Lock(writegate.WriteRollupService)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !d.tryRollupParquetRead() {
		result = metrics.RollupNoop
		return 0, nil
	}
	defer d.parquetMu.RUnlock()
	horizon, published, minimum := d.publicationBounds()
	sourceMax = published
	// Failed attempts advance the horizon without publishing rows. Until the
	// first publication, retain the initial watermark so catch-up starts at
	// metadata's minimum rather than taking hour-sized steps from the epoch.
	if published == 0 {
		result = metrics.RollupNoop
		updateRollupProgress(metrics.RollupService, true, d.rollupWatermarks[serviceRollupStateKey], 0)
		return 0, nil
	}
	lastWatermark, err := d.cachedRollupWatermark(ctx, serviceRollupStateKey, horizon)
	if err != nil {
		return 0, err
	}
	watermark = lastWatermark
	if horizon <= lastWatermark {
		result = metrics.RollupNoop
		updateRollupProgress(metrics.RollupService, true, lastWatermark, published)
		return 0, nil
	}
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	windowStart, windowEnd, _ := rollupWindow(lastWatermark, minimum, horizon)
	newWatermark := windowEnd

	args := []any{windowStart, windowEnd, windowStart, windowEnd, windowStart, windowEnd}
	if _, err := tx.ExecContext(ctx, createServicePassAffectedSQL, args...); err != nil {
		return 0, fmt.Errorf("service_rollup/affected: %w", err)
	}
	var lower, upper sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT epoch_ns(min(bucket)), epoch_ns(max(bucket) + INTERVAL 1 MINUTE) FROM service_pass_affected`).Scan(&lower, &upper); err != nil {
		return 0, fmt.Errorf("service_rollup/bounds: %w", err)
	}
	var rows int64
	// An empty ingestion window still advances its watermark, but cannot
	// delete or insert any buckets. Keep exact signed nanoseconds in Go.
	if lower.Valid && upper.Valid {
		lo, hi := time.Unix(0, lower.Int64).UTC(), time.Unix(0, upper.Int64).UTC()
		if _, err := tx.ExecContext(ctx, serviceRollupDeleteSQL, lo, hi); err != nil {
			return 0, fmt.Errorf("service_rollup/delete: %w", err)
		}
		res, err := tx.ExecContext(ctx, serviceRollupInsertSQL, lo, hi, lo, hi, lo, hi)
		if err != nil {
			return 0, fmt.Errorf("service_rollup/insert: %w", err)
		}
		rows, err = res.RowsAffected()
		if err != nil {
			slog.Warn("service rollup rows affected unavailable", "err", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE service_pass_affected`); err != nil {
		return 0, fmt.Errorf("service_rollup/drop_affected: %w", err)
	}

	if err := storeRollupWatermark(ctx, tx, serviceRollupStateKey, newWatermark); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	result = metrics.RollupSuccess
	d.rollupWatermarks[serviceRollupStateKey] = newWatermark
	updateRollupProgress(metrics.RollupService, true, newWatermark, published)

	recordedRows = rows
	return rows, nil
}

// The tick budget bounds starting further passes. Finish each admitted
// sub-window so a slow pass commits its cursor rather than retrying forever.
// Each refresh returns after releasing the write gate, admitting queued writers.
func drainEdgeRollup(ctx context.Context, budget time.Duration, refresh func(context.Context) (int64, bool, error)) (int64, error) {
	deadline := time.Now().Add(budget)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, pending, err := refresh(ctx)
		if err != nil {
			return total, err
		}
		total += n
		if !pending || !time.Now().Before(deadline) {
			return total, nil
		}
	}
}

func (d *Duck) refreshEdgeRollupPass(ctx context.Context) (int64, bool, error) {
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
	unlock := d.writeGate.Lock(writegate.WriteRollupEdge)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if !d.tryRollupParquetRead() {
		result = metrics.RollupNoop
		return 0, false, nil
	}
	defer d.parquetMu.RUnlock()
	horizon, published, minimum := d.publicationBounds()
	sourceMax = published
	// Failed attempts advance the horizon without publishing rows. Until the
	// first publication, retain the initial watermark so catch-up starts at
	// metadata's minimum rather than taking hour-sized steps from the epoch.
	if published == 0 {
		result = metrics.RollupNoop
		updateRollupProgress(metrics.RollupEdge, true, d.rollupWatermarks[edgeRollupStateKey], 0)
		return 0, false, nil
	}
	lastWatermark, err := d.cachedRollupWatermark(ctx, edgeRollupStateKey, horizon)
	if err != nil {
		return 0, false, err
	}
	watermark = lastWatermark
	if horizon <= lastWatermark {
		result = metrics.RollupNoop
		updateRollupProgress(metrics.RollupEdge, true, lastWatermark, published)
		return 0, false, nil
	}
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	// A previous pass that ran out of sub-window budget recorded where to
	// resume and left the ingested watermark untouched. Reuse that window
	// verbatim so its unprocessed start_time tail is finished before the
	// watermark is allowed past it.
	subCursor, err := rollupWatermark(ctx, tx, edgeRollupSubCursorKey)
	if err != nil {
		return 0, false, err
	}
	resumeEnd, err := rollupWatermark(ctx, tx, edgeRollupSubWindowKey)
	if err != nil {
		return 0, false, err
	}
	resuming := subCursor > 0 && resumeEnd > lastWatermark

	windowStart, windowEnd, chunked := rollupWindow(lastWatermark, minimum, horizon)
	// An unfinished start-time sub-window keeps its original ingest horizon.
	watermark = min(windowStart, windowEnd)
	if resuming {
		windowEnd = resumeEnd
		chunked = windowEnd < horizon
	}
	newWatermark := windowEnd

	// Discover affected buckets once per frozen ingestion window, including
	// recent-arrival repairs. Persist only their counts, atomically with the
	// cursor. Resumes read these small summaries rather than the original spans.
	if !resuming {
		if _, err := tx.ExecContext(ctx, "DELETE FROM read_edge_buckets"); err != nil {
			return 0, false, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO read_edge_buckets "+edgePassBucketsSelectSQL, windowStart, windowEnd); err != nil {
			return 0, false, err
		}
	}
	// Keep the per-pass name connection-local. It never holds individual spans.
	if _, err := tx.ExecContext(ctx, "CREATE TEMP VIEW edge_pass_buckets AS SELECT * FROM read_edge_buckets"); err != nil {
		return 0, false, err
	}

	// Query the start_time range that the affected ingested-window spans cover.
	// We sub-window this range in edgeStartChunkNanos increments so the
	// call_edges self-join never fans out over more than 30 min of start_time,
	// even when a burst catches up hours of backlog in a single ingested window.
	var minStartT, maxStartT sql.NullTime
	err = tx.QueryRowContext(ctx, `
SELECT MIN(bucket::TIMESTAMP_NS), MAX(bucket::TIMESTAMP_NS)
FROM edge_pass_buckets`).Scan(&minStartT, &maxStartT)
	if err != nil {
		return 0, false, err
	}

	var totalAffected int64
	completed := true
	var nextCursor int64
	if minStartT.Valid {
		subLo := minStartT.Time
		if subCursor > 0 {
			if resumeAt := time.Unix(0, subCursor).In(subLo.Location()); resumeAt.After(subLo) {
				subLo = resumeAt
			}
		}
		maxT := maxStartT.Time
		processed := 0
		for !subLo.After(maxT) {
			// Release the snapshot at a resumable boundary as soon as a
			// publisher waits. Always finish the first sub-window so a busy
			// compaction cycle cannot starve the rollup.
			if processed == maxEdgeSubWindowsPerPass || (processed > 0 && d.parquetMu.publisherWaiting()) {
				completed = false
				nextCursor = subLo.UnixNano()
				break
			}
			subHi, err := edgeSubWindowEnd(ctx, tx, subLo, maxT, maxEdgeSpansPerSubWindow)
			if err != nil {
				return 0, false, err
			}
			if _, err := tx.ExecContext(ctx, edgeRollupDeleteSQL, subLo, subHi); err != nil {
				return 0, false, err
			}
			res, err := insertEdgeRollup(ctx, tx, subLo, subHi)
			if err != nil {
				return 0, false, err
			}
			rows, err := res.RowsAffected()
			if err != nil {
				slog.Warn("edge rollup rows affected unavailable", "err", err)
			} else {
				totalAffected += rows
			}
			subLo = subHi
			processed++
		}
	}

	// An unfinished window keeps the ingested watermark where it was: the
	// sub-window cursor is the only durable record that part of this window is
	// already aggregated, and advancing past it would drop the remainder.
	storedWatermark := newWatermark
	if completed {
		if err := storeRollupWatermark(ctx, tx, edgeRollupStateKey, newWatermark); err != nil {
			return 0, false, err
		}
		if err := clearEdgeRollupCursor(ctx, tx); err != nil {
			return 0, false, err
		}
	} else {
		storedWatermark = lastWatermark
		if _, err := tx.ExecContext(ctx, `DELETE FROM read_edge_buckets WHERE bucket < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, time.Unix(0, nextCursor)); err != nil {
			return 0, false, err
		}
		if err := storeRollupWatermark(ctx, tx, edgeRollupSubCursorKey, nextCursor); err != nil {
			return 0, false, err
		}
		if err := storeRollupWatermark(ctx, tx, edgeRollupSubWindowKey, windowEnd); err != nil {
			return 0, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DROP VIEW edge_pass_buckets"); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}

	result = metrics.RollupSuccess
	recordedRows = totalAffected
	d.rollupWatermarks[edgeRollupStateKey] = storedWatermark
	updateRollupProgress(metrics.RollupEdge, true, max(storedWatermark, min(windowStart, windowEnd)), published)
	return totalAffected, !completed || chunked, nil
}

// clearEdgeRollupCursor discards the resume point and its bucket summaries
// together, so the next pass opens a fresh ingested window.
func clearEdgeRollupCursor(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM read_edge_buckets"); err != nil {
		return err
	}
	if err := storeRollupWatermark(ctx, tx, edgeRollupSubCursorKey, 0); err != nil {
		return err
	}
	return storeRollupWatermark(ctx, tx, edgeRollupSubWindowKey, 0)
}

// rollupChunkNanos caps how much ingest history one rollup pass scans. A pass
// that starts far behind — first run on an existing dataset, or recovery after
// an outage — catches up one chunk per tick instead of aggregating the whole
// backlog in a single statement. Unbounded catch-up is what took prod down on
// 2026-06-13 (UTC): the edge rollup's first pass covered 12 days of spans,
// spilled 375 GiB to temp, filled the disk, and never committed.
// One-hour chunks preserve fast catch-up while bounding the work admitted to a
// transaction. Publication may wait for the current chunk, but does not cancel
// it and force the same cache work to restart indefinitely.
const rollupChunkNanos = int64(time.Hour)

// edgeStartChunkNanos bounds how wide a start_time range one edge-rollup
// DELETE+INSERT processes, so the call_edges self-join over a wide backlog
// (catch-up/bulk-load) can't exhaust memory. The pass loops over sub-windows.
const edgeStartChunkNanos = int64(30 * time.Minute)

// maxEdgeSpansPerSubWindow makes the start-time window adaptive under dense
// ingest. The one-minute rollup bucket is the indivisible lower bound: a hot
// bucket above this limit still runs as one correct aggregate instead of being
// split into partial results.
const maxEdgeSpansPerSubWindow int64 = 250_000

// maxEdgeSubWindowsPerPass bounds how many start_time sub-windows one pass
// processes, and with it how long that pass holds the Parquet read gate.
//
// Chunking ingested time alone does not bound the work: a seed load or a
// backfill compresses a wide start_time range into a narrow ingested window,
// so a single one-hour chunk can expand to hundreds of sub-windows and hold
// the gate for as long as that takes. No timeout can bound that from outside —
// cancelling mid-transaction only discards the work and retries it forever. So
// the pass instead stops at a fixed number of sub-windows, persists where to
// resume, and leaves the ingested watermark where it was. Combined with the
// adaptive row limit above, every pass makes bounded forward progress without
// assuming a uniform event rate. One sub-window per pass keeps each hold short
// enough for version and anomaly writes to get the gate during a backlog.
const maxEdgeSubWindowsPerPass = 1

func edgeSubWindowEnd(ctx context.Context, tx *sql.Tx, subLo, maxT time.Time, rowLimit int64) (time.Time, error) {
	maxEnd := maxT.Add(time.Minute)
	subHi := subLo.Add(time.Duration(edgeStartChunkNanos))
	if subHi.After(maxEnd) {
		subHi = maxEnd
	}
	for rowLimit > 0 && subHi.Sub(subLo) > time.Minute {
		var rows int64
		if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(SUM(spans), 0)::BIGINT
FROM edge_pass_buckets
WHERE bucket >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND bucket < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS`, subLo, subHi).Scan(&rows); err != nil {
			return time.Time{}, err
		}
		if rows <= rowLimit {
			break
		}
		minutes := int64(subHi.Sub(subLo) / time.Minute / 2)
		if minutes < 1 {
			minutes = 1
		}
		subHi = subLo.Add(time.Duration(minutes) * time.Minute)
	}
	return subHi, nil
}

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

// loadRollupWatermarks restores progress at open. Failed attempts can leave
// an empty processed tail beyond the last durable stamp. That tail is not
// persisted in batch metadata, so a backwards clock after restart can reuse it.
// Rewind only that empty tail (and any partial window extending into it) before
// admitting fresh stamps. Affected-bucket recomputation is idempotent.
func (d *Duck) loadRollupWatermarks(ctx context.Context) error {
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	horizon, _, _ := d.publicationBounds()
	watermarks := make(map[string]int64, 2)
	for _, key := range []string{serviceRollupStateKey, edgeRollupStateKey} {
		value, err := rollupWatermark(ctx, tx, key)
		if err != nil {
			return err
		}
		if value > horizon {
			value = horizon
			if err := storeRollupWatermark(ctx, tx, key, value); err != nil {
				return err
			}
		}
		watermarks[key] = value
	}
	resumeEnd, err := rollupWatermark(ctx, tx, edgeRollupSubWindowKey)
	if err != nil {
		return err
	}
	if resumeEnd > horizon {
		if err := clearEdgeRollupCursor(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.rollupWatermarks = watermarks
	return nil
}

// cachedRollupWatermark is called under writeGate. Read persisted progress once;
// after that an idle pass needs neither a connection nor a Parquet snapshot.
func (d *Duck) cachedRollupWatermark(ctx context.Context, key string, horizon int64) (int64, error) {
	if d.rollupWatermarks == nil {
		d.rollupWatermarks = make(map[string]int64)
	}
	if value, ok := d.rollupWatermarks[key]; ok {
		return value, nil
	}
	if horizon == 0 {
		return 0, nil
	}
	var value int64
	err := d.writer().QueryRowContext(ctx, `SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key = ?`, key).Scan(&value)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	d.rollupWatermarks[key] = value
	return value, nil
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

// serviceRollupP50SQL and serviceRollupP95SQL build the latency columns of a
// rollup pass.
//
// These are approximate on purpose. quantile_cont is holistic: it retains every
// value of every group in a vector on the raw allocator, so its cost grows with
// the rows a pass scans, and memory_limit does not bound it -- DuckDB reports
// zero usage while the process grows by gigabytes. A pass covering an hour of
// ingest at this product's certified rate scans tens of millions of spans, and
// that is how the process reached 11.5 GiB anon-rss on a box configured for a
// 4 GB limit.
//
// approx_quantile keeps a fixed-size t-digest per group instead. Measured on
// 30M rows: 861 MiB of untracked growth becomes 8 MiB. The error is a fraction
// of a percent on a latency percentile that already describes a one-minute
// bucket, which is not a figure anyone reads to three significant digits.
//
// The COALESCE keeps a service that only makes outbound calls -- a load
// generator, a cron worker -- measured on what it does have rather than
// silently reported as having no latency at all.
func serviceRollupP50SQL(alias string) string { return serviceRollupQuantileSQL(alias, 1, "p50_ms") }

func serviceRollupP95SQL(alias string) string { return serviceRollupQuantileSQL(alias, 2, "p95_ms") }

func serviceRollupQuantileSQL(alias string, index int, column string) string {
	served := fmt.Sprintf("approx_quantile(%[1]s.duration_ms, [0.50, 0.95]) FILTER (WHERE COALESCE(%[1]s.kind, '') NOT IN ('SPAN_KIND_CLIENT', 'SPAN_KIND_PRODUCER'))", alias)
	all := fmt.Sprintf("approx_quantile(%s.duration_ms, [0.50, 0.95])", alias)
	return fmt.Sprintf("COALESCE(%s[%d], %s[%d]) AS %s", served, index, all, index, column)
}

const createServicePassAffectedSQL = `
CREATE OR REPLACE TEMP TABLE service_pass_affected AS
  SELECT DISTINCT namespace, date_trunc('minute', start_time::TIMESTAMP_NS) AS bucket, service
  FROM spans
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND start_time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time::TIMESTAMP_NS) AS bucket, service
  FROM logs
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != ''
  UNION
  SELECT DISTINCT namespace, date_trunc('minute', time::TIMESTAMP_NS) AS bucket, service
  FROM metrics
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND time IS NOT NULL
    AND service IS NOT NULL
    AND service != '';`

const serviceRollupDeleteSQL = `
DELETE FROM service_rollup
WHERE bucket >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND bucket < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND EXISTS (
    SELECT 1 FROM service_pass_affected a
    WHERE a.namespace = service_rollup.namespace
      AND a.bucket = service_rollup.bucket
      AND a.service = service_rollup.service
);`

var serviceRollupInsertSQL = `
WITH span_agg AS (
  SELECT
    s.namespace,
    date_trunc('minute', s.start_time::TIMESTAMP_NS) AS bucket,
    s.service,
    COUNT(*) AS spans,
    COUNT(*) FILTER (WHERE COALESCE(s.kind, '') NOT IN ('SPAN_KIND_CLIENT', 'SPAN_KIND_PRODUCER')) AS served_spans,
    -- Latency describes the work a service performs, not the calls it waits
    -- on. A CLIENT or PRODUCER span measures a dependency: a subscription held
    -- open for ten minutes makes its subscriber look broken while saying
    -- nothing about how that subscriber serves anyone. Grading on those is what
    -- marked half the demo's services unhealthy for holding a flagd event
    -- stream open. Error rate still counts every span, because a failing
    -- outbound call is the caller's problem too.
    --
    -- COALESCE keeps a service that only makes outbound calls — a load
    -- generator, a cron worker — measured on what it does have rather than
    -- silently reported as having no latency at all.
    ` + serviceRollupP50SQL("s") + `,
    ` + serviceRollupP95SQL("s") + `,
    avg(CASE WHEN s.status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate
  FROM spans s
  JOIN service_pass_affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time::TIMESTAMP_NS)
   AND a.service = s.service
  -- Bound the scan to the affected bucket range so Telemetry prunes parquet by
  -- start_time stats instead of scanning all history (the join on a computed
  -- date_trunc bucket alone does not prune). The range is a provable superset
  -- of the affected buckets — every row in an affected bucket has start_time in
  -- [MIN(bucket), MAX(bucket)+1min) — so it drops no rows. A late span with an
  -- old start_time widens the range (and this scan) only for the pass that
  -- ingests it, same trade-off as the edge rollup's parent bound.
  WHERE s.start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.start_time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
  GROUP BY s.namespace, date_trunc('minute', s.start_time::TIMESTAMP_NS), s.service
),
log_agg AS (
  SELECT
    l.namespace,
    date_trunc('minute', l.time::TIMESTAMP_NS) AS bucket,
    l.service,
    COUNT(*) AS log_count
  FROM logs l
  JOIN service_pass_affected a
    ON a.namespace = l.namespace
   AND a.bucket = date_trunc('minute', l.time::TIMESTAMP_NS)
   AND a.service = l.service
  WHERE l.time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND l.time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
  GROUP BY l.namespace, date_trunc('minute', l.time::TIMESTAMP_NS), l.service
),
metric_agg AS (
  SELECT
    m.namespace,
    date_trunc('minute', m.time::TIMESTAMP_NS) AS bucket,
    m.service,
    COUNT(DISTINCT m.name) AS metric_count
  FROM metrics m
  JOIN service_pass_affected a
    ON a.namespace = m.namespace
   AND a.bucket = date_trunc('minute', m.time::TIMESTAMP_NS)
   AND a.service = m.service
  WHERE m.time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND m.time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
  GROUP BY m.namespace, date_trunc('minute', m.time::TIMESTAMP_NS), m.service
)
INSERT INTO service_rollup (
  namespace,
  bucket,
  service,
  spans,
  served_spans,
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
  COALESCE(s.served_spans, 0),
  COALESCE(s.p50_ms, 0),
  COALESCE(s.p95_ms, 0),
  COALESCE(s.error_rate, 0),
  COALESCE(l.log_count, 0),
  COALESCE(m.metric_count, 0)
FROM service_pass_affected a
LEFT JOIN span_agg s USING (namespace, bucket, service)
LEFT JOIN log_agg l USING (namespace, bucket, service)
LEFT JOIN metric_agg m USING (namespace, bucket, service);`

// edgeArrivalGap bounds recent-arrival repair behind the completed ingest
// watermark. Older relationships are not guaranteed to be repaired.
const edgeArrivalGap = 5 * time.Minute

// Only relationship keys are read from the recent ingestion range. Its stamp
// predicates allow Parquet footer pruning independently of event-time history.
// Messaging uses the aggregate's exact keys, including the shared minute.
var createEdgePassBucketsSQL = "CREATE TEMP TABLE edge_pass_buckets AS " + edgePassBucketsSelectSQL

var edgePassBucketsSelectSQL = fmt.Sprintf(`
WITH incoming AS MATERIALIZED (
  SELECT namespace, trace_id, span_id, start_time, kind,
         messaging_destination, messaging_system
  FROM spans
  WHERE ingested_unix_nano > $1::BIGINT
    AND ingested_unix_nano <= $2::BIGINT
    AND start_time IS NOT NULL
),
recent AS MATERIALIZED (
  SELECT namespace, trace_id, parent_span_id, start_time, kind,
         messaging_destination, messaging_system
  FROM spans
  WHERE ingested_unix_nano > ($1::BIGINT - %d)
    AND ingested_unix_nano <= $1::BIGINT
    AND start_time IS NOT NULL
),
affected_spans AS (
  SELECT namespace, start_time FROM incoming
  UNION ALL
  SELECT child.namespace, child.start_time
  FROM recent child
  SEMI JOIN incoming parent
    ON child.namespace = parent.namespace
   AND child.trace_id = parent.trace_id
   AND child.parent_span_id = parent.span_id
  UNION ALL
  SELECT r.namespace, r.start_time
  FROM recent r
  SEMI JOIN incoming i
    ON r.namespace = i.namespace
   AND date_trunc('minute', r.start_time::TIMESTAMP_NS) = date_trunc('minute', i.start_time::TIMESTAMP_NS)
   AND r.messaging_destination = i.messaging_destination
   AND r.messaging_system = i.messaging_system
   AND r.kind = CASE i.kind
     WHEN 'SPAN_KIND_PRODUCER' THEN 'SPAN_KIND_CONSUMER'
     WHEN 'SPAN_KIND_CONSUMER' THEN 'SPAN_KIND_PRODUCER' END
)
SELECT namespace,
       date_trunc('minute', start_time::TIMESTAMP_NS)::TIMESTAMPTZ_NS AS bucket,
       COUNT(*) AS spans
FROM affected_spans
GROUP BY namespace, bucket
`, int64(edgeArrivalGap))

const edgeAffectedBucketsSQL = `
WITH affected AS (
  SELECT namespace, bucket::TIMESTAMP_NS AS bucket
  FROM edge_pass_buckets
  WHERE bucket >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND bucket < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS
)
`

const edgeRollupDeleteSQL = edgeAffectedBucketsSQL + `
DELETE FROM edge_rollup
WHERE EXISTS (
  SELECT 1
  FROM affected
  WHERE affected.namespace = edge_rollup.namespace
    AND affected.bucket = edge_rollup.bucket
);`

const edgeChildTracesSQL = edgeAffectedBucketsSQL + `,
-- Compute the affected bucket range once for the child and parent filters.
bounds AS (
  SELECT MIN(bucket) AS lo, MAX(bucket) AS hi FROM affected
),
-- Only traces with children in affected buckets can contribute call edges.
-- Bound this distinct relation to the same child window as call_edges, before
-- building the parent side. Namespace is part of the trace identity.
child_traces AS (
  SELECT DISTINCT child.namespace, child.trace_id
  FROM spans child
  JOIN affected a
    ON a.namespace = child.namespace
   AND a.bucket = date_trunc('minute', child.start_time::TIMESTAMP_NS)
  CROSS JOIN bounds
  WHERE child.service IS NOT NULL
    AND child.service != ''
    AND child.start_time >= (bounds.lo)::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND child.start_time < (bounds.hi + INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
)`

// Parent lookup is connection-local and transactional, like edge_pass_buckets.
// The wide scan is issued only after an explicit unmatched-trace check.
// The result reports whether that scan ran.
func prepareEdgeParents(ctx context.Context, tx *sql.Tx, lo, hi time.Time) (bool, error) {
	if _, err := tx.ExecContext(ctx, createEdgeParentsSQL, lo, hi); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, createEdgeUnmatchedSQL, lo, hi); err != nil {
		return false, err
	}
	var unmatched bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM edge_pass_unmatched)").Scan(&unmatched); err != nil {
		return false, err
	}
	if unmatched {
		// Replace these traces rather than unioning overlapping parent windows.
		// This retains duplicates without a disjoint time filter on the wide scan.
		if _, err := tx.ExecContext(ctx, deleteEdgeUnmatchedParentsSQL); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, insertEdgeDistantParentsSQL, lo, hi); err != nil {
			return false, err
		}
	}
	_, err := tx.ExecContext(ctx, "DROP TABLE edge_pass_unmatched")
	return unmatched, err
}

func insertEdgeRollup(ctx context.Context, tx *sql.Tx, lo, hi time.Time) (sql.Result, error) {
	if _, err := prepareEdgeParents(ctx, tx, lo, hi); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, edgeRollupInsertSQL, lo, hi)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE edge_pass_parents"); err != nil {
		return nil, err
	}
	return res, nil
}

const createEdgeParentsSQL = `CREATE TEMP TABLE edge_pass_parents AS ` + edgeChildTracesSQL + `
SELECT parent.namespace, parent.span_id, parent.trace_id, parent.service
FROM spans parent
SEMI JOIN child_traces child
  ON parent.namespace = child.namespace
 AND parent.trace_id = child.trace_id
CROSS JOIN bounds
WHERE parent.start_time >= (bounds.lo - INTERVAL 5 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND parent.start_time <= (bounds.hi + INTERVAL 5 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND parent.service IS NOT NULL
  AND parent.service != ''`

const createEdgeUnmatchedSQL = `CREATE TEMP TABLE edge_pass_unmatched AS ` + edgeAffectedBucketsSQL + `,
bounds AS (
  SELECT MIN(bucket) AS lo, MAX(bucket) AS hi FROM affected
)
SELECT DISTINCT child.namespace, child.trace_id
FROM spans child
JOIN affected a
  ON a.namespace = child.namespace
 AND a.bucket = date_trunc('minute', child.start_time::TIMESTAMP_NS)
ANTI JOIN edge_pass_parents parent
  ON parent.namespace = child.namespace
 AND parent.trace_id = child.trace_id
 AND parent.span_id = child.parent_span_id
CROSS JOIN bounds
WHERE child.service IS NOT NULL
  AND child.service != ''
  AND child.parent_span_id IS NOT NULL
  AND child.parent_span_id != ''
  AND child.start_time >= bounds.lo::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND child.start_time < (bounds.hi + INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS`

const deleteEdgeUnmatchedParentsSQL = `
DELETE FROM edge_pass_parents
WHERE EXISTS (
  SELECT 1 FROM edge_pass_unmatched child
  WHERE child.namespace = edge_pass_parents.namespace
    AND child.trace_id = edge_pass_parents.trace_id
)`

const insertEdgeDistantParentsSQL = edgeAffectedBucketsSQL + `,
bounds AS (
  SELECT MIN(bucket) AS lo, MAX(bucket) AS hi FROM affected
)
INSERT INTO edge_pass_parents
SELECT parent.namespace, parent.span_id, parent.trace_id, parent.service
FROM spans parent
SEMI JOIN edge_pass_unmatched child
  ON parent.namespace = child.namespace
 AND parent.trace_id = child.trace_id
CROSS JOIN bounds
WHERE parent.start_time >= (bounds.lo - INTERVAL 1 HOUR)::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND parent.start_time <= (bounds.hi + INTERVAL 1 HOUR)::TIMESTAMP_NS::TIMESTAMPTZ_NS
  AND parent.service IS NOT NULL
  AND parent.service != ''`

const edgeRollupInsertSQL = edgeAffectedBucketsSQL + `,
bounds AS (
  SELECT MIN(bucket) AS lo, MAX(bucket) AS hi FROM affected
),
parent_scope AS (
  SELECT namespace, span_id, trace_id, service FROM edge_pass_parents
),
call_edges AS (
  SELECT
    child.namespace,
    date_trunc('minute', child.start_time::TIMESTAMP_NS) AS bucket,
    parent.service AS caller,
    child.service AS callee,
    COUNT(*) AS calls,
    AVG(child.duration_ms) AS avg_ms,
    AVG(CASE WHEN child.status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate,
    'call' AS edge_type
  FROM spans child
  JOIN parent_scope parent
    ON child.parent_span_id = parent.span_id
   AND child.trace_id = parent.trace_id
   AND child.namespace = parent.namespace
  JOIN affected a
    ON a.namespace = child.namespace
   AND a.bucket = date_trunc('minute', child.start_time::TIMESTAMP_NS)
  -- Keep bounds as an explicit CROSS JOIN after the keyed joins, so the
  -- planner can fold its single row into the child filters. The plan test
  -- guards against Cartesian or correlated span-to-span joins.
  CROSS JOIN bounds
  WHERE child.service IS NOT NULL
    AND child.service != ''
    AND parent.service != child.service
    AND child.start_time >= (bounds.lo)::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND child.start_time < (bounds.hi + INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
  GROUP BY child.namespace, date_trunc('minute', child.start_time::TIMESTAMP_NS), parent.service, child.service
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
    date_trunc('minute', s.start_time::TIMESTAMP_NS) AS bucket,
    s.service,
    s.messaging_destination AS destination,
    s.messaging_system AS msg_system
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time::TIMESTAMP_NS)
  WHERE s.kind = 'SPAN_KIND_PRODUCER'
    AND s.start_time >= ((SELECT lo FROM bounds))::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.start_time < ((SELECT hi FROM bounds) + INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.service IS NOT NULL
    AND s.service != ''
    AND s.messaging_destination IS NOT NULL
),
consumers AS (
  SELECT
    s.namespace,
    date_trunc('minute', s.start_time::TIMESTAMP_NS) AS bucket,
    s.service,
    s.messaging_destination AS destination,
    s.messaging_system AS msg_system,
    COUNT(*) AS calls
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time::TIMESTAMP_NS)
  WHERE s.kind = 'SPAN_KIND_CONSUMER'
    AND s.start_time >= ((SELECT lo FROM bounds))::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.start_time < ((SELECT hi FROM bounds) + INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.service IS NOT NULL
    AND s.service != ''
    AND s.messaging_destination IS NOT NULL
  GROUP BY s.namespace, date_trunc('minute', s.start_time::TIMESTAMP_NS), s.service,
    s.messaging_destination,
    s.messaging_system
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

// acquireRead takes a connection from the read pool. The wait is already
// counted by the pool's own gauges; taking it explicitly keeps it out of the
// statement histogram.
func (d *Duck) acquireRead(ctx context.Context) (*sql.Conn, error) {
	return d.DB.Conn(ctx)
}

// QueryContext executes a read against immutable Parquet files and DuckDB's
// local rollup cache.
func (d *Duck) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	if err := d.lockParquetRead(ctx, readerQuery); err != nil {
		return nil, err
	}
	// The connection is taken first and timed separately: DB.QueryContext would
	// fold the wait for a free connection into the statement's duration, and
	// telling queueing from execution is the whole reason this is measured.
	conn, err := d.acquireRead(ctx)
	if err != nil {
		d.parquetMu.RUnlock()
		return nil, err
	}
	var tx *sql.Tx
	var db snapshotSQL = conn
	if window, ok := queryrows.ReadWindow(ctx); ok && d.repository != nil {
		tx, err = beginRead(ctx, conn)
		if err == nil {
			db = tx
			query, err = d.bindSnapshot(ctx, tx, query, window)
		}
		if err != nil {
			endRead(conn, tx)
			d.parquetMu.RUnlock()
			return nil, err
		}
	}
	started := time.Now()
	rows, err := db.QueryContext(ctx, query, args...)
	metrics.DuckDBStatement.Observe(time.Since(started).Seconds())
	if err != nil {
		endRead(conn, tx)
		d.parquetMu.RUnlock()
		return nil, err
	}
	release := func() {
		// The connection goes back to the pool only once its rows are done
		// with it, which is what closing the rows first guarantees.
		//
		// A caller that neither closes nor exhausts its rows now leaks the
		// pool slot as well as the snapshot read-lock — database/sql used to
		// return the connection when the context died, and an explicitly held
		// one is the caller's until it says otherwise. Every call site closes
		// on every path; this is what it costs if one stops.
		endRead(conn, tx)
		d.parquetMu.RUnlock()
	}
	return &lockedRows{Rows: rows, unlock: release}, nil
}

// beginRead opens a read transaction that only its owner ends. A transaction
// begun with the request context is rolled back by database/sql from another
// goroutine when that context is cancelled, while DuckDB may still deliver the
// cancelled statement's interrupt to the ROLLBACK. The rollback then fails,
// the transaction stays open, and the pooled connection fails every later read.
// Statements inside still run under ctx, so cancellation still stops them.
func beginRead(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		discardConn(conn)
	}
	return tx, err
}

// endRead rolls back tx, if any, and returns conn to the pool. A connection
// whose rollback failed may still hold the transaction, so it is discarded
// instead of being handed to the next reader.
func endRead(conn *sql.Conn, tx *sql.Tx) {
	if tx != nil && tx.Rollback() != nil {
		discardConn(conn)
	}
	_ = conn.Close()
}

// discardConn closes conn's driver connection rather than pooling it.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
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
	if _, ok := queryrows.ReadWindow(ctx); ok && d.repository != nil {
		rows, err := d.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			return rows.Scan(dest...)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}

	if err := d.lockParquetRead(ctx, readerQuery); err != nil {
		return err
	}
	defer d.parquetMu.RUnlock()
	conn, err := d.acquireRead(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	// This one covers the scan as well as the statement: a single row cannot
	// be fetched separately from being read. Both are DuckDB's time rather
	// than the pool's, which is the distinction the histogram is drawing.
	started := time.Now()
	defer func() { metrics.DuckDBStatement.Observe(time.Since(started).Seconds()) }()
	return conn.QueryRowContext(ctx, query, args...).Scan(dest...)
}

// Trace pins the immutable Parquet snapshot for the full indexed-file read.
func (d *Duck) Trace(ctx context.Context, query telemetry.TraceQuery) ([]telemetry.IndexedSpan, telemetry.TraceTotals, error) {
	if err := d.lockParquetRead(ctx, readerQuery); err != nil {
		return nil, telemetry.TraceTotals{}, err
	}
	defer d.parquetMu.RUnlock()
	return d.repository.Parquet.Trace(ctx, query)
}

// PublishParquet limits reader exclusion to the atomic directory swap.
//
// The drain and the swap get separate budgets. Sharing one deadline meant a
// publisher that spent most of it waiting for readers entered the exclusive
// window with almost nothing left, cancelled its own renames, and failed the
// pass — having already paid the full cost of excluding every reader. The swap
// is renames plus fsync against a bounded batch count, so it is budgeted for
// what that work costs rather than for whatever the drain happened to leave.
func (d *Duck) PublishParquet(ctx context.Context, publish func(context.Context) error) error {
	drainCtx, cancelDrain := context.WithTimeout(ctx, parquetDrainBudget)
	defer cancelDrain()
	metrics.ParquetPublishWaiters.Inc()
	waitStarted := time.Now()
	err := d.parquetMu.LockContext(drainCtx)
	metrics.ParquetPublishWaiters.Dec()
	metrics.ParquetPublishWait.Observe(time.Since(waitStarted).Seconds())
	if err != nil {
		metrics.ParquetPublishTimeouts.Inc()
		return fmt.Errorf("wait for Parquet readers: %w", err)
	}
	defer d.parquetMu.Unlock()
	swapStarted := time.Now()
	defer func() { metrics.ParquetPublishHold.Observe(time.Since(swapStarted).Seconds()) }()
	swapCtx, cancelSwap := context.WithTimeout(ctx, parquetSwapBudget)
	defer cancelSwap()
	return publish(swapCtx)
}

// Reader classes for the snapshot-wait metric. A query that waits and a rollup
// that waits are different problems with the same symptom, and the whole point
// of measuring here is to tell them apart.
const (
	readerQuery  = "query"
	readerRollup = "rollup"
	readerStats  = "stats"
)

// lockParquetRead records snapshot admission waits. Only ordinary queries use
// the publisher's grace; rollups and stats wait behind queued publishers.
func (d *Duck) lockParquetRead(ctx context.Context, reader string) error {
	start := time.Now()
	mode := parquetReadBackground
	if reader == readerQuery {
		mode = parquetReadQuery
	}
	if err := d.parquetMu.RLockContext(ctx, mode); err != nil {
		metrics.RecordParquetRead(reader, time.Since(start).Seconds(), false)
		return errors.Join(ErrParquetReadWait, err)
	}
	metrics.RecordParquetRead(reader, time.Since(start).Seconds(), true)
	return nil
}

// Keep gate-first lock ordering, but never park the analytical writer gate
// behind a publication. The owning loop retries skipped work on its cadence.
func (d *Duck) tryRollupParquetRead() bool {
	start := time.Now()
	admitted := d.parquetMu.TryRLock(parquetReadBackground)
	metrics.RecordParquetRead(readerRollup, time.Since(start).Seconds(), admitted)
	return admitted
}

// ---- Queries for API ----

// CompletedServiceRollupMinute is the exclusive end of detector input. The
// minute containing the ingest watermark may still be incomplete.
func (d *Duck) CompletedServiceRollupMinute(ctx context.Context) (time.Time, error) {
	var stamp int64
	if err := d.QueryRowScan(ctx, []any{&stamp}, `SELECT coalesce((SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key=?),0)`, serviceRollupStateKey); err != nil {
		return time.Time{}, err
	}
	if stamp == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, stamp).UTC().Truncate(time.Minute), nil
}

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
  strftime(time::TIMESTAMP_NS, '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
  body,
  service AS service_name,
  severity
FROM logs
WHERE time >= (now() - INTERVAL %d MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
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
WHERE time >= (now() - INTERVAL %d MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
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
  WHERE start_time >= (now() - INTERVAL %d MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
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

// WithReadTransaction keeps derived annotation rows and completeness flags in
// one read snapshot. Its callback only receives the query surface.
func (d *Duck) WithReadTransaction(ctx context.Context, read func(queryrows.Queryer) error) error {
	conn, err := d.acquireRead(ctx)
	if err != nil {
		return err
	}
	tx, err := beginRead(ctx, conn)
	defer endRead(conn, tx)
	if err != nil {
		return err
	}
	return read(queryrows.SQLAdapter{DB: tx})
}
