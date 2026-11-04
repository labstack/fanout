package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/engine/writegate"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/readpressure"
	"github.com/labstack/fanout/internal/telemetry"
)

// readCacheVersion covers the schema and semantics of DuckDB's read caches:
// the rollups and their progress, version history, and the per-batch read
// caches. They are derived from immutable Parquet, so a mismatch drops them
// together and they rebuild. Bump it whenever one's shape or the meaning of
// its rows changes.
//
// anomaly_log is not one of them: it holds detector output that nothing can
// rebuild, so the drop keeps it. Its table is created once; any change to its
// schema must be a separate, explicit migration step.
const readCacheVersion = 14

const (
	readCacheBatchLimit = 64
	// Amortize candidate maintenance and bounded pressure pauses without
	// allowing a pass to grow with retained history. File count stays capped.
	readCacheRowBudget = 256000
)

// CreateCacheTables creates DuckDB's tables: the rebuildable read caches and
// the anomaly history. The telemetry rows themselves live in immutable Parquet
// batches.
func CreateCacheTables(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS read_cache_version (version INTEGER)`); err != nil {
		return err
	}
	var version int
	err = tx.QueryRow(`SELECT coalesce(max(version),0) FROM read_cache_version`).Scan(&version)
	if err != nil {
		return err
	}
	if version != readCacheVersion {
		rows, err := tx.Query(`SELECT table_name FROM duckdb_tables() WHERE database_name=current_database() AND schema_name='main' AND NOT temporary AND table_name NOT IN ('read_cache_version','anomaly_log')`)
		if err != nil {
			return err
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, name := range tables {
			if _, err := tx.Exec(`DROP TABLE "` + strings.ReplaceAll(name, `"`, `""`) + `"`); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM read_cache_version`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO read_cache_version VALUES (?)`, readCacheVersion); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		createServiceRollupTable,
		createSpanRollupTable, createLogRollupTable, createEndpointRollupTable,
		createEdgeRollupTable,
		createRollupStateTable,
		createVersionRollupTable,
		createVersionBatchesTable,
		createAnomalyLogTable,
		`CREATE TABLE IF NOT EXISTS read_edge_buckets (namespace VARCHAR, bucket TIMESTAMPTZ_NS, spans BIGINT)`,
		`CREATE TABLE IF NOT EXISTS read_metric_names (batch_id VARCHAR,name VARCHAR)`,
		`CREATE TABLE IF NOT EXISTS read_batches (batch_id VARCHAR PRIMARY KEY,replacement_id VARCHAR)`,
		`CREATE TABLE IF NOT EXISTS read_resource_seconds (batch_id VARCHAR,signal VARCHAR,namespace VARCHAR,service VARCHAR,resource VARIANT,bucket TIMESTAMP_NS,first_seen TIMESTAMPTZ_NS,last_seen TIMESTAMPTZ_NS)`,
		`CREATE TABLE IF NOT EXISTS read_resource_inventory (batch_id VARCHAR,signal VARCHAR,namespace VARCHAR,service VARCHAR,resource VARIANT,first_seen TIMESTAMPTZ_NS,last_seen TIMESTAMPTZ_NS)`,
		`CREATE TABLE IF NOT EXISTS read_trace_candidates (trace_id VARCHAR,namespace VARCHAR,service VARCHAR,min_start BIGINT,max_start BIGINT,max_end BIGINT,has_error INTEGER)`,
		`CREATE TABLE IF NOT EXISTS read_trace_parts (batch_id VARCHAR, namespace VARCHAR, service VARCHAR, trace_id VARCHAR, min_start BIGINT, max_start BIGINT, max_end BIGINT, has_error INTEGER)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS rollup_schema_version (version INTEGER)`); err != nil {
		return err
	}
	var rollupVersion int
	if err := tx.QueryRow(`SELECT coalesce(max(version),0) FROM rollup_schema_version`).Scan(&rollupVersion); err != nil {
		return err
	}
	if rollupVersion != rollup_schema_version {
		for _, stmt := range []string{`DROP TABLE service_rollup`, `DROP TABLE span_rollup`, `DROP TABLE log_rollup`, `DROP TABLE endpoint_rollup`, `DROP TABLE edge_rollup`, createEdgeRollupTable, createServiceRollupTable, createSpanRollupTable, createLogRollupTable, createEndpointRollupTable,
			`DELETE FROM rollup_state WHERE cache_key = '` + serviceRollupStateKey + `'`,
			`DELETE FROM rollup_state WHERE cache_key IN ('` + edgeRollupStateKey + `','` + edgeRollupSubCursorKey + `','` + edgeRollupSubWindowKey + `')`,
			`DELETE FROM rollup_schema_version`} {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO rollup_schema_version VALUES (?)`, rollup_schema_version); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func batchMarkers(ctx context.Context, db snapshotSQL) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT batch_id FROM read_batches`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	markers := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		markers[id] = true
	}
	return markers, rows.Err()
}
func idsSQL(ids []string) string {
	if len(ids) == 0 {
		return "NULL"
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = sqlLiteral(id)
	}
	return strings.Join(quoted, ",")
}

