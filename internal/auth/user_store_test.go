package auth

import (
	"errors"
	"strings"
	"testing"

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

func newTestUserStore(t *testing.T) *UserStore {
	t.Helper()
	return NewUserStore(newTestSQLite(t).DB)
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

	newRole := "admin"
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

func TestUserStore_APIKeyLifecycle(t *testing.T) {
	s := newTestUserStore(t)
	u, _ := s.Create("key@example.com", "", "operator")

	key, err := s.GenerateAPIKey(u.ID)
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	// Keys share the "fo_" namespace with the ingest token (not monk's "mk_").
	if !strings.HasPrefix(key, "fo_") {
		t.Errorf("key = %q, want fo_-prefixed value", key)
	}

	got, err := s.GetByAPIKey(key)
	if err != nil {
		t.Fatalf("GetByAPIKey: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("GetByAPIKey id = %q, want %q", got.ID, u.ID)
	}

	if err := s.RevokeAPIKey(u.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := s.GetByAPIKey(key); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetByAPIKey after revoke = %v, want ErrUserNotFound", err)
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
