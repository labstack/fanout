package dashboard

import (
	"math"
	"slices"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestPackedRowBandsSnapOnlyFreeNewPanels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		panels []panel.Panel
		want   []panel.Grid
	}{
		{"wall", []panel.Panel{
			{Width: 4, Height: "s"}, {Width: 4, Height: "s"}, {Width: 4, Height: "s"},
			{Width: 6, Height: "m"}, {Width: 6, Height: "m"},
			{Viz: "service_map", Width: 6, Height: "l"}, {Viz: "health", Width: 6, Height: "m"},
			{Viz: "heatmap", Width: 12, Height: "m"}, {Viz: "traces", Width: 12, Height: "m"}, {Viz: "log_patterns", Width: 12, Height: "m"},
		}, []panel.Grid{{X: 0, Y: 0, W: 4, H: 3}, {X: 4, Y: 0, W: 4, H: 3}, {X: 8, Y: 0, W: 4, H: 3}, {X: 0, Y: 3, W: 6, H: 6}, {X: 6, Y: 3, W: 6, H: 6}, {X: 0, Y: 9, W: 6, H: 10}, {X: 6, Y: 9, W: 6, H: 10}, {X: 0, Y: 19, W: 12, H: 6}, {X: 0, Y: 25, W: 12, H: 6}, {X: 0, Y: 31, W: 12, H: 6}}},
		{"text beside series", []panel.Panel{{Viz: "text", Width: 6, Height: "s"}, {Viz: "timeseries", Width: 6, Height: "m"}}, []panel.Grid{{X: 0, Y: 0, W: 6, H: 6}, {X: 6, Y: 0, W: 6, H: 6}}},
		{"authored short", []panel.Panel{{Grid: &panel.Grid{X: 0, Y: 0, W: 6, H: 3}}, {Width: 6, Height: "m"}}, []panel.Grid{{X: 0, Y: 0, W: 6, H: 3}, {X: 6, Y: 0, W: 6, H: 6}}},
		{"authored tall", []panel.Panel{{Grid: &panel.Grid{X: 0, Y: 0, W: 6, H: 10}}, {Width: 6, Height: "s"}}, []panel.Grid{{X: 0, Y: 0, W: 6, H: 10}, {X: 6, Y: 0, W: 6, H: 3}}},
		{"occupied hole", []panel.Panel{{Width: 3, Height: "s"}, {Width: 9, Height: "m"}, {Width: 3, Height: "s"}}, []panel.Grid{{X: 0, Y: 0, W: 3, H: 3}, {X: 3, Y: 0, W: 9, H: 6}, {X: 0, Y: 3, W: 3, H: 3}}},
	} {
		for name, pack := range map[string]func([]panel.Panel){"pack": PackMissing, "compact": Compact} {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				panels := slices.Clone(tc.panels)
				pack(panels)
				for i, p := range panels {
					if *p.Grid != tc.want[i] {
						t.Errorf("panel %d = %+v, want %+v", i, *p.Grid, tc.want[i])
					}
				}
				if needsPack(panels) {
					t.Fatal("packed panels overlap or have unusable grids")
				}
				before := make([]panel.Grid, len(panels))
				for i, p := range panels {
					before[i] = *p.Grid
				}
				pack(panels)
				for i, p := range panels {
					if *p.Grid != before[i] {
						t.Errorf("second pack changed panel %d", i)
					}
				}
			})
		}
	}
}

func TestServiceMapDefaultLayoutLarge(t *testing.T) {
	for _, pack := range []func([]panel.Panel){PackMissing} {
		panels := []panel.Panel{{ID: "map", Viz: "service_map", Width: 12}}
		pack(panels)
		if panels[0].Grid.H != 10 {
			t.Fatalf("service map rows=%d", panels[0].Grid.H)
		}
	}
}

func TestStatHeightMinimumAndGaugeBand(t *testing.T) {
	for _, height := range []string{"", "s", "m", "l"} {
		t.Run(height, func(t *testing.T) {
			panels := []panel.Panel{{Viz: "stat", Width: 3, Height: height}, {Viz: "gauge", Width: 3, Height: "s"}}
			PackMissing(panels)
			want := 4
			if height == "m" {
				want = 6
			}
			if height == "l" {
				want = 10
			}
			for i, p := range panels {
				if p.Grid.H != want {
					t.Errorf("panel %d height = %d, want %d", i, p.Grid.H, want)
				}
			}
		})
	}
}

func TestPackedRowBandsFillOnlyFreeColumns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		panels []panel.Panel
		want   []panel.Grid
	}{
		{"three stats", []panel.Panel{{Viz: "stat", Width: 3, Height: "s"}, {Viz: "stat", Width: 3, Height: "s"}, {Viz: "stat", Width: 3, Height: "s"}}, []panel.Grid{{X: 0, W: 4, H: 4}, {X: 4, W: 4, H: 4}, {X: 8, W: 4, H: 4}}},
		{"text and series", []panel.Panel{{Viz: "text", Width: 4, Height: "s"}, {Viz: "timeseries", Width: 6, Height: "m"}}, []panel.Grid{{X: 0, W: 5, H: 6}, {X: 5, W: 7, H: 6}}},
		{"leftmost gets remainder", []panel.Panel{{Width: 3, Height: "s"}, {Width: 3, Height: "s"}, {Width: 2, Height: "s"}}, []panel.Grid{{X: 0, W: 5, H: 3}, {X: 5, W: 4, H: 3}, {X: 9, W: 3, H: 3}}},
		{"authored right", []panel.Panel{{Width: 3, Height: "s"}, {Grid: &panel.Grid{X: 9, W: 2, H: 3}}}, []panel.Grid{{X: 0, W: 3, H: 3}, {X: 9, W: 2, H: 3}}},
		{"authored between", []panel.Panel{{Width: 3, Height: "s"}, {Grid: &panel.Grid{X: 3, W: 3, H: 3}}, {Width: 3, Height: "s"}}, []panel.Grid{{X: 0, W: 3, H: 3}, {X: 3, W: 3, H: 3}, {X: 6, W: 3, H: 3}}},
		{"right occupied lower down", []panel.Panel{{Width: 6, Height: "l"}, {Width: 3, Height: "s"}, {Width: 6, Height: "m"}}, []panel.Grid{{X: 0, W: 6, H: 10}, {X: 6, W: 3, H: 3}, {X: 6, Y: 3, W: 6, H: 6}}},
		{"authored only", []panel.Panel{{Viz: "stat", Grid: &panel.Grid{X: 2, W: 3, H: 3}}, {Grid: &panel.Grid{X: 5, W: 4, H: 3}}}, []panel.Grid{{X: 2, W: 3, H: 3}, {X: 5, W: 4, H: 3}}},
	} {
		for name, pack := range map[string]func([]panel.Panel){"pack": PackMissing, "compact": Compact} {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				panels := slices.Clone(tc.panels)
				// Each runner gets its own authored grid pointers.
				for i, p := range panels {
					if p.Grid != nil {
						g := *p.Grid
						panels[i].Grid = &g
					}
				}
				pack(panels)
				for i, p := range panels {
					if *p.Grid != tc.want[i] {
						t.Errorf("panel %d = %+v, want %+v", i, *p.Grid, tc.want[i])
					}
				}
				if needsPack(panels) {
					t.Fatal("packed panels overlap or have unusable grids")
				}
				pack(panels)
				for i, p := range panels {
					if *p.Grid != tc.want[i] {
						t.Errorf("second pack changed panel %d: %+v", i, *p.Grid)
					}
				}
			})
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
