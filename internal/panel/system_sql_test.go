package panel

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func systemMetric(at time.Time, name string, value float64, host string, attributes map[string]any) telemetry.Metric {
	m := metricCounter(at, value)
	m.Name = name
	m.Type = "gauge"
	m.Monotonic = false
	m.Temporality = ""
	m.Unit = "1"
	m.Attributes = attributes
	m.Resource = map[string]any{"host.name": host}
	return m
}
func systemRun(t *testing.T, e *Executor, start, end time.Time, namespace string) ([]Result, *Dashboard) {
	t.Helper()
	e.now = func() time.Time { return end }
	d, err := e.System(t.Context(), SystemRequest{Time: Time{From: &start, To: &end}, Namespace: namespace})
	if err != nil {
		t.Fatal(err)
	}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: *d})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status == StatusError {
			t.Fatalf("%s: %s", r.ID, r.Error)
		}
	}
	return results, d
}
func systemResult(t *testing.T, results []Result, id string) Result {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("missing result %s", id)
	return Result{}
}
func systemCell(t *testing.T, r Result, column string, row int) any {
	t.Helper()
	values := metricValues(t, r, column)
	if row >= len(values) {
		t.Fatalf("%s row %d: %v", column, row, values)
	}
	return values[row]
}
func systemNear(t *testing.T, got any, want float64) {
	t.Helper()
	v, ok := got.(float64)
	if !ok || math.Abs(v-want) > 1e-8 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestSystemCheckSilenceIsBadAfterTwoIntervals(t *testing.T) {
	e, db := metricFixture(t)
	at := fixtureStart
	attrs := map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}
	for _, offset := range []time.Duration{0, time.Minute, time.Minute} {
		insertMetrics(t, db, systemMetric(at.Add(offset), "httpcheck.status", 1, "outside", attrs))
	}
	for _, tc := range []struct {
		end     time.Time
		state   string
		failure float64
	}{{at.Add(time.Minute), "reporting interval unknown", 2}, {at.Add(3*time.Minute - time.Nanosecond), "reachable", 0}, {at.Add(3 * time.Minute), "no recent result", 2}, {at.Add(8 * time.Minute), "no recent result", 2}} {
		results, _ := systemRun(t, e, at, tc.end, "test")
		r := systemResult(t, results, "check_states")
		n := r.Frame.Rows - 1
		if systemCell(t, r, "state", n) != tc.state || systemCell(t, r, "failure", n) != tc.failure {
			t.Fatalf("end=%s frame=%+v", tc.end, r.Frame)
		}
		if !strings.Contains(systemCell(t, r, "endpoint_from", n).(string), "GET https://example.test from outside") {
			t.Fatal("identity label")
		}
	}
	// A changed cadence and retry keep the latest distinct reporting gap.
	insertMetrics(t, db, systemMetric(at.Add(4*time.Minute), "httpcheck.status", 1, "outside", attrs))
	results, _ := systemRun(t, e, at, at.Add(10*time.Minute-time.Nanosecond), "test")
	r := systemResult(t, results, "check_states")
	if systemCell(t, r, "state", r.Frame.Rows-1) != "reachable" {
		t.Fatal("new cadence")
	}
	results, _ = systemRun(t, e, at, at.Add(10*time.Minute), "test")
	r = systemResult(t, results, "check_states")
	if systemCell(t, r, "state", r.Frame.Rows-1) != "no recent result" {
		t.Fatal("cadence equality")
	}
	attrs = map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_code": "503"}
	insertMetrics(t, db, systemMetric(at.Add(11*time.Minute), "httpcheck.status", 1, "outside", attrs))
	results, _ = systemRun(t, e, at, at.Add(11*time.Minute+time.Second), "test")
	r = systemResult(t, results, "check_states")
	if systemCell(t, r, "state", r.Frame.Rows-1) != "down" {
		t.Fatal("resume with failure")
	}
}