// RefreshReadCaches acknowledges complete immutable files, not an ingest-time
// watermark. A pass only aggregates its selected batches: it never rebuilds a
// retained-history index. Contributions and acknowledgements commit together.
func (d *Duck) RefreshReadCaches(ctx context.Context) (int64, error) {
	rows, _, err := d.refreshReadCaches(ctx)
	return rows, err
}

// refreshReadCaches also reports whether pending files remain beyond the
// pass's budget, so the worker repeats at once only while it is behind.
func (d *Duck) refreshReadCaches(ctx context.Context) (rows int64, backlog bool, err error) {
	started := time.Now()
	didWork := false
	defer func() {
		result := metrics.RollupSuccess
		if err != nil {
			result = metrics.RollupError
		} else if !didWork {
			result = metrics.RollupNoop
		}
		metrics.RecordRollupComponent(metrics.RollupReadCache, result, rows, time.Since(started).Seconds())
	}()

	unlock, err := d.cacheGate.LockContext(ctx, writegate.ReadCache)
	if err != nil {
		return 0, false, err
	}
	defer unlock()
	ctx = readpressure.WithSignal(ctx, &d.readPressure)
	background, err := d.lockBackground(ctx)
	if err != nil {
		return 0, false, err
	}
	defer background()
	pin := d.pinParquet()
	defer d.unpinParquet(pin)
	if err := d.applyCacheTransfers(ctx); err != nil {
		return 0, true, err
	}
	batches := d.repository.Parquet.BatchMetadata()
	markers, err := batchMarkers(ctx, d.writer())
	if err != nil {
		return 0, false, err
	}
	active := map[string]bool{}
	for _, b := range batches {
		active[b.ID] = true
	}
	stale := []string{}
	for id := range markers {
		if !active[id] {
			publishing := d.pendingCacheTransfer(id, false)
			if !publishing {
				stale = append(stale, id)
			}
		}
	}
	sort.SliceStable(batches, func(i, j int) bool { return newestBatchEvent(batches[i]) < newestBatchEvent(batches[j]) })
	pendingCount := 0
	for _, b := range batches {
		if !markers[b.ID] && b.Spans+b.Logs+b.Metrics > 0 {
			pendingCount++
		}
	}
	metrics.ReadCachePendingBatches.Set(float64(pendingCount))
	pending := []telemetry.BatchMetadata{}
	pendingRows, selected := 0, 0
	for i := len(batches) - 1; i >= 0; i-- {
		if !markers[batches[i].ID] && !d.pendingCacheTransfer(batches[i].ID, true) {
			rows := batches[i].Spans + batches[i].Logs + batches[i].Metrics
			// A complete file is indivisible. Admit one oversized file to make
			// progress, otherwise bound new work by rows as well as file count.
			if len(pending) > 0 && pendingRows+rows > readCacheRowBudget {
				break
			}
			pending = append(pending, batches[i])
			pendingRows += rows
			if rows > 0 {
				selected++
			}
			if len(pending) == readCacheBatchLimit {
				break
			}
		}
	}
	if len(pending) == 0 && len(stale) == 0 {
		return 0, false, nil
	}
	didWork = true
	changed, err := changedRetiredBatches(ctx, d.writer(), markers, active, stale)
	if err != nil {
		return 0, false, err
	}
	// Keep the bounded multi-file SQL kernel: per-file transactions spend
	// more time preparing statements than aggregating rows under steady ingest.
	// Yield between these cache batches, never before the first unit of work.
	if d.cachePassStarted {
		if err := d.readPressure.Yield(ctx); err != nil {
			return 0, true, err
		}
	}
	d.cachePassStarted = true
	rows, err = d.cacheBatches(ctx, pending, stale, changed)

	return rows, err == nil && selected < pendingCount, err
}

