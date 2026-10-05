package db_test

import (
	"testing"

	"github.com/labstack/fanout/internal/db/generated"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestDashboardVersionsCascadeAndOptimisticUpdate(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES ('u1', 'u1@example.com')`); err != nil {
		t.Fatal(err)
	}
	q := generated.New(sqlite.DB)
	ctx := t.Context()
	if n, err := q.InsertDashboardBelowOwnerLimit(ctx, generated.InsertDashboardBelowOwnerLimitParams{ID: "d1", OwnerID: "u1", Name: "Ops", Version: 1, SpecJson: "{}", PanelCount: 0, CreatedAt: "t", UpdatedAt: "t"}); err != nil || n != 1 {
		t.Fatal(err)
	}
	if err := q.InsertDashboardVersion(ctx, generated.InsertDashboardVersionParams{DashboardID: "d1", Version: 1, SpecJson: "{}", AuthorKind: "system", CreatedAt: "t"}); err != nil {
		t.Fatal(err)
	}
	n, err := q.UpdateDashboard(ctx, generated.UpdateDashboardParams{Name: "Ops", NextVersion: 2, SpecJson: "{}", UpdatedAt: "t2", ID: "d1", OwnerID: "u1", BaseVersion: 1})
	if err != nil || n != 1 {
		t.Fatalf("update = %d %v", n, err)
	}
	if n, _ := q.UpdateDashboard(ctx, generated.UpdateDashboardParams{Name: "Ops", NextVersion: 2, SpecJson: "{}", UpdatedAt: "t3", ID: "d1", OwnerID: "u1", BaseVersion: 1}); n != 0 {
		t.Fatal("a stale base version updated the row")
	}
	if _, err := sqlite.DB.Exec(`DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	versions, err := q.ListDashboardVersions(ctx, "d1")
	if err != nil || len(versions) != 0 {
		t.Fatalf("versions survived their dashboard: %v %v", versions, err)
	}
}
