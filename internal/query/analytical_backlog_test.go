package query

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// Equal-ingest immutable batches cover six edge sub-windows. Channel barriers
// keep the backlog unfinished until both annotation writers have committed.
func TestAnalyticalBacklogAllowsVersionAndAnomalyProgress(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().Truncate(time.Minute)
	base := now.Add(-3 * time.Hour)
	seedAnalyticalBacklog(t, repo, base, now.UnixNano(), 0)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	firstCommitted := make(chan struct{})
	resume := make(chan struct{})
	edgeDone := make(chan error, 1)
	edgeJoined := make(chan struct{})
	go func() {
		defer close(edgeJoined)
		_, err := d.refreshEdgeRollup(ctx)
		if err != nil {
			edgeDone <- err
			close(firstCommitted)
			return
		}
		close(firstCommitted)
		select {
		case <-resume:
		case <-ctx.Done():
			edgeDone <- ctx.Err()
			return
		}
		for pass := 0; pass < 20; pass++ {
			if _, err := d.refreshEdgeRollup(ctx); err != nil {
				edgeDone <- err
				return
			}
			var watermark, cursor int64
			if err := d.DB.QueryRowContext(ctx, `SELECT
  coalesce(max(last_ingested_unix_nano) FILTER (WHERE cache_key = ?),0),
  coalesce(max(last_ingested_unix_nano) FILTER (WHERE cache_key = ?),0)
FROM rollup_state`, edgeRollupStateKey, edgeRollupSubCursorKey).Scan(&watermark, &cursor); err != nil {
				edgeDone <- err
				return
			}
			if cursor == 0 && watermark == now.UnixNano() {
				edgeDone <- nil
				return
			}
		}
		edgeDone <- errors.New("edge backlog failed to converge")
	}()
	t.Cleanup(func() { cancel(); <-edgeJoined })
	<-firstCommitted
	watermark, cursor := readEdgeRollupState(t, d)
	if watermark != 0 || cursor != base.Add(30*time.Minute).UnixNano() {
		t.Fatalf("first committed pass must yield after one sub-window: watermark=%d cursor=%d want=%d", watermark, cursor, base.Add(30*time.Minute).UnixNano())
	}

	ready := make(chan struct{})
	versions, anomaly := make(chan error, 1), make(chan error, 1)
	go func() {
		<-ready
		_, err := d.RefreshVersionRollup(ctx)
		versions <- err
	}()
	go func() {
		<-ready
		anomaly <- d.RecordAnomalies(ctx, []annotations.Anomaly{{Namespace: "ns-a", Service: "gateway", Kind: "latency", From: base, To: base.Add(time.Minute), Title: "Backlog", Severity: "warning"}}, now)
	}()
	close(ready)
	if err := <-versions; err != nil {
		t.Fatal(err)
	}
	if err := <-anomaly; err != nil {
		t.Fatal(err)
	}
	var versionRows, anomalyRows int
	if err := d.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM version_rollup),(SELECT count(*) FROM anomaly_log)`).Scan(&versionRows, &anomalyRows); err != nil || versionRows == 0 || anomalyRows != 1 {
		t.Fatalf("writers did not progress: versions=%d anomalies=%d err=%v", versionRows, anomalyRows, err)
	}
	if gotWatermark, gotCursor := readEdgeRollupState(t, d); gotWatermark != watermark || gotCursor != cursor {
		t.Fatal("backlog drained before the annotation writers committed")
	}

	// The completed-batch writer still works with an analytical transaction
	// occupying its gate and pool slot.
	unlock := d.writeGate.Lock(writegate.WriteRollupEdge)
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO rollup_state VALUES ('backlog-test',1,now())`); err == nil {
		_, err = d.RefreshReadCaches(ctx)
	}
	rollbackErr := tx.Rollback()
	unlock()
	if err != nil || rollbackErr != nil {
		t.Fatalf("independent cache writer: %v rollback=%v", err, rollbackErr)
	}
	assertBacklogMarkers(t, d, repo)

	// Cancellation cannot acknowledge the unfinished tail; the retry below
	// must resume from exactly the committed cursor.
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := d.refreshEdgeRollup(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pass: %v", err)
	}
	if gotWatermark, gotCursor := readEdgeRollupState(t, d); gotWatermark != watermark || gotCursor != cursor {
		t.Fatal("canceled pass changed committed progress")
	}
	close(resume)
	select {
	case err := <-edgeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertBacklogEdges(t, d)
	if _, err := d.RefreshReadCaches(ctx); err != nil {
		t.Fatal(err)
	}
	before := len(repo.Parquet.BatchMetadata())
	if n, err := repo.CompactParquet(ctx, d, 8); err != nil || n != 8 {
		t.Fatalf("compaction: inputs=%d err=%v", n, err)
	}
	if err := d.runRepositoryMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if after := len(repo.Parquet.BatchMetadata()); after >= before {
		t.Fatalf("compaction made no progress: before=%d after=%d", before, after)
	}
	if _, err := d.RefreshReadCaches(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DrainVersionRollup(ctx); err != nil {
		t.Fatal(err)
	}
	assertBacklogMarkers(t, d, repo)
	assertBacklogEdges(t, d)
}

func TestServiceRollupBacklogDrainsWithoutDroppingLateBuckets(t *testing.T) {
	for _, ingestStep := range []time.Duration{0, 20 * time.Minute} {
		t.Run(ingestStep.String(), func(t *testing.T) {
			d, repo := versionEngine(t)
			base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Hour)
			seedAnalyticalBacklog(t, repo, base, base.UnixNano(), ingestStep)
			ctx := t.Context()
			if _, err := d.refreshServiceRollup(ctx); err != nil {
				t.Fatal(err)
			}
			tip := base.Add(11 * ingestStep).UnixNano()
			// Below the live tip, but inside its publication safety lag. This
			// revisits an old event bucket containing service-only outbound work.
			late := telemetrystore.Batch{ID: "late", Spans: []telemetry.Span{{Namespace: "ns-a", ServiceName: "outbound", TraceID: "late", SpanID: "late", Kind: "SPAN_KIND_CLIENT", StartUnixNanos: base.UnixNano(), EndUnixNanos: base.Add(time.Millisecond).UnixNano(), DurationMS: 1, IngestedAt: tip - int64(time.Second)}}, Logs: []telemetry.Log{{Namespace: "ns-b", ServiceName: "gateway", EventUnixNanos: base.UnixNano(), TimeUnixNanos: base.UnixNano(), IngestedAt: tip - int64(time.Second), Body: "late"}}}
			if err := repo.Commit(ctx, late); err != nil {
				t.Fatal(err)
			}
			passes := 1
			for ; passes < 10; passes++ {
				if _, err := d.refreshServiceRollup(ctx); err != nil {
					t.Fatal(err)
				}
				var watermark int64
				if err := d.DB.QueryRowContext(ctx, `SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key=?`, serviceRollupStateKey).Scan(&watermark); err != nil {
					t.Fatal(err)
				}
				if watermark == tip {
					break
				}
			}
			if passes == 10 || (ingestStep > 0 && passes < 3) {
				t.Fatalf("unexpected drain passes=%d", passes)
			}
			var mismatches int
			if err := d.DB.QueryRowContext(ctx, `WITH raw AS (
 SELECT namespace,date_trunc('minute',start_time::TIMESTAMP_NS) AS bucket,service,count(*) AS spans,0::BIGINT AS logs FROM spans GROUP BY ALL
 UNION ALL
 SELECT namespace,date_trunc('minute',time::TIMESTAMP_NS),service,0::BIGINT,count(*) FROM logs GROUP BY ALL
), expected AS (SELECT namespace,bucket,service,sum(spans) AS spans,sum(logs) AS logs FROM raw GROUP BY ALL)
SELECT count(*) FROM expected e FULL JOIN service_rollup r USING(namespace,bucket,service)
WHERE coalesce(e.spans,0)<>coalesce(r.spans,0) OR coalesce(e.logs,0)<>coalesce(r.log_count,0)`).Scan(&mismatches); err != nil || mismatches != 0 {
				t.Fatalf("raw/rollup bucket mismatches=%d err=%v", mismatches, err)
			}
			if n, err := d.refreshServiceRollup(ctx); err != nil || n != 0 {
				t.Fatalf("plateau must be idle: rows=%d err=%v", n, err)
			}
		})
	}
}

