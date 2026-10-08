package panel

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestDrillLogsKeepCheckedSelection(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{ServiceName: "checkout", Severity: "ERROR", Body: "failed", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at}, {ServiceName: "frontend", Severity: "INFO", Body: "ok", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Logs", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "levels", Title: "Levels", Viz: "bar", Query: &Query{From: "logs", Where: []string{"service = 'checkout'"}, Measures: []string{"count()"}, By: []string{"severity"}}}}}
	req := ExemplarRequest{Dashboard: d, PanelID: "levels", Kind: "logs", From: fixtureStart, To: fixtureStart.Add(time.Minute), Dimensions: map[string]string{"severity": "ERROR"}}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Logs == nil || got.Logs.Rows != 1 || got.Logs.Values[2][0] != "checkout" {
		t.Fatalf("logs: %+v", got)
	}
	req.Dimensions = map[string]string{"severity": "INFO"}
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || got.Logs.Rows != 0 {
		t.Fatalf("guard escaped: %+v %v", got, err)
	}
}

func TestExemplarKindValidation(t *testing.T) {
	e := newFixtureExecutor(t)
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", Kind: "other", From: fixtureStart, To: fixtureStart.Add(time.Minute)}
	_, err := e.Exemplars(t.Context(), req)
	var got Problems
	want := Problem{Path: "kind", Message: "kind must be traces or logs", Hint: "choose traces or logs"}
	if !errors.As(err, &got) || !slices.Contains(got, want) {
		t.Fatalf("got %v want %+v", err, want)
	}
}
func TestExemplarCapturedWindowDoesNotMove(t *testing.T) {
	e := newFixtureExecutor(t)
	from, to := fixtureStart, fixtureStart.Add(time.Hour)
	d := shopDashboard()
	d.Panels[2].Time = &PanelTime{Shift: "1h"}
	req := ExemplarRequest{Dashboard: d, PanelID: "by_route", Time: &Time{From: &from, To: &to, Refresh: "off"}, From: from, To: to}
	e.now = func() time.Time { return fixtureStart.Add(10 * time.Hour) }
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 20 {
		t.Fatalf("request clock or double shift changed selection: %+v %v", got, err)
	}
}

func TestDrillLogsCapAndSource(t *testing.T) {
	engine, repo := newTestEngine(t)
	logs := make([]telemetry.Log, 201)
	for i := range logs {
		at := fixtureStart.Add(time.Duration(i) * time.Millisecond).UnixNano()
		logs[i] = telemetry.Log{ServiceName: "checkout", Severity: "ERROR", Body: "failed", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at}
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Logs", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "levels", Title: "Levels", Viz: "bar", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"severity"}}}}}
	req := ExemplarRequest{Dashboard: d, PanelID: "levels", Kind: "logs", From: fixtureStart, To: fixtureStart.Add(time.Minute), Dimensions: map[string]string{"severity": "ERROR"}}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || got.Logs == nil || got.Logs.Rows != 200 || !got.Logs.Truncated || len(got.Traces) != 0 {
		t.Fatalf("cap: %+v %v", got, err)
	}
	req.Dashboard.Panels[0].Query.From = "spans"
	req.Dashboard.Panels[0].Query.By = []string{"service"}
	req.Dimensions = nil
	_, err = e.Exemplars(t.Context(), req)
	var problems Problems
	if !errors.As(err, &problems) || !slices.Contains(problems, Problem{Path: "kind", Message: "log selections need a logs-source panel"}) {
		t.Fatalf("source accepted: %v", err)
	}
}
