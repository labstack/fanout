package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/telemetry"
)

const createVersionRollupTable = `CREATE TABLE version_rollup (
 namespace VARCHAR, service VARCHAR, service_version VARCHAR,
 first_seen TIMESTAMPTZ_NS, last_seen TIMESTAMPTZ_NS,
 PRIMARY KEY(namespace,service,service_version))`
const createVersionBatchesTable = `CREATE TABLE version_rollup_batches (batch_id VARCHAR PRIMARY KEY,max_ingested BIGINT)`
const createAnomalyLogTable = `CREATE TABLE anomaly_log (
 namespace VARCHAR,service VARCHAR,kind VARCHAR,start_time TIMESTAMPTZ_NS,end_time TIMESTAMPTZ_NS,
 title VARCHAR,severity VARCHAR,PRIMARY KEY(namespace,service,kind,start_time))`

func createAnnotationTables(db *sql.DB) error {
	if err := ensureCacheTable(db, "version_rollup", createVersionRollupTable,
		"namespace", "service", "service_version", "first_seen", "last_seen"); err != nil {
		return err
	}
	if err := ensureCacheTable(db, "version_rollup_batches", createVersionBatchesTable,
		"batch_id", "max_ingested"); err != nil {
		return err
	}
	return ensureCacheTable(db, "anomaly_log", createAnomalyLogTable,
		"namespace", "service", "kind", "start_time", "end_time", "title", "severity")
}

func (d *Duck) RefreshVersionRollup(ctx context.Context) (processed int64, retErr error) {
	if d.repository == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	var materialized int64
	defer func() {
		result := metrics.RollupSuccess
		if retErr != nil {
			result = metrics.RollupError
			materialized = 0
		} else if processed == 0 {
			result = metrics.RollupNoop
		}
		metrics.RecordRollupComponent(metrics.RollupVersion, result, materialized, time.Since(start).Seconds())
		if retErr != nil && (errors.Is(retErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			slog.Warn("version rollup pass timed out", "err", retErr)
		}
	}()
	unlock, err := d.writeGate.LockContext(ctx, writegate.WriteRollupVersion)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if err := d.lockRollupParquetRead(ctx); err != nil {
		return 0, err
	}
	defer d.parquetMu.RUnlock()
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT batch_id FROM version_rollup_batches`)
	if err != nil {
		return 0, err
	}
	marked := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		marked[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	batches := d.repository.Parquet.BatchMetadata()
	active := map[string]bool{}
	pending := []telemetry.BatchMetadata{}
	count := 0
	for _, b := range batches {
		active[b.ID] = true
		size := b.Spans + b.Logs + b.Metrics
		if !marked[b.ID] && size > 64000 {
			slog.Warn("version rollup batch exceeds row budget", "batch_id", b.ID, "rows", size)
			continue
		}
		if marked[b.ID] || len(pending) >= 64 || count+size > 64000 {
			continue
		}
		pending = append(pending, b)
		count += size
	}
	for id := range marked {
		d.cachePublishMu.Lock()
		publishing := d.cachePublishing[id]
		d.cachePublishMu.Unlock()
		if !active[id] && !publishing {
			if _, err := tx.ExecContext(ctx, `DELETE FROM version_rollup_batches WHERE batch_id=?`, id); err != nil {
				return 0, err
			}
		}
	}
	parts := []string{}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		selected := []telemetry.BatchMetadata{}
		for _, b := range pending {
			if _, n := batchTime(b, signal); n > 0 {
				selected = append(selected, b)
			}
		}
		if len(selected) == 0 {
			continue
		}
		version, column := `TRY_CAST(resource['service.version'] AS VARCHAR)`, "time"
		if signal == "spans" {
			version = "service_version"
			column = "start_time"
		}
		parts = append(parts, fmt.Sprintf(`SELECT coalesce(namespace,'') AS namespace,coalesce(service,'') AS service,%s AS service_version,%s AS t FROM (%s)`, version, column, cleanSource(signal, d.physicalSource(signal, selected))))
	}
	if len(parts) > 0 {
		statement := `INSERT INTO version_rollup SELECT namespace,service,service_version,min(t),max(t) FROM (` + strings.Join(parts, " UNION ALL ") + `) WHERE service<>'' AND service_version IS NOT NULL AND service_version<>'' GROUP BY 1,2,3
ON CONFLICT(namespace,service,service_version) DO UPDATE SET first_seen=least(version_rollup.first_seen,excluded.first_seen),last_seen=greatest(version_rollup.last_seen,excluded.last_seen)`
		res, err := tx.ExecContext(ctx, statement)
		if err != nil {
			return 0, err
		}
		if rows, err := res.RowsAffected(); err == nil {
			materialized = rows
		} else {
			slog.Warn("version rollup rows affected unavailable", "err", err)
		}
	}
	for _, b := range pending {
		if _, err := tx.ExecContext(ctx, `INSERT INTO version_rollup_batches VALUES (?,?)`, b.ID, b.MaxIngestedNanos); err != nil {
			return 0, err
		}
	}
	// Retire inactive services once every 64 committed passes, including startup.
	if d.versionRollupPasses%64 == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM version_rollup WHERE (namespace,service) IN (SELECT namespace,service FROM version_rollup GROUP BY 1,2 HAVING max(last_seen)<?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, time.Now().UTC().Add(-30*24*time.Hour)); err != nil {
			return 0, err
		}
	}
	// Keep initial observations before recent versions; bound pathological cardinality.
	var historyCount int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM version_rollup`).Scan(&historyCount); err != nil {
		return 0, err
	}
	if historyCount > 100000 {
		res, err := tx.ExecContext(ctx, `DELETE FROM version_rollup WHERE (namespace,service,service_version) NOT IN (
 SELECT namespace,service,service_version FROM (
 SELECT *,row_number() OVER(PARTITION BY namespace,service ORDER BY first_seen,service_version) AS initial_rank FROM version_rollup)
 ORDER BY (initial_rank=1) DESC,last_seen DESC,namespace,service,service_version LIMIT 100000)`)
		if err != nil {
			return 0, err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if removed > 0 {
			if err := storeRollupWatermark(ctx, tx, "version_rollup_v1_history_limited", 1); err != nil {
				return 0, err
			}
		}
	}
	remaining := 0
	for _, b := range batches {
		if !marked[b.ID] {
			remaining++
		}
	}
	limited := int64(0)
	if remaining > len(pending) {
		limited = 1
	}
	if err := storeRollupWatermark(ctx, tx, "version_rollup_v1_limited", limited); err != nil {
		return 0, err
	}
	var tip int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(max_ingested),0) FROM version_rollup_batches`).Scan(&tip); err != nil {
		return 0, err
	}
	var previous int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(last_ingested_unix_nano),0) FROM rollup_state WHERE cache_key='version_rollup_v1'`).Scan(&previous); err != nil {
		return 0, err
	}
	if err := storeRollupWatermark(ctx, tx, "version_rollup_v1", max(tip, previous)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	d.versionRollupPasses++
	return int64(len(pending)), nil
}

