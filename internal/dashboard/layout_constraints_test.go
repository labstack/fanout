package dashboard

import (
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/panel"
)

func TestAddPreservesSavedLayout(t *testing.T) {
	for _, author := range []Author{{Kind: "user"}, agent} {
		s := newTestService(t)
		r, err := s.Create(t.Context(), "owner", threePanels(), author)
		if err != nil {
			t.Fatal(err)
		}
		r.Spec.Panels[0].Grid = &panel.Grid{X: 0, W: 8, H: 3}
		r.Spec.Panels[1].Grid = &panel.Grid{X: 8, W: 4, H: 3}
		r, err = s.Replace(t.Context(), "owner", r.ID, r.Spec, 1, author, "Widen A")
		if err != nil {
			t.Fatal(err)
		}
		before := r.Spec
		copy := r.Spec.Panels[2]
		copy.ID = "copy"
		r, err = s.Edit(t.Context(), "owner", r.ID, []Operation{{Op: "add_panel", After: "c", Panel: &copy}}, 2, author, "Duplicate C")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.Spec.Panels[:3], before.Panels) || len(r.Spec.Panels) != 4 || r.Spec.Panels[3].Grid == nil {
			t.Fatalf("layout changed: before=%+v after=%+v", before.Panels, r.Spec.Panels)
		}
	}
}

func TestRemovalClosesRealGap(t *testing.T) {
	for _, replace := range []bool{false, true} {
		s := newTestService(t)
		spec := threePanels()
		for i := range spec.Panels {
			spec.Panels[i].Grid = &panel.Grid{Y: i * 3, W: 12, H: 3}
		}
		r, err := s.Create(t.Context(), "owner", spec, agent)
		if err != nil {
			t.Fatal(err)
		}
		if replace {
			r.Spec.Panels = append(r.Spec.Panels[:1], r.Spec.Panels[2:]...)
			r, err = s.Replace(t.Context(), "owner", r.ID, r.Spec, 1, agent, "")
		} else {
			r, err = s.Edit(t.Context(), "owner", r.ID, []Operation{{Op: "remove_panel", ID: "b"}}, 1, agent, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		if g := r.Spec.Panels[1].Grid; g.Y != 3 || g.W != 12 || g.H != 3 {
			t.Fatalf("gap remains: %+v", g)
		}
	}
}

func TestReplaceHugeGridIsBounded(t *testing.T) {
	s := newTestService(t)
	r, err := s.Create(t.Context(), "owner", threePanels(), agent)
	if err != nil {
		t.Fatal(err)
	}
	r.Spec.Panels = r.Spec.Panels[:2]
	r.Spec.Panels[1].Grid = &panel.Grid{Y: 1 << 40, W: 6, H: 3}
	started := time.Now()
	r, err = s.Replace(t.Context(), "owner", r.ID, r.Spec, 1, agent, "")
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("Replace took %s", elapsed)
	}
	if err != nil {
		t.Fatal(err)
	}
	if needsPack(r.Spec.Panels) || r.Spec.Panels[1].Grid.Y > 6 {
		t.Fatalf("unpacked grid: %+v", r.Spec.Panels[1].Grid)
	}
}

func TestDefaultThresholdDirections(t *testing.T) {
	for _, p := range DefaultSpec().Panels {
		if len(p.Thresholds) > 0 && p.Better == "" {
			t.Errorf("%s lacks explicit better", p.ID)
		}
		if p.ID == "error_logs" && p.Better != "lower" {
			t.Errorf("Error logs better=%q", p.Better)
		}
	}
}

func TestEditCompactsBoundedGrids(t *testing.T) {
	d := threePanels()
	d.Panels[0].Grid = &panel.Grid{W: 6, H: 3}
	d.Panels[1].Grid = &panel.Grid{Y: 1 << 40, W: 6, H: 3}
	d.Panels[2].Grid = &panel.Grid{X: 6, W: 6, H: 3}
	started := time.Now()
	out, _, err := Apply(d, []Operation{{Op: "remove_panel", ID: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= 100*time.Millisecond || needsPack(out.Panels) {
		t.Fatal("Edit did not bound supplied grids")
	}
}

func TestCompactFindsLowestFreeGap(t *testing.T) {
	panels := []panel.Panel{
		{Grid: &panel.Grid{W: 6, H: 10}},
		{Grid: &panel.Grid{Y: 10, W: 12, H: 3}},
		{Grid: &panel.Grid{X: 6, Y: 20, W: 6, H: 3}},
	}
	Compact(panels)
	if panels[2].Grid.Y != 0 {
		t.Fatalf("did not fill lowest gap: %+v", panels[2].Grid)
	}
}
