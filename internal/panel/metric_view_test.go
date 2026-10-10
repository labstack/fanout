package panel

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestMetricViewMapsKindsAndKeepsNanosecondStart(t *testing.T) {
	e, db := metricFixture(t)
	for i, stored := range []string{"gauge", "sum", "sum", "histogram", "exponential_histogram", "summary"} {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Second), 1)
		m.Type, m.Monotonic = stored, i == 1
		m.Name = fmt.Sprintf("metric%d", i)
		m.StartTimeUnixNanos = fixtureStart.Add(-time.Minute).UnixNano() + 17
		if i == 0 {
			m.StartTimeUnixNanos = 0
			m.Temporality = ""
		}
		if i == 4 {
			m.Temporality = "delta"
			m.StartTimeUnixNanos = -17
		}
		if i == 5 {
			m.Temporality = ""
		}
		insertMetrics(t, db, m)
	}
	r := runMetricPanel(t, e, Panel{Viz: "table", SQL: `SELECT name,type,temporality,
        epoch_ns(start_time)::DOUBLE AS start_nanos,typeof(start_time) AS start_type
        FROM metrics WHERE $__window(time) ORDER BY name`}, fixtureStart, fixtureStart.Add(time.Minute))
	if got := metricValues(t, r, "type"); !reflect.DeepEqual(got, []any{"gauge", "counter", "updown", "histogram", "histogram", "summary"}) {
		t.Fatalf("kinds: %v", got)
	}
	if got := metricValues(t, r, "temporality"); !reflect.DeepEqual(got, []any{"", "cumulative", "cumulative", "cumulative", "delta", ""}) {
		t.Fatalf("temporality: %v", got)
	}
	var signed int64
	if err := db.QueryRow("SELECT epoch_ns(start_time) FROM metrics WHERE name='metric4'").Scan(&signed); err != nil || signed != -17 {
		t.Fatalf("signed start=%d err=%v", signed, err)
	}
	if metricValues(t, r, "start_nanos")[0] != nil {
		t.Fatal("zero start must be NULL")
	}
	for _, got := range metricValues(t, r, "start_type") {
		if got != "TIMESTAMPTZ_NS" {
			t.Fatalf("start type: %v", got)
		}
	}
	var exact int64
	if err := db.QueryRow("SELECT epoch_ns(start_time) FROM metrics WHERE name='metric1'").Scan(&exact); err != nil || exact != fixtureStart.Add(-time.Minute).UnixNano()+17 {
		t.Fatalf("nanoseconds=%d err=%v", exact, err)
	}
}
