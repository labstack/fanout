package intelligence

import (
	"database/sql"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func createDetectorLogs(t *testing.T, db *sql.DB) {
	t.Helper()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), `SET TimeZone='UTC'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE logs (
		time TIMESTAMPTZ_NS, time_unix_nano BIGINT, body_template VARCHAR,
		body VARCHAR, severity VARCHAR, service VARCHAR, namespace VARCHAR)`); err != nil {
		t.Fatal(err)
	}
}

func TestLogPatternsIncludeObservedTimeAndExactEventBounds(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 1}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	start := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	end := start.Add(100 * time.Nanosecond)
	batch := telemetrystore.Batch{ID: "pattern-event-time"}
	for _, observedOnly := range []bool{false, true} {
		for _, at := range []time.Time{start.Add(-time.Nanosecond), start, start.Add(time.Nanosecond), end.Add(-time.Nanosecond), end} {
			log := telemetry.Log{Namespace: "default", ServiceName: "api", Severity: "ERROR", Body: "original", BodyTemplate: "original", TimeUnixNanos: at.UnixNano()}
			if observedOnly {
				log.Body, log.BodyTemplate = "observed", "observed"
				log.TimeUnixNanos, log.ObservedTimeNanos = 0, at.UnixNano()
			}
			batch.Logs = append(batch.Logs, log)
		}
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	d := NewDetector(duck, DefaultDetectorConfig())
	patterns, err := d.detectLogPatterns(t.Context(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 2 {
		t.Fatalf("patterns=%+v, want original and observed timestamps", patterns)
	}
	for _, pattern := range patterns {
		if pattern.Count != 3 || !pattern.FirstSeen.Equal(start) || !pattern.LastSeen.Equal(end.Add(-time.Nanosecond)) {
			t.Fatalf("event-time bounds lost precision: %+v", pattern)
		}
	}
}

func TestDetectorRefreshesPatternsAtStalledAndZeroWatermarks(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "zero"
		if completed {
			name = "stalled"
		}
		t.Run(name, func(t *testing.T) {
			db, err := sql.Open("duckdb", "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := query.CreateCacheTables(db); err != nil {
				t.Fatal(err)
			}
			createDetectorLogs(t, db)
			end := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
			if completed {
				seedDetectorRecoveryRollup(t, db, end)
			}
			d := NewDetector(&query.Duck{DB: db}, DefaultDetectorConfig())
			d.runCheck(t.Context())
			previous := d.LatestSnapshot()
			if previous == nil || len(previous.Patterns) != 0 {
				t.Fatalf("initial snapshot: %+v", previous)
			}
			at := time.Now().UTC().Add(-time.Minute)
			if _, err := db.Exec(`INSERT INTO logs SELECT ?::TIMESTAMP_NS::TIMESTAMPTZ_NS, ?, 'failure', 'failure', 'ERROR', 'api', 'default' FROM range(10)`, at, at.UnixNano()); err != nil {
				t.Fatal(err)
			}
			// Changing rollup data must not change the cached anomaly evaluation.
			if _, err := db.Exec(`UPDATE service_rollup SET error_rate=0,p95_ms=0,spans=1`); err != nil {
				t.Fatal(err)
			}
			d.runCheck(t.Context())
			got := d.LatestSnapshot()
			if got == previous || len(got.Patterns) != 1 || got.Patterns[0].Count != 10 || got.HealthScore != previous.HealthScore-5 {
				t.Fatalf("live logs did not refresh: %+v", got)
			}
			if len(got.Anomalies) != len(previous.Anomalies) {
				t.Fatal("stalled anomaly evaluation changed")
			}
			for i, a := range got.Anomalies {
				if a != previous.Anomalies[i] || !a.WindowEnd.Equal(end) {
					t.Fatalf("anomaly interval or values changed: %+v", a)
				}
			}
			// Move the live window beyond the logs without changing the watermark.
			d.config.LookbackWindow = 30 * time.Second
			d.runCheck(t.Context())
			if expired := d.LatestSnapshot(); len(expired.Patterns) != 0 || expired.HealthScore != previous.HealthScore {
				t.Fatalf("expired patterns retained: %+v", expired)
			}
		})
	}
}
