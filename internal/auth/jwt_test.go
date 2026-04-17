package auth

import "testing"

func TestSignAndVerifyAccess(t *testing.T) {
	secret := "test-secret-key"

	token, err := SignAccess(secret, "user-123", "test@example.com", "admin")
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
	if claims.Email != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", claims.Email)
	}
	if claims.Role != "admin" {
		t.Errorf("role = %q, want admin", claims.Role)
	}
}

func TestVerifyAccess_WrongSecret(t *testing.T) {
	token, _ := SignAccess("secret-1", "user-123", "test@example.com", "admin")
	_, err := VerifyAccess("secret-2", token)
	if err == nil {
		t.Error("should fail with wrong secret")
	}
}

func TestSignAndVerifyRefresh(t *testing.T) {
	secret := "test-refresh-secret"

	token, err := SignRefresh(secret, "user-456")
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	userID, err := VerifyRefresh(secret, token)
	if err != nil {
		t.Fatalf("VerifyRefresh: %v", err)
	}
	if userID != "user-456" {
		t.Errorf("userID = %q, want user-456", userID)
	}
}

func TestGenerateSecret_Length(t *testing.T) {
	s, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if len(s) != 64 {
		t.Errorf("secret length = %d, want 64", len(s))
	}
}
