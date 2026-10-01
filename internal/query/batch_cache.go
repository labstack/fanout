package query

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

func createBatchCaches(db *sql.DB) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS read_batches (batch_id VARCHAR PRIMARY KEY)`,
		createReadEndpointTable,
		`CREATE TABLE IF NOT EXISTS read_span_events (batch_id VARCHAR,namespace VARCHAR,service VARCHAR,trace_id VARCHAR,start_time TIMESTAMP_NS,start_unix_nano BIGINT,end_unix_nano BIGINT,duration_ms DOUBLE,status VARCHAR,http_method VARCHAR,http_route VARCHAR,operation VARCHAR)`,
		`CREATE TABLE IF NOT EXISTS read_log_times (batch_id VARCHAR,namespace VARCHAR,service VARCHAR,time TIMESTAMP_NS,severity VARCHAR,count BIGINT)`,
		`CREATE TABLE IF NOT EXISTS read_logs (batch_id VARCHAR, bucket TIMESTAMP_NS, namespace VARCHAR, service VARCHAR, severity VARCHAR, count BIGINT)`,
		`CREATE TABLE IF NOT EXISTS read_trace_parts (batch_id VARCHAR, namespace VARCHAR, service VARCHAR, trace_id VARCHAR, min_start BIGINT, max_start BIGINT, max_end BIGINT, has_error INTEGER)`,
		`CREATE TABLE IF NOT EXISTS read_traces (scope INTEGER, namespace VARCHAR, service VARCHAR, trace_id VARCHAR, min_start BIGINT, max_start BIGINT, max_end BIGINT, has_error INTEGER)`,
		`CREATE TABLE IF NOT EXISTS read_trace_dirty (trace_id VARCHAR)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
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
// watermark. A late commit is a new batch and is never skipped. Each batch's
// contributions, trace index and marker commit together. Work yields between
// batches so a historical backfill does not monopolize the write connection.
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
	unlock, err := d.writeGate.LockContext(ctx, writegate.WriteReadCache)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if err := d.lockRollupParquetRead(ctx); err != nil {
		return 0, err
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
			stale = append(stale, id)
		}
	}
	sort.SliceStable(batches, func(i, j int) bool { return newestBatchEvent(batches[i]) < newestBatchEvent(batches[j]) })
	pendingCount := 0
	for _, b := range batches {
		if !markers[b.ID] {
			pendingCount++
		}
	}
	metrics.ReadCachePendingBatches.Set(float64(pendingCount))
	pending := []telemetry.BatchMetadata{}
	for i := len(batches) - 1; i >= 0; i-- {
		if !markers[batches[i].ID] {
			pending = append(pending, batches[i])
			if len(pending) == 16 {
				break
			}
		}
	}
	if len(pending) == 0 && len(stale) == 0 {
		return 0, nil
	}
	didWork = true
	return d.cacheBatches(ctx, pending, stale)
}

