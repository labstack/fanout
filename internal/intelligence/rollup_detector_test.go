package intelligence

import (
	"database/sql"
	"math"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestServiceDetectorsWeightMinutesBySpanCount(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := query.CreateCacheTables(db); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 12, 15, 0, 0, time.UTC)
	end := start.Add(15 * time.Minute)
	for bucket := 0; bucket < 6; bucket++ {
		at := start.Add(time.Duration(bucket-3) * 5 * time.Minute)
		for _, v := range []struct {
			offset    int
			spans     int
			rate, p95 float64
		}{{0, 10, .9, 100}, {1, 90, .1, 10}} {
			if _, err := db.Exec(`INSERT INTO service_rollup(namespace,bucket,service,spans,served_spans,p95_ms,error_rate) VALUES ('prod',?,'api',?,?,?,?)`, at.Add(time.Duration(v.offset)*time.Minute), v.spans, v.spans, v.p95+float64(bucket), v.rate); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, test := range []struct {
		name, statement string
		want            float64
	}{
		{"errors", errorRateAnomalySQL(start.UnixNano(), end.UnixNano(), "AND namespace='prod'"), .18},
		{"latency", latencyAnomalySQL(start.UnixNano(), end.UnixNano(), "AND namespace='prod'"), 23},
		{"volume", volumeAnomalySQL(start.UnixNano(), end.UnixNano(), "AND namespace='prod'"), 100},
	} {
		var service string
		var current, baseline, z float64
		if err := db.QueryRow(test.statement).Scan(&service, &current, &baseline, &z); err != nil {
			t.Fatal(err)
		}
		if service != "api" || math.Abs(current-test.want) > 1e-9 {
			t.Fatalf("%s current=%g want=%g", test.name, current, test.want)
		}
		var count int
		rows, err := db.Query(test.statement)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			count++
		}
		rows.Close()
		if count != 1 {
			t.Fatalf("%s services=%d", test.name, count)
		}
	}
	rows, err := db.Query(errorRateAnomalySQL(start.UnixNano(), end.UnixNano(), "AND namespace='elsewhere'"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("namespace scope leaked")
	}
}
func TestDetectorsStopBeforeTheServiceWatermarkMinute(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := query.CreateCacheTables(db); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, rate := range []float64{0, .5, 1} {
		if _, err := db.Exec(`INSERT INTO service_rollup(namespace,bucket,service,spans,served_spans,p95_ms,error_rate) VALUES ('prod',?,'api',100,100,10,?)`, base.Add(time.Duration(i)*5*time.Minute), rate); err != nil {
			t.Fatal(err)
		}
	}
	watermark := base.Add(10*time.Minute + 30*time.Second).UnixNano()
	if _, err := db.Exec(`INSERT INTO rollup_state VALUES ('service_rollup_v3',?,now())`, watermark); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultDetectorConfig()
	cfg.LookbackWindow = 5 * time.Minute
	detector := NewDetector(&query.Duck{DB: db}, cfg)
	snapshot, err := detector.GenerateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	anomalies := snapshot.Anomalies
	if len(anomalies) != 1 || anomalies[0].Type != AnomalyErrorRateChange || anomalies[0].Current != 50 || anomalies[0].Baseline != 0 {
		t.Fatalf("incomplete minute or wrong baseline: %#v", anomalies)
	}
	if _, err := db.Exec(`DELETE FROM rollup_state`); err != nil {
		t.Fatal(err)
	}
	if result, err := detector.GenerateSnapshot(t.Context()); err != nil || len(result.Anomalies) != 0 {
		t.Fatalf("unbuilt rollup graded: %#v, %v", result, err)
	}
}

func TestDetectorPersistsEvaluatedWindowAndSkipsStalledMinute(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
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
	end := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Hour)
	start := end.Add(-15 * time.Minute)
	for i := -3; i < 3; i++ {
		rate := 0.0
		if i >= 0 {
			rate = .5
		}
		if _, err := duck.DB.Exec(`INSERT INTO service_rollup(namespace,bucket,service,spans,served_spans,p95_ms,error_rate) VALUES ('default',?,'api',100,100,10,?)`, start.Add(time.Duration(i)*5*time.Minute), rate); err != nil {
			t.Fatal(err)
		}
	}
	watermark := end.Add(30 * time.Second).UnixNano()
	if _, err := duck.DB.Exec(`INSERT INTO rollup_state VALUES ('service_rollup_v3',?,now())`, watermark); err != nil {
		t.Fatal(err)
	}
	detector := NewDetector(duck, DefaultDetectorConfig())
	detector.runCheck(t.Context())
	previous := detector.LatestSnapshot()
	if previous == nil || len(previous.Anomalies) != 1 {
		t.Fatalf("snapshot: %+v", previous)
	}
	a := previous.Anomalies[0]
	if !a.WindowStart.Equal(start) || !a.WindowEnd.Equal(end) || !a.DetectedAt.After(end) {
		t.Fatalf("anomaly bounds: %+v", a)
	}
	batch := <-detector.annotationBatches
	if err := duck.RecordAnomalies(t.Context(), batch.findings, batch.at); err != nil {
		t.Fatal(err)
	}
	read := func() []annotations.Anomaly {
		t.Helper()
		result, err := annotations.New(duck).Read(t.Context(), annotations.Request{From: start.Add(-time.Minute), To: end.Add(time.Hour), Namespace: ""})
		if err != nil {
			t.Fatal(err)
		}
		return result.Anomalies
	}
	persisted := read()
	if len(persisted) != 1 || !persisted[0].From.Equal(start) || !persisted[0].To.Equal(end) {
		t.Fatalf("historical interval: %+v", persisted)
	}
	// A recheck would see different values; an unchanged completed minute must
	// retain the exact prior snapshot and enqueue no annotation write.
	if _, err := duck.DB.Exec(`UPDATE service_rollup SET error_rate=.9 WHERE bucket >= ?`, start); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		detector.runCheck(t.Context())
	}
	if detector.LatestSnapshot() != previous || len(detector.annotationBatches) != 0 {
		t.Fatal("stalled watermark re-evaluated or queued annotations")
	}
	after := read()
	if len(after) != 1 || !after[0].From.Equal(persisted[0].From) || !after[0].To.Equal(persisted[0].To) {
		t.Fatalf("stalled interval changed: %+v", after)
	}
	// Crossing only seconds in the same minute still skips evaluation.
	if _, err := duck.DB.Exec(`UPDATE rollup_state SET last_ingested_unix_nano=? WHERE cache_key='service_rollup_v3'`, end.Add(59*time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
	detector.runCheck(t.Context())
	if detector.LatestSnapshot() != previous || len(detector.annotationBatches) != 0 {
		t.Fatal("incomplete minute triggered evaluation")
	}
	if _, err := duck.DB.Exec(`UPDATE rollup_state SET last_ingested_unix_nano=? WHERE cache_key='service_rollup_v3'`, end.Add(time.Minute).UnixNano()); err != nil {
		t.Fatal(err)
	}
	detector.runCheck(t.Context())
	if detector.LatestSnapshot() == previous || len(detector.annotationBatches) != 1 {
		t.Fatal("advanced minute did not trigger evaluation")
	}
}