func insertHostCapacity(t *testing.T, db *sql.DB, at time.Time, host string, percent float64) {
	t.Helper()
	for _, offset := range []time.Duration{-time.Minute, 0, time.Minute} {
		for _, state := range []string{"idle", "user"} {
			value := 100 + float64((offset+time.Minute)/time.Second)
			if state == "user" {
				value = 100 + 3*float64((offset+time.Minute)/time.Second)
			}
			m := systemMetric(at.Add(offset), "system.cpu.time", value, host, map[string]any{"state": state, "cpu": "0"})
			m.Type = "sum"
			m.Monotonic = true
			m.Temporality = "cumulative"
			m.Unit = "s"
			insertMetrics(t, db, m)
		}
	}
	for _, offset := range []time.Duration{0, time.Minute} {
		for _, state := range []string{"used", "free"} {
			v := percent
			if state == "free" {
				v = 100 - percent
			}
			m := systemMetric(at.Add(offset), "system.memory.usage", v, host, map[string]any{"state": state})
			m.Type = "sum"
			m.Unit = "By"
			m.Temporality = "cumulative"
			insertMetrics(t, db, m)
			m.Name = "system.filesystem.usage"
			m.Attributes = map[string]any{"state": state, "mountpoint": "/full", "device": "/dev/disk"}
			insertMetrics(t, db, m)
		}
	}
}
func TestSystemCapacityUsesRatesLatestValuesAndThresholds(t *testing.T) {
	e, db := metricFixture(t)
	at := fixtureStart
	for _, percent := range []float64{0, 79.99, 80, 89.99, 90, 91} {
		insertHostCapacity(t, db, at, fmt.Sprintf("host-%g", percent), percent)
	}
	results, d := systemRun(t, e, at, at.Add(2*time.Minute), "test")
	r := systemResult(t, results, "hosts")
	if r.Frame.Rows != 6 {
		t.Fatalf("rows=%d", r.Frame.Rows)
	}
	for i, want := range []float64{91, 90, 89.99, 80, 79.99, 0} {
		systemNear(t, systemCell(t, r, "memory_percent", i), want)
		systemNear(t, systemCell(t, r, "filesystem_percent", i), want)
		systemNear(t, systemCell(t, r, "cpu_percent", i), 75)
		if systemCell(t, r, "reporting", i) != "reporting" {
			t.Fatal("reporting")
		}
	}
	for _, p := range d.Panels {
		if p.ID == "hosts" && (!reflect.DeepEqual(p.Thresholds, []Threshold{{Value: 80, Status: "warn"}, {Value: 90, Status: "bad"}}) || p.Better != "lower") {
			t.Fatalf("thresholds=%+v", p)
		}
	}
	reference := runMetricPanel(t, e, Panel{Viz: "table", Query: &Query{From: "metrics", Where: []string{"name='system.cpu.time'", "resource['host.name']='host-91'"}, By: []string{"attributes['state']"}, Measures: []string{"rate()"}}}, at, at.Add(2*time.Minute))
	rates := map[string]float64{}
	for i, state := range metricValues(t, reference, "state") {
		rates[state.(string)] = metricValues(t, reference, "rate")[i].(float64)
	}
	systemNear(t, systemCell(t, r, "cpu_percent", 0), 100*rates["user"]/(rates["idle"]+rates["user"]))
	// Fullest mount uses last values, including reserved state, and never adds percentages.
	for _, state := range []string{"used", "reserved", "free"} {
		v := 5.0
		if state == "used" {
			v = 90
		}
		insertMetrics(t, db, systemMetric(at.Add(time.Minute), "system.filesystem.usage", v, "host-91", map[string]any{"state": state, "mountpoint": "/reserve", "device": "disk2"}))
	}
	results, _ = systemRun(t, e, at, at.Add(2*time.Minute), "test")
	r = systemResult(t, results, "hosts")
	systemNear(t, systemCell(t, r, "filesystem_percent", 0), 95)
	if systemCell(t, r, "mountpoint", 0) != "/reserve" {
		t.Fatal("mount")
	}
}

