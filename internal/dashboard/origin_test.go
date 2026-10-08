package dashboard

import (
	appstore "github.com/labstack/fanout/internal/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildOriginStorageAndOwnership(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	for _, owner := range []string{"owner", "other"} {
		if _, err := s.db.Exec(`INSERT INTO agui_threads(thread_id,owner_id) VALUES (?,?)`, owner+"-thread", owner); err != nil {
			t.Fatal(err)
		}
	}
	origin := BuildOrigin{ThreadID: "owner-thread", MessageID: "user-message", RequestExcerpt: "Build latency 🐈"}
	record, err := s.Create(WithBuildOrigin(ctx, origin), "owner", textSpec("Origin"), agent)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "owner")
	if err != nil || len(list) != 1 || list[0].Origin == nil || *list[0].Origin != origin {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	// Edits, replacement and restore keep the original request, even in another thread.
	changedCtx := WithBuildOrigin(ctx, BuildOrigin{ThreadID: "other-thread", MessageID: "spoof", RequestExcerpt: "new"})
	replaced, err := s.Replace(changedCtx, "owner", record.ID, textSpec("Replaced"), 1, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Edit(changedCtx, "owner", record.ID, []Operation{{Op: "rename", Name: "Edited"}}, replaced.Version, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Restore(changedCtx, "owner", record.ID, 1, agent); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List(ctx, "owner")
	if list[0].Origin == nil || *list[0].Origin != origin {
		t.Fatalf("origin changed: %+v", list)
	}
	// Same-owner reference must be checked in the transaction, with no partial create.
	for _, thread := range []string{"other-thread", "missing"} {
		_, err = s.Create(WithBuildOrigin(ctx, BuildOrigin{ThreadID: thread, MessageID: "user", RequestExcerpt: "spoof"}), "owner", textSpec(thread), agent)
		if err == nil {
			t.Fatal("accepted nonowned origin")
		}
		var n int
		_ = s.db.QueryRow(`SELECT count(*) FROM dashboards WHERE name=?`, thread).Scan(&n)
		if n != 0 {
			t.Fatal("failed origin left a dashboard")
		}
	}
	external, err := s.Create(ctx, "owner", textSpec("External"), agent)
	if err != nil {
		t.Fatal(err)
	}
	list, _ = s.List(ctx, "owner")
	for _, item := range list {
		if item.ID == external.ID && item.Origin != nil {
			t.Fatal("fabricated origin")
		}
	}
	if _, err = s.db.Exec(`DELETE FROM agui_threads WHERE thread_id='owner-thread'`); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List(ctx, "owner")
	for _, item := range list {
		if item.ID == record.ID && item.Origin != nil {
			t.Fatal("retained deleted private request")
		}
	}
}
func TestOriginRollsBackWithVersionFailureAndCascadesOnDashboardDelete(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	_, err := s.db.Exec(`INSERT INTO agui_threads(thread_id,owner_id) VALUES ('thread','owner')`)
	if err != nil {
		t.Fatal(err)
	}
	origin := BuildOrigin{ThreadID: "thread", MessageID: "user", RequestExcerpt: "Request"}
	_, err = s.db.Exec(`CREATE TRIGGER reject_version BEFORE INSERT ON dashboard_versions BEGIN SELECT RAISE(ABORT,'reject'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create(WithBuildOrigin(ctx, origin), "owner", textSpec("Rollback"), agent)
	if err == nil {
		t.Fatal("version failure accepted")
	}
	for _, table := range []string{"dashboards", "dashboard_origins"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s count=%d err=%v", table, n, err)
		}
	}
	_, _ = s.db.Exec(`DROP TRIGGER reject_version`)
	record, err := s.Create(WithBuildOrigin(ctx, origin), "owner", textSpec("Delete"), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "owner", record.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM dashboard_origins`).Scan(&n)
	if n != 0 {
		t.Fatal("origin did not cascade")
	}
}
func TestBuildOriginContextAndUnicode(t *testing.T) {
	origin := BuildOrigin{ThreadID: "thread", MessageID: "user", RequestExcerpt: strings.Repeat("🐈", 280)}
	if got, ok := BuildOriginFromContext(WithBuildOrigin(t.Context(), origin)); !ok || got != origin {
		t.Fatal(got, ok)
	}
	if _, ok := BuildOriginFromContext(t.Context()); ok {
		t.Fatal("invented origin")
	}
}

func TestBuildOriginPersistsAfterReopeningControlDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sqlite")
	sqlite, err := appstore.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sqlite.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test'); INSERT INTO agui_threads(thread_id,owner_id) VALUES ('source','owner')`)
	if err != nil {
		t.Fatal(err)
	}
	origin := BuildOrigin{ThreadID: "source", MessageID: "user", RequestExcerpt: "Original request"}
	service := New(sqlite.DB, structural{})
	_, err = service.Create(WithBuildOrigin(t.Context(), origin), "owner", textSpec("Persisted"), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Close(); err != nil {
		t.Fatal(err)
	}
	sqlite, err = appstore.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	list, err := New(sqlite.DB, structural{}).List(t.Context(), "owner")
	if err != nil || len(list) != 1 || list[0].Origin == nil || *list[0].Origin != origin {
		t.Fatalf("reopened=%+v %v", list, err)
	}
}

func TestAutomaticDefaultHasNoInventedBuildOrigin(t *testing.T) {
	s := newTestService(t)
	ctx := WithBuildOrigin(t.Context(), BuildOrigin{ThreadID: "missing", MessageID: "user", RequestExcerpt: "Not a dashboard request"})
	list, err := s.List(ctx, "owner")
	if err != nil || len(list) != 1 || list[0].Origin != nil {
		t.Fatalf("default=%+v %v", list, err)
	}
}
