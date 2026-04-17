package auth

import (
	"testing"

	appstore "github.com/labstack/fanout/internal/store"
)

func newTestUserStore(t *testing.T) *UserStore {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return NewUserStore(sqlite.DB)
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

func TestUserStore_EnsureAdmin(t *testing.T) {
	s := newTestUserStore(t)

	if err := s.EnsureAdmin("admin@example.com"); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}

	u, err := s.GetByEmail("admin@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if u.Role != "admin" {
		t.Errorf("role = %q, want admin", u.Role)
	}

	// Idempotent
	if err := s.EnsureAdmin("admin@example.com"); err != nil {
		t.Fatalf("EnsureAdmin (second): %v", err)
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
