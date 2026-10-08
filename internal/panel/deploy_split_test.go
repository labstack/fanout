package panel

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDeploySplitUsesEffectiveWindowAndRates(t *testing.T) {
	engine, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO version_rollup VALUES ('shop','checkout','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS),('shop','checkout','v2',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, at.Add(-time.Hour), at, at.Add(15*time.Minute), at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Query.Measures = []string{"rate()"}
	d.Panels[0].Options = &Options{Split: "deploy"}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if got[0].Status != "ok" || f.Rows != 4 || f.Columns[1].Name != "period" {
		t.Fatalf("split: %+v", got)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Periods map[string]struct{ From, To time.Time }
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if before, since := wire.Periods["Before deploy"], wire.Periods["Since deploy"]; !before.From.Equal(at) || !before.To.Equal(at.Add(15*time.Minute)) || !since.From.Equal(before.To) || !since.To.Equal(at.Add(time.Hour)) {
		t.Fatalf("missing period windows: %s", raw)
	}
	req := ExemplarRequest{Dashboard: d, PanelID: d.Panels[0].ID, From: at, To: at.Add(15 * time.Minute), Dimensions: map[string]string{"http_route": "/cart"}}
	exemplars, err := e.Exemplars(t.Context(), req)
	if err != nil || len(exemplars.Traces) != 15 {
		t.Fatalf("before exemplars: %+v %v", exemplars, err)
	}
	for _, trace := range exemplars.Traces {
		if !trace.Start.Before(req.To) {
			t.Fatalf("since trace in before selection: %+v", trace)
		}
	}
	for _, v := range f.Values[2] {
		if v.(float64) != 1.0/60 {
			t.Fatalf("rate used full window: %v", f.Values)
		}
	}
	d.Panels[0].Time = &PanelTime{Range: "15m"}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame.Note == "" || len(got[0].Frame.Columns) != 2 {
		t.Fatalf("no deploy: %+v %v", got, err)
	}
}

func TestDeploySplitAllFallsBackAndRequiresEquality(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Options = &Options{Split: "deploy"}
	d.Variables[0].IncludeAll = true
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d, Vars: map[string]Value{"service": {All: true}}})
	if err != nil || got[0].Frame == nil || got[0].Frame.Note != "Service is All; showing the unsplit whole-window frame." || len(got[0].Frame.Columns) != 2 {
		t.Fatalf("All: %+v %v", got, err)
	}
	d.Panels[0].Query.Where = nil
	err = e.Validate(t.Context(), &d)
	var problems Problems
	want := Problem{Path: "panels[0].options.split", Message: "deploy split requires a service equality filter", Hint: "add service = 'name' or service = $service"}
	if !errors.As(err, &problems) || !slices.Contains(problems, want) {
		t.Fatalf("got %v want %+v", err, want)
	}
}

func TestDeploySplitLatestDeployAndBoundaries(t *testing.T) {
	engine, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	at := fixtureStart
	for i, offset := range []time.Duration{-time.Hour, 0, 15 * time.Minute, 45 * time.Minute, time.Hour} {
		if _, err := engine.DB.Exec(`INSERT INTO version_rollup VALUES ('shop','checkout',?,?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, string(rune('a'+i)), at.Add(offset), at.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Options = &Options{Split: "deploy"}
	d.Panels[0].Query.Where = append(d.Panels[0].Query.Where, "namespace = 'shop'")
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	wantNote := "Split at " + at.Add(45*time.Minute).Format("2006-01-02T15:04:05.999999999Z07:00") + " · checkout d"
	if got[0].Status != StatusOK || f == nil || f.Note != wantNote || f.Rows != 4 {
		t.Fatalf("latest deploy: %+v", got)
	}
	for r := range f.Rows {
		want := 45.0
		if f.Values[1][r] == "Since deploy" {
			want = 15
		}
		if f.Values[2][r] != want {
			t.Fatalf("wrong subwindow count: %+v", f)
		}
	}
	d.Panels[0].Time = &PanelTime{Range: "15m"}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame == nil || got[0].Frame.Note != "No deploy in this panel's time range; showing the whole window." {
		t.Fatalf("boundary deploy should remain unsplit: %+v %v", got, err)
	}
}

func TestDeploySplitChecksASTScopeReferences(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, tc := range []struct {
		expr  string
		valid bool
	}{
		{"http_route = '/service/namespace'", true},
		{"namespace = 'shop'", true},
		{"'shop' = namespace", true},
		{"service LIKE '%out'", false},
		{"namespace IN ('shop')", false},
		{"service = 'checkout' OR http_route = '/cart'", false},
		{"service = 'checkout' AND namespace = 'shop'", false},
		{"CAST(service AS VARCHAR) = 'checkout'", false},
		{"namespace = service", false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			d := shopDashboard()
			d.Panels = d.Panels[2:3]
			d.Panels[0].Options = &Options{Split: "deploy"}
			d.Panels[0].Query.Where = []string{"service = $service", tc.expr}
			err := e.Validate(t.Context(), &d)
			if tc.valid && err != nil || !tc.valid && (err == nil || !strings.Contains(err.Error(), "options.split")) {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}
