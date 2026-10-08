package dashboard

import (
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestChangesNameOnlyCommittedPanelFields(t *testing.T) {
	a := panel.Dashboard{Panels: []panel.Panel{{ID: "latency", Title: "Latency", Viz: "stat", Unit: "ms"}, {ID: "errors", Title: "Errors", Viz: "text", Content: "unchanged"}}}
	b := panel.Dashboard{Panels: []panel.Panel{{ID: "latency", Title: "Latency", Viz: "stat", Unit: "s"}, {ID: "new", Title: "New", Viz: "text", Content: "note"}}}
	got := Changes(a, b)
	if len(got.Panels) != 3 || got.Panels[0].PanelID != "latency" || got.Panels[0].Kind != "changed" || len(got.Panels[0].Fields) != 1 || got.Panels[0].Fields[0] != "unit" || got.Panels[1].PanelID != "new" || got.Panels[1].Kind != "added" || got.Panels[2].PanelID != "errors" || got.Panels[2].Kind != "removed" {
		t.Fatalf("changes=%+v", got)
	}
	if same := Changes(a, a); len(same.Panels) != 0 || same.LayoutChanged {
		t.Fatal("no-op reports changes")
	}
}

func TestChangesSeparatePackingFromAuthoredFieldsAndOrder(t *testing.T) {
	a := threePanels()
	PackMissing(a.Panels)
	b, _ := clone(a)
	b.Panels = []panel.Panel{b.Panels[0], b.Panels[2]}
	Compact(b.Panels)
	got := Changes(a, b)
	if !got.LayoutChanged || len(got.Panels) != 1 || got.Panels[0].PanelID != "b" || got.Panels[0].Kind != "removed" {
		t.Fatalf("packing changes=%+v", got)
	}
	b, _ = clone(a)
	b.Panels[0].Width = 12
	b.Panels[0].Height = "m"
	b.Panels[0].Title = "New title"
	b.Panels[0].Grid.X++
	b.Panels[0], b.Panels[1] = b.Panels[1], b.Panels[0]
	got = Changes(a, b)
	if !got.LayoutChanged || len(got.Panels) != 2 || !got.Panels[0].PositionChanged || !got.Panels[1].PositionChanged || len(got.Panels[0].Fields) != 0 || len(got.Panels[1].Fields) != 3 {
		t.Fatalf("authored changes=%+v", got)
	}
	if got.Panels[1].Fields[0] != "height" || got.Panels[1].Fields[1] != "title" || got.Panels[1].Fields[2] != "width" {
		t.Fatal(got)
	}
}

func TestChangesReportWholeAuthoredFieldsAndDashboardFields(t *testing.T) {
	a := panel.Dashboard{Name: "Before", Panels: []panel.Panel{{ID: "p", Title: "Before", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}, Thresholds: []panel.Threshold{{Value: 10, Status: "warn"}}}}}
	b, _ := clone(a)
	b.Name = "After"
	b.Description = "Description"
	b.Time.Range = "2h"
	b.Panels[0].Query.Measures[0] = "p95(duration_ms)"
	b.Panels[0].Thresholds[0].Value = 20
	got := Changes(a, b)
	if len(got.Panels) != 1 || len(got.Panels[0].Fields) != 2 || got.Panels[0].Fields[0] != "query" || got.Panels[0].Fields[1] != "thresholds" || len(got.DashboardFields) != 3 || got.DashboardFields[0] != "description" || got.DashboardFields[1] != "name" || got.DashboardFields[2] != "time" {
		t.Fatal(got)
	}
}
