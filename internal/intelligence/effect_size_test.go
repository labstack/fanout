package intelligence

import (
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/engine"
)

func TestLatencyAnomaliesRequireStatisticalAndPracticalSignificance(t *testing.T) {
	db := effectSizeDB(t)
	start := time.Date(2026, 10, 1, 12, 15, 0, 0, time.UTC)
	end := start.Add(15 * time.Minute)
	tests := []struct {
		name                      string
		baseline, current, stddev float64
		want                      bool
	}{
		{"ad", 1.3, 1.4, .01, false},
		{"flagd", .3, .3001, .00001, false},
		{"email", 5.8, 5.8001, .00001, false},
		{"sub_millisecond_doubling", .3, .6, .001, false},
		{"real_regression", 20, 60, 1, true},
		{"flat_baseline_regression", 20, 60, 0, true},
		{"absolute_below", 10, 15 - 1e-6, .01, false},
		{"absolute_at", 10, 15, .01, true},
		{"absolute_above", 10, 15 + 1e-6, .01, true},
		{"relative_below", 100, 120 - 1e-6, .01, false},
		{"relative_at", 100, 120, .01, true},
		{"relative_above", 100, 120 + 1e-6, .01, true},
		{"noisy_baseline", 100, 125, 30, false},
		{"statistical_below", 20, 30 - 1e-6, 5, false},
		{"statistical_at", 20, 30, 5, true},
		{"improvement", 60, 20, 1, false},
	}
	for _, tt := range tests {
		for i := range 6 {
			p95 := tt.current
			if i < 3 {
				p95 = tt.baseline + float64(i-1)*tt.stddev
			}
			if _, err := db.Exec(`INSERT INTO service_rollup VALUES (?, 'default', ?, 100, ?)`, tt.name, start.Add(time.Duration(i-3)*5*time.Minute), p95); err != nil {
				t.Fatal(err)
			}
		}
	}
	d := NewDetector(&engine.Duck{DB: db}, DefaultDetectorConfig())
	anomalies, err := d.detectLatencyAnomalies(t.Context(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertEffectSizeFinding(t, d, anomalies, tt.name, AnomalyLatencyDegradation, tt.want, start, end)
		})
	}
}

func TestVolumeAnomaliesRequireStatisticalAndPracticalSignificance(t *testing.T) {
	db := effectSizeDB(t)
	start := time.Date(2026, 10, 1, 12, 15, 0, 0, time.UTC)
	end := start.Add(15 * time.Minute)
	tests := []struct {
		name                      string
		baseline, current, jitter int
		want                      bool
	}{
		{"payment", 250, 263, 1, false},
		{"ad", 730, 779, 1, false},
		{"currency", 1274, 935, 12, true},
		{"frontend_proxy", 32031, 21573, 481, true},
		{"silent", 100, 0, 1, true},
		{"flat_baseline_silent", 100, 0, 0, true},
		{"doubling", 100, 200, 1, true},
		{"flat_baseline_doubling", 100, 200, 0, true},
		{"thirty_percent_drop", 100, 70, 1, true},
		{"surge_below", 100000, 124999, 1, false},
		{"surge_at", 100000, 125000, 1, true},
		{"surge_above", 100000, 125001, 1, true},
		{"drop_below", 100000, 75001, 1, false},
		{"drop_at", 100000, 75000, 1, true},
		{"drop_above", 100000, 74999, 1, true},
		{"noisy_baseline", 100, 130, 30, false},
		{"statistical_below", 100, 149, 20, false},
		{"statistical_at", 100, 150, 20, true},
		{"steady", 100, 100, 0, false},
		{"no_baseline", 0, 100, 0, false},
	}
	for _, tt := range tests {
		for i := range 6 {
			count := tt.current
			if i < 3 {
				count = tt.baseline + (i-1)*tt.jitter
			}
			// Silence has no current rollup rows, rather than synthetic zeros.
			if count == 0 {
				continue
			}
			if _, err := db.Exec(`INSERT INTO service_rollup VALUES (?, 'default', ?, ?, 10)`, tt.name, start.Add(time.Duration(i-3)*5*time.Minute), count); err != nil {
				t.Fatal(err)
			}
		}
	}
	d := NewDetector(&engine.Duck{DB: db}, DefaultDetectorConfig())
	anomalies, err := d.detectVolumeAnomalies(t.Context(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertEffectSizeFinding(t, d, anomalies, tt.name, AnomalyVolumeChange, tt.want, start, end)
		})
	}
}

