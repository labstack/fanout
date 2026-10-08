package panel

import (
	"encoding/json"
	"github.com/labstack/fanout/internal/telemetry"
	"testing"
	"time"
)

func TestPreviewPatternContextEngineScoped(t *testing.T) {
	engine, repo := newTestEngine(t)
	var logs []telemetry.Log
	add := func(namespace, severity, service, secret string, at time.Time, n int) {
		for range n {
			timestamp := at.UnixNano()
			logs = append(logs, telemetry.Log{Namespace: namespace, Severity: severity, ServiceName: service, Body: "token=" + secret + " failed <*>", BodyTemplate: "token=" + secret + " failed <*>", EventUnixNanos: timestamp, TimeUnixNanos: timestamp, IngestedAt: timestamp})
		}
	}
	add("shop", "ERROR", "zeta", "one", fixtureStart, 2)
	add("shop", "WARN", "alpha", "two", fixtureStart.Add(time.Minute), 2)
	add("elsewhere", "FATAL", "outside", "three", fixtureStart, 10)
	add("shop", "FATAL", "outside", "four", fixtureStart.Add(-time.Second), 10)
	add("shop", "FATAL", "outside", "five", fixtureStart.Add(time.Hour), 10)
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Scoped context", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", Where: []string{"namespace = 'shop'"}, By: []string{"body_template"}, Measures: []string{"count()"}, Bucket: "auto"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(got) != 1 || got[0].Status != StatusOK || got[0].Frame == nil {
		t.Fatalf("patterns: %+v %v", got, err)
	}
	f := got[0].Frame
	names := []string{"body_template", "count", "trend", "severity", "service"}
	if len(f.Columns) != len(names) || f.Rows != 1 {
		t.Fatalf("group size/schema: %+v", f)
	}
	cols := map[string]int{}
	for i, c := range f.Columns {
		cols[c.Name] = i
	}
	for _, name := range names {
		if _, ok := cols[name]; !ok {
			t.Fatalf("missing %s", name)
		}
	}
	pattern := "token=[REDACTED] failed <*>"
	if f.Values[cols["body_template"]][0] != pattern || f.Values[cols["count"]][0] != float64(4) || f.Values[cols["severity"]][0] != "ERROR" || f.Values[cols["service"]][0] != "alpha" {
		t.Fatalf("scoped context: %+v", f)
	}
	var trend []float64
	if err := json.Unmarshal([]byte(f.Values[cols["trend"]][0].(string)), &trend); err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, n := range trend {
		total += n
	}
	if total != 4 || len(trend) > 240 {
		t.Fatalf("trend: %v", trend)
	}
	d.Panels[0].Viz = "logs"
	d.Panels[0].Query = &Query{From: "logs", Where: []string{"namespace = 'shop'", "body_template = '" + pattern + "'"}}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK || got[0].Frame == nil || got[0].Frame.Rows != 4 {
		t.Fatalf("drill: %+v %v", got, err)
	}
	for i, c := range got[0].Frame.Columns {
		if c.Name == "body" {
			for _, body := range got[0].Frame.Values[i] {
				if body != pattern {
					t.Fatalf("unredacted drill: %v", body)
				}
			}
			return
		}
	}
	t.Fatal("missing drill body")
}