func (d *Duck) cacheBatches(ctx context.Context, batches []telemetry.BatchMetadata, stale []string) (int64, error) {
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var written int64
	exec := func(stmt string, args ...any) error {
		result, err := tx.ExecContext(ctx, stmt, args...)
		if err == nil && strings.HasPrefix(stmt, "INSERT INTO read_") && !strings.HasPrefix(stmt, "INSERT INTO read_trace_dirty") && !strings.HasPrefix(stmt, "INSERT INTO read_batches") {
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			written += n
		}
		return err
	}
	if err := exec(`DELETE FROM read_trace_dirty`); err != nil {
		return 0, err
	}
	if len(stale) > 0 {
		if err := exec(`INSERT INTO read_trace_dirty SELECT DISTINCT trace_id FROM read_trace_parts WHERE batch_id IN (` + idsSQL(stale) + `)`); err != nil {
			return 0, err
		}
		for _, table := range []string{"read_endpoints", "read_logs", "read_span_events", "read_log_times", "read_trace_parts", "read_batches"} {
			if err := exec("DELETE FROM " + table + " WHERE batch_id IN (" + idsSQL(stale) + ")"); err != nil {
				return 0, err
			}
		}
	}
	started := time.Now()
	for _, b := range batches {
		spans := cleanSource("spans", d.physicalSource("spans", []telemetry.BatchMetadata{b}))
		logs := cleanSource("logs", d.physicalSource("logs", []telemetry.BatchMetadata{b}))
		if b.Spans > 0 {
			if err := exec(`INSERT INTO read_span_events SELECT ?::VARCHAR, namespace,coalesce(service,''),trace_id,start_time::TIMESTAMP_NS,start_unix_nano,end_unix_nano,duration_ms,status,http_method,http_route,operation FROM (`+spans+`) ORDER BY start_time`, b.ID); err != nil {
				return 0, err
			}
			spans = `SELECT * EXCLUDE(batch_id) FROM read_span_events WHERE batch_id=` + sqlLiteral(b.ID)
			kernel := endpointBatchSelect + "FROM (" + spans + ") s GROUP BY 1,2,3,4,5"
			if err := exec(`INSERT INTO read_endpoints SELECT ?::VARCHAR, * FROM (`+kernel+`)`, b.ID); err != nil {
				return 0, fmt.Errorf("cache endpoints: %w", err)
			}
			if err := exec(`INSERT INTO read_trace_parts SELECT ?::VARCHAR, coalesce(namespace,''), coalesce(service,''), trace_id,
    min(start_unix_nano), max(start_unix_nano), max(end_unix_nano), max(CASE WHEN upper(status) IN ('ERROR','STATUS_CODE_ERROR') THEN 1 ELSE 0 END)
    FROM (`+spans+`) WHERE trace_id<>'' GROUP BY 2,3,4`, b.ID); err != nil {
				return 0, err
			}
			if err := exec(`INSERT INTO read_trace_dirty SELECT DISTINCT trace_id FROM read_trace_parts WHERE batch_id=?`, b.ID); err != nil {
				return 0, err
			}
		}
		if b.Logs > 0 {
			if err := exec(`INSERT INTO read_log_times SELECT ?::VARCHAR,coalesce(namespace,''),coalesce(service,''),time::TIMESTAMP_NS,coalesce(lower(severity),''),count(*) FROM (`+logs+`) GROUP BY 2,3,4,5 ORDER BY 4`, b.ID); err != nil {
				return 0, err
			}
			if err := exec(`INSERT INTO read_logs SELECT batch_id,date_trunc('minute',time),namespace,service,severity,sum(count)::BIGINT FROM read_log_times WHERE batch_id=? GROUP BY 1,2,3,4,5`, b.ID); err != nil {
				return 0, err
			}
		}

		if err := exec(`INSERT INTO read_batches VALUES (?)`, b.ID); err != nil {
			return 0, err
		}
		if time.Since(started) >= 2*time.Second {
			break
		}
	}
	if err := exec(`DELETE FROM read_traces WHERE trace_id IN (SELECT trace_id FROM read_trace_dirty)`); err != nil {
		return 0, err
	}
	if err := exec(`INSERT INTO read_traces
 SELECT grouping_id(namespace,service)::INTEGER, coalesce(namespace,''), coalesce(service,''), trace_id,
 min(min_start), max(max_start), max(max_end), max(has_error)
 FROM read_trace_parts WHERE trace_id IN (SELECT trace_id FROM read_trace_dirty)
 GROUP BY GROUPING SETS ((trace_id,namespace,service),(trace_id,namespace),(trace_id,service),(trace_id))`); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM read_trace_dirty`); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}
func minuteInterior(w queryrows.Window) (time.Time, time.Time) {
	start := w.Start.Truncate(time.Minute)
	if !start.Equal(w.Start) {
		start = start.Add(time.Minute)
	}
	return start, w.End.Truncate(time.Minute)
}
func timeNanosLiteral(t time.Time) string {
	return "make_timestamp_ns(" + fmt.Sprint(t.UnixNano()) + ")"
}
func timeLiteral(t time.Time) string {
	return "make_timestamp_ns(" + fmt.Sprint(t.UnixNano()) + ")::TIMESTAMPTZ_NS"
}
func nativeWindowPredicate(column string, w queryrows.Window) string {
	return column + ">=" + timeNanosLiteral(w.Start) + " AND " + column + "<" + timeNanosLiteral(w.End)
}
func windowPredicate(column string, w queryrows.Window) string {
	return column + ">=" + timeLiteral(w.Start) + " AND " + column + "<" + timeLiteral(w.End)
}
func traceScope(w queryrows.Window) string {
	mode := 0
	if w.Namespace == "" {
		mode |= 2
	}
	if w.Service == "" {
		mode |= 1
	}
	return fmt.Sprintf("scope=%d AND namespace=%s AND service=%s", mode, sqlLiteral(w.Namespace), sqlLiteral(w.Service))
}
func (d *Duck) aggregateSources(ctx context.Context, db snapshotSQL, batches []telemetry.BatchMetadata, w queryrows.Window, sources map[string]string) error {
	markers, err := batchMarkers(ctx, db)
	if err != nil {
		return err
	}
	cachedIDs := []string{}
	active := map[string]bool{}
	cached := []telemetry.BatchMetadata{}
	uncached := []telemetry.BatchMetadata{}
	signal := "spans"
	if w.Kind == queryrows.LogHistogramRead {
		signal = "logs"
	}
	for _, b := range batches {
		active[b.ID] = true
		if !overlapping(b, signal, w) {
			continue
		}
		if markers[b.ID] {
			cachedIDs = append(cachedIDs, b.ID)
			cached = append(cached, b)
		} else {
			uncached = append(uncached, b)
		}
	}
	switch w.Kind {
	case queryrows.EndpointRead, queryrows.LogHistogramRead:
		start, end := minuteInterior(w)
		column := "start_time"
		if signal == "logs" {
			column = "time"
		}
		boundary := nativeWindowPredicate(column, w) + " AND (" + column + "<" + timeNanosLiteral(start) + " OR " + column + ">=" + timeNanosLiteral(end) + ")"
		interior := column + ">=" + timeLiteral(start) + " AND " + column + "<" + timeLiteral(end)
		if w.Kind == queryrows.EndpointRead {
			projection := "namespace,service,start_time,http_method,http_route,operation,duration_ms,status"
			raw := "SELECT " + projection + " FROM (" + cleanSource("spans", d.physicalSource("spans", uncached)) + ") WHERE " + windowPredicate(column, w)
			cachedSource := `SELECT namespace,service,start_time::TIMESTAMPTZ_NS AS start_time,http_method,http_route,operation,duration_ms,status FROM read_span_events WHERE batch_id IN (` + idsSQL(cachedIDs) + `) AND ` + boundary
			sources["endpoint_tail"] = raw + " UNION ALL SELECT * FROM (" + cachedSource + ") WHERE " + windowPredicate(column, w) + " AND NOT (" + interior + ")"
			sources["endpoint_minutes"] = "SELECT * EXCLUDE(batch_id) FROM read_endpoints WHERE batch_id IN (" + idsSQL(cachedIDs) + ") AND bucket>=" + timeNanosLiteral(start) + " AND bucket<" + timeNanosLiteral(end)
		} else {
			raw := "SELECT namespace,service,time,coalesce(lower(severity),'') AS severity,1::BIGINT AS count FROM (" + cleanSource("logs", d.physicalSource("logs", uncached)) + ") WHERE " + windowPredicate(column, w)
			cachedSource := `SELECT namespace,service,time::TIMESTAMPTZ_NS AS time,severity,count FROM read_log_times WHERE batch_id IN (` + idsSQL(cachedIDs) + `) AND ` + boundary
			sources["log_tail"] = raw + " UNION ALL SELECT * FROM (" + cachedSource + ") WHERE " + windowPredicate(column, w) + " AND NOT (" + interior + ")"
			sources["log_minutes"] = "SELECT * EXCLUDE(batch_id) FROM read_logs WHERE batch_id IN (" + idsSQL(cachedIDs) + ") AND bucket>=" + timeNanosLiteral(start) + " AND bucket<" + timeNanosLiteral(end)
		}

	case queryrows.TraceCandidateRead:
		valid := true
		for id := range markers {
			if !active[id] {
				valid = false
				break
			}
		}
		index := `SELECT * FROM read_traces WHERE ` + traceScope(w)
		if !valid {
			index += " AND false"
			uncached = append(uncached, cached...)
			cached = nil
		}
		full := fmt.Sprintf("min_start>=%d AND max_start<%d", w.Start.UnixNano(), w.End.UnixNano())
		partial := index + fmt.Sprintf(" AND max_start>=%d AND min_start<%d AND NOT (%s)", w.Start.UnixNano(), w.End.UnixNano(), full)
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM ("+partial+")").Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			cached = nil
		}
		projection := "namespace,service,trace_id,start_time,start_unix_nano,end_unix_nano,status"
		tail := "SELECT " + projection + " FROM (" + cleanSource("spans", d.physicalSource("spans", uncached)) + ") WHERE " + windowPredicate("start_time", w)
		if len(cached) > 0 {
			cachedSource := `SELECT namespace,service,trace_id,start_time::TIMESTAMPTZ_NS AS start_time,start_unix_nano,end_unix_nano,status FROM read_span_events WHERE batch_id IN (` + idsSQL(cachedIDs) + `) AND ` + nativeWindowPredicate("start_time", w)
			tail += " UNION ALL SELECT * FROM (" + cachedSource + ") WHERE " + windowPredicate("start_time", w) + " AND trace_id IN (SELECT trace_id FROM (" + partial + "))"
		}

		sources["trace_tail"] = tail
		sources["trace_candidates"] = index + " AND " + full
	}
	return nil
}

// Endpoint latency is stored as a mergeable fixed-boundary histogram. Unlike
// averaging minute p95 values, summing these bin counts preserves the latency
// distribution across arbitrary query windows. Bounds include Fanout's health
// thresholds (750ms and 2s) and cap the overflow bucket at five minutes.
const endpointBatchSelect = `SELECT
  s.namespace,
  date_trunc('minute', s.start_time::TIMESTAMP_NS) AS bucket,
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
`

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