// boundedAnomalies keeps the latest 10,000 inputs, drops invalid ranges, and
// leaves the caller's slice unchanged.
func boundedAnomalies(findings []annotations.Anomaly) []annotations.Anomaly {
	findings = append([]annotations.Anomaly(nil), findings...)
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].To.Before(findings[j].To) })
	if len(findings) > 10000 {
		findings = findings[len(findings)-10000:]
	}
	valid := findings[:0]
	for _, a := range findings {
		if a.From.Before(a.To) {
			valid = append(valid, a)
		}
	}
	return valid
}

// JSON is only the bulk transport into the private write table; stored times
// remain native nanosecond UTC instants.
const anomalyRowsSQL = `SELECT value->>'namespace' AS namespace,value->>'service' AS service,value->>'kind' AS kind,
 make_timestamp_ns((value->>'start_nanos')::BIGINT)::TIMESTAMPTZ_NS AS start_time,
 make_timestamp_ns((value->>'end_nanos')::BIGINT)::TIMESTAMPTZ_NS AS end_time,
 value->>'title' AS title,value->>'severity' AS severity FROM json_each(?)`

func encodeAnomalies(findings []annotations.Anomaly) ([]byte, error) {
	type row struct {
		Namespace string `json:"namespace"`
		Service   string `json:"service"`
		Kind      string `json:"kind"`
		Start     int64  `json:"start_nanos"`
		End       int64  `json:"end_nanos"`
		Title     string `json:"title"`
		Severity  string `json:"severity"`
	}
	rows := make([]row, 0, len(findings))
	for _, a := range findings {
		rows = append(rows, row{a.Namespace, a.Service, a.Kind, a.From.UnixNano(), a.To.UnixNano(), a.Title, a.Severity})
	}
	return json.Marshal(rows)
}

func mergeAnomalies(existing, incoming []annotations.Anomaly) []annotations.Anomaly {
	type episode struct {
		a        annotations.Anomaly
		priority int
	}
	all := make([]episode, 0, len(existing)+len(incoming))
	for _, a := range existing {
		all = append(all, episode{a, -1})
	}
	for i, a := range incoming {
		all = append(all, episode{a, i})
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i].a, all[j].a
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.From.Before(b.From)
	})
	merged := make([]episode, 0, len(all))
	for _, e := range all {
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			if last.a.Namespace == e.a.Namespace && last.a.Service == e.a.Service && last.a.Kind == e.a.Kind && !e.a.From.After(last.a.To) {
				if e.a.To.After(last.a.To) {
					last.a.To = e.a.To
				}
				if e.priority > last.priority {
					last.a.Title = e.a.Title
					last.a.Severity = e.a.Severity
					last.priority = e.priority
				}
				continue
			}
		}
		merged = append(merged, e)
	}
	out := make([]annotations.Anomaly, 0, len(merged))
	for _, e := range merged {
		out = append(out, e.a)
	}
	return out
}

