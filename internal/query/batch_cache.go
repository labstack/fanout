package query

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

// readCacheVersion covers both schema and aggregation semantics. These tables
// are disposable; a mismatch rebuilds them together from immutable Parquet.
const readCacheVersion = 4

const (
	readCacheBatchLimit = 64
	readCacheRowBudget  = 64000
)

func createBatchCaches(db *sql.DB) error {
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
		rows, err := tx.Query(`SELECT table_name FROM duckdb_tables() WHERE schema_name='main' AND starts_with(table_name,'read_') AND table_name<>'read_cache_version'`)
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
		`CREATE TABLE IF NOT EXISTS read_batches (batch_id VARCHAR PRIMARY KEY,replacement_id VARCHAR)`,
		`CREATE TABLE IF NOT EXISTS read_trace_candidates (trace_id VARCHAR PRIMARY KEY,namespace VARCHAR,service VARCHAR,min_start BIGINT,max_start BIGINT,max_end BIGINT,has_error INTEGER)`,
		`CREATE TABLE IF NOT EXISTS read_trace_parts (batch_id VARCHAR, namespace VARCHAR, service VARCHAR, trace_id VARCHAR, min_start BIGINT, max_start BIGINT, max_end BIGINT, has_error INTEGER)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
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
func (d *Duck) RefreshReadCaches(ctx context.Context) (rows int64, err error) {
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

	if d.repository == nil {
		return 0, nil
	}
	unlock, err := d.cacheGate.LockContext(ctx, writegate.WriteReadCache)
	if err != nil {
		return 0, err
	}
	defer unlock()
	// Cache retries are frequent. A queued publication must not park the cache
	// gate for the analytical rollup's much longer admission lease.
	admitted := d.parquetMu.TryRLock()
	metrics.RecordParquetRead(readerRollup, 0, admitted)
	if !admitted {
		return 0, nil
	}
	defer d.parquetMu.RUnlock()
	batches := d.repository.Parquet.BatchMetadata()
	markers, err := batchMarkers(ctx, d.writer())
	if err != nil {
		return 0, err
	}
	active := map[string]bool{}
	for _, b := range batches {
		active[b.ID] = true
	}
	stale := []string{}
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
	sort.SliceStable(batches, func(i, j int) bool { return newestBatchEvent(batches[i]) < newestBatchEvent(batches[j]) })
	pendingCount := 0
	for _, b := range batches {
		if !markers[b.ID] && b.Spans > 0 {
			pendingCount++
		}
	}
	metrics.ReadCachePendingBatches.Set(float64(pendingCount))
	pending := []telemetry.BatchMetadata{}
	pendingRows := 0
	for i := len(batches) - 1; i >= 0; i-- {
		if !markers[batches[i].ID] {
			rows := batches[i].Spans
			// A complete file is indivisible. Admit one oversized file to make
			// progress, otherwise bound new work by rows as well as file count.
			if len(pending) > 0 && pendingRows+rows > readCacheRowBudget {
				break
			}
			pending = append(pending, batches[i])
			pendingRows += rows
			if len(pending) == readCacheBatchLimit {
				break
			}
		}
	}
	if len(pending) == 0 && len(stale) == 0 {
		return 0, nil
	}
	didWork = true
	changed, err := changedRetiredBatches(ctx, d.writer(), markers, active, stale)
	if err != nil {
		return 0, err
	}
	return d.cacheBatches(ctx, pending, stale, changed)
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
		if err == nil && strings.HasPrefix(stmt, "INSERT INTO read_") && !strings.HasPrefix(stmt, "INSERT INTO read_batches") {
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
		for _, table := range []string{"read_trace_parts", "read_batches"} {
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
		if err := exec(`INSERT INTO read_trace_candidates ` + traceCandidateSelect + ` FROM read_trace_parts WHERE trace_id IN (SELECT trace_id FROM cache_retired_traces) GROUP BY trace_id ON CONFLICT (trace_id) DO UPDATE SET namespace=excluded.namespace,service=excluded.service,min_start=excluded.min_start,max_start=excluded.max_start,max_end=excluded.max_end,has_error=excluded.has_error`); err != nil {
			return 0, err
		}
		if err := exec(`DROP TABLE cache_retired_traces`); err != nil {
			return 0, err
		}
	}
	// Bind spans once for the whole pass. Materialize new
	// batch parts and one indexed candidate per trace. The temporary parts table
	// feeds both inserts without rereading Parquet or scanning retained parts.
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
	if spans := source("spans"); spans != "" {
		if err := exec(`CREATE OR REPLACE TEMP TABLE cache_new_trace_parts AS SELECT batch_id,coalesce(namespace,'') AS namespace,coalesce(service,'') AS service,trace_id,
   min(start_unix_nano) AS min_start,max(start_unix_nano) AS max_start,max(end_unix_nano) AS max_end,max(CASE WHEN upper(status) IN ('ERROR','STATUS_CODE_ERROR') THEN 1 ELSE 0 END) AS has_error
   FROM (` + spans + `) WHERE trace_id<>'' GROUP BY 1,2,3,4`); err != nil {
			return 0, err
		}
		if err := exec(`INSERT INTO read_trace_parts SELECT * FROM cache_new_trace_parts`); err != nil {
			return 0, err
		}
		candidates := traceCandidateSelect + ` FROM cache_new_trace_parts GROUP BY trace_id`
		if err := exec(`INSERT INTO read_trace_candidates ` + candidates + ` ON CONFLICT (trace_id) DO UPDATE SET
   namespace=CASE WHEN read_trace_candidates.namespace=excluded.namespace THEN excluded.namespace END,
   service=CASE WHEN read_trace_candidates.service=excluded.service THEN excluded.service END,
   min_start=least(read_trace_candidates.min_start,excluded.min_start),
   max_start=greatest(read_trace_candidates.max_start,excluded.max_start),
   max_end=greatest(read_trace_candidates.max_end,excluded.max_end),
   has_error=greatest(read_trace_candidates.has_error,excluded.has_error)`); err != nil {
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
func (d *Duck) aggregateSources(ctx context.Context, db snapshotSQL, batches []telemetry.BatchMetadata, w queryrows.Window, sources map[string]string) error {
	markers, err := batchMarkers(ctx, db)
	if err != nil {
		return err
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
	for _, signal := range []string{"spans", "logs", "metrics"} {
		bounds, count := batchTime(b, signal)
		if count > 0 {
			if !bounds.Known {
				return b.MaxIngestedNanos
			}
			newest = max(newest, bounds.MaxNanos)
		}
	}
	return newest
}

// PublishParquetReplacement derives the output cache from complete inputs before
// the namespace swap. The pending publication set protects still-inactive
// output contributions without holding the write gate during reader drain.
// Readers use active IDs, so either namespace sees exactly one set of contributions. After
// a crash, an absent output marker simply causes a rebuild from the output file.
func (d *Duck) PublishParquetReplacement(ctx context.Context, output telemetry.BatchMetadata, inputs []string, publish func(context.Context) error) error {
	if len(inputs) == 0 {
		return d.PublishParquet(ctx, publish)
	}
	unlock, err := d.cacheGate.LockContext(ctx, writegate.WriteReadCache)
	if err != nil {
		return err
	}
	d.cachePublishMu.Lock()
	if d.cachePublishing == nil {
		d.cachePublishing = map[string]bool{}
	}
	d.cachePublishing[output.ID] = true
	d.cachePublishMu.Unlock()
	defer func() { d.cachePublishMu.Lock(); delete(d.cachePublishing, output.ID); d.cachePublishMu.Unlock() }()
	if err := d.transferBatchCache(ctx, output.ID, inputs); err != nil {
		slog.Warn("compaction cache transfer failed; output will be cached from Parquet", "err", err)
	}
	unlock()
	if err := d.transferVersionMarker(ctx, output, inputs); err != nil {
		slog.Warn("compaction version marker transfer failed; output will be read", "err", err)
	}
	return d.PublishParquet(ctx, publish)
}

func (d *Duck) transferBatchCache(ctx context.Context, output string, inputs []string) error {
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