func TestSystemStoppedResourcesRemainVisible(t *testing.T) {
	e, db := metricFixture(t)
	at := fixtureStart
	insertHostCapacity(t, db, at, "stopped", 91)
	for _, offset := range []time.Duration{0, time.Minute} {
		m := systemMetric(at.Add(offset), "container.cpu.utilization", .5, "host", nil)
		m.Resource["container.id"] = "dead"
		m.Resource["container.name"] = "stopped-container"
		insertMetrics(t, db, m)
		m.Name = "container.memory.usage.total"
		m.Value = 100
		insertMetrics(t, db, m)
	}
	for _, tc := range []struct {
		end       time.Time
		reporting string
		numeric   bool
	}{{at.Add(3*time.Minute - time.Nanosecond), "reporting", true}, {at.Add(3 * time.Minute), "not reporting", true}, {at.Add(6 * time.Minute), "not reporting", true}, {at.Add(6*time.Minute + time.Nanosecond), "not reporting", false}, {at.Add(8 * time.Minute), "not reporting", false}} {
		results, _ := systemRun(t, e, at, tc.end, "test")
		for _, id := range []string{"hosts", "containers"} {
			r := systemResult(t, results, id)
			if r.Frame.Rows != 1 || systemCell(t, r, "reporting", 0) != tc.reporting {
				t.Fatalf("%s end=%s frame=%+v", id, tc.end, r.Frame)
			}
			if tc.numeric {
				if systemCell(t, r, "cpu_percent", 0) == nil {
					t.Fatal("premature numeric expiry")
				}
			} else if systemCell(t, r, "cpu_percent", 0) != nil {
				t.Fatal("expired CPU")
			}
			if tc.reporting == "not reporting" {
				systemNear(t, systemCell(t, r, "status", 0), 100)
			}
		}
	}
	results, _ := systemRun(t, e, at, at.Add(2*time.Minute), "test")
	r := systemResult(t, results, "containers")
	if systemCell(t, r, "memory_percent", 0) != nil || systemCell(t, r, "measurement", 0) != "memory limit not reported" {
		t.Fatalf("missing limit=%+v", r.Frame)
	}
}

func TestSystemVersionsReadAllSignals(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart
	spans := shopSpans()[:1]
	spans[0].Resource = map[string]any{"service.version": "v1", "process.runtime.name": "Go"}
	logs := []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", TimeUnixNanos: at.Add(time.Minute).UnixNano(), EventUnixNanos: at.Add(time.Minute).UnixNano(), Resource: map[string]any{"service.version": "v2", "process.runtime.version": "1.26"}}}
	m := systemMetric(at.Add(2*time.Minute), "version-only", 1, "host", nil)
	m.Namespace = "shop"
	m.ServiceName = "checkout"
	m.Resource["service.version"] = "v3"
	m.Resource["telemetry.sdk.language"] = "go"
	m.Resource["telemetry.sdk.version"] = "1"
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "system-versions", Spans: spans, Logs: logs, Metrics: metricSeriesIDs([]telemetry.Metric{m})}); err != nil {
		t.Fatal(err)
	}
	v2 := spans[0]
	v2.TraceID = "v2"
	v2.SpanID = "v2"
	v2.StartUnixNanos = at.Add(time.Minute).UnixNano()
	v2.EndUnixNanos = v2.StartUnixNanos + 100000000
	v2.Resource = logs[0].Resource
	v2.ServiceVersion = "v2"
	commit(t, repo, []telemetry.Span{v2}, nil)
	e := NewExecutor(engine, 30)
	e.SetRollupReader(observability.New(engine, engine, 30))
	results, d := systemRun(t, e, at, at.Add(3*time.Minute), "shop")
	r := systemResult(t, results, "versions")
	if !reflect.DeepEqual(metricValues(t, r, "version"), []any{"v3", "v2", "v1"}) {
		t.Fatalf("versions=%+v", r.Frame)
	}
	if systemCell(t, r, "first_seen", 0) != at.Add(2*time.Minute).UnixMilli() || systemCell(t, r, "sdk_language", 0) != "go" || d.Annotations.Deploys == nil || !*d.Annotations.Deploys {
		t.Fatal("version metadata")
	}
	if _, err := engine.DrainVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := annotations.New(engine)
	a, err := service.Read(t.Context(), annotations.Request{From: at, To: at.Add(3 * time.Minute), Namespace: "shop", Services: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, deploy := range a.Deploys {
		found = found || deploy.Version == "v2" && deploy.At.Equal(at.Add(time.Minute))
	}
	if !found {
		t.Fatalf("deploys=%+v", a)
	}
}

func TestSystemSQLReadsCounterLookbackFromSeparateParquetFiles(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart
	for i, offset := range []time.Duration{-time.Minute, 0, time.Minute} {
		var metrics []telemetry.Metric
		for _, state := range []string{"idle", "user"} {
			m := systemMetric(at.Add(offset), "system.cpu.time", 100+float64(i)*60, "host", map[string]any{"state": state})
			if state == "user" {
				m.Value = 100 + float64(i)*180
			}
			m.Type = "sum"
			m.Monotonic = true
			m.Temporality = "cumulative"
			metrics = append(metrics, m)
		}
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("system-lookback-%d", i), Metrics: metricSeriesIDs(metrics)}); err != nil {
			t.Fatal(err)
		}
	}
	e := NewExecutor(engine, 30)
	results, _ := systemRun(t, e, at, at.Add(2*time.Minute), "test")
	r := systemResult(t, results, "hosts")
	systemNear(t, systemCell(t, r, "cpu_percent", 0), 75)
	// Ordinary public SQL can request an earlier range alongside its selected window.
	r = runMetricPanel(t, e, Panel{Viz: "table", SQL: `SELECT count(*)::DOUBLE AS points FROM metrics WHERE ($__window(time) OR time < $__from::TIMESTAMP_NS::TIMESTAMPTZ_NS) AND name='system.cpu.time'`}, at, at.Add(2*time.Minute))
	systemNear(t, systemCell(t, r, "points", 0), 6)
}