func (d *Duck) cacheBatches(ctx context.Context, batches []telemetry.BatchMetadata, stale, changed []string) (int64, error) {
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var written int64
	exec := func(stmt string) error {
		result, err := tx.ExecContext(ctx, stmt)
		if err == nil && (strings.HasPrefix(stmt, "INSERT INTO read_") && !strings.HasPrefix(stmt, "INSERT INTO read_batches") || strings.HasPrefix(stmt, "MERGE INTO read_")) {
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			written += n
		}
		return err
	}
	if len(changed) > 0 {
		if err := exec(`CREATE OR REPLACE TEMP TABLE cache_retired_traces AS SELECT DISTINCT trace_id FROM read_trace_parts WHERE batch_id IN (` + idsSQL(changed) + `)`); err != nil {
			return 0, err
		}
	}
	if len(stale) > 0 {
		for _, table := range []string{"read_resource_inventory", "read_resource_seconds", "read_metric_names", "read_trace_parts", "read_batches"} {
			if err := exec("DELETE FROM " + table + " WHERE batch_id IN (" + idsSQL(stale) + ")"); err != nil {
				return 0, err
			}
		}
	}
	if len(changed) > 0 {
		// Retention can change a trace's extrema or error flag. Compaction whose
		// complete replacement is cached preserves them and bypasses this repair.
		if err := exec(`DELETE FROM read_trace_candidates WHERE trace_id IN (SELECT trace_id FROM cache_retired_traces) AND trace_id NOT IN (SELECT trace_id FROM read_trace_parts)`); err != nil {
			return 0, err
		}
		if err := exec(`MERGE INTO read_trace_candidates c USING (` + traceCandidateSelect + ` FROM read_trace_parts WHERE trace_id IN (SELECT trace_id FROM cache_retired_traces) GROUP BY trace_id) AS excluded ON c.trace_id=excluded.trace_id
   WHEN MATCHED THEN UPDATE SET namespace=excluded.namespace,service=excluded.service,min_start=excluded.min_start,max_start=excluded.max_start,max_end=excluded.max_end,has_error=excluded.has_error
   WHEN NOT MATCHED THEN INSERT ` + traceCandidateColumns); err != nil {
			return 0, err
		}
		if err := exec(`DROP TABLE cache_retired_traces`); err != nil {
			return 0, err
		}
	}
	// Each aggregation scans only the pass's new files. Materialize trace
	// parts once for both trace inserts without scanning retained parts.
	source := func(signal string) string {
		var selected []telemetry.BatchMetadata
		for _, b := range batches {
			_, count := batchTime(b, signal)
			if count == 0 {
				continue
			}
			selected = append(selected, b)
		}
		if len(selected) == 0 {
			return ""
		}
		// One Parquet scan binds the pass's files. The virtual filename column
		// identifies each contribution without a separate UNION branch per file.
		physical := strings.Replace(d.physicalSource(signal, selected), "SELECT ", "SELECT filename AS __fanout_filename,", 1)
		return strings.Replace(cleanSource(signal, physical), "SELECT\n", "SELECT "+d.batchIDColumn(signal, "__fanout_filename")+" AS batch_id,\n", 1)
	}
	for _, s := range telemetry.Signals {
		if rows := source(s.Name); rows != "" {
			clock := s.Clock()
			if err := exec(`INSERT INTO read_resource_inventory SELECT batch_id,` + sqlLiteral(s.Name) + `,namespace,service,resource,min(` + clock + `),max(` + clock + `) FROM (` + rows + `) GROUP BY batch_id,namespace,service,resource`); err != nil {
				return 0, err
			}
			if err := exec(`INSERT INTO read_resource_seconds SELECT batch_id,` + sqlLiteral(s.Name) + `,namespace,service,resource,date_trunc('second',` + clock + `::TIMESTAMP_NS),min(` + clock + `),max(` + clock + `) FROM (` + rows + `) GROUP BY ALL`); err != nil {
				return 0, err
			}
		}
	}
	if metrics := source("metrics"); metrics != "" {
		if err := exec(`INSERT INTO read_metric_names SELECT DISTINCT batch_id,name FROM (` + metrics + `)`); err != nil {
			return 0, err
		}
	}
	if spans := source("spans"); spans != "" {
		if err := exec(`CREATE OR REPLACE TEMP TABLE cache_new_trace_parts AS SELECT batch_id,coalesce(namespace,'') AS namespace,coalesce(service,'') AS service,trace_id,
   min(start_unix_nano) AS min_start,max(start_unix_nano) AS max_start,max(end_unix_nano) AS max_end,max(CASE WHEN status = 'STATUS_CODE_ERROR' THEN 1 ELSE 0 END) AS has_error
   FROM (` + spans + `) WHERE trace_id<>'' GROUP BY 1,2,3,4`); err != nil {
			return 0, err
		}
		if err := exec(`INSERT INTO read_trace_parts SELECT * FROM cache_new_trace_parts`); err != nil {
			return 0, err
		}
		candidates := traceCandidateSelect + ` FROM cache_new_trace_parts GROUP BY trace_id`
		if err := exec(`MERGE INTO read_trace_candidates c USING (` + candidates + `) AS excluded ON c.trace_id=excluded.trace_id
   WHEN MATCHED THEN UPDATE SET
   namespace=CASE WHEN c.namespace=excluded.namespace THEN excluded.namespace END,
   service=CASE WHEN c.service=excluded.service THEN excluded.service END,
   min_start=least(c.min_start,excluded.min_start),
   max_start=greatest(c.max_start,excluded.max_start),
   max_end=greatest(c.max_end,excluded.max_end),
   has_error=greatest(c.has_error,excluded.has_error)
   WHEN NOT MATCHED THEN INSERT ` + traceCandidateColumns); err != nil {
			return 0, err
		}
		if err := exec(`DROP TABLE cache_new_trace_parts`); err != nil {
			return 0, err
		}
	}
	if len(batches) > 0 {
		var values []string
		for _, b := range batches {
			values = append(values, "("+sqlLiteral(b.ID)+")")
		}
		if err := exec(`INSERT INTO read_batches(batch_id) VALUES ` + strings.Join(values, ",")); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}
func timeLiteral(t time.Time) string {
	return "make_timestamp_ns(" + fmt.Sprint(t.UnixNano()) + ")::TIMESTAMPTZ_NS"
}
func windowPredicate(column string, w queryrows.Window) string {
	return column + ">=" + timeLiteral(w.Start) + " AND " + column + "<" + timeLiteral(w.End)
}
func (d *Duck) aggregateSources(ctx context.Context, db snapshotSQL, batches []telemetry.BatchMetadata, w queryrows.Window, inventory bool, sources map[string]string) error {
	markers, err := batchMarkers(ctx, db)
	if err != nil {
		return err
	}
	if inventory {
		window, ok := queryrows.ReadInventoryWindow(ctx)
		if !ok {
			return fmt.Errorf("resource_inventory requires a logical panel window")
		}
		// Markers and contributions come from the same MVCC snapshot. A hole
		// limits the ingest horizon even when newer batches are already cached.
		fresh := time.Now()
		for _, b := range batches {
			if markers[b.ID] || window.RetainedInventory {
				continue
			}
			for _, signal := range telemetry.Signals {
				bounds, count := batchTime(b, signal.Name)
				if count > 0 && (overlapping(b, signal.Name, window) && bounds.Known && bounds.MinNanos >= window.Start.UnixNano() && bounds.MaxNanos < window.End.UnixNano()) {
					fresh = minTime(fresh, time.Unix(0, b.MinIngestedNanos).Add(-time.Nanosecond))
					break
				}
			}
		}
		queryrows.RecordCoverage(ctx, fresh)
		sources["resource_inventory"] = d.resourceInventorySource(batches, markers, window)
	}
	if w.Kind == queryrows.RawRead {
		return nil
	}
	var cachedIDs []string
	var cached, uncached []telemetry.BatchMetadata
	for _, b := range batches {
		if !overlapping(b, "spans", w) {
			continue
		}
		if markers[b.ID] {
			cachedIDs = append(cachedIDs, b.ID)
			cached = append(cached, b)
		} else {
			uncached = append(uncached, b)
		}
	}
	// Use the incremental candidate index, repairing changed retired traces
	// and ambiguous scopes from active batch parts. Compaction keeps it usable.
	scope := "batch_id IN (" + idsSQL(cachedIDs) + ")"
	if w.Namespace != "" {
		scope += " AND namespace=" + sqlLiteral(w.Namespace)
	}
	if w.Service != "" {
		scope += " AND service=" + sqlLiteral(w.Service)
	}
	index, err := d.traceCandidateSource(ctx, db, batches, markers, w)
	if err != nil {
		return err
	}
	full := fmt.Sprintf("min_start>=%d AND max_start<%d", w.Start.UnixNano(), w.End.UnixNano())
	partial := "SELECT * FROM (" + index + ") WHERE NOT (" + full + ") AND " + fmt.Sprintf("max_start>=%d AND min_start<%d", w.Start.UnixNano(), w.End.UnixNano())
	projection := "namespace,service,trace_id,start_time,start_unix_nano,end_unix_nano,status"
	tail := "SELECT " + projection + " FROM (" + cleanSource("spans", d.snapshotSource("spans", uncached, batches)) + ") WHERE " + windowPredicate("start_time", w)
	// Only files whose scoped trace parts straddle a boundary require raw rows.
	var partialFiles []telemetry.BatchMetadata
	for _, b := range cached {
		bounds, _ := batchTime(b, "spans")
		if !bounds.Known || bounds.MinNanos < w.Start.UnixNano() || bounds.MaxNanos >= w.End.UnixNano() {
			partialFiles = append(partialFiles, b)
		}
	}
	interior := fmt.Sprintf("min_start>=%d AND max_start<%d", w.Start.UnixNano(), w.End.UnixNano())
	if len(partialFiles) > 0 {
		tail += " UNION ALL SELECT " + projection + " FROM (" + cleanSource("spans", d.snapshotSource("spans", partialFiles, batches)) + ") WHERE " + windowPredicate("start_time", w) + " AND trace_id IN (SELECT trace_id FROM (" + partial + "))"
		// A partial trace may also have in-window contributions in other batches.
		// Include these parts as candidates; boundary rows merge them in the kernel.
		sources["trace_candidates"] = `SELECT trace_id,min(min_start) AS min_start,max(max_end) AS max_end,max(has_error) AS has_error FROM read_trace_parts WHERE ` + scope + ` AND ` + interior + ` GROUP BY trace_id`
	} else {
		// Every overlapping cached file lies inside the window, so a partial
		// trace's other spans are in files outside it. Its in-window parts are
		// still a candidate; the kernel needs one row per trace, and partial
		// traces are disjoint from the fully contained index rows.
		sources["trace_candidates"] = "SELECT trace_id,min_start,max_end,has_error FROM (" + index + ") WHERE " + full +
			` UNION ALL SELECT trace_id,min(min_start),max(max_end),max(has_error) FROM read_trace_parts WHERE ` + scope + ` AND ` + interior +
			` AND trace_id IN (SELECT trace_id FROM (` + partial + `)) GROUP BY trace_id`
	}
	sources["trace_tail"] = tail
	return nil
}

func newestBatchEvent(b telemetry.BatchMetadata) int64 {
	newest := int64(-1 << 63)
	for _, s := range telemetry.Signals {
		bounds, count := batchTime(b, s.Name)
		if count > 0 {
			if !bounds.Known {
				return b.MaxIngestedNanos
			}
			newest = max(newest, bounds.MaxNanos)
		}
	}
	return newest
}

// PublishParquetReplacement reuses complete input caches when the SQL leases
// are idle. Busy leases defer the transfer to the refresh worker, without
// delaying the catalog swap or decoding the output again.
func (d *Duck) PublishParquetReplacement(ctx context.Context, output telemetry.BatchMetadata, inputs []string, publish func(context.Context) error) error {
	if len(inputs) == 0 {
		return d.PublishParquet(ctx, publish)
	}
	transfer := &batchCacheTransfer{output: output.ID, inputs: append([]string(nil), inputs...)}
	d.cachePublishMu.Lock()
	d.cacheTransfers = append(d.cacheTransfers, transfer)
	d.cachePublishMu.Unlock()
	swap := func() error {
		err := d.PublishParquet(ctx, publish)
		d.cachePublishMu.Lock()
		if err == nil {
			transfer.published = true
		} else {
			d.removeCacheTransfer(transfer)
		}
		d.cachePublishMu.Unlock()
		return err
	}
	unlock := d.cacheGate.TryLock(writegate.ReadCache)
	if unlock == nil {
		return swap()
	}
	d.cachePublishMu.Lock()
	if d.cachePublishing == nil {
		d.cachePublishing = map[string]bool{}
	}
	d.cachePublishing[output.ID] = true
	d.cachePublishMu.Unlock()
	defer func() { d.cachePublishMu.Lock(); delete(d.cachePublishing, output.ID); d.cachePublishMu.Unlock() }()
	if err := d.transferBatchCache(ctx, output.ID, inputs); err != nil {
		d.cachePublishMu.Lock()
		d.removeCacheTransfer(transfer)
		d.cachePublishMu.Unlock()
		slog.Warn("compaction cache transfer failed; output will be cached from Parquet", "err", err)
	}
	unlock()
	if err := d.transferVersionMarker(ctx, output, inputs); err != nil {
		slog.Warn("compaction version marker transfer failed; output will be read", "err", err)
	}
	return swap()
}

func (d *Duck) transferBatchCache(ctx context.Context, output string, inputs []string) error {
	background := d.tryLockBackground()
	if background == nil {
		return nil
	}
	defer background()
	return d.transferBatchCacheLocked(ctx, output, inputs)
}

// Caller holds cacheGate and the background SQL lease.
func (d *Duck) transferBatchCacheLocked(ctx context.Context, output string, inputs []string) error {
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT count(*)>0 FROM read_batches WHERE batch_id=?`, output).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM read_batches WHERE batch_id IN (`+idsSQL(inputs)+`)`).Scan(&count); err != nil {
		return err
	}
	if count != len(inputs) {
		return nil
	}
	where := " WHERE batch_id IN (" + idsSQL(inputs) + ")"
	for _, stmt := range []string{
		`INSERT INTO read_resource_inventory SELECT ` + sqlLiteral(output) + `,signal,namespace,service,resource,min(first_seen),max(last_seen) FROM read_resource_inventory` + where + ` GROUP BY signal,namespace,service,resource`,
		`INSERT INTO read_resource_seconds SELECT ` + sqlLiteral(output) + `,signal,namespace,service,resource,bucket,min(first_seen),max(last_seen) FROM read_resource_seconds` + where + ` GROUP BY signal,namespace,service,resource,bucket`,
		`INSERT INTO read_metric_names SELECT DISTINCT ` + sqlLiteral(output) + `,name FROM read_metric_names` + where,
		`INSERT INTO read_trace_parts SELECT ` + sqlLiteral(output) + `,namespace,service,trace_id,min(min_start),max(max_start),max(max_end),max(has_error) FROM read_trace_parts` + where + ` GROUP BY 2,3,4`,
		`INSERT INTO read_batches(batch_id) VALUES (` + sqlLiteral(output) + `)`,
		`UPDATE read_batches SET replacement_id=` + sqlLiteral(output) + where,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MERGE joins against the candidates instead of probing a key: an index on
// every retained trace is loaded whole to upsert or checkpoint, and at a few
// million traces that alone outgrew a 1 GB memory limit.
const traceCandidateColumns = `VALUES (excluded.trace_id,excluded.namespace,excluded.service,excluded.min_start,excluded.max_start,excluded.max_end,excluded.has_error)`

// One candidate row per trace is sufficient while the requested namespace and
// service are unambiguous. Mixed scopes are NULL and use exact batch parts.
const traceCandidateSelect = `SELECT trace_id,
 CASE WHEN min(namespace)=max(namespace) THEN min(namespace) END AS namespace,
 CASE WHEN min(service)=max(service) THEN min(service) END AS service,
 min(min_start) AS min_start,max(max_start) AS max_start,max(max_end) AS max_end,max(has_error) AS has_error`

// Only retention or an uncached replacement changes the active cached trace
// bounds. Follow complete compaction replacements, including multiple levels.
func changedRetiredBatches(ctx context.Context, db snapshotSQL, markers, active map[string]bool, stale []string) ([]string, error) {
	if len(stale) == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT batch_id,replacement_id FROM read_batches WHERE replacement_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	replacements := map[string]string{}
	for rows.Next() {
		var id, to string
		if err := rows.Scan(&id, &to); err != nil {
			return nil, err
		}
		replacements[id] = to
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var changed []string
	for _, id := range stale {
		covered := false
		visited := map[string]bool{}
		for to := replacements[id]; to != "" && !visited[to]; to = replacements[to] {
			visited[to] = true
			if active[to] && markers[to] {
				covered = true
				break
			}
		}
		if !covered {
			changed = append(changed, id)
		}
	}
	return changed, nil
}

func (d *Duck) traceCandidateSource(ctx context.Context, db snapshotSQL, batches []telemetry.BatchMetadata, markers map[string]bool, w queryrows.Window) (string, error) {
	active := map[string]bool{}
	var cachedIDs, stale []string
	for _, b := range batches {
		active[b.ID] = true
		if markers[b.ID] {
			cachedIDs = append(cachedIDs, b.ID)
		}
	}
	for id := range markers {
		if !active[id] {
			d.cachePublishMu.Lock()
			publishing := d.cachePublishing[id]
			d.cachePublishMu.Unlock()
			if !publishing {
				stale = append(stale, id)
			}
		}
	}
	changed, err := changedRetiredBatches(ctx, db, markers, active, stale)
	if err != nil {
		return "", err
	}
	index := `SELECT * FROM read_trace_candidates`
	if len(changed) > 0 {
		dirty := `SELECT trace_id FROM read_trace_parts WHERE batch_id IN (` + idsSQL(changed) + `)`
		index += ` WHERE trace_id NOT IN (` + dirty + `) UNION ALL ` + traceCandidateSelect + ` FROM read_trace_parts WHERE batch_id IN (` + idsSQL(cachedIDs) + `) AND trace_id IN (` + dirty + `) GROUP BY trace_id`
	}
	var exact, possible, ambiguous []string
	for _, field := range []struct{ name, value string }{{"namespace", w.Namespace}, {"service", w.Service}} {
		if field.value == "" {
			continue
		}
		match := field.name + "=" + sqlLiteral(field.value)
		exact = append(exact, match)
		possible = append(possible, "("+match+" OR "+field.name+" IS NULL)")
		ambiguous = append(ambiguous, field.name+" IS NULL")
	}
	projection := `trace_id,min_start,max_start,max_end,has_error`
	if len(exact) == 0 {
		return "SELECT " + projection + " FROM (" + index + ")", nil
	}
	result := "SELECT " + projection + " FROM (" + index + ") WHERE " + strings.Join(exact, " AND ")
	mixed := "SELECT trace_id FROM (" + index + ") WHERE " + strings.Join(possible, " AND ") + " AND (" + strings.Join(ambiguous, " OR ") + ")"
	// Most traces belong to a single namespace/service. Do not put a retained
	// batch-parts aggregation in the ranking/join plan when this snapshot has no
	// ambiguous candidates for the requested scope. The probe shares its read
	// transaction and the same retired-contribution repair.
	var hasMixed bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM ("+mixed+") LIMIT 1)").Scan(&hasMixed); err != nil {
		return "", err
	}
	if !hasMixed {
		return result, nil
	}
	scope := "batch_id IN (" + idsSQL(cachedIDs) + ") AND " + strings.Join(exact, " AND ")
	return result + ` UNION ALL SELECT trace_id,min(min_start),max(max_start),max(max_end),max(has_error) FROM read_trace_parts WHERE ` + scope + ` AND trace_id IN (` + mixed + `) GROUP BY trace_id`, nil
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
