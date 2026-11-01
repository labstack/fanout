package query

import (
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

// A metric panel's value columns carry the unit the metric reports, mapped
// to the panel units; counts and rates keep their own.
func TestMetricPanelUsesReportedUnit(t *testing.T) {
	e, db := metricFixture(t)
	at := fixtureStart.Add(30 * time.Minute).UnixNano()
	for _, m := range []telemetry.Metric{
		{ServiceName: "ad", Name: "jvm.memory.used", Unit: "By", Type: "gauge", TimeUnixNanos: at, Value: 672.4e6, IngestedAt: at},
		{ServiceName: "ad", Name: "http.server.request.duration", Unit: "s", Type: "gauge", TimeUnixNanos: at, Value: 0.2, IngestedAt: at},
		{ServiceName: "ad", Name: "jvm.thread.count", Unit: "{thread}", Type: "gauge", TimeUnixNanos: at, Value: 12, IngestedAt: at},
		{ServiceName: "ad", Name: "jvm.cpu.recent_utilization", Unit: "1", Type: "gauge", TimeUnixNanos: at, Value: 0.1, IngestedAt: at},
	} {
		insertMetrics(t, db, m)
	}
	for _, tc := range []struct {
		name, measure, want string
	}{
		{"jvm.memory.used", "last(value)", "bytes"},
		{"http.server.request.duration", "avg(value)", "s"},
		{"jvm.thread.count", "max(value)", "count"},
		{"jvm.cpu.recent_utilization", "avg(value)", ""},
		{"jvm.memory.used", "count()", "count"},
	} {
		p := Panel{Viz: "stat", Query: &Spec{From: "metrics", Where: []string{"name = '" + tc.name + "'"}, Measures: []string{tc.measure}}}
		r := runMetricPanel(t, e, p, fixtureStart, fixtureStart.Add(time.Hour))
		if r.Frame == nil || r.Frame.Rows == 0 {
			t.Fatalf("%s %s: no rows: %+v", tc.name, tc.measure, r)
		}
		if got := r.Frame.Columns[len(r.Frame.Columns)-1].Unit; got != tc.want {
			t.Errorf("%s %s: unit %q, want %q", tc.name, tc.measure, got, tc.want)
		}
	}
}

// A lookup that finds no single unit is not cached, so a later window that
// does see one is not left without it.
func TestMetricUnitDoesNotCacheAMiss(t *testing.T) {
	e, db := metricFixture(t)
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}
	if got := e.metricUnit(t.Context(), "jvm.memory.used", scope); got != "" {
		t.Fatalf("unit before data = %q", got)
	}
	at := fixtureStart.Add(30 * time.Minute).UnixNano()
	insertMetrics(t, db, telemetry.Metric{ServiceName: "ad", Name: "jvm.memory.used", Unit: "By", Type: "gauge", TimeUnixNanos: at, Value: 1, IngestedAt: at})
	if got := e.metricUnit(t.Context(), "jvm.memory.used", scope); got != "By" {
		t.Fatalf("unit after data = %q, want By", got)
	}
}
