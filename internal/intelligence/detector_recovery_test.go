package intelligence

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/engine/writegate"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// Fail just one statement while retaining native DuckDB watermark reads and
// all preceding anomaly queries, including when the third query fails.
type detectorFailureConnector struct {
	driver.Connector
	fail string
}

func (c *detectorFailureConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return detectorFailureConn{Conn: conn, connector: c}, nil
}

type detectorFailureConn struct {
	driver.Conn
	connector *detectorFailureConnector
}

func (c detectorFailureConn) QueryContext(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
	if c.connector.fail != "" && strings.Contains(statement, c.connector.fail) {
		return nil, context.DeadlineExceeded
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, statement, args)
}

func seedDetectorRecoveryRollup(t *testing.T, db *sql.DB, end time.Time) {
	t.Helper()
	for i, count := range []int{100, 200, 300, 3000, 3000, 3000} {
		rate, p95 := 0.0, float64(i+1)*10
		if i >= 3 {
			rate, p95 = .5, 1000
		}
		if _, err := db.Exec(`INSERT INTO service_rollup(namespace,bucket,service,spans,served_spans,p95_ms,error_rate) VALUES ('default',?,'api',?,?,?,?)`, end.Add(time.Duration(i-6)*5*time.Minute), count, count, p95, rate); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO rollup_state VALUES ('service_rollup_v3',?,now())`, end.Add(30*time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
}

func TestDetectorRetriesFailedQueriesAtUnchangedWatermark(t *testing.T) {
	for _, statement := range []string{"AS current_rate", "AS current_p95", "AS current_count"} {
		for _, withPrevious := range []bool{false, true} {
			name := statement + "/startup"
			if withPrevious {
				name = statement + "/previous"
			}
			t.Run(name, func(t *testing.T) {
				native, err := duckdb.NewConnector("", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer native.Close()
				connector := &detectorFailureConnector{Connector: native}
				db := sql.OpenDB(connector)
				defer db.Close()
				if err := engine.CreateCacheTables(db); err != nil {
					t.Fatal(err)
				}
				createDetectorLogs(t, db)
				end := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Hour)
				seedDetectorRecoveryRollup(t, db, end)
				d := NewDetector(&engine.Duck{DB: db}, DefaultDetectorConfig())
				if withPrevious {
					d.publishSnapshot(Snapshot{GeneratedAt: time.Now().Add(-time.Minute), evaluatedThrough: end.Add(-time.Minute), HealthScore: 85})
					<-d.annotationBatches
				}
				previous := d.LatestSnapshot()
				connector.fail = statement
				if _, err := d.GenerateSnapshot(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("query error was not propagated: %v", err)
				}
				d.runCheck(t.Context())
				if d.LatestSnapshot() != previous || len(d.annotationBatches) != 0 {
					t.Fatal("failed evaluation published a snapshot or queued annotations")
				}
				// Recover without changing the watermark or the rollup data.
				connector.fail = ""
				d.runCheck(t.Context())
				got := d.LatestSnapshot()
				if got == nil || got == previous || !got.evaluatedThrough.Equal(end) || len(got.Anomalies) != 3 || got.HealthScore >= 100 {
					t.Fatalf("recovered snapshot: %+v", got)
				}
				batch := <-d.annotationBatches
				if len(batch.findings) != 3 {
					t.Fatalf("recovered findings: %+v", batch)
				}
				for _, a := range batch.findings {
					if !a.From.Equal(end.Add(-15*time.Minute)) || !a.To.Equal(end) {
						t.Fatalf("recovered interval: %+v", a)
					}
				}
				d.runCheck(t.Context())
				if latest := d.LatestSnapshot(); latest == got || latest.Anomalies[0] != got.Anomalies[0] {
					t.Fatal("live refresh changed the successful anomaly evaluation")
				}
			})
		}
	}
}

func TestDetectorRetriesPersistenceAtUnchangedWatermark(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	end := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Hour)
	seedDetectorRecoveryRollup(t, duck.DB, end)
	d := NewDetector(duck, DefaultDetectorConfig())
	d.runCheck(t.Context())
	previous := d.LatestSnapshot()
	unlock := duck.WriteGate().Lock(writegate.RollupService)
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	attempts := make(chan error, 8)
	go func() {
		defer close(done)
		attempt := 0
		d.persistAnnotations(ctx, func(ctx context.Context, findings []annotations.Anomaly, at time.Time) error {
			// Exercise a real gate timeout without waiting the full 10 seconds.
			attempt++
			if attempt <= 2 {
				writeCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				ctx = writeCtx
			}
			err := duck.RecordAnomalies(ctx, findings, at)
			attempts <- err
			return err
		})
	}()
	defer func() { cancel(); <-done }()
	waitAttempt := func() error {
		t.Helper()
		select {
		case err := <-attempts:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("persistence did not retry without a new batch")
			return nil
		}
	}
	// Two failed writes also exercise rescheduling after a failed timer retry.
	for range 2 {
		if err := waitAttempt(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected gate timeout: %v", err)
		}
	}
	d.runCheck(t.Context())
	if latest := d.LatestSnapshot(); latest == previous || latest.Anomalies[0] != previous.Anomalies[0] {
		t.Fatal("live refresh changed the stalled anomaly evaluation")
	}
	refreshed := d.LatestSnapshot()
	unlock()
	unlock = nil
	if err := waitAttempt(); err != nil {
		t.Fatalf("persistence did not recover: %v", err)
	}
	got, err := annotations.New(duck).Read(t.Context(), annotations.Request{From: end.Add(-time.Hour), To: end.Add(time.Hour)})
	if err != nil || len(got.Anomalies) != 3 {
		t.Fatalf("persisted findings: %+v, %v", got, err)
	}
	for _, a := range got.Anomalies {
		if !a.From.Equal(end.Add(-15*time.Minute)) || !a.To.Equal(end) {
			t.Fatalf("retry changed evaluated interval: %+v", a)
		}
	}
	if d.LatestSnapshot() != refreshed {
		t.Fatal("persistence retry changed snapshot")
	}
}

func TestDetectorRetriesPatternFailureWithoutPublishingHealthySnapshot(t *testing.T) {
	for _, withPrevious := range []bool{false, true} {
		name := "startup"
		if withPrevious {
			name = "previous"
		}
		t.Run(name, func(t *testing.T) {
			native, err := duckdb.NewConnector("", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer native.Close()
			connector := &detectorFailureConnector{Connector: native}
			db := sql.OpenDB(connector)
			defer db.Close()
			if err := engine.CreateCacheTables(db); err != nil {
				t.Fatal(err)
			}
			createDetectorLogs(t, db)
			end := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
			seedDetectorRecoveryRollup(t, db, end)
			d := NewDetector(&engine.Duck{DB: db}, DefaultDetectorConfig())
			if withPrevious {
				d.runCheck(t.Context())
				<-d.annotationBatches
			}
			previous := d.LatestSnapshot()
			at := time.Now().UTC().Add(-time.Minute)
			if _, err := db.Exec(`INSERT INTO logs SELECT ?::TIMESTAMP_NS::TIMESTAMPTZ_NS, ?, 'failure', 'failure', 'ERROR', 'api', 'default' FROM range(10)`, at, at.UnixNano()); err != nil {
				t.Fatal(err)
			}
			connector.fail = "AS occurrence_count"
			if _, err := d.GenerateSnapshot(t.Context()); err == nil {
				t.Fatal("pattern query failure was treated as an empty successful result")
			}
			d.runCheck(t.Context())
			if d.LatestSnapshot() != previous || len(d.annotationBatches) != 0 {
				t.Fatal("pattern failure published a snapshot or annotations")
			}
			// The anomaly evaluation completed even if patterns failed. Recover
			// patterns while refusing to re-run that completed anomaly query.
			connector.fail = "AS current_rate"
			d.runCheck(t.Context())
			got := d.LatestSnapshot()
			if got == nil || got == previous || len(got.Patterns) != 1 || len(got.Anomalies) != 3 || got.HealthScore >= 100 {
				t.Fatalf("pattern query was not retried at the same watermark: %+v", got)
			}
		})
	}
}

func TestDetectorKeepsUnpublishedFindingsWhenWatermarkAdvances(t *testing.T) {
	native, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	connector := &detectorFailureConnector{Connector: native, fail: "AS occurrence_count"}
	db := sql.OpenDB(connector)
	defer db.Close()
	if err := engine.CreateCacheTables(db); err != nil {
		t.Fatal(err)
	}
	createDetectorLogs(t, db)
	end := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	seedDetectorRecoveryRollup(t, db, end)
	d := NewDetector(&engine.Duck{DB: db}, DefaultDetectorConfig())
	for range 2 {
		d.runCheck(t.Context())
	}
	if d.LatestSnapshot() != nil || len(d.annotationBatches) != 0 {
		t.Fatal("pattern failure published a snapshot or annotations")
	}
	if d.evaluation == nil || len(d.evaluation.findings) != 3 {
		t.Fatalf("missing successful anomaly evaluation: %+v", d.evaluation)
	}
	want := detectorAnnotations(Snapshot{Anomalies: d.evaluation.findings}, d.duck.DefaultNamespace())

	// Replace the failed-publication evaluation with a healthy, later window
	// while patterns still fail, then recover at that new watermark.
	advanced := end.Add(time.Minute)
	if _, err := db.Exec(`UPDATE service_rollup SET error_rate=0,p95_ms=10,spans=100,served_spans=100`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE rollup_state SET last_ingested_unix_nano=? WHERE cache_key='service_rollup_v3'`, advanced.Add(30*time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
	d.runCheck(t.Context())
	if d.evaluation == nil || !d.evaluation.through.Equal(advanced) || len(d.evaluation.findings) != 0 || d.LatestSnapshot() != nil || len(d.annotationBatches) != 0 {
		t.Fatalf("healthy evaluation did not replace the unpublished evaluation: %+v", d.evaluation)
	}
	connector.fail = ""
	d.runCheck(t.Context())
	got := d.LatestSnapshot()
	if got == nil || !got.evaluatedThrough.Equal(advanced) || len(got.Anomalies) != 0 || got.HealthScore != 100 {
		t.Fatalf("recovered snapshot should reflect the healthy window: %+v", got)
	}
	batch := <-d.annotationBatches
	if len(batch.findings) != len(want) {
		t.Fatalf("unpublished incident lost or duplicated: %+v", batch.findings)
	}
	for i, finding := range batch.findings {
		if finding != want[i] {
			t.Fatalf("unpublished finding changed: got %+v, want %+v", finding, want[i])
		}
	}

	// Persist the recovered batch through the real annotation storage path.
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	if err := duck.RecordAnomalies(t.Context(), batch.findings, batch.at); err != nil {
		t.Fatal(err)
	}
	persisted, err := annotations.New(duck).Read(t.Context(), annotations.Request{From: end.Add(-time.Hour), To: advanced.Add(time.Hour)})
	if err != nil || len(persisted.Anomalies) != len(want) {
		t.Fatalf("persisted findings: %+v, %v", persisted, err)
	}
	for _, finding := range persisted.Anomalies {
		if !finding.From.Equal(end.Add(-15*time.Minute)) || !finding.To.Equal(end) {
			t.Fatalf("unpublished incident interval changed: %+v", finding)
		}
	}
	d.runCheck(t.Context())
	if batch := <-d.annotationBatches; len(batch.findings) != 0 {
		t.Fatalf("published findings were carried again: %+v", batch.findings)
	}
}
