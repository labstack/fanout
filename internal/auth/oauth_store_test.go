package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOAuthStoreAuthorizationCodeIsSingleUse(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("oauth@example.com", "OAuth User", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://localhost:4321/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	raw, err := store.CreateAuthorizationCode(t.Context(), OAuthAuthorizationCode{
		ClientID: client.ClientID, UserID: user.ID,
		RedirectURI: "http://localhost:4321/callback", Scope: MCPScopeTelemetryRead,
		Resource: "https://fanout.example.com/mcp", CodeChallenge: "challenge",
	})
	if err != nil {
		t.Fatalf("CreateAuthorizationCode: %v", err)
	}
	code, err := store.ConsumeAuthorizationCode(t.Context(), raw)
	if err != nil {
		t.Fatalf("ConsumeAuthorizationCode: %v", err)
	}
	if code.ClientID != client.ClientID || code.UserID != user.ID {
		t.Fatalf("consumed code = %#v", code)
	}
	if _, err := store.ConsumeAuthorizationCode(t.Context(), raw); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("second consume = %v, want invalid grant", err)
	}
}

func TestOAuthStoreRejectsRetiredStoredGrants(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("legacy-oauth@example.com", "Legacy OAuth", "viewer", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Legacy client", "", []string{"http://localhost:4321/callback"})
	if err != nil {
		t.Fatal(err)
	}
	resource := "https://fanout.example.com/mcp"
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead+" "+MCPScopeDashboardManage, resource)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "fanout:read fanout:dashboard"
	if _, err := sqlite.DB.Exec(`UPDATE oauth_tokens SET scope = ? WHERE family_id IN (SELECT family_id FROM oauth_tokens WHERE token_hash = ?)`, legacy, oauthHash(pair.AccessToken)); err != nil {
		t.Fatal(err)
	}

	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("retired access grant=%v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, ""); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("retired refresh grant=%v", err)
	}
	var active int
	if err := sqlite.DB.QueryRow(`SELECT count(*) FROM oauth_tokens WHERE revoked_at IS NULL`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("retired token family remained active: %d %v", active, err)
	}
}

func TestOAuthStoreRefreshRotationAndReuseRevokesFamily(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("rotate@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	base := time.Now().UTC()
	store.now = func() time.Time { return base }
	client, err := store.RegisterClient(t.Context(), "Claude Code", "", []string{"http://localhost:8765/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	second, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, "")
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
		t.Fatal("rotation must issue fresh credentials")
	}
	store.now = func() time.Time { return base.Add(OAuthRefreshReuseGrace + time.Second) }
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("reused refresh = %v, want reuse detection", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), second.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("access token after family revocation = %v, want invalid token", err)
	}
}

// Agent processes sharing one stored credential refresh from stale copies;
// the sibling that presents the rotated token gets its own pair within the
// grace window, and only from the same client while the family is live.
func TestOAuthStoreRefreshGraceAdmitsConcurrentSibling(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("sibling@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	base := time.Now().UTC()
	store.now = func() time.Time { return base }
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://127.0.0.1:14550/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	other, err := store.RegisterClient(t.Context(), "Other", "", []string{"http://127.0.0.1:14551/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	second, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, "")
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), other.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("another client's sibling refresh = %v, want invalid grant", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), second.AccessToken, resource); err != nil {
		t.Fatalf("rotated access token = %v", err)
	}
	store.now = func() time.Time { return base.Add(OAuthRefreshReuseGrace) }
	sibling, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, "")
	if err != nil {
		t.Fatalf("sibling refresh within grace = %v", err)
	}
	if sibling.RefreshToken == second.RefreshToken || sibling.AccessToken == second.AccessToken {
		t.Fatal("a sibling must receive its own credentials")
	}
	if _, err := store.VerifyAccessToken(t.Context(), sibling.AccessToken, resource); err != nil {
		t.Fatalf("sibling access token = %v", err)
	}
	// The sibling refresh must not move the rotation time and extend the window.
	store.now = func() time.Time { return base.Add(OAuthRefreshReuseGrace + time.Second) }
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("refresh after grace = %v, want reuse detection", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), sibling.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("sibling access token after reuse = %v, want invalid token", err)
	}
}

