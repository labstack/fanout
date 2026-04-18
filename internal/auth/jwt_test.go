package auth

import (
	"testing"
	"time"
)

func TestSignAndVerifyAccess(t *testing.T) {
	secret := "test-secret-key"

	token, err := SignAccess(secret, "user-123")
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	claims, err := VerifyAccess(secret, token)
	if err != nil {
		t.Fatalf("VerifyAccess: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Errorf("subject = %q, want user-123", claims.Subject)
	}
	if claims.TokenType != accessTokenType {
		t.Errorf("token type = %q, want %q", claims.TokenType, accessTokenType)
	}
}

func TestVerifyAccess_WrongSecret(t *testing.T) {
	token, _ := SignAccess("secret-1", "user-123")
	_, err := VerifyAccess("secret-2", token)
	if err == nil {
		t.Error("should fail with wrong secret")
	}
}

func TestSignAndVerifyRefresh(t *testing.T) {
	secret := "test-refresh-secret"
	issuedAt := time.Now().UTC().Truncate(time.Second)

	token, err := SignRefresh(secret, "user-456", issuedAt)
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	claims, err := VerifyRefresh(secret, token)
	if err != nil {
		t.Fatalf("VerifyRefresh: %v", err)
	}
	if claims.Subject != "user-456" {
		t.Errorf("subject = %q, want user-456", claims.Subject)
	}
	if claims.IssuedAt == nil || !claims.IssuedAt.Time.Equal(issuedAt) {
		t.Fatalf("issued_at = %v, want %v", claims.IssuedAt, issuedAt)
	}
	if claims.TokenType != refreshTokenType {
		t.Errorf("token type = %q, want %q", claims.TokenType, refreshTokenType)
	}
}

func TestVerifyRefresh_RejectsAccessToken(t *testing.T) {
	secret := "test-refresh-secret"
	token, err := SignAccess(secret, "user-456")
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}
	if _, err := VerifyRefresh(secret, token); err == nil {
		t.Fatal("VerifyRefresh should reject access token")
	}
}

func TestVerifyAccess_RejectsRefreshToken(t *testing.T) {
	secret := "test-refresh-secret"
	token, err := SignRefresh(secret, "user-456", time.Now())
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}
	if _, err := VerifyAccess(secret, token); err == nil {
		t.Fatal("VerifyAccess should reject refresh token")
	}
}