func seedAnalyticalBacklog(t *testing.T, repo *telemetrystore.Repository, base time.Time, ingest int64, step time.Duration) {
	t.Helper()
	for batch := range 12 {
		b := telemetrystore.Batch{ID: fmt.Sprintf("backlog-%02d", batch)}
		for _, ns := range []string{"ns-a", "ns-b"} {
			n := base.Add(time.Duration(batch) * 15 * time.Minute).UnixNano()
			for i, service := range []string{"gateway", "payments", "outbound"} {
				kind, parent := "SPAN_KIND_CLIENT", ""
				if i == 0 {
					kind = "SPAN_KIND_SERVER"
				} else if i == 1 {
					parent = "s0"
				}
				b.Spans = append(b.Spans, telemetry.Span{Namespace: ns, ServiceName: service, ServiceVersion: "v1", TraceID: fmt.Sprint(batch), SpanID: fmt.Sprint("s", i), ParentSpanID: parent, Kind: kind, StartUnixNanos: n, EndUnixNanos: n + int64(time.Millisecond), DurationMS: 1, IngestedAt: ingest + int64(time.Duration(batch)*step)})
			}
			b.Logs = append(b.Logs, telemetry.Log{Namespace: ns, ServiceName: "gateway", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: ingest + int64(time.Duration(batch)*step), Body: "backlog"})
		}
		if err := repo.Commit(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
}

func assertBacklogEdges(t *testing.T, d *Duck) {
	t.Helper()
	var mismatches int
	if err := d.DB.QueryRow(`WITH expected AS (
 SELECT namespace,date_trunc('minute',start_time::TIMESTAMP_NS) AS bucket,count(*) AS calls FROM spans WHERE service='payments' GROUP BY ALL
), actual AS (SELECT namespace,bucket,sum(calls) AS calls FROM edge_rollup GROUP BY ALL)
SELECT count(*) FROM expected e FULL JOIN actual a USING(namespace,bucket)
WHERE coalesce(e.calls,0)<>coalesce(a.calls,0)`).Scan(&mismatches); err != nil || mismatches != 0 {
		t.Fatalf("edge bucket mismatches=%d err=%v", mismatches, err)
	}
}

func assertBacklogMarkers(t *testing.T, d *Duck, repo *telemetrystore.Repository) {
	t.Helper()
	cached, err := batchMarkers(t.Context(), d.DB)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.DB.QueryContext(t.Context(), `SELECT batch_id FROM version_rollup_batches`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	marked := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		marked[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	active := repo.Parquet.BatchMetadata()
	if len(cached) != len(active) || len(marked) != len(active) {
		t.Fatalf("inexact markers: active=%d cached=%d version=%d", len(active), len(cached), len(marked))
	}
	for _, b := range active {
		if !cached[b.ID] || !marked[b.ID] {
			t.Fatalf("active batch %s missing cache/version acknowledgement", b.ID)
		}
	}
}
