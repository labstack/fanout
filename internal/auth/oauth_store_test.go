package auth

import (
	"errors"
	"testing"
	"time"
)

func TestOAuthStoreAuthorizationCodeIsSingleUse(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.Create("oauth@example.com", "OAuth User", "admin")
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
		RedirectURI: "http://localhost:4321/callback", Scope: "fanout:read",
		Resource: "https://demo.fanout.test/mcp", CodeChallenge: "challenge",
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

func TestOAuthStoreRefreshRotationAndReuseRevokesFamily(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.Create("rotate@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Claude Code", "", []string{"http://localhost:8765/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://demo.fanout.test/mcp"
	first, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, "fanout:read", resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	second, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource)
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
		t.Fatal("rotation must issue fresh credentials")
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, first.RefreshToken, resource); !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("reused refresh = %v, want reuse detection", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), second.AccessToken, resource); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("access token after family revocation = %v, want invalid token", err)
	}
}

func TestOAuthStoreRejectsWrongAudienceAndInactiveRefresh(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, err := users.Create("inactive-oauth@example.com", "", "operator")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	store := NewOAuthStore(sqlite.DB)
	client, err := store.RegisterClient(t.Context(), "Codex", "", []string{"http://localhost:9876/callback"})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	resource := "https://demo.fanout.test/mcp"
	pair, err := store.IssueTokenPair(t.Context(), client.ClientID, user.ID, "fanout:read", resource)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}
	if _, err := store.VerifyAccessToken(t.Context(), pair.AccessToken, "https://other.example/mcp"); !errors.Is(err, ErrInvalidOAuthToken) {
		t.Fatalf("wrong audience = %v, want invalid token", err)
	}
	active := false
	if _, err := users.Update(user.ID, nil, nil, nil, &active); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), client.ClientID, pair.RefreshToken, resource); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("inactive user refresh = %v, want invalid grant", err)
	}
}

func TestOAuthStoreExpiredCodeFails(t *testing.T) {
	sqlite := newTestSQLite(t)
	users := NewUserStore(sqlite.DB)
	user, _ := users.Create("expired@example.com", "", "admin")
	store := NewOAuthStore(sqlite.DB)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	client, _ := store.RegisterClient(t.Context(), "Client", "", []string{"http://localhost:1111/callback"})
	raw, err := store.CreateAuthorizationCode(t.Context(), OAuthAuthorizationCode{
		ClientID: client.ClientID, UserID: user.ID, RedirectURI: "http://localhost:1111/callback",
		Scope: "fanout:read", Resource: "https://demo.fanout.test/mcp", CodeChallenge: "challenge",
	})
	if err != nil {
		t.Fatalf("CreateAuthorizationCode: %v", err)
	}
	store.now = func() time.Time { return now.Add(OAuthCodeTTL + time.Second) }
	if _, err := store.ConsumeAuthorizationCode(t.Context(), raw); !errors.Is(err, ErrInvalidOAuthGrant) {
		t.Fatalf("expired code = %v, want invalid grant", err)
	}
}
