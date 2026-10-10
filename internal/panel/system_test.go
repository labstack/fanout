package panel

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSystemSectionsFollowAvailableTelemetry(t *testing.T) {
	e, db := metricFixture(t)
	d, err := e.System(t.Context(), SystemRequest{Time: Time{Range: "1h"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(d *Dashboard) []string {
		var out []string
		for _, p := range d.Panels {
			out = append(out, p.ID)
		}
		return out
	}
	if !reflect.DeepEqual(ids(d), []string{"reachable_missing", "services_missing", "capacity_missing", "versions_missing"}) {
		t.Fatalf("panels=%v", ids(d))
	}
	for _, p := range d.Panels {
		if p.Viz != "text" || !strings.Contains(p.Content, systemSetupURL) {
			t.Fatalf("missing=%+v", p)
		}
	}
	m := metricCounter(fixtureStart.Add(59*time.Minute), 1)
	m.Name = "system.memory.usage"
	m.Type = "sum"
	m.Monotonic = false
	m.Resource = map[string]any{"host.name": "host"}
	m.Attributes = map[string]any{"state": "used"}
	insertMetrics(t, db, m)
	d, err = e.System(t.Context(), SystemRequest{Time: Time{Range: "1h"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(d), []string{"reachable_missing", "services_missing", "hosts", "containers_missing", "versions_missing"}) {
		t.Fatalf("panels=%v", ids(d))
	}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: *d})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status == StatusError {
			t.Fatalf("execution=%+v", r)
		}
	}
}

func TestSystemDiscoveryUsesSelectedWindowAndNamespace(t *testing.T) {
	e, db := metricFixture(t)
	start, end := fixtureStart, fixtureStart.Add(time.Hour)
	for _, at := range []time.Time{start.Add(-time.Nanosecond), end} {
		m := metricCounter(at, 1)
		m.Name = "httpcheck.status"
		insertMetrics(t, db, m)
	}
	d, err := e.System(t.Context(), SystemRequest{Time: Time{From: &start, To: &end}, Namespace: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Panels[0].Viz != "text" {
		t.Fatalf("exclusive discovery=%+v", d.Panels)
	}
	m := metricCounter(start, 1)
	m.Name = "httpcheck.status"
	m.Type = "gauge"
	m.Resource = map[string]any{"host.name": "outside"}
	m.Attributes = map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}
	insertMetrics(t, db, m)
	d, err = e.System(t.Context(), SystemRequest{Time: Time{From: &start, To: &end}, Namespace: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Panels[0].Viz != "text" {
		t.Fatal("namespace leaked")
	}
	d, err = e.System(t.Context(), SystemRequest{Time: Time{Range: "1h"}, Namespace: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Panels[0].Viz != "state_timeline" || !d.Time.From.Equal(start) || !d.Time.To.Equal(end) || d.Time.Refresh != "30s" {
		t.Fatalf("dashboard=%+v", d)
	}
}

func TestSystemDiscoverySeesNewTelemetryOnNextRefresh(t *testing.T) {
	e, db := metricFixture(t)
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.System(t.Context(), SystemRequest{Time: Time{Range: "1h"}}); err != nil {
		t.Fatal(err)
	}
	m := metricCounter(fixtureStart.Add(time.Minute), 1)
	m.Name = "container.cpu.utilization"
	m.Type = "gauge"
	m.Unit = "1"
	m.Resource = map[string]any{"host.name": "host", "container.id": "id", "container.name": "container"}
	insertMetrics(t, db, m)
	d, err := e.System(t.Context(), SystemRequest{Time: Time{Range: "1h"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range d.Panels {
		found = found || p.ID == "containers"
	}
	if !found {
		t.Fatalf("fresh discovery=%+v", d.Panels)
	}
}

func TestSystemExactDiscoveryFindsMetricsBeyondPublicSchemaCap(t *testing.T) {
	e, db := metricFixture(t)
	if _, err := db.Exec(`INSERT INTO telemetry.metrics (series_id,namespace,service,name,unit,metric_type,metric_time,time_unix_nano,value,attributes,resource,ingested_unix_nano)
 SELECT i::UBIGINT,'test','svc','common-'||i,'1','gauge',?::TIMESTAMP_NS,?,1,'{}'::JSON::VARIANT,'{}'::JSON::VARIANT,? FROM generate_series(1,301) t(i) CROSS JOIN generate_series(1,2) t2(j)`, fixtureStart, fixtureStart.UnixNano(), fixtureStart.UnixNano()); err != nil {
		t.Fatal(err)
	}
	m := metricCounter(fixtureStart, 1)
	m.Name = "system.memory.usage"
	m.Type = "gauge"
	m.Resource = map[string]any{"host.name": "host", "service.version": "v1"}
	insertMetrics(t, db, m)
	schema, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range schema.Signals["metrics"].Metrics {
		if m.Name == "system.memory.usage" {
			t.Fatal("fixture must put fixed metric beyond cap")
		}
	}
	d, err := e.System(t.Context(), SystemRequest{Time: Time{Range: "1h"}, Namespace: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Panels[2].ID != "hosts" || d.Panels[len(d.Panels)-1].ID != "versions" {
		t.Fatalf("exact presence=%+v", d.Panels)
	}
}

func TestSystemVersionsAppearWithoutTraceTelemetry(t *testing.T) {
	for _, signal := range []string{"logs", "metrics"} {
		t.Run(signal, func(t *testing.T) {
			e, db := metricFixture(t)
			at := fixtureStart
			if signal == "metrics" {
				m := metricCounter(at, 1)
				m.Resource = map[string]any{"service.version": "v2"}
				insertMetrics(t, db, m)
			} else {
				if _, err := db.Exec(`INSERT INTO telemetry.logs(namespace,service,log_time,time_unix_nano,resource,attributes) VALUES ('test','svc',?::TIMESTAMP_NS,?,'{"service.version":"v2"}'::JSON::VARIANT,'{}'::JSON::VARIANT)`, at, at.UnixNano()); err != nil {
					t.Fatal(err)
				}
			}
			results, d := systemRun(t, e, at, at.Add(time.Hour), "test")
			if len(d.Panels) != 4 || d.Panels[3].ID != "versions" || systemCell(t, systemResult(t, results, "versions"), "version", 0) != "v2" {
				t.Fatalf("%s=%+v", signal, d.Panels)
			}
		})
	}
}
