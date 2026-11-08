package auth

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	appstore "github.com/labstack/fanout/internal/store"
)

const testIssuer = "https://issuer.example"

func provisionFixture(subject, email string, role Role) ProvisionRequest {
	return ProvisionRequest{Issuer: testIssuer, Subject: subject, Email: email, Role: role, Status: UserStatusActive}
}

// fileSQLite returns a database with a real connection pool. The :memory:
// database holds one connection, which would hide transaction races.
func fileSQLite(t *testing.T) *appstore.SQLite {
	t.Helper()
	db, err := appstore.NewSQLite(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func provisionSource() AuditEvent {
	return AuditEvent{RemoteIP: "192.0.2.1", UserAgent: "test", Metadata: map[string]any{"mode": "scim"}}
}

func countRows(t *testing.T, db *appstore.SQLite, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.DB.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return count
}

func TestProvisionFailureAfterAccountInsertLeavesNothing(t *testing.T) {
	failures := map[string]string{
		"identity insert": `CREATE TRIGGER fail_write BEFORE INSERT ON user_identities BEGIN SELECT RAISE(ABORT, 'injected'); END`,
		"audit insert":    `CREATE TRIGGER fail_write BEFORE INSERT ON auth_audit_events WHEN NEW.event_type = 'identity.linked' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	}
	for name, trigger := range failures {
		t.Run(name, func(t *testing.T) {
			db := fileSQLite(t)
			users := NewUserStore(db.DB)
			if _, err := db.DB.Exec(trigger); err != nil {
				t.Fatalf("install trigger: %v", err)
			}
			_, _, _, err := users.ProvisionFederated(t.Context(), provisionFixture("subject-1", "owner@example.com", RoleAdmin), provisionSource())
			if err == nil {
				t.Fatal("provisioning succeeded despite the injected failure")
			}
			if got := countRows(t, db, `SELECT COUNT(*) FROM users`); got != 0 {
				t.Fatalf("users after rollback = %d, want 0", got)
			}
			if got := countRows(t, db, `SELECT COUNT(*) FROM user_identities`); got != 0 {
				t.Fatalf("identities after rollback = %d, want 0", got)
			}
			if got := countRows(t, db, `SELECT COUNT(*) FROM auth_audit_events`); got != 0 {
				t.Fatalf("audit events after rollback = %d, want 0", got)
			}
			if _, err := db.DB.Exec(`DROP TRIGGER fail_write`); err != nil {
				t.Fatalf("drop trigger: %v", err)
			}
			if _, _, created, err := users.ProvisionFederated(t.Context(), provisionFixture("subject-1", "owner@example.com", RoleAdmin), provisionSource()); err != nil || !created {
				t.Fatalf("retry after failure: created=%v err=%v", created, err)
			}
		})
	}
}

func TestProvisionRetryReturnsExistingAccountUnchanged(t *testing.T) {
	db := fileSQLite(t)
	users := NewUserStore(db.DB)
	first, identity, created, err := users.ProvisionFederated(t.Context(), provisionFixture("subject-1", "owner@example.com", RoleAdmin), provisionSource())
	if err != nil || !created || identity.UserID != first.ID {
		t.Fatalf("first provision: created=%v identity=%+v err=%v", created, identity, err)
	}
	if identity.LastLoginAt != "" {
		t.Fatalf("a provisioned identity has not signed in, got last login %q", identity.LastLoginAt)
	}
	// A delayed retry carrying stale values must not overwrite the account.
	stale := provisionFixture("subject-1", "old@example.com", RoleViewer)
	stale.Status = UserStatusSuspended
	again, _, created, err := users.ProvisionFederated(t.Context(), stale, provisionSource())
	if err != nil || created {
		t.Fatalf("retry: created=%v err=%v", created, err)
	}
	if again.ID != first.ID || again.Email != "owner@example.com" || again.Role != RoleAdmin || again.Status != UserStatusActive {
		t.Fatalf("retry changed the account: %+v", again)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'user.provisioned'`); got != 1 {
		t.Fatalf("user.provisioned events = %d, want 1", got)
	}
}

func TestProvisionRefusesEmailHeldByDifferentAccount(t *testing.T) {
	db := fileSQLite(t)
	users := NewUserStore(db.DB)
	local, err := users.CreateWithAudit("taken@example.com", "", RoleAdmin, AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("create local account: %v", err)
	}
	_, _, _, err = users.ProvisionFederated(t.Context(), provisionFixture("subject-1", "taken@example.com", RoleViewer), provisionSource())
	if !errors.Is(err, ErrUserConflict) {
		t.Fatalf("provision over local account = %v, want ErrUserConflict", err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, local.ID); got != 0 {
		t.Fatalf("local account was linked by email: %d identities", got)
	}
}

func TestProvisionOnEmptyDatabaseRequiresActiveAdministrator(t *testing.T) {
	for name, req := range map[string]ProvisionRequest{
		"viewer":          provisionFixture("subject-1", "first@example.com", RoleViewer),
		"operator":        provisionFixture("subject-1", "first@example.com", RoleOperator),
		"suspended admin": {Issuer: testIssuer, Subject: "subject-1", Email: "first@example.com", Role: RoleAdmin, Status: UserStatusSuspended},
	} {
		t.Run(name, func(t *testing.T) {
			db := fileSQLite(t)
			users := NewUserStore(db.DB)
			if _, _, _, err := users.ProvisionFederated(t.Context(), req, provisionSource()); !errors.Is(err, ErrFirstUserMustBeAdmin) {
				t.Fatalf("provision = %v, want ErrFirstUserMustBeAdmin", err)
			}
			if got := countRows(t, db, `SELECT COUNT(*) FROM users`); got != 0 {
				t.Fatalf("users = %d, want 0", got)
			}
		})
	}
}

func TestConcurrentAdministratorsOnEmptyDatabaseAreAllCreated(t *testing.T) {
	db := fileSQLite(t)
	users := NewUserStore(db.DB)
	const requests = 12
	start := make(chan struct{})
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := provisionFixture(fmt.Sprintf("subject-%d", i), fmt.Sprintf("admin-%d@example.com", i), RoleAdmin)
			_, _, created, err := users.ProvisionFederated(context.Background(), req, provisionSource())
			if err == nil && !created {
				err = errors.New("not created")
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent administrator rejected: %v", err)
		}
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM users WHERE role = 'admin'`); got != requests {
		t.Fatalf("administrators = %d, want %d", got, requests)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM user_identities`); got != requests {
		t.Fatalf("identities = %d, want %d", got, requests)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'user.provisioned' AND json_extract(metadata_json, '$.first_user') = 1`); got != 1 {
		t.Fatalf("first-user events = %d, want exactly 1", got)
	}
}

func TestConcurrentRetriesOfOneIdentityCreateOneAccount(t *testing.T) {
	db := fileSQLite(t)
	users := NewUserStore(db.DB)
	const requests = 10
	start := make(chan struct{})
	type outcome struct {
		id      string
		created bool
		err     error
	}
	outcomes := make(chan outcome, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			user, _, created, err := users.ProvisionFederated(context.Background(), provisionFixture("subject-1", "owner@example.com", RoleAdmin), provisionSource())
			outcomes <- outcome{user.ID, created, err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)
	created, ids := 0, map[string]bool{}
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("retry failed: %v", result.err)
		}
		ids[result.id] = true
		if result.created {
			created++
		}
	}
	if created != 1 || len(ids) != 1 {
		t.Fatalf("created = %d across %d accounts, want 1 and 1", created, len(ids))
	}
}

func TestConcurrentRemovalNeverLeavesNoActiveAdministrator(t *testing.T) {
	viewer, suspended := RoleViewer, UserStatusSuspended
	for name, change := range map[string]UserChange{
		"deactivation": {Status: &suspended},
		"demotion":     {Role: &viewer},
	} {
		t.Run(name, func(t *testing.T) {
			db := fileSQLite(t)
			users := NewUserStore(db.DB)
			const admins = 8
			ids := make([]string, admins)
			for i := range admins {
				user, _, _, err := users.ProvisionFederated(t.Context(), provisionFixture(fmt.Sprintf("subject-%d", i), fmt.Sprintf("admin-%d@example.com", i), RoleAdmin), provisionSource())
				if err != nil {
					t.Fatalf("seed administrator: %v", err)
				}
				ids[i] = user.ID
			}
			start := make(chan struct{})
			errs := make(chan error, admins)
			var wg sync.WaitGroup
			for _, id := range ids {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, _, err := users.UpdateProvisioned(id, change, provisionSource())
					errs <- err
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			refused := 0
			for err := range errs {
				switch {
				case errors.Is(err, ErrLastActiveAdmin):
					refused++
				case err != nil:
					t.Fatalf("update failed: %v", err)
				}
			}
			active, err := users.CountActiveAdmins()
			if err != nil {
				t.Fatalf("CountActiveAdmins: %v", err)
			}
			if active != 1 || refused != 1 {
				t.Fatalf("active administrators = %d, refused = %d, want 1 and 1", active, refused)
			}
		})
	}
}

func TestUpdateProvisionedRepeatWritesNothing(t *testing.T) {
	db := fileSQLite(t)
	users := NewUserStore(db.DB)
	if _, _, _, err := users.ProvisionFederated(t.Context(), provisionFixture("owner", "owner@example.com", RoleAdmin), provisionSource()); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	member, _, _, err := users.ProvisionFederated(t.Context(), provisionFixture("member", "member@example.com", RoleViewer), provisionSource())
	if err != nil {
		t.Fatalf("seed member: %v", err)
	}
	role := RoleOperator
	updated, changed, err := users.UpdateProvisioned(member.ID, UserChange{Role: &role}, provisionSource())
	if err != nil || !changed || updated.Role != RoleOperator {
		t.Fatalf("first update: %+v changed=%v err=%v", updated, changed, err)
	}
	version := updated.AuthVersion
	again, changed, err := users.UpdateProvisioned(member.ID, UserChange{Role: &role}, provisionSource())
	if err != nil || changed {
		t.Fatalf("repeat update: changed=%v err=%v", changed, err)
	}
	if again.AuthVersion != version {
		t.Fatalf("repeat bumped the auth version: %d -> %d", version, again.AuthVersion)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'role.changed' AND target_id = ?`, member.ID); got != 1 {
		t.Fatalf("role.changed events = %d, want 1", got)
	}
}

func TestUpdateProvisionedSuspensionRevokesOAuthAndKeepsIdentity(t *testing.T) {
	db := fileSQLite(t)
	users := NewUserStore(db.DB)
	identities := NewIdentityStore(db.DB)
	if _, _, _, err := users.ProvisionFederated(t.Context(), provisionFixture("owner", "owner@example.com", RoleAdmin), provisionSource()); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	member, _, _, err := users.ProvisionFederated(t.Context(), provisionFixture("member", "member@example.com", RoleViewer), provisionSource())
	if err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO oauth_clients (client_id, client_name, redirect_uris_json, grant_types_json, response_types_json, token_endpoint_auth_method, created_at) VALUES ('client-1', 'Client', '[]', '[]', '[]', 'none', 1)`); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO oauth_authorization_codes (code_hash, client_id, user_id, redirect_uri, scope, resource, code_challenge, expires_at, created_at) VALUES ('code-1', 'client-1', ?, 'r', 's', 'res', 'c', 9999999999, 1)`, member.ID); err != nil {
		t.Fatalf("insert code: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO oauth_tokens (token_hash, kind, client_id, user_id, scope, resource, expires_at, created_at, family_id) VALUES ('hash-1', 'access', 'client-1', ?, 's', 'res', 9999999999, 1, 'family-1')`, member.ID); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	suspended := UserStatusSuspended
	if _, _, err := users.UpdateProvisioned(member.ID, UserChange{Status: &suspended}, provisionSource()); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM oauth_tokens WHERE user_id = ? AND revoked_at IS NOT NULL`, member.ID); got != 1 {
		t.Fatalf("revoked tokens = %d, want 1", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM oauth_authorization_codes WHERE user_id = ?`, member.ID); got != 0 {
		t.Fatalf("authorization codes = %d, want 0", got)
	}

	active := UserStatusActive
	if _, _, err := users.UpdateProvisioned(member.ID, UserChange{Status: &active}, provisionSource()); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	email := "renamed@example.com"
	if _, _, err := users.UpdateProvisioned(member.ID, UserChange{Email: &email}, provisionSource()); err != nil {
		t.Fatalf("email change: %v", err)
	}
	identity, err := identities.Find(t.Context(), testIssuer, "member")
	if err != nil || identity.UserID != member.ID {
		t.Fatalf("identity after reactivation and email change: %+v err=%v", identity, err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'user.reactivated' AND target_id = ?`, member.ID); got != 1 {
		t.Fatalf("user.reactivated events = %d, want 1", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM oauth_tokens WHERE user_id = ? AND revoked_at IS NULL`, member.ID); got != 0 {
		t.Fatalf("live tokens after reactivation = %d, want 0", got)
	}
}

func TestAdministratorSuspensionRevokesOAuth(t *testing.T) {
	db := fileSQLite(t)
	users := NewUserStore(db.DB)
	user, err := users.CreateWithAudit("member@example.com", "", RoleViewer, AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO oauth_clients (client_id, client_name, redirect_uris_json, grant_types_json, response_types_json, token_endpoint_auth_method, created_at) VALUES ('client-1', 'Client', '[]', '[]', '[]', 'none', 1)`); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO oauth_tokens (token_hash, kind, client_id, user_id, scope, resource, expires_at, created_at, family_id) VALUES ('hash-1', 'access', 'client-1', ?, 's', 'res', 9999999999, 1, 'family-1')`, user.ID); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	status := UserStatusSuspended
	if _, err := users.UpdateWithAudit(user.ID, nil, nil, nil, &status, AuditEvent{EventType: "user.suspended", Outcome: "success"}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM oauth_tokens WHERE user_id = ? AND revoked_at IS NULL`, user.ID); got != 0 {
		t.Fatalf("live tokens = %d, want 0", got)
	}
}