func TestAnomalyScatterFloorsRetainFlatAndSingleBucketBaselines(t *testing.T) {
	for _, kind := range []string{"latency", "volume"} {
		for _, scatter := range []int{0, 1, 2} {
			for _, buckets := range []int{1, 3} {
				t.Run(fmt.Sprintf("%s/scatter_%d/buckets_%d", kind, scatter, buckets), func(t *testing.T) {
					db := effectSizeDB(t)
					start := time.Date(2026, 10, 1, 12, 15, 0, 0, time.UTC)
					end := start.Add(15 * time.Minute)
					for i := range buckets {
						value := 100 + (i-buckets/2)*scatter
						spans, p95 := 100, 100
						if kind == "latency" {
							p95 = value
						} else {
							spans = value
						}
						if _, err := db.Exec(`INSERT INTO service_rollup VALUES ('api', 'default', ?, ?, ?)`, start.Add(time.Duration(i-3)*5*time.Minute), spans, p95); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := db.Exec(`INSERT INTO service_rollup VALUES ('api', 'default', ?, 150, 150)`, start); err != nil {
						t.Fatal(err)
					}
					statement := latencyAnomalySQL(start.UnixNano(), end.UnixNano(), "")
					if kind == "volume" {
						statement = volumeAnomalySQL(start.UnixNano(), end.UnixNano(), "")
					}
					var service string
					var current, baseline, z float64
					if err := db.QueryRow(statement).Scan(&service, &current, &baseline, &z); err != nil {
						t.Fatal(err)
					}
					denominator := 1.0
					if buckets == 3 {
						denominator = math.Max(float64(scatter), 1)
					}
					if want := 50 / denominator; math.Abs(z-want) > 1e-9 {
						t.Fatalf("z=%g, want %g", z, want)
					}
				})
			}
		}
	}
}

func effectSizeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE service_rollup (service TEXT, namespace TEXT, bucket TIMESTAMP_NS, spans BIGINT, p95_ms DOUBLE)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertEffectSizeFinding(t *testing.T, d *Detector, anomalies []Anomaly, service string, kind AnomalyType, want bool, start, end time.Time) {
	t.Helper()
	var found []Anomaly
	for _, a := range anomalies {
		if a.ServiceName == service {
			found = append(found, a)
		}
	}
	if !want {
		if len(found) != 0 {
			t.Fatalf("noise emitted findings: %+v", found)
		}
		if score := d.calculateHealthScore(found, nil); score != 100 {
			t.Fatalf("noise lowered health to %g", score)
		}
		if len(detectorAnnotations(Snapshot{Anomalies: found}, "default")) != 0 || len(d.generateInsights(found, nil)) != 0 {
			t.Fatal("noise emitted annotations or insights")
		}
		return
	}
	if len(found) != 1 || found[0].Type != kind || !found[0].WindowStart.Equal(start) || !found[0].WindowEnd.Equal(end) {
		t.Fatalf("expected one qualified finding in the evaluated window: %+v", found)
	}
	if score := d.calculateHealthScore(found, nil); score >= 100 {
		t.Fatalf("qualified finding did not lower health: %g", score)
	}
	annotations := detectorAnnotations(Snapshot{Anomalies: found}, "default")
	severity := "warn"
	if math.Abs(found[0].ZScore) >= 4 {
		severity = "bad"
	}
	if len(annotations) != 1 || annotations[0].Severity != severity {
		t.Fatalf("qualified finding severity: %+v", annotations)
	}
}