// A clock stepped back after a rotation must not stretch the window: the
// rotation is then dated in the future, which counts as reuse.
func TestOAuthStoreRefreshGraceRejectsFutureRotation(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("clock@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	base := time.Now().UTC()
	store.now = func() time.Time { return base }
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://127.0.0.1:14550/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	second, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, "")
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	store.now = func() time.Time { return base.Add(-time.Hour) }
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("refresh after a clock step back = %v, want reuse detection", err)
	}
	store.now = func() time.Time { return base }
	if _, err := store.VerifyAccessToken(t.Context(), second.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("access token after rejected replay = %v, want family revoked", err)
	}
}

// A rotated token replayed just after it expires, though within the grace
// window, is reuse: the family is revoked rather than left live.
func TestOAuthStoreRefreshGraceRevokesExpiredReplay(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("expiring@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	base := time.Now().UTC()
	store.now = func() time.Time { return base }
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://127.0.0.1:14550/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	rotatedAt := base.Add(OAuthRefreshTTL - 30*time.Second)
	store.now = func() time.Time { return rotatedAt }
	second, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, "")
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	store.now = func() time.Time { return base.Add(OAuthRefreshTTL + time.Second) }
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("expired replay within grace = %v, want reuse detection", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), second.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("descendant access token = %v, want family revoked", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, second.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("descendant refresh token = %v, want family revoked", err)
	}
}

// A revoked family has no live sibling, so the grace window never revives it.
func TestOAuthStoreRefreshGraceDoesNotReviveRevokedFamily(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("revoked-sibling@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://127.0.0.1:14550/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if err := users.RevokeAllSessionsWithAudit(user.ID, AuditEvent{EventType: "session.revoked", Outcome: "success"}); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("sibling refresh of a revoked family = %v, want reuse detection", err)
	}
}

