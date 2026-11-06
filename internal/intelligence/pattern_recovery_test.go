package intelligence

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// detectorEngine is a real engine whose detector reads can fail on demand: a
// statement containing fail returns context.DeadlineExceeded, as a read that
// timed out does.
type detectorEngine struct {
	*engine.Duck
	repo *telemetrystore.Repository
	fail string
	logs int
}

func (e *detectorEngine) QueryContext(ctx context.Context, statement string, args ...any) (queryrows.Rows, error) {
	if e.fail != "" && strings.Contains(statement, e.fail) {
		return nil, context.DeadlineExceeded
	}
	return e.Duck.QueryContext(ctx, statement, args...)
}

func newDetectorEngine(t *testing.T) *detectorEngine {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = duck.Close(); _ = repo.Close() })
	return &detectorEngine{Duck: duck, repo: repo}
}

// commitFailures commits ten identical ERROR logs from api at at.
func (e *detectorEngine) commitFailures(t *testing.T, at time.Time) {
	t.Helper()
	e.logs++
	batch := telemetrystore.Batch{ID: fmt.Sprintf("failures-%d", e.logs)}
	for range 10 {
		batch.Logs = append(batch.Logs, telemetry.Log{Namespace: "default", ServiceName: "api", Severity: "ERROR", Body: "failure", BodyTemplate: "failure", TimeUnixNanos: at.UnixNano()})
	}
	if err := e.repo.Commit(t.Context(), batch); err != nil {
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
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
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
			e := newDetectorEngine(t)
			end := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
			if completed {
				seedDetectorRecoveryRollup(t, e.DB, end)
			}
			d := NewDetector(e, DefaultDetectorConfig())
			d.runCheck(t.Context())
			previous := d.LatestSnapshot()
			if previous == nil || len(previous.Patterns) != 0 {
				t.Fatalf("initial snapshot: %+v", previous)
			}
			at := time.Now().UTC().Add(-time.Minute)
			e.commitFailures(t, at)
			// Changing rollup data must not change the cached anomaly evaluation.
			if _, err := e.DB.Exec(`UPDATE service_rollup SET error_rate=0,p95_ms=0,spans=1`); err != nil {
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