func TestSystemDurationUsesLatestValuesAndHistogramPercentiles(t *testing.T) {
	for _, kind := range []string{"gauge", "updown", "histogram"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			at := fixtureStart
			attrs := map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}
			for i := range 3 {
				m := systemMetric(at.Add(time.Duration(i-1)*time.Minute), "httpcheck.duration", float64(i+1), "outside", attrs)
				m.Unit = "s"
				if kind == "updown" {
					m.Type = "sum"
					m.Temporality = "cumulative"
				}
				if kind == "histogram" {
					m.Type = "histogram"
					m.Temporality = "cumulative"
					m.HistBoundsJSON = "[1,2]"
					m.HistCountsJSON = fmt.Sprintf("[%d,%d,0]", i*5, i*5)
					m.HistCount = int64(i * 10)
					m.HistSum = float64(i * 15)
				}
				insertMetrics(t, db, m)
			}
			results, _ := systemRun(t, e, at, at.Add(2*time.Minute), "test")
			r := systemResult(t, results, "check_latency")
			if kind == "histogram" {
				for _, v := range metricValues(t, r, "latency") {
					systemNear(t, v, 1900)
				}
				ref := runMetricPanel(t, e, Panel{Viz: "timeseries", Query: &Query{From: "metrics", Where: []string{"name='httpcheck.duration'"}, Measures: []string{"p95(value)"}, Bucket: "1m"}}, at, at.Add(2*time.Minute))
				for _, v := range metricValues(t, ref, "p95") {
					if v != nil {
						systemNear(t, v, 1.9)
					}
				}
			} else {
				if !reflect.DeepEqual(metricValues(t, r, "latency"), []any{3000.0, 2000.0}) {
					t.Fatalf("latency=%+v", r.Frame)
				}
			}
		})
	}
}

func TestSystemCPUUsesResetDeltaRetriesAndMixedCadences(t *testing.T) {
	e, db := metricFixture(t)
	at := fixtureStart
	for _, state := range []string{"idle", "user"} {
		for i := range 4 {
			value := float64(100 + i*60)
			if state == "user" {
				value = float64(100 + i*180)
			}
			if i >= 2 {
				value -= 200
			}
			m := systemMetric(at.Add(time.Duration(i-1)*time.Minute), "system.cpu.time", value, "reset", map[string]any{"state": state})
			m.Type = "sum"
			m.Monotonic = true
			m.Temporality = "cumulative"
			if i >= 2 {
				m.StartTimeUnixNanos = at.Add(time.Minute).UnixNano()
				if value < 0 {
					m.Value = 20
				}
			}
			insertMetrics(t, db, m)
			m.Value = 999999
			m.IngestedAt--
			insertMetrics(t, db, m)
		}
	}
	// Independent delta CPU with a different reporting interval.
	for _, state := range []string{"idle", "user"} {
		for i := range 2 {
			m := systemMetric(at.Add(time.Duration(i)*2*time.Minute), "system.cpu.time", 60, "delta", map[string]any{"state": state})
			if state == "user" {
				m.Value = 180
			}
			m.Type = "sum"
			m.Monotonic = true
			m.Temporality = "delta"
			m.StartTimeUnixNanos = at.Add(time.Duration(i-1) * 2 * time.Minute).UnixNano()
			insertMetrics(t, db, m)
		}
	}
	results, _ := systemRun(t, e, at, at.Add(3*time.Minute), "test")
	r := systemResult(t, results, "hosts")
	for row, host := range metricValues(t, r, "host") {
		ref := runMetricPanel(t, e, Panel{Viz: "table", Query: &Query{From: "metrics", Where: []string{"name='system.cpu.time'", "resource['host.name']=" + sqlString(host.(string))}, By: []string{"attributes['state']"}, Measures: []string{"rate()"}}}, at, at.Add(3*time.Minute))
		rates := map[string]float64{}
		for i, state := range metricValues(t, ref, "state") {
			rates[state.(string)] = metricValues(t, ref, "rate")[i].(float64)
		}
		systemNear(t, systemCell(t, r, "cpu_percent", row), 100*rates["user"]/(rates["idle"]+rates["user"]))
	}
}

