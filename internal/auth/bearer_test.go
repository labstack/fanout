package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Nil stores make any database access fail: malformed credentials must return
// the path's normal denial before opening a connection or computing a lookup.
func TestMalformedBearerSecretsSkipDatabase(t *testing.T) {
	ctx := context.Background()
	store := &OAuthStore{}
	links := &CodeStore{}
	for _, value := range []string{"", "bad", strings.Repeat("a", 32), strings.Repeat("a", 48), "fo_" + strings.Repeat("a", 48), "foa_" + strings.Repeat("!", 43)} {
		if _, err := store.VerifyAccessToken(ctx, value, ""); !errors.Is(err, ErrInvalidOAuthToken) {
			t.Fatalf("access: %v", err)
		}
		if _, err := store.RotateRefreshToken(ctx, "", value, "", ""); !errors.Is(err, ErrInvalidOAuthGrant) {
			t.Fatalf("refresh: %v", err)
		}
		if _, err := store.ConsumeAuthorizationCode(ctx, value); !errors.Is(err, ErrInvalidOAuthGrant) {
			t.Fatalf("code: %v", err)
		}
		if _, ok, err := links.VerifyLoginLink(value); ok || err != nil {
			t.Fatalf("login link: %v, %v", ok, err)
		}
	}
	for _, prefix := range []string{"foi_", "foa_", "for_", "foc_"} {
		value := prefix + strings.Repeat("A", 43)
		if prefix != "foa_" {
			if _, err := store.VerifyAccessToken(ctx, value, ""); !errors.Is(err, ErrInvalidOAuthToken) {
				t.Fatal(err)
			}
		}
		if prefix != "for_" {
			if _, err := store.RotateRefreshToken(ctx, "", value, "", ""); !errors.Is(err, ErrInvalidOAuthGrant) {
				t.Fatal(err)
			}
		}
		if prefix != "foc_" {
			if _, err := store.ConsumeAuthorizationCode(ctx, value); !errors.Is(err, ErrInvalidOAuthGrant) {
				t.Fatal(err)
			}
		}
		if _, ok, err := links.VerifyLoginLink(value); ok || err != nil {
			t.Fatalf("login link: %v, %v", ok, err)
		}
	}
}
