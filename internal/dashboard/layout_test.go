package dashboard

import (
	"math"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestServiceMapDefaultLayoutLarge(t *testing.T) {
	for _, pack := range []func([]panel.Panel){PackMissing} {
		panels := []panel.Panel{{ID: "map", Viz: "service_map", Width: 12}}
		pack(panels)
		if panels[0].Grid.H != 10 {
			t.Fatalf("service map rows=%d", panels[0].Grid.H)
		}
	}
}

func TestPackFillsGapsWithoutStretching(t *testing.T) {
	panels := []panel.Panel{
		{ID: "a", Width: 3, Height: "s"},
		{ID: "chart", Width: 9, Height: "m"},
		{ID: "b", Width: 3, Height: "s"},
		{ID: "table", Width: 12, Height: "m"},
	}
	PackMissing(panels)
	want := map[string]panel.Grid{
		"a":     {X: 0, Y: 0, W: 3, H: 3},
		"chart": {X: 3, Y: 0, W: 9, H: 6},
		"b":     {X: 0, Y: 3, W: 3, H: 3},
		"table": {X: 0, Y: 6, W: 12, H: 6},
	}
	for _, p := range panels {
		if *p.Grid != want[p.ID] {
			t.Errorf("%s = %+v, want %+v", p.ID, *p.Grid, want[p.ID])
		}
	}
	if needsPack(panels) {
		t.Fatal("packed layout reported as needing a pack")
	}
	panels[1].Grid.X = 1
	if !needsPack(panels) {
		t.Fatal("overlap not detected")
	}
}

func TestNeedsPackRejectsHugeOrOverflowingGrids(t *testing.T) {
	for name, g := range map[string]panel.Grid{
		"overflow": {X: math.MaxInt64 - 1, W: 3, H: 3},
		"far row":  {Y: 1000000, W: 3, H: 3},
		"tall":     {W: 3, H: 1000000},
	} {
		if !needsPack([]panel.Panel{{ID: "a", Grid: &g}}) {
			t.Errorf("%s not detected", name)
		}
	}
}