func TestSystemSQLTimelineValidatesShapesAndKeepsNewestTrouble(t *testing.T) {
	e, _ := metricFixture(t)
	at := fixtureStart
	for _, projection := range []string{"time::VARCHAR AS time,'item' AS item,1::DOUBLE AS failure", "time,'item' AS item,'bad' AS failure", "time,1 AS item,1 AS failure", "time,'item' AS item,1 AS failure,2 AS extra"} {
		d := Dashboard{Name: "Shape", Panels: []Panel{{ID: "state", Title: "State", Viz: "state_timeline", SQL: "SELECT " + projection + " FROM metrics WHERE $__window(time)", Thresholds: []Threshold{{Value: 1, Status: "bad"}}}}}
		if err := e.Validate(t.Context(), &d); err == nil {
			t.Fatalf("accepted %s", projection)
		}
	}
	p := Panel{Viz: "state_timeline", Thresholds: []Threshold{{Value: 1, Status: "bad"}}, SQL: `SELECT make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)+i*60000000000)::TIMESTAMPTZ_NS AS time,'endpoint' AS endpoint_from,2::DOUBLE AS failure,'no recent result' AS state FROM generate_series(0,1499) t(i) WHERE $__window(time) ORDER BY time DESC`}
	r := runMetricPanel(t, e, p, at, at.Add(25*time.Hour))
	if !r.Frame.Truncated || r.Frame.Rows > maxSeriesPoints || r.Interval != "1m" || systemCell(t, r, "time", r.Frame.Rows-1) != at.Add(1499*time.Minute).UnixMilli() || systemCell(t, r, "state", r.Frame.Rows-1) != "no recent result" {
		t.Fatalf("timeline=%+v", r)
	}
}

func TestSystemKeepsCheckIdentitiesAndResourceIdentitiesSeparate(t *testing.T) {
	e, db := metricFixture(t)
	at := fixtureStart
	for _, namespace := range []string{"test", "other"} {
		for _, host := range []string{"a", "b"} {
			for _, method := range []string{"GET", "HEAD"} {
				for i := range 2 {
					m := systemMetric(at.Add(time.Duration(i)*time.Minute), "httpcheck.status", 1, host, map[string]any{"http.url": "https://example.test", "http.method": method, "http.status_class": "2xx"})
					m.Namespace = namespace
					insertMetrics(t, db, m)
				}
			}
			for _, id := range []string{"one", "two"} {
				for i := range 2 {
					m := systemMetric(at.Add(time.Duration(i)*time.Minute), "container.cpu.utilization", .5, host, nil)
					m.Namespace = namespace
					m.Resource["container.id"] = id
					m.Resource["container.name"] = "same-label"
					insertMetrics(t, db, m)
				}
			}
		}
	}
	results, _ := systemRun(t, e, at, at.Add(2*time.Minute), "")
	checks := systemResult(t, results, "check_states")
	labels := map[any]bool{}
	for _, v := range metricValues(t, checks, "endpoint_from") {
		labels[v] = true
	}
	if len(labels) != 8 || systemResult(t, results, "containers").Frame.Rows != 8 {
		t.Fatal("identities merged")
	}
	results, _ = systemRun(t, e, at, at.Add(2*time.Minute), "test")
	if systemResult(t, results, "containers").Frame.Rows != 4 {
		t.Fatal("namespace leaked")
	}
	for i := range 2 {
		m := systemMetric(at.Add(time.Duration(i)*time.Minute), "system.memory.usage", 1, "", nil)
		insertMetrics(t, db, m)
	}
	results, d := systemRun(t, e, at, at.Add(2*time.Minute), "test")
	if systemResult(t, results, "hosts").Frame.Rows != 0 {
		t.Fatal("anonymous hosts merged")
	}
	found := false
	for _, p := range d.Panels {
		found = found || p.ID == "capacity_identity"
	}
	if !found {
		t.Fatal("identity explanation missing")
	}
}

