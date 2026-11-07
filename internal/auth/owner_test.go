package auth

import (
	"context"
	"testing"
)

func TestDesignateOwnerCreatesActiveAdministratorInEmptyDatabase(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)

	user, outcome, err := users.DesignateOwner("  Owner@Example.com ")
	if err != nil {
		t.Fatalf("DesignateOwner: %v", err)
	}
	if outcome != OwnerCreated {
		t.Fatalf("outcome = %v, want OwnerCreated", outcome)
	}
	if user.Email != "owner@example.com" || user.Role != RoleAdmin || user.Status != UserStatusActive {
		t.Fatalf("owner = %+v, want active admin owner@example.com", user)
	}
	var source string
	if err := sqlite.DB.QueryRowContext(context.Background(),
		`SELECT json_extract(metadata_json, '$.source') FROM auth_audit_events WHERE event_type = 'setup.completed' AND target_id = ?`, user.ID,
	).Scan(&source); err != nil {
		t.Fatalf("setup audit event: %v", err)
	}
	if source != "owner_email" {
		t.Fatalf("audit source = %q, want owner_email", source)
	}
}

func TestDesignateOwnerIsIdempotentForTheSameOwner(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	created, _, err := users.DesignateOwner("owner@example.com")
	if err != nil {
		t.Fatalf("first DesignateOwner: %v", err)
	}
	again, outcome, err := users.DesignateOwner("OWNER@example.com")
	if err != nil {
		t.Fatalf("second DesignateOwner: %v", err)
	}
	if outcome != OwnerPresent || again.ID != created.ID {
		t.Fatalf("outcome = %v user = %+v, want OwnerPresent for %s", outcome, again, created.ID)
	}
	if count, _ := users.CountUsers(); count != 1 {
		t.Fatalf("users = %d, want 1", count)
	}
}

func TestDesignateOwnerRefusesASecondOwner(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	if _, err := users.CreateFirstAdminWithAudit("admin@example.com", "", AuditEvent{EventType: "setup.completed", Outcome: "success"}); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	refused, outcome, err := users.DesignateOwner(" Owner@Example.com ")
	if err != nil {
		t.Fatalf("DesignateOwner: %v", err)
	}
	if outcome != OwnerRefused || refused.ID != "" || refused.Email != "owner@example.com" {
		t.Fatalf("outcome = %v user = %+v, want OwnerRefused reporting owner@example.com", outcome, refused)
	}
	if _, err := users.GetByEmail("owner@example.com"); err != ErrUserNotFound {
		t.Fatalf("GetByEmail(owner) err = %v, want ErrUserNotFound", err)
	}
	if count, _ := users.CountUsers(); count != 1 {
		t.Fatalf("users = %d, want 1", count)
	}
}

func TestDesignateOwnerLeavesAnExistingAccountUnchanged(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	if _, err := users.CreateFirstAdminWithAudit("admin@example.com", "", AuditEvent{EventType: "setup.completed", Outcome: "success"}); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	viewer, err := users.CreateWithAudit("owner@example.com", "", RoleViewer, AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create viewer: %v", err)
	}
	user, outcome, err := users.DesignateOwner("owner@example.com")
	if err != nil {
		t.Fatalf("DesignateOwner: %v", err)
	}
	if outcome != OwnerPresent || user.ID != viewer.ID || user.Role != RoleViewer {
		t.Fatalf("outcome = %v user = %+v, want unchanged viewer", outcome, user)
	}
}

func TestDesignateOwnerRejectsInvalidEmail(t *testing.T) {
	users := NewUserStore(newTestSQLite(t).DB)
	if _, outcome, err := users.DesignateOwner("not-an-email"); err == nil || outcome != OwnerUnknown {
		t.Fatalf("DesignateOwner = (%v, %v), want OwnerUnknown with an error", outcome, err)
	}
	if count, _ := users.CountUsers(); count != 0 {
		t.Fatalf("users = %d, want 0", count)
	}
}