func TestRevokeAllSessionsAlsoRevokesOAuthCredentials(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("incident@example.com", "", "operator", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Incident client", "", []string{"http://localhost/callback"})
	if err != nil {
		t.Fatal(err)
	}
	resource := "https://fanout.example.com/mcp"
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatal(err)
	}
	code, err := store.CreateAuthorizationCode(t.Context(), OAuthAuthorizationCode{
		ClientID: client.ClientID, UserID: user.ID, RedirectURI: "http://localhost/callback",
		Scope: MCPScopeTelemetryRead, Resource: resource, CodeChallenge: "challenge",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := users.RevokeAllSessionsWithAudit(user.ID, AuditEvent{EventType: "session.revoked", Outcome: "success"}); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("replayed access token = %v, want invalid token", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("replayed refresh token = %v, want reuse detection", err)
	}
	if _, err := store.ConsumeAuthorizationCode(t.Context(), code); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("pending authorization code after revocation = %v, want invalid grant", err)
	}
}

func TestOAuthStoreRejectsWrongAudienceAndSuspendedRefresh(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("suspended-oauth@example.com", "", "operator", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://localhost:9876/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, "https://other.example/mcp"); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("wrong audience = %v, want invalid token", err)
	}
	status := UserStatusSuspended
	if _, err := users.UpdateWithAudit(user.ID, nil, nil, nil, &status, AuditEvent{EventType: "user.updated", Outcome: "success"}); err != nil {
		t.Fatalf("suspend user: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, ""); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("suspended user refresh = %v, want invalid grant", err)
	}
}

// Replaying a rotated refresh token AFTER it has also expired is still
// evidence of theft: reuse detection must run before the expiry check and
// revoke the whole family instead of returning a plain invalid grant.
func TestOAuthStoreReuseDetectionRunsBeforeExpiryCheck(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("late-reuse@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	base := time.Now().UTC()
	store.now = func() time.Time { return base }
	client, err := store.RegisterClient(t.Context(), "Client", "", []string{"http://localhost:2222/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	store.now = func() time.Time { return base.Add(OAuthRefreshTTL + time.Hour) }
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("expired reused refresh = %v, want reuse detection", err)
	}
	var live int
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE revoked_at IS NULL`).Scan(&live); err != nil {
		t.Fatalf("count live tokens: %v", err)
	}
	if live != 0 {
		t.Fatalf("family not fully revoked after late reuse: %d live rows", live)
	}
}

// A transient DB failure during rotation must surface as an infrastructure
// error — not invalid_grant — and must NOT revoke the token family.
func TestOAuthStoreRotateDBErrorDoesNotRevokeFamily(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("db-hiccup@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Client", "", []string{"http://localhost:3333/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	// Simulate an infrastructure failure on the user-status read.
	if _, err := sqlite.DB.Exec(`ALTER TABLE users RENAME TO users_offline`); err != nil {
		t.Fatalf("hide users table: %v", err)
	}
	_, err = store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, "")
	if err == nil || errors.Is(err, ErrInvalidOAuthGrant) || errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("rotate during DB failure = %v, want wrapped infrastructure error", err)
	}

	// After the DB recovers the same refresh token must still rotate: the
	// failure must not have revoked the token or its family.
	if _, err := sqlite.DB.Exec(`ALTER TABLE users_offline RENAME TO users`); err != nil {
		t.Fatalf("restore users table: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, ""); err != nil {
		t.Fatalf("rotate after DB recovery = %v, want success", err)
	}
}

// VerifyAccessToken must distinguish "the token is bad" from "the read
// failed": only the former is ErrInvalidOAuthToken.
func TestOAuthStoreVerifyAccessTokenDBErrorIsNotInvalidToken(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("verify-hiccup@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Client", "", []string{"http://localhost:4444/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	if _, err := sqlite.DB.Exec(`ALTER TABLE oauth_tokens RENAME TO oauth_tokens_offline`); err != nil {
		t.Fatalf("hide tokens table: %v", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, resource); err == nil || errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("verify during DB failure = %v, want wrapped infrastructure error", err)
	}
	if _, err := sqlite.DB.Exec(`ALTER TABLE oauth_tokens_offline RENAME TO oauth_tokens`); err != nil {
		t.Fatalf("restore tokens table: %v", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, resource); err != nil {
		t.Fatalf("verify after DB recovery = %v, want success", err)
	}
}

func TestOAuthStoreCleanupExpired(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("cleanup@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	base := time.Now().UTC()
	store.now = func() time.Time { return base }
	client, err := store.RegisterClient(t.Context(), "Client", "", []string{"http://localhost:5555/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	if _, err := store.CreateAuthorizationCode(t.Context(), OAuthAuthorizationCode{
		ClientID: client.ClientID, UserID: user.ID, RedirectURI: "http://localhost:5555/callback",
		Scope: MCPScopeTelemetryRead, Resource: resource, CodeChallenge: "challenge",
	}); err != nil {
		t.Fatalf("CreateAuthorizationCode: %v", err)
	}
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	// Nothing is collectible yet: the code is unexpired, the family still has
	// a live refresh token (so the rotated-and-revoked row is retained for
	// reuse detection), and the client is younger than the GC age.
	deleted, err := store.CleanupExpired(t.Context(), base)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("early cleanup deleted %d rows, want 0", deleted)
	}
	// The revoked row survived, so reuse detection still fires after the grace window.
	store.now = func() time.Time { return base.Add(OAuthRefreshReuseGrace + time.Second) }
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("reuse after cleanup = %v, want reuse detection", err)
	}

	// Once the family and code are fully dead and the client has aged out
	// with no remaining state, everything is collected:
	// 1 code + 4 token rows (two pairs) + 1 client.
	deleted, err = store.CleanupExpired(t.Context(), base.Add(OAuthRefreshTTL+time.Hour))
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if deleted != 6 {
		t.Fatalf("late cleanup deleted %d rows, want 6", deleted)
	}
	for _, table := range []string{"oauth_authorization_codes", "oauth_tokens", "oauth_clients"} {
		var n int
		if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s still holds %d rows after cleanup", table, n)
		}
	}
}

func TestOAuthStoreCleanupKeepsLiveFamiliesAndActiveClients(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("cleanup-live@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	base := time.Now().UTC()
	store.now = func() time.Time { return base }
	client, err := store.RegisterClient(t.Context(), "Client", "", []string{"http://localhost:6666/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://fanout.example.com/mcp"
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	// Past the access TTL and the client GC age, but well within the refresh
	// TTL: the expired access row stays (its family is live) and the client
	// stays (tokens still reference it).
	at := base.Add(oauthClientGCAge + time.Hour)
	deleted, err := store.CleanupExpired(t.Context(), at)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("cleanup of a live family deleted %d rows, want 0", deleted)
	}
	if _, err := store.GetClient(t.Context(), client.ClientID); err != nil {
		t.Fatalf("client with live tokens was collected: %v", err)
	}
	store.now = func() time.Time { return at }
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, ""); err != nil {
		t.Fatalf("rotate after cleanup = %v, want success", err)
	}
}

func TestOAuthTokenPairStringRedactsSecrets(t *testing.T) {
	pair := OAuthTokenPair{AccessToken: "foa_secret", RefreshToken: "for_secret", ExpiresIn: 900, Scope: MCPScopeTelemetryRead}
	got := fmt.Sprintf("%v", pair)
	if strings.Contains(got, "foa_secret") || strings.Contains(got, "for_secret") {
		t.Fatalf("String() leaked token material: %s", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Fatalf("String() = %s, want redaction marker", got)
	}
}

func TestOAuthStoreExpiredCodeFails(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, _ := users.CreateWithAudit("expired@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	store := NewOAuthStore(sqlite.DB)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	client, _ := store.RegisterClient(t.Context(), "Client", "", []string{"http://localhost:1111/callback"})
	raw, err := store.CreateAuthorizationCode(t.Context(), OAuthAuthorizationCode{
		ClientID: client.ClientID, UserID: user.ID, RedirectURI: "http://localhost:1111/callback",
		Scope: MCPScopeTelemetryRead, Resource: "https://fanout.example.com/mcp", CodeChallenge: "challenge",
	})
	if err != nil {
		t.Fatalf("CreateAuthorizationCode: %v", err)
	}
	store.now = func() time.Time { return now.Add(OAuthCodeTTL + time.Second) }
	if _, err := store.ConsumeAuthorizationCode(t.Context(), raw); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("expired code = %v, want invalid grant", err)
	}
}

func TestOAuthConnectionsListLiveFamilies(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("connections@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.CreateWithAudit("other-connections@example.com", "", "viewer", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewOAuthStore(sqlite.DB)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	register := func(name string) OAuthClient {
		t.Helper()
		client, err := store.RegisterClient(t.Context(), name, "", []string{"http://localhost/callback"})
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	issue := func(client OAuthClient, owner User) OAuthTokenPair {
		t.Helper()
		pair, err := store.IssueTokenPair(t.Context(), client.ClientID, owner.ID, MCPScopeTelemetryRead+" "+MCPScopeDashboardManage, "https://fanout.example.com/mcp")
		if err != nil {
			t.Fatal(err)
		}
		return pair
	}
	client := register("Codex")
	issue(client, user)
	if _, err := sqlite.DB.Exec(`UPDATE oauth_tokens SET revoked_at = ? WHERE client_id = ?`, now.Unix(), client.ClientID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	connected := now
	first := issue(client, user)
	now = now.Add(time.Hour)
	issue(client, user)
	now = now.Add(time.Hour)
	newest := register("Claude Code")
	issue(newest, user)
	now = now.Add(time.Hour)
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, "https://fanout.example.com/mcp", MCPScopeTelemetryRead); err != nil {
		t.Fatal(err)
	}
	expired := register("Expired")
	issue(expired, user)
	if _, err := sqlite.DB.Exec(`UPDATE oauth_tokens SET expires_at = ? WHERE client_id = ? AND kind = 'refresh'`, now.Unix(), expired.ClientID); err != nil {
		t.Fatal(err)
	}
	revoked := register("Revoked")
	issue(revoked, user)
	if _, err := sqlite.DB.Exec(`UPDATE oauth_tokens SET revoked_at = ? WHERE client_id = ? AND kind = 'refresh'`, now.Unix(), revoked.ClientID); err != nil {
		t.Fatal(err)
	}
	otherClient := register("Other user's agent")
	issue(otherClient, other)
	issue(client, other)
	unused := register("Unused")

	connections, err := store.ListConnections(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 2 || connections[0].ClientID != newest.ClientID || connections[1].ClientID != client.ClientID {
		t.Fatalf("connections = %+v", connections)
	}
	got := connections[1]
	if got.ClientName != "Codex" || !got.ConnectedAt.Equal(connected) || !got.RefreshedAt.Equal(now) || got.Scope != MCPScopeTelemetryRead {
		t.Fatalf("connection = %+v", got)
	}
	for _, clientID := range []string{expired.ClientID, revoked.ClientID, unused.ClientID, otherClient.ClientID, "unknown"} {
		if err := store.RevokeConnection(t.Context(), user.ID, clientID, AuditEvent{}); !errors.Is(err, ErrOAuthConnectionNotFound) {
			t.Fatalf("non-live connection revoke = %v", err)
		}
	}
	connections, err = store.ListConnections(t.Context(), "unknown")
	if err != nil || connections == nil || len(connections) != 0 {
		t.Fatalf("empty connections = %+v, %v", connections, err)
	}
}

func TestOAuthConnectionRevocationIsScopedAndImmediate(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("revoke-connection@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.CreateWithAudit("other-revoke@example.com", "", "viewer", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://localhost/callback"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RegisterClient(t.Context(), "Claude Code", "", []string{"http://localhost/callback"})
	if err != nil {
		t.Fatal(err)
	}
	issue := func(clientID, userID string) OAuthTokenPair {
		t.Helper()
		pair, err := store.IssueTokenPair(t.Context(), clientID, userID, MCPScopeTelemetryRead, "https://fanout.example.com/mcp")
		if err != nil {
			t.Fatal(err)
		}
		return pair
	}
	pair := issue(client.ClientID, user.ID)
	sibling, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, "https://fanout.example.com/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	family := issue(client.ClientID, user.ID)
	otherUser := issue(client.ClientID, other.ID)
	otherClient := issue(second.ClientID, user.ID)
	code := func(userID string) string {
		t.Helper()
		raw, err := store.CreateAuthorizationCode(t.Context(), OAuthAuthorizationCode{
			ClientID: client.ClientID, UserID: userID, RedirectURI: "http://localhost/callback",
			Scope: MCPScopeTelemetryRead, Resource: "https://fanout.example.com/mcp", CodeChallenge: "challenge",
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	pendingCode, otherUserCode := code(user.ID), code(other.ID)
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, "https://fanout.example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeConnection(t.Context(), user.ID, client.ClientID, AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{pair.AccessToken, sibling.AccessToken, family.AccessToken} {
		if _, err := store.VerifyAccessToken(t.Context(), token, "https://fanout.example.com/mcp"); !errors.Is(err, ErrInvalidOAuthToken) {
			t.Fatalf("revoked access = %v", err)
		}
	}
	for _, token := range []string{pair.RefreshToken, sibling.RefreshToken, family.RefreshToken} {
		if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, token, "https://fanout.example.com/mcp", ""); err == nil {
			t.Fatal("disconnected refresh token accepted")
		}
	}
	for _, token := range []string{otherUser.AccessToken, otherClient.AccessToken} {
		if _, err := store.VerifyAccessToken(t.Context(), token, "https://fanout.example.com/mcp"); err != nil {
			t.Fatalf("unrelated access = %v", err)
		}
	}
	if _, err := store.ConsumeAuthorizationCode(t.Context(), pendingCode); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("pending code after disconnect = %v, want invalid grant", err)
	}
	if _, err := store.ConsumeAuthorizationCode(t.Context(), otherUserCode); err != nil {
		t.Fatalf("other user's pending code = %v", err)
	}
	var live int
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE client_id = ? AND user_id = ? AND revoked_at IS NULL`, client.ClientID, user.ID).Scan(&live); err != nil || live != 0 {
		t.Fatalf("unrevoked tokens = %d, %v", live, err)
	}
	if err := store.RevokeConnection(t.Context(), user.ID, client.ClientID, AuditEvent{}); !errors.Is(err, ErrOAuthConnectionNotFound) {
		t.Fatalf("second revoke = %v", err)
	}
	if err := store.RevokeConnection(t.Context(), "unknown", second.ClientID, AuditEvent{}); !errors.Is(err, ErrOAuthConnectionNotFound) {
		t.Fatalf("other user's connection = %v", err)
	}
	if err := store.RevokeConnection(t.Context(), user.ID, "unknown", AuditEvent{}); !errors.Is(err, ErrOAuthConnectionNotFound) {
		t.Fatalf("unknown connection = %v", err)
	}
	var events int
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'oauth_connection.revoked' AND actor_user_id = ? AND target_type = 'oauth_client' AND target_id = ? AND json_extract(metadata_json, '$.client_name') = 'Codex'`, user.ID, client.ClientID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("revocation audits = %d, %v", events, err)
	}
}

func TestOAuthConnectionRevocationRollsBackWithAuditFailure(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("rollback-connection@example.com", "", "admin", AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://localhost/callback"})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, MCPScopeTelemetryRead, "https://fanout.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.DB.Exec(`CREATE TRIGGER reject_connection_audit BEFORE INSERT ON auth_audit_events BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeConnection(t.Context(), user.ID, client.ClientID, AuditEvent{}); err == nil {
		t.Fatal("audit failure must fail revocation")
	}
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, "https://fanout.example.com/mcp"); err != nil {
		t.Fatalf("rolled-back access = %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, "https://fanout.example.com/mcp", ""); err != nil {
		t.Fatalf("rolled-back refresh = %v", err)
	}
}
