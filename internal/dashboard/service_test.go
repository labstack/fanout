package dashboard

import (
	"context"
	"errors"
	"testing"

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
}
