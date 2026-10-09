package dashboard

import (
	"reflect"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestChangesReportOnlyPanelsOutsideTheLongestCommonOrder(t *testing.T) {
	before := panel.Dashboard{Name: "Order"}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		before.Panels = append(before.Panels, panel.Panel{ID: id, Title: id, Viz: "text", Content: id})
	}
	for _, tc := range []struct {
		name                             string
		order, positions, removed, added []string
	}{
		{name: "first_to_last", order: []string{"b", "c", "d", "e", "a"}, positions: []string{"a"}},
		{name: "last_to_first", order: []string{"e", "a", "b", "c", "d"}, positions: []string{"e"}},
		{name: "swap_two", order: []string{"b", "a", "c", "d", "e"}, positions: []string{"a"}},
		{name: "no_op", order: []string{"a", "b", "c", "d", "e"}},
		{name: "remove_plus_move", order: []string{"e", "a", "c", "d"}, positions: []string{"e"}, removed: []string{"b"}},
		{name: "remove", order: []string{"a", "c", "d", "e"}, removed: []string{"b"}},
		{name: "add", order: []string{"a", "new", "b", "c", "d", "e"}, added: []string{"new"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := panel.Dashboard{Name: before.Name}
			for _, id := range tc.order {
				if id == "new" {
					after.Panels = append(after.Panels, panel.Panel{ID: id, Title: id, Viz: "text", Content: id})
				}
				for _, p := range before.Panels {
					if p.ID == id {
						after.Panels = append(after.Panels, p)
					}
				}
			}
			got := Changes(before, after)
			var positions, removed, added []string
			for _, p := range got.Panels {
				if p.PositionChanged {
					positions = append(positions, p.PanelID)
				}
				if p.Kind == "removed" {
					removed = append(removed, p.PanelID)
				}
				if p.Kind == "added" {
					added = append(added, p.PanelID)
				}
			}
			if !reflect.DeepEqual(positions, tc.positions) || !reflect.DeepEqual(removed, tc.removed) || !reflect.DeepEqual(added, tc.added) || len(got.Panels) != len(positions)+len(removed)+len(added) {
				t.Fatalf("changes=%+v", got)
			}
		})
	}
}

func TestChangesIgnoreNewNormalizationDefaultsWithoutMutatingSnapshots(t *testing.T) {
	before := panel.Dashboard{Name: "Old defaults", Panels: []panel.Panel{
		{ID: "a", Title: "A", Viz: "text", Content: "a"},
		{ID: "b", Title: "B", Viz: "timeseries", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}},
	}}
	after, err := clone(before)
	if err != nil {
		t.Fatal(err)
	}
	panel.Normalize(&after)
	after.Panels[0].Content = "edited"
	original := mustJSON(before)
	got := Changes(before, after)
	if len(got.Panels) != 1 || got.Panels[0].PanelID != "a" || !reflect.DeepEqual(got.Panels[0].Fields, []string{"content"}) || len(got.DashboardFields) != 0 {
		t.Fatalf("normalization drift=%+v", got)
	}
	if string(original) != string(mustJSON(before)) || before.Panels[1].Query.Bucket != "" {
		t.Fatal("diff mutated its base")
	}
}

func TestDashboardChangesExcludeTheSpecFormatVersion(t *testing.T) {
	before := textSpec("Format")
	panel.Normalize(&before)
	after, _ := clone(before)
	after.Version++
	if got := Changes(before, after); len(got.DashboardFields) != 0 {
		t.Fatalf("format reported as authored change: %+v", got)
	}
}

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
	if !got.LayoutChanged || len(got.Panels) != 1 || got.Panels[0].PanelID != "a" || !got.Panels[0].PositionChanged || len(got.Panels[0].Fields) != 3 {
		t.Fatalf("authored changes=%+v", got)
	}
	if !reflect.DeepEqual(got.Panels[0].Fields, []string{"height", "title", "width"}) {
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

func TestCreateChangesHaveNoLayoutOrDashboardChips(t *testing.T) {
	after := textSpec("Created")
	PackMissing(after.Panels)
	got := Changes(panel.Dashboard{}, after)
	if got.LayoutChanged || len(got.DashboardFields) != 0 || len(got.Panels) != 1 || got.Panels[0].Kind != "added" {
		t.Fatalf("create changes=%+v", got)
	}
}
