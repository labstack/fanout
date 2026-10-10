package panel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMetricSchemaExposesKindsAndTemporality(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		name, kind, temp string
		mono             bool
	}{
		{"network", "sum", "cumulative", true}, {"network", "sum", "delta", true},
		{"network", "gauge", "", false}, {"memory", "sum", "cumulative", false}, {"temperature", "gauge", "", false},
		{"latency", "exponential_histogram", "delta", false}, {"summary", "summary", "", false},
	} {
		m := metricCounter(fixtureStart, 1)
		m.Name, m.Type, m.Temporality, m.Monotonic = p.name, p.kind, p.temp, p.mono
		insertMetrics(t, db, m)
	}
	// Unit, window and namespace are independent discovery boundaries.
	otherUnit := metricCounter(fixtureStart, 1)
	otherUnit.Name, otherUnit.Unit = "network", "s"
	insertMetrics(t, db, otherUnit)
	insertMetrics(t, db, otherUnit)
	outside := metricCounter(fixtureStart.Add(-time.Nanosecond), 1)
	outside.Name = "outside_window"
	insertMetrics(t, db, outside)
	outside.TimeUnixNanos = fixtureStart.Add(time.Hour).UnixNano()
	insertMetrics(t, db, outside)
	outside = metricCounter(fixtureStart, 1)
	outside.Name, outside.Namespace = "outside_namespace", "other"
	insertMetrics(t, db, outside)
	schema, err := e.Schema(t.Context(), SchemaRequest{Window: "1h", Namespace: "test"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	if len(schema.Signals["metrics"].Metrics) != 8 {
		t.Fatalf("metrics=%+v", schema.Signals["metrics"].Metrics)
	}
	first := schema.Signals["metrics"].Metrics[0]
	if first.Name != "network" || first.Unit != "s" || first.Type != "counter" || first.Temporality != "cumulative" || first.Count != 2 {
		t.Fatalf("unit groups or count ordering lost: %+v", first)
	}
	for _, m := range schema.Signals["metrics"].Metrics {
		seen[m.Name+"/"+m.Type+"/"+m.Temporality] = true
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"type":`) {
			t.Fatalf("type missing: %s", raw)
		}
		if strings.Contains(string(raw), `"kind":`) {
			t.Fatalf("unexpected kind field: %s", raw)
		}
		if m.Temporality == "" && strings.Contains(string(raw), `"temporality":`) {
			t.Fatalf("empty temporality: %s", raw)
		}
		if m.Temporality != "" && !strings.Contains(string(raw), `"temporality":"`+m.Temporality+`"`) {
			t.Fatalf("temporality missing: %s", raw)
		}
	}
	for _, key := range []string{"network/counter/cumulative", "network/counter/delta", "network/gauge/", "memory/updown/cumulative", "temperature/gauge/", "latency/histogram/delta", "summary/summary/"} {
		if !seen[key] {
			t.Fatalf("missing %s: %v", key, seen)
		}
	}
	fields := map[string]ColumnSchema{}
	for _, f := range schema.Signals["metrics"].Columns {
		fields[f.Name] = f
	}
	if fields["start_time"].Type != TypeTime || fields["temporality"].Type != TypeString {
		t.Fatalf("columns=%v", fields)
	}
	if _, ok := fields["series_id"]; ok {
		t.Fatal("internal series identity should not be advertised to agents")
	}
	values := fields["temporality"].Values
	if len(values) != 2 || values[0].Value != "cumulative" || values[0].Count != 4 || values[1].Value != "delta" || values[1].Count != 2 {
		t.Fatalf("temporality values=%+v", values)
	}
	for _, kind := range []string{"gauge", "counter", "updown", "histogram", "summary"} {
		if !strings.Contains(fields["type"].Description, kind) {
			t.Fatalf("type description=%s", fields["type"].Description)
		}
	}
}
