//go:build readbench

package panel

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/query/querytest"
	"github.com/labstack/fanout/internal/telemetry"
)

// This small review fixture is separate from the Phase B scale generator.
// Each kind has 420,000 real Parquet rows: 1,000 series, six points per
// minute, 70 one-minute batches, including ten minutes before a one-hour window.
func TestMetricMeasuresExecuteAtReviewScale(t *testing.T) {
	db, err := sql.Open("duckdb", "") // pinned engine defaults, without tuning
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec("SET TimeZone='UTC'"); err != nil {
		t.Fatal(err)
	}
	if err := querytest.CreateTables(db, query.CreateCacheTables); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE telemetry.metrics"); err != nil {
		t.Fatal(err)
	}
	var version, threads, memory string
	if err := db.QueryRow("SELECT version(),current_setting('threads')::VARCHAR,current_setting('memory_limit')::VARCHAR").Scan(&version, &threads, &memory); err != nil {
		t.Fatal(err)
	}
	t.Logf("engine=%s threads=%s memory_limit=%s (defaults)", version, threads, memory)
	e := NewExecutor(&query.Duck{DB: db}, 30)
	start, end := fixtureStart, fixtureStart.Add(time.Hour)
	e.now = func() time.Time { return end }
	for _, kind := range []string{"gauge", "counter", "histogram"} {
		store, err := telemetry.OpenParquetStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		base := make([]telemetry.Metric, 1000)
		for series := range base {
			m := metricCounter(start, 0)
			m.ServiceName = fmt.Sprintf("service%d", series%20)
			m.Attributes = map[string]any{"series": int64(series), "nested": map[string]any{"enabled": true, "labels": []any{"a", "b"}}}
			m.Resource = map[string]any{"host.index": int64(series / 50), "container.index": int64(series)}
			if kind == "gauge" {
				m.Type, m.Temporality, m.Monotonic = "gauge", "", false
			}
			if kind == "histogram" {
				m.Type, m.Monotonic = "histogram", false
				m.HistBoundsJSON = "[10,20]"
			}
			m.SeriesID = telemetry.MetricSeriesID(m)
			base[series] = m
		}
		for minute := -10; minute < 60; minute++ {
			points := make([]telemetry.Metric, 0, 6000)
			for step := range 6 {
				at := start.Add(time.Duration(minute)*time.Minute + time.Duration(step)*10*time.Second)
				for series, m := range base {
					m.TimeUnixNanos, m.EventUnixNanos, m.IngestedAt = at.UnixNano(), at.UnixNano(), at.UnixNano()
					m.Value = float64((minute+10)*60 + step*10 + series%11)
					if kind == "histogram" {
						n := int64((minute+10)*6 + step + 1)
						m.HistCount, m.HistSum = 10*n, float64(100*n)
						m.HistCountsJSON = fmt.Sprintf("[%d,%d,%d]", 2*n, 3*n, 5*n)
					}
					points = append(points, m)
				}
			}
			if err := store.CommitBatch(t.Context(), telemetry.BatchMetadata{ID: fmt.Sprintf("minute%d", minute+10)}, nil, nil, points); err != nil {
				t.Fatal(err)
			}
		}
		pattern := "'" + strings.ReplaceAll(store.Pattern("metrics"), "'", "''") + "'"
		if _, err := db.Exec("CREATE OR REPLACE VIEW telemetry.metrics AS SELECT * FROM read_parquet(" + pattern + ",hive_partitioning=false,union_by_name=false)"); err != nil {
			t.Fatal(err)
		}
		if err := query.CreateViews(db); err != nil {
			t.Fatal(err)
		}
		var rows, series int64
		if err := db.QueryRow("SELECT count(*),count(DISTINCT series_id) FROM metrics").Scan(&rows, &series); err != nil || rows != 420000 || series != 1000 {
			t.Fatalf("fixture rows=%d series=%d err=%v", rows, series, err)
		}
		shapes := []struct {
			id, viz      string
			measures, by []string
			interval     time.Duration
		}{
			{"table_five", "table", []string{"avg(value)", "min(value)", "max(value)", "last(value)", "p95(value)"}, []string{"service"}, 0},
			{"timeseries_three", "timeseries", []string{"avg(value)", "min(value)", "max(value)"}, nil, time.Minute},
			{"sum_by_service", "timeseries", []string{"sum(value)"}, []string{"service"}, time.Minute},
			{"avg_by_service", "timeseries", []string{"avg(value)"}, []string{"service"}, time.Minute},
			{"stat_last", "stat", []string{"last(value)"}, nil, 0},
		}
		if kind == "counter" {
			shapes = []struct {
				id, viz      string
				measures, by []string
				interval     time.Duration
			}{
				{"rate_by_service", "timeseries", []string{"rate()"}, []string{"service"}, time.Minute},
				{"rate_by_service_10s", "timeseries", []string{"rate()"}, []string{"service"}, 10 * time.Second},
				{"sum_by_service", "timeseries", []string{"sum(value)"}, []string{"service"}, time.Minute},
				{"stat_rate", "stat", []string{"rate()"}, nil, 0},
			}
		}
		if kind == "histogram" {
			shapes = []struct {
				id, viz      string
				measures, by []string
				interval     time.Duration
			}{
				{"table_histogram_five", "table", []string{"rate()", "sum(value)", "avg(value)", "p50(value)", "p95(value)"}, []string{"service"}, 0},
				{"timeseries_histogram_three", "timeseries", []string{"rate()", "avg(value)", "p95(value)"}, nil, time.Minute},
				{"p95_by_service", "timeseries", []string{"p95(value)"}, []string{"service"}, time.Minute},
				{"stat_histogram_p95", "stat", []string{"p95(value)"}, nil, 0},
			}
		}
		for _, shape := range shapes {
			p := Panel{ID: shape.id, Title: shape.id, Viz: shape.viz, Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: shape.measures, By: shape.by}}
			if shape.interval > 0 {
				p.Query.Bucket = "1m"
				if shape.interval == 10*time.Second {
					p.Query.Bucket = "10s"
				}
			}
			d := Dashboard{Name: "Review timings", Time: Time{From: &start, To: &end}, Panels: []Panel{p}}
			Normalize(&d)
			checked, problems, err := Check(t.Context(), e.engine, &d)
			if err != nil || len(problems) > 0 {
				t.Fatalf("check: %v %v", err, problems)
			}
			p = d.Panels[0]
			scope := Scope{Start: start, End: end, Interval: shape.interval}
			if dir := os.Getenv("FANOUT_METRIC_PROFILE_DIR"); dir != "" {
				for _, stmt := range []string{"SET enable_profiling='json'", "SET profiling_output=" + sqlString(filepath.Join(t.TempDir(), "profile.json"))} {
					if _, err := db.Exec(stmt); err != nil {
						t.Fatal(err)
					}
				}
				compiled, err := compileQuery(&p, checked.Measures[p.ID], checked.Filters[p.ID], scope)
				if err != nil {
					t.Fatal(err)
				}
				var key, plan string
				if err := db.QueryRow("EXPLAIN (ANALYZE, FORMAT JSON) "+compiled.SQL, compiled.Args...).Scan(&key, &plan); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, kind+"-"+shape.id+".json"), []byte(plan), 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("PRAGMA disable_profiling"); err != nil {
					t.Fatal(err)
				}
			}
			for _, mode := range []string{"query", "executor"} {
				samples := []float64{}
				for trial := 0; trial < 8; trial++ {
					began := time.Now()
					if mode == "query" {
						f, _, err := e.runScope(t.Context(), &p, checked, scope)
						if err != nil || f == nil || f.Rows == 0 {
							t.Fatalf("%s: frame=%+v err=%v", shape.id, f, err)
						}
						if !metricFrameHasValue(f) {
							t.Fatalf("%s: no measure values", shape.id)
						}
					} else {
						results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
						if err != nil || len(results) != 1 || results[0].Status != StatusOK {
							t.Fatalf("%s: %+v %v", shape.id, results, err)
						}
					}
					elapsed := float64(time.Since(began)) / float64(time.Millisecond)
					if trial > 0 {
						samples = append(samples, elapsed)
					}
				}
				sort.Float64s(samples)
				t.Logf("shape=%s kind=%s mode=%s samples=%d p50_ms=%.3f p95_ms=%.3f", shape.id, kind, mode, len(samples), samples[len(samples)/2], samples[len(samples)-1])
			}
		}
	}
}
