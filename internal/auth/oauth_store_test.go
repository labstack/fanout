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
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("reused refresh = %v, want reuse detection", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), second.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("access token after family revocation = %v, want invalid token", err)
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
	if err := users.RevokeAllSessionsWithAudit(user.ID, AuditEvent{EventType: "session.revoked", Outcome: "success"}); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("replayed access token = %v, want invalid token", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, ""); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("replayed refresh token = %v, want reuse detection", err)
	}
}

func TestOAuthStoreRejectsWrongAudienceAndInactiveRefresh(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.CreateWithAudit("inactive-oauth@example.com", "", "operator", AuditEvent{EventType: "user.created", Outcome: "success"})
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
	active := false
	if _, err := users.UpdateWithAudit(user.ID, nil, nil, nil, &active, AuditEvent{EventType: "user.updated", Outcome: "success"}); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource, ""); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("inactive user refresh = %v, want invalid grant", err)
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

	// Simulate an infrastructure failure on the user-active read.
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
	// The revoked row survived, so reuse detection still fires.
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
