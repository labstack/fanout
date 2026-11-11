package engine

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
	telemetrystore "github.com/labstack/pitot/internal/telemetry/store"
)

func TestReceiverSummariesRepairRetriesLateDataAndRetirement(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-48 * time.Hour)
	commit := func(id string, seconds []int, values []float64) {
		t.Helper()
		var points []telemetry.Metric
		for i, sec := range seconds {
			m := telemetry.Metric{Namespace: "ns", ServiceName: "svc", Name: "system.cpu.time", Type: "counter", Temporality: "cumulative", Unit: "s", TimeUnixNanos: at.Add(time.Duration(sec) * time.Second).UnixNano(), Value: values[i], Resource: map[string]any{"host.name": "host", "service.version": "1"}, Attributes: map[string]any{"state": "user", "cpu": "0", "unbounded.label": fmt.Sprint(i)}}
			m.SeriesID = telemetry.MetricSeriesID(m)
			points = append(points, m)
		}
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Metrics: points}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(increase float64) {
		t.Helper()
		if _, err := d.RefreshDashboardRollups(t.Context()); err != nil {
			t.Fatal(err)
		}
		var rows, first, last, previous int
		var got, seconds float64
		if err := d.DB.QueryRow(`SELECT count(*),min(first_point.value),max(last_point.value),max(previous_point.value),sum(counter_increase),sum(counter_seconds) FROM receiver_rollup`).Scan(&rows, &first, &last, &previous, &got, &seconds); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || first != 100 || last != 30 || previous != 20 || got != increase || seconds != 50 {
			t.Fatalf("rows=%d first=%d last=%d previous=%d increase=%v seconds=%v", rows, first, last, previous, got, seconds)
		}
		requireRowCount(t, d.DB, `SELECT count(*) FROM receiver_points WHERE name=?`, "system.cpu.time", 3)
	}
	// More than three points, a reset in a discarded interior witness, and
	// arbitrary labels: one summary must retain the complete counter increase.
	commit("first", []int{0, 10, 20, 30, 40, 50}, []float64{100, 110, 5, 15, 20, 30})
	check(40)
	// A retry corrects the interior point to 120. Recompute the affected minute
	// rather than incrementing the old contribution (which would double count).
	commit("retry", []int{10}, []float64{120})
	check(50)
	for i := range 6 {
		commit(fmt.Sprintf("extra-%d", i), []int{50}, []float64{30})
	}
	// Compact actual nonempty contributions; analytical rows remain unchanged.
	if _, err := repo.CompactParquet(t.Context(), d, 2); err != nil {
		t.Fatal(err)
	}
	check(50)
	d.cfg.RetentionDays = 1
	if err := d.runRepositoryMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireRowCount(t, d.DB, `SELECT count(*) FROM receiver_rollup WHERE name=?`, "system.cpu.time", 0)
	requireRowCount(t, d.DB, `SELECT count(*) FROM resource_rollup WHERE service=?`, "svc", 0)
	requireRowCount(t, d.DB, `SELECT count(*) FROM receiver_entity_rollup WHERE service=?`, "svc", 0)
}