func TestSystemAllSectionsUseExistingPanelsAndScopedAnnotations(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart
	spans := shopSpans()
	for i := range spans {
		spans[i].Resource = map[string]any{"host.name": "inside", "service.version": "v1"}
	}
	client := spans[0]
	client.TraceID = "external"
	client.SpanID = "external"
	client.Kind = "SPAN_KIND_CLIENT"
	client.StatusCode = "STATUS_CODE_ERROR"
	client.PeerService = "outside.example"
	client.Attributes = map[string]any{"server.address": "outside.example", "server.port": int64(443), "http.request.method": "GET"}
	spans = append(spans, client)
	var metrics []telemetry.Metric
	for i := range 2 {
		m := systemMetric(at.Add(time.Duration(i)*time.Minute), "httpcheck.status", 1, "inside", map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"})
		metrics = append(metrics, m)
		m.Name = "httpcheck.duration"
		m.Unit = "ms"
		metrics = append(metrics, m)
		m.Name = "system.memory.usage"
		m.Attributes = map[string]any{"state": "used"}
		metrics = append(metrics, m)
		m.Name = "container.cpu.utilization"
		m.Unit = "1"
		m.Attributes = nil
		m.Resource = map[string]any{"host.name": "inside", "container.id": "id", "container.name": "container"}
		metrics = append(metrics, m)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "all-system", Spans: spans, Metrics: metricSeriesIDs(metrics)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { engine.RunRollups(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		err := engine.DB.QueryRow("SELECT count(*) FROM service_rollup").Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rollup publication timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e := NewExecutor(engine, 30)
	e.SetRollupReader(observability.New(engine, engine, 30))
	results, d := systemRun(t, e, at, at.Add(time.Hour), "shop")
	topology := systemResult(t, results, "service_map")
	badPeer, badEdge := false, false
	for row, kind := range metricValues(t, topology, "kind") {
		if kind == "node" && systemCell(t, topology, "service", row) == "outside.example" {
			badPeer = systemCell(t, topology, "health", row) == "unhealthy"
		}
		if kind == "edge" && systemCell(t, topology, "callee", row) == "outside.example" {
			badEdge = systemCell(t, topology, "error_rate", row) == 100.0
		}
	}
	if !badPeer || !badEdge {
		t.Fatalf("external dependency missing: %+v", topology.Frame)
	}
	// Metrics are in test, so this scoped spec includes service/version sections only.
	if d.Panels[1].ID != "service_health" || d.Panels[2].ID != "service_map" {
		t.Fatalf("order=%+v", d.Panels)
	}
	health := systemResult(t, results, "service_health")
	if health.AnnotationScope == nil || !health.AnnotationScope.NamespaceScoped || len(health.Frame.Health.ErrorTrend) != len(health.Frame.Health.ErrorTrendTimes) {
		t.Fatalf("health=%+v", health)
	}
	_, d = systemRun(t, e, at, at.Add(time.Hour), "")
	want := []string{"check_states", "check_latency", "service_health", "service_map", "hosts", "containers", "versions"}
	var ids []string
	for _, p := range d.Panels {
		if p.ID != "check_vantage" {
			ids = append(ids, p.ID)
		}
		if p.SQL != "" && len(p.SQL) > 8000 {
			t.Fatal("SQL limit")
		}
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("panels=%v", ids)
	}
	// Same host in another namespace must not establish a local vantage.
	for _, p := range d.Panels {
		if p.ID == "check_vantage" {
			t.Fatal("cross-namespace local warning")
		}
	}
}

func TestSystemVantageWarningUsesObservedHostsInSameNamespace(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart
	spans := shopSpans()[:1]
	spans[0].Resource = map[string]any{"host.name": "inside"}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.SetRollupReader(observability.New(engine, engine, 30))
	e.now = func() time.Time { return at.Add(time.Hour) }
	attrs := map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}
	add := func(host, namespace, id string) {
		m := systemMetric(at, "httpcheck.status", 1, host, attrs)
		m.Namespace = namespace
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Metrics: metricSeriesIDs([]telemetry.Metric{m})}); err != nil {
			t.Fatal(err)
		}
	}
	warning := func(namespace string) string {
		d, err := e.System(t.Context(), SystemRequest{Time: Time{Range: "1h"}, Namespace: namespace})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range d.Panels {
			if p.ID == "check_vantage" {
				return p.Content
			}
		}
		return ""
	}
	add("inside", "shop", "local-check")
	if !strings.Contains(warning("shop"), "Every check runs on a host") {
		t.Fatal("local warning missing")
	}
	add("outside", "shop", "outside-check")
	if warning("shop") != "" {
		t.Fatal("outside vantage did not remove local warning")
	}
	add("", "shop", "unknown-check")
	if !strings.Contains(warning("shop"), "vantage is unknown") {
		t.Fatal("unknown warning missing")
	}
}