func (d *Duck) RecordAnomalies(ctx context.Context, findings []annotations.Anomaly, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	unlock, err := d.writeGate.LockContext(ctx, writegate.WriteAnomalyLog)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	findings = boundedAnomalies(findings)
	encoded, err := encodeAnomalies(findings)
	if err != nil {
		return err
	}
	// One bounds relation and one query load the overlapping rows for all keys.
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE annotation_bounds AS SELECT namespace,service,kind,min(start_time) AS start_time,max(end_time) AS end_time FROM (`+anomalyRowsSQL+`) GROUP BY 1,2,3`, string(encoded)); err != nil {
		return err
	}
	overlap := `a.namespace=b.namespace AND a.service=b.service AND a.kind=b.kind AND a.end_time>=b.start_time AND a.start_time<=b.end_time`
	rows, err := tx.QueryContext(ctx, `SELECT a.namespace,a.service,a.kind,a.start_time::TIMESTAMP_NS,a.end_time::TIMESTAMP_NS,a.title,a.severity FROM anomaly_log a WHERE EXISTS (SELECT 1 FROM annotation_bounds b WHERE `+overlap+`)`)
	if err != nil {
		return err
	}
	existing := []annotations.Anomaly{}
	for rows.Next() {
		var a annotations.Anomaly
		if err := rows.Scan(&a.Namespace, &a.Service, &a.Kind, &a.From, &a.To, &a.Title, &a.Severity); err != nil {
			rows.Close()
			return err
		}
		existing = append(existing, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	encoded, err = encodeAnomalies(mergeAnomalies(existing, findings))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE annotation_updates AS `+anomalyRowsSQL, string(encoded)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE annotation_candidates AS SELECT * FROM (
 SELECT * FROM anomaly_log a WHERE NOT EXISTS (SELECT 1 FROM annotation_bounds b WHERE `+overlap+`)
 UNION ALL SELECT * FROM annotation_updates) WHERE end_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, now.UTC().Add(-30*24*time.Hour)); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM annotation_candidates`).Scan(&count); err != nil {
		return err
	}
	retained := `SELECT * FROM annotation_candidates`
	if count > 10000 {
		retained += ` ORDER BY end_time DESC,namespace,service,kind,start_time LIMIT 10000`
	}
	// One delete covers merged episodes, expired rows, and (only if necessary)
	// history overflow. Unaffected episodes stay in place.
	if _, err := tx.ExecContext(ctx, `DELETE FROM anomaly_log a WHERE EXISTS (SELECT 1 FROM annotation_bounds b WHERE `+overlap+`) OR (namespace,service,kind,start_time) NOT IN (SELECT namespace,service,kind,start_time FROM (`+retained+`))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO anomaly_log SELECT f.* FROM (`+retained+`) f WHERE EXISTS (SELECT 1 FROM annotation_updates u WHERE f.namespace=u.namespace AND f.service=u.service AND f.kind=u.kind AND f.start_time=u.start_time)`); err != nil {
		return err
	}
	for _, table := range []string{"annotation_candidates", "annotation_updates", "annotation_bounds"} {
		if _, err := tx.ExecContext(ctx, `DROP TABLE `+table); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Duck) DrainVersionRollup(ctx context.Context) (int64, error) {
	return drainVersionRollup(ctx, d.RefreshVersionRollup)
}

// The phase budget governs starting passes; each in-flight pass has its own
// deadline derived from the caller and releases its gates before returning.
func drainVersionRollup(ctx context.Context, refresh func(context.Context) (int64, error)) (int64, error) {
	deadline := time.Now().Add(2 * time.Second)
	var total int64
	for ctx.Err() == nil && time.Now().Before(deadline) {
		n, err := refresh(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			return total, err
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
	return total, ctx.Err()
}

func (d *Duck) transferVersionMarker(ctx context.Context, output telemetry.BatchMetadata, inputs []string) error {
	unlock, err := d.writeGate.LockContext(ctx, writegate.WriteRollupVersion)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range inputs {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM version_rollup_batches WHERE batch_id=?`, id).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return nil
		} // The full output must be read when any input is cold.
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO version_rollup_batches VALUES (?,?) ON CONFLICT(batch_id) DO UPDATE SET max_ingested=greatest(version_rollup_batches.max_ingested,excluded.max_ingested)`, output.ID, output.MaxIngestedNanos); err != nil {
		return err
	}
	return tx.Commit()
}
