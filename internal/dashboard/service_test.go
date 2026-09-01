package dashboard

import (
	"context"
	"errors"
	"testing"

	appid "github.com/labstack/fanout/internal/id"
	controlstore "github.com/labstack/fanout/internal/store"
)

func TestServiceCreatesNamedOwnerScopedDashboards(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for _, owner := range []string{"owner-a", "owner-b"} {
		if _, err := database.DB.ExecContext(ctx, `INSERT INTO users(id,email,name,role,active) VALUES(?,?,?,'admin',1)`, owner, owner+"@example.test", owner); err != nil {
			t.Fatal(err)
		}
	}
	service := New(database.DB)

	initial, err := service.List(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 1 || initial[0].Name != "System overview" || !initial[0].IsDefault {
		t.Fatalf("initial dashboards = %#v", initial)
	}

	state := State{
		Filters: Filters{Window: "15m", Namespace: "prod"},
		Widgets: []Widget{{ID: "errors", Type: "logs", Title: "Recent errors", Config: map[string]any{"severity": "ERROR"}, Enabled: true}},
		Layout:  []Layout{{I: "errors", X: 0, Y: 0, W: 12, H: 4, MinW: 4, MinH: 2}},
	}
	created, err := service.Create(ctx, "owner-a", CreateInput{Name: "Incident room", Description: "Errors requiring attention.", State: state})
	if err != nil {
		t.Fatal(err)
	}
	if created.State.Widgets[0].Config["severity"] != "ERROR" {
		t.Fatalf("widget config = %#v", created.State.Widgets[0].Config)
	}
	assertDashboardUUIDv7s(t, created)
	if _, err := service.Get(ctx, "owner-b", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner read = %v", err)
	}
	if _, err := service.Create(ctx, "owner-a", CreateInput{Name: "incident ROOM", State: state}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name = %v", err)
	}

	created.Description = "Updated description."
	updated, err := service.Update(ctx, "owner-a", created.ID, UpdateInput{Name: created.Name, Description: created.Description, State: created.State})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != created.Description {
		t.Fatalf("description = %q", updated.Description)
	}
	if err := service.Delete(ctx, "owner-a", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, "owner-a", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted dashboard = %v", err)
	}
}

func TestServiceMigratesLegacyCanvasOnFirstRead(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.DB.ExecContext(ctx, `INSERT INTO users(id,email,name,role,active) VALUES('owner','owner@example.test','Owner','admin',1)`); err != nil {
		t.Fatal(err)
	}
	legacy := `{"layout":[{"i":"health","x":0,"y":0,"w":12,"h":3}],"widgets":[{"id":"health","type":"overview","title":"Legacy health","enabled":true}],"filters":{"window":"6h","namespace":"legacy"}}`
	if _, err := database.DB.ExecContext(ctx, `INSERT INTO dashboard_state(owner_id,state_json) VALUES('owner',?)`, legacy); err != nil {
		t.Fatal(err)
	}
	item, err := New(database.DB).Default(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if item.State.Filters.Window != "6h" || item.State.Widgets[0].Title != "Legacy health" {
		t.Fatalf("migrated dashboard = %#v", item)
	}
	assertDashboardUUIDv7s(t, item)
}

func TestValidateRejectsInvalidStates(t *testing.T) {
	newID := func() string {
		t.Helper()
		id, err := appid.New()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := newID(), newID()
	valid := func() State {
		return State{
			Filters: Filters{Window: "1h"},
			Widgets: []Widget{
				{ID: first, Type: "overview", Title: "Health", Enabled: true},
				{ID: second, Type: "logs", Title: "Errors", Enabled: true},
			},
			Layout: []Layout{
				{I: first, X: 0, Y: 0, W: 6, H: 3},
				{I: second, X: 6, Y: 0, W: 6, H: 3},
			},
		}
	}
	if err := Validate("Baseline", "", valid()); err != nil {
		t.Fatalf("baseline state rejected: %v", err)
	}
	for _, window := range []string{"168h", "720h"} {
		state := valid()
		state.Filters.Window = window
		if err := Validate("Baseline", "", state); err != nil {
			t.Fatalf("window %s rejected: %v", window, err)
		}
	}
	cases := []struct {
		name   string
		mutate func(*State)
	}{
		{"bad widget type", func(s *State) { s.Widgets[0].Type = "chart" }},
		{"out of 12-column bounds", func(s *State) { s.Layout[1].X = 8 }},
		{"negative position", func(s *State) { s.Layout[0].X = -1 }},
		{"zero widgets", func(s *State) { s.Widgets = nil; s.Layout = nil }},
		{"too many widgets", func(s *State) {
			s.Widgets = nil
			s.Layout = nil
			for i := 0; i < 33; i++ {
				id := newID()
				s.Widgets = append(s.Widgets, Widget{ID: id, Type: "logs", Title: "W", Enabled: true})
				s.Layout = append(s.Layout, Layout{I: id, X: 0, Y: i, W: 12, H: 1})
			}
		}},
		{"layout references unknown widget id", func(s *State) { s.Layout[1].I = newID() }},
		{"layout count mismatch", func(s *State) { s.Layout = s.Layout[:1] }},
		{"duplicate widget id", func(s *State) { s.Widgets[1].ID = first }},
		{"non-UUID widget id", func(s *State) { s.Widgets[0].ID = "health"; s.Layout[0].I = "health" }},
		{"invalid window", func(s *State) { s.Filters.Window = "7d" }},
		{"overlapping widgets", func(s *State) { s.Layout[1].X = 3 }},
		{"contained widget overlaps", func(s *State) { s.Layout[1] = Layout{I: second, X: 1, Y: 1, W: 2, H: 1} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := valid()
			tc.mutate(&state)
			err := Validate("Baseline", "", state)
			if err == nil {
				t.Fatal("expected validation rejection")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error %v is not a ValidationError", err)
			}
		})
	}
}

func TestValidateAcceptsAdjacentWidgets(t *testing.T) {
	// Widgets sharing an edge (touching, not intersecting) are legal.
	first, err := appid.New()
	if err != nil {
		t.Fatal(err)
	}
	second, err := appid.New()
	if err != nil {
		t.Fatal(err)
	}
	state := State{
		Filters: Filters{Window: "1h"},
		Widgets: []Widget{
			{ID: first, Type: "overview", Title: "Health", Enabled: true},
			{ID: second, Type: "logs", Title: "Errors", Enabled: true},
		},
		Layout: []Layout{
			{I: first, X: 0, Y: 0, W: 12, H: 3},
			{I: second, X: 0, Y: 3, W: 12, H: 3},
		},
	}
	if err := Validate("Stacked", "", state); err != nil {
		t.Fatalf("adjacent widgets rejected: %v", err)
	}
}

func TestNormalizeStateIDsRemapsNonUUIDWidgetIDs(t *testing.T) {
	stable, err := appid.New()
	if err != nil {
		t.Fatal(err)
	}
	state := State{
		Widgets: []Widget{
			{ID: "health", Type: "overview", Title: "Health", Enabled: true},
			{ID: stable, Type: "logs", Title: "Errors", Enabled: true},
		},
		Layout: []Layout{
			{I: "health", X: 0, Y: 0, W: 6, H: 3},
			{I: stable, X: 6, Y: 0, W: 6, H: 3},
		},
	}
	normalized, err := normalizeStateIDs(state)
	if err != nil {
		t.Fatal(err)
	}
	remapped := normalized.Widgets[0].ID
	if !appid.IsV7(remapped) || remapped == "health" {
		t.Fatalf("non-UUID widget id remapped to %q", remapped)
	}
	if normalized.Widgets[1].ID != stable {
		t.Fatalf("stable UUIDv7 id changed to %q", normalized.Widgets[1].ID)
	}
	if normalized.Layout[0].I != remapped {
		t.Fatalf("layout reference %q does not follow remapped widget id %q", normalized.Layout[0].I, remapped)
	}
	if normalized.Layout[1].I != stable {
		t.Fatalf("layout reference for stable id changed to %q", normalized.Layout[1].I)
	}
	if state.Widgets[0].ID != "health" || state.Layout[0].I != "health" {
		t.Fatalf("normalizeStateIDs mutated its input: %#v", state)
	}
}

func assertDashboardUUIDv7s(t *testing.T, item Dashboard) {
	t.Helper()
	if !appid.IsV7(item.ID) {
		t.Fatalf("dashboard id %q is not UUIDv7", item.ID)
	}
	widgetIDs := make(map[string]bool, len(item.State.Widgets))
	for _, widget := range item.State.Widgets {
		if !appid.IsV7(widget.ID) {
			t.Fatalf("widget id %q is not UUIDv7", widget.ID)
		}
		widgetIDs[widget.ID] = true
	}
	for _, layout := range item.State.Layout {
		if !appid.IsV7(layout.I) || !widgetIDs[layout.I] {
			t.Fatalf("layout id %q is not a persisted widget UUIDv7", layout.I)
		}
	}
}
