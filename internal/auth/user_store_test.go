package auth

import (
	"errors"
	"testing"
	"time"

	appstore "github.com/labstack/fanout/internal/store"
)

func newTestSQLite(t *testing.T) *appstore.SQLite {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return sqlite
}

func TestAuthorizationMutationRollsBackWhenAuditWriteFails(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer db.Close()
	users := NewUserStore(db.DB)
	user, err := users.Create("audit@example.com", "", "viewer")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO sessions (token_hash, user_id, data, created_at, last_activity_at, absolute_expires_at) VALUES ('audit-token', ?, X'00', 1, 1, 9999999999)`, user.ID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := db.DB.Exec(`ALTER TABLE auth_audit_events RENAME TO auth_audit_events_offline`); err != nil {
		t.Fatalf("hide audit table: %v", err)
	}
	role := RoleOperator
	if _, err := users.UpdateWithAudit(user.ID, nil, nil, &role, nil, AuditEvent{EventType: "role.changed", Outcome: "success"}); err == nil {
		t.Fatal("UpdateWithAudit succeeded without audit table")
	}
	unchanged, err := users.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if unchanged.Role != "viewer" || unchanged.AuthVersion != user.AuthVersion {
		t.Fatalf("mutation was not rolled back: %+v", unchanged)
	}
	var sessions int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, user.ID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 1 {
		t.Fatalf("session revocation was not rolled back: rows=%d", sessions)
	}
}

func newTestUserStore(t *testing.T) *UserStore {
	t.Helper()
	return NewUserStore(newTestSQLite(t).DB)
}

func TestUserAndIdentityConflictsAreTyped(t *testing.T) {
	db := newTestSQLite(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("conflict@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create("conflict@example.com", "", "viewer"); !errors.Is(err, ErrUserConflict) {
		t.Fatalf("duplicate user = %v, want ErrUserConflict", err)
	}
	identities := NewIdentityStore(db.DB)
	audit := NewAuditStore(db.DB)
	event := AuditEvent{EventType: "identity.linked", Outcome: "success"}
	if _, err := identities.LinkWithAudit(t.Context(), user.ID, "https://issuer.example", "subject", user.Email, audit, event); err != nil {
		t.Fatal(err)
	}
	if _, err := identities.LinkWithAudit(t.Context(), user.ID, "https://issuer.example", "subject", user.Email, audit, event); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("duplicate identity = %v, want ErrIdentityConflict", err)
	}
}

func TestUserInfrastructureFailureIsNotConflict(t *testing.T) {
	db := newTestSQLite(t)
	users := NewUserStore(db.DB)
	if _, err := db.DB.Exec("ALTER TABLE users RENAME TO users_offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create("infra@example.com", "", "viewer"); err == nil || errors.Is(err, ErrUserConflict) {
		t.Fatalf("infrastructure failure = %v, want non-conflict error", err)
	}
}

func TestUserStore_CreateAndGet(t *testing.T) {
	s := newTestUserStore(t)

	u, err := s.Create("test@example.com", "Test User", "operator")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.Email != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", u.Email)
	}
	if u.Role != "operator" {
		t.Errorf("role = %q, want operator", u.Role)
	}

	got, err := s.GetByEmail("test@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("id mismatch")
	}
}

func TestUserStore_List(t *testing.T) {
	s := newTestUserStore(t)
	s.Create("a@example.com", "", "viewer")
	s.Create("b@example.com", "", "operator")

	users, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("len = %d, want 2", len(users))
	}
}

func TestUserStore_Update(t *testing.T) {
	s := newTestUserStore(t)
	u, _ := s.Create("up@example.com", "", "viewer")

	newRole := RoleAdmin
	updated, err := s.Update(u.ID, nil, nil, &newRole, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Role != "admin" {
		t.Errorf("role = %q, want admin", updated.Role)
	}
}

func TestUserStore_Delete(t *testing.T) {
	s := newTestUserStore(t)
	u, _ := s.Create("del@example.com", "", "viewer")

	if err := s.Delete(u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := s.GetByID(u.ID)
	if err == nil {
		t.Fatal("expected ErrUserNotFound after delete")
	}
}

func TestUserStore_CreateFirstAdmin(t *testing.T) {
	s := newTestUserStore(t)

	user, err := s.CreateFirstAdmin("admin@example.com", "Admin")
	if err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("role = %q, want admin", user.Role)
	}

	_, err = s.CreateFirstAdmin("other@example.com", "Other")
	if !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("second CreateFirstAdmin error = %v, want ErrSetupComplete", err)
	}
}

func TestUserTimestampsUseOneSortableFormat(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.Create("timestamps@example.com", "", RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]string{"created": user.CreatedAt, "updated": user.UpdatedAt} {
		if _, err := time.Parse(userTimestampLayout, value); err != nil {
			t.Fatalf("%s timestamp %q: %v", label, value, err)
		}
	}
	loginAt := time.Date(2026, 7, 22, 12, 34, 56, 123456789, time.UTC)
	if err := users.TouchLoginAt(user.ID, loginAt); err != nil {
		t.Fatal(err)
	}
	touched, err := users.GetByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if touched.LoggedInAt != "2026-07-22T12:34:56.123Z" || touched.UpdatedAt != touched.LoggedInAt {
		t.Fatalf("touch timestamps = logged %q updated %q", touched.LoggedInAt, touched.UpdatedAt)
	}

	if _, err := sqlite.DB.Exec(`INSERT INTO users(id,email,role,active) VALUES(?,?,?,1)`, "default-time", "default-time@example.com", "viewer"); err != nil {
		t.Fatal(err)
	}
	var created, updated string
	if err := sqlite.DB.QueryRow(`SELECT created_at, updated_at FROM users WHERE id = ?`, "default-time").Scan(&created, &updated); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(userTimestampLayout, created); err != nil {
		t.Fatalf("schema created timestamp %q: %v", created, err)
	}
	if _, err := time.Parse(userTimestampLayout, updated); err != nil {
		t.Fatalf("schema updated timestamp %q: %v", updated, err)
	}
}
