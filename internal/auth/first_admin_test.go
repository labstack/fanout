package auth

import (
	"context"
	"testing"
)

func TestDesignateFirstAdminCreatesActiveAdministratorInEmptyDatabase(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)

	user, outcome, err := users.DesignateFirstAdmin("  First@Example.com ")
	if err != nil {
		t.Fatalf("DesignateFirstAdmin: %v", err)
	}
	if outcome != FirstAdminCreated {
		t.Fatalf("outcome = %v, want FirstAdminCreated", outcome)
	}
	if user.Email != "first@example.com" || user.Role != RoleAdmin || user.Status != UserStatusActive {
		t.Fatalf("firstAdmin = %+v, want active admin first@example.com", user)
	}
	var source string
	if err := sqlite.DB.QueryRowContext(context.Background(),
		`SELECT json_extract(metadata_json, '$.source') FROM auth_audit_events WHERE event_type = 'setup.completed' AND target_id = ?`, user.ID,
	).Scan(&source); err != nil {
		t.Fatalf("setup audit event: %v", err)
	}
	if source != "first_admin_email" {
		t.Fatalf("audit source = %q, want first_admin_email", source)
	}
}

func TestDesignateFirstAdminIsIdempotentForTheSameAddress(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	created, _, err := users.DesignateFirstAdmin("first@example.com")
	if err != nil {
		t.Fatalf("first DesignateFirstAdmin: %v", err)
	}
	again, outcome, err := users.DesignateFirstAdmin("FIRST@example.com")
	if err != nil {
		t.Fatalf("second DesignateFirstAdmin: %v", err)
	}
	if outcome != FirstAdminPresent || again.ID != created.ID {
		t.Fatalf("outcome = %v user = %+v, want FirstAdminPresent for %s", outcome, again, created.ID)
	}
	if count, _ := users.CountUsers(); count != 1 {
		t.Fatalf("users = %d, want 1", count)
	}
}

func TestDesignateFirstAdminRefusesADifferentAddress(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	if _, err := users.CreateFirstAdminWithAudit("admin@example.com", "", AuditEvent{EventType: "setup.completed", Outcome: "success"}); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	refused, outcome, err := users.DesignateFirstAdmin(" First@Example.com ")
	if err != nil {
		t.Fatalf("DesignateFirstAdmin: %v", err)
	}
	if outcome != FirstAdminRefused || refused.ID != "" || refused.Email != "first@example.com" {
		t.Fatalf("outcome = %v user = %+v, want FirstAdminRefused reporting first@example.com", outcome, refused)
	}
	if _, err := users.GetByEmail("first@example.com"); err != ErrUserNotFound {
		t.Fatalf("GetByEmail(firstAdmin) err = %v, want ErrUserNotFound", err)
	}
	if count, _ := users.CountUsers(); count != 1 {
		t.Fatalf("users = %d, want 1", count)
	}
}

func TestDesignateFirstAdminLeavesAnExistingAccountUnchanged(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	if _, err := users.CreateFirstAdminWithAudit("admin@example.com", "", AuditEvent{EventType: "setup.completed", Outcome: "success"}); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	viewer, err := users.CreateWithAudit("first@example.com", "", RoleViewer, AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create viewer: %v", err)
	}
	user, outcome, err := users.DesignateFirstAdmin("first@example.com")
	if err != nil {
		t.Fatalf("DesignateFirstAdmin: %v", err)
	}
	if outcome != FirstAdminPresent || user.ID != viewer.ID || user.Role != RoleViewer {
		t.Fatalf("outcome = %v user = %+v, want unchanged viewer", outcome, user)
	}
}

func TestDesignateFirstAdminRejectsInvalidEmail(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	if _, outcome, err := users.DesignateFirstAdmin("not-an-email"); err == nil || outcome != FirstAdminUnknown {
		t.Fatalf("DesignateFirstAdmin = (%v, %v), want FirstAdminUnknown with an error", outcome, err)
	}
	if count, _ := users.CountUsers(); count != 0 {
		t.Fatalf("users = %d, want 0", count)
	}
}