func TestReceiverSummariesPreserveFiniteValuesAcrossNonfiniteScrapes(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	var points []telemetry.Metric
	for _, name := range []string{"system.cpu.time", "container.memory.usage.total"} {
		for i, value := range []float64{100, math.NaN(), 110, 5, math.NaN(), math.NaN()} {
			m := telemetry.Metric{Namespace: "ns", ServiceName: "svc", Name: name, Type: "gauge", TimeUnixNanos: at.Add(time.Duration(i) * 10 * time.Second).UnixNano(), Value: value, Resource: map[string]any{"host.name": "host", "container.id": "container"}}
			if name == "system.cpu.time" {
				m.Type = "counter"
				m.Temporality = "cumulative"
			}
			m.SeriesID = telemetry.MetricSeriesID(m)
			points = append(points, m)
		}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "nonfinite", Metrics: points}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshDashboardRollups(t.Context()); err != nil {
		t.Fatal(err)
	}
	var increase, seconds, value float64
	if err := d.DB.QueryRow(`SELECT counter_increase,counter_seconds FROM receiver_rollup WHERE name='system.cpu.time'`).Scan(&increase, &seconds); err != nil {
		t.Fatal(err)
	}
	if increase != 15 || seconds != 30 {
		t.Fatalf("increase=%v seconds=%v", increase, seconds)
	}
	if err := d.DB.QueryRow(`SELECT arg_max(value,time) FROM receiver_points WHERE name='container.memory.usage.total' AND isfinite(value)`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 5 {
		t.Fatalf("latest finite gauge=%v", value)
	}
}

func TestReceiverSummaryCoverageSharesItsReadSnapshot(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "coverage", Metrics: []telemetry.Metric{{Namespace: "ns", ServiceName: "svc", Name: "httpcheck.status", Type: "gauge", TimeUnixNanos: at.UnixNano(), Value: 1}}}); err != nil {
		t.Fatal(err)
	}
	read := func() time.Time {
		t.Helper()
		ctx, c := queryrows.WithCoverage(queryrows.WithWindow(t.Context(), queryrows.Window{Start: at, End: at.Add(time.Minute)}))
		var n int
		if err := d.QueryRowScan(ctx, []any{&n}, `SELECT count(*) FROM receiver_points`); err != nil {
			t.Fatal(err)
		}
		return c.FreshTo()
	}
	cold := read()
	oldest := repo.Parquet.BatchMetadata()[0].MinIngestedNanos
	if cold.IsZero() || cold.UnixNano() >= oldest {
		t.Fatalf("cold coverage=%v oldest=%d", cold, oldest)
	}
	if _, err := d.RefreshDashboardRollups(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh, err := d.RollupWatermark(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := read(); !got.Equal(fresh) {
		t.Fatalf("coverage=%v horizon=%v", got, fresh)
	}
}

func TestReceiverEntityMinutesCollapseCPUsAndRepairCheckState(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	var points []telemetry.Metric
	add := func(name, kind string, sec int, value float64, attrs map[string]any) {
		m := telemetry.Metric{Namespace: "ns", ServiceName: "svc", Name: name, Type: kind, Temporality: "cumulative", TimeUnixNanos: at.Add(time.Duration(sec) * time.Second).UnixNano(), Value: value, Resource: map[string]any{"host.name": "host"}, Attributes: attrs}
		m.SeriesID = telemetry.MetricSeriesID(m)
		points = append(points, m)
	}
	for cpu := range 8 {
		for _, state := range []string{"idle", "user"} {
			for sec := 0; sec < 60; sec += 10 {
				add("system.cpu.time", "counter", sec, float64(sec), map[string]any{"cpu": cpu, "state": state})
			}
		}
	}
	attrs := map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}
	for sec := 0; sec < 60; sec += 10 {
		add("httpcheck.status", "gauge", sec, 1, attrs)
	}
	add("httpcheck.status", "counter", 50, 1, attrs)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "entities", Metrics: points}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshDashboardRollups(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireRowCount(t, d.DB, `SELECT count(*) FROM receiver_rollup WHERE name=?`, "system.cpu.time", 16)
	requireRowCount(t, d.DB, `SELECT count(*) FROM receiver_entity_rollup WHERE receiver=?`, "host", 1)
	var first, previous, last int64
	var flag int
	read := func() {
		t.Helper()
		if err := d.DB.QueryRow(`SELECT epoch_ns(first_observation.time),epoch_ns(previous_observation.time),epoch_ns(last_observation.time),last_observation.flag FROM receiver_entity_rollup WHERE receiver='check'`).Scan(&first, &previous, &last, &flag); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if first != at.UnixNano() || previous != at.Add(40*time.Second).UnixNano() || last != at.Add(50*time.Second).UnixNano() || flag != 2 {
		t.Fatalf("first=%d previous=%d last=%d flag=%d", first, previous, last, flag)
	}
	points = nil
	add("httpcheck.status", "gauge", 55, 1, attrs)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "late-check", Metrics: points}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshDashboardRollups(t.Context()); err != nil {
		t.Fatal(err)
	}
	read()
	if previous != at.Add(50*time.Second).UnixNano() || last != at.Add(55*time.Second).UnixNano() || flag != 1 {
		t.Fatalf("late previous=%d last=%d flag=%d", previous, last, flag)
	}
}

func TestReceiverSummariesUseConvertedResourceKeys(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	m := telemetry.Metric{Namespace: "ns", ServiceName: "svc", Name: "container.memory.usage.total", Type: "gauge", TimeUnixNanos: at.UnixNano(), Value: 1, Resource: map[string]any{"host.name": "host", "container.id": []any{"unsupported"}, "service.version": []any{"unsupported"}}}
	m.SeriesID = telemetry.MetricSeriesID(m)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "converted", Metrics: []telemetry.Metric{m}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshDashboardRollups(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireRowCount(t, d.DB, "SELECT count(*) FROM receiver_rollup WHERE container_id IS NULL AND resource['container.id'] IS NULL AND service=?", "svc", 1)
	requireRowCount(t, d.DB, "SELECT count(*) FROM resource_rollup WHERE resource['service.version'] IS NULL AND service=?", "svc", 1)
	requireRowCount(t, d.DB, "SELECT count(*) FROM metrics WHERE resource['container.id'] IS NOT NULL AND _r_container_id IS NULL AND _r_service_version IS NULL AND service=?", "svc", 1)
}

// Unrelated resource keys may split the input aggregation but must not split
// the projected inventory. Dedicated semconv columns remain authoritative.
func TestResourceSummariesCollapseUnrelatedKeysAndKeepExactBounds(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	points := []telemetry.Metric{}
	for i := range 6 {
		version := any(int64(42))
		if i%2 == 0 {
			version = "42"
		}
		points = append(points, telemetry.Metric{Namespace: "ns", ServiceName: "svc", Name: "custom", Type: "gauge", TimeUnixNanos: at.Add(time.Duration(i)*time.Second + time.Nanosecond).UnixNano(), Resource: map[string]any{"host.name": "host", "service.version": version, "unrelated": i}})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "resources", Metrics: points}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshDashboardRollups(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	var first, last int64
	if err := d.DB.QueryRow(`SELECT count(*),epoch_ns(min(first_seen)),epoch_ns(max(last_seen)) FROM resource_rollup WHERE signal='metrics' AND resource['service.version']::VARCHAR='42'`).Scan(&n, &first, &last); err != nil {
		t.Fatal(err)
	}
	if n != 1 || first != at.Add(time.Nanosecond).UnixNano() || last != at.Add(5*time.Second+time.Nanosecond).UnixNano() {
		t.Fatalf("rows=%d bounds=%d,%d", n, first, last)
	}
}
