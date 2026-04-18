package config

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRotateBootstrapPersistsConfig(t *testing.T) {
	store := newTestStore(t)

	token, cfg, err := store.RotateBootstrap(context.Background(), "test", "first boot")
	if err != nil {
		t.Fatalf("RotateBootstrap: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}
	if cfg.Token == "" || cfg.TokenHash == "" || cfg.ExpiresAt == "" {
		t.Fatalf("cfg = %#v, want token hash and expiry", cfg)
	}
	if cfg.Token != token {
		t.Fatalf("cfg token = %q, want %q", cfg.Token, token)
	}

	stored, err := store.GetBootstrap(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrap: %v", err)
	}
	if stored != cfg {
		t.Fatalf("stored = %#v, want %#v", stored, cfg)
	}
	if err := CheckBootstrapToken(token, stored, time.Now().UTC()); err != nil {
		t.Fatalf("CheckBootstrapToken: %v", err)
	}
}

func TestCheckBootstrapTokenRejectsExpiredToken(t *testing.T) {
	cfg := BootstrapConfig{
		TokenHash: HashBootstrapToken("fanout-setup-dead-beef"),
		ExpiresAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
	}

	err := CheckBootstrapToken("fanout-setup-dead-beef", cfg, time.Now().UTC())
	if !errors.Is(err, ErrBootstrapTokenExpired) {
		t.Fatalf("err = %v, want %v", err, ErrBootstrapTokenExpired)
	}
}

func TestClearBootstrapRemovesConfig(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.RotateBootstrap(context.Background(), "test", "first boot"); err != nil {
		t.Fatalf("RotateBootstrap: %v", err)
	}
	if err := store.ClearBootstrap(context.Background()); err != nil {
		t.Fatalf("ClearBootstrap: %v", err)
	}

	cfg, err := store.GetBootstrap(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrap: %v", err)
	}
	if cfg.TokenHash != "" || cfg.ExpiresAt != "" {
		t.Fatalf("cfg = %#v, want empty bootstrap config", cfg)
	}
}

func TestEnsureBootstrapReusesValidToken(t *testing.T) {
	store := newTestStore(t)
	token, first, err := store.EnsureBootstrap(context.Background(), "test", "first boot")
	if err != nil {
		t.Fatalf("EnsureBootstrap: %v", err)
	}
	reused, second, err := store.EnsureBootstrap(context.Background(), "test", "restart")
	if err != nil {
		t.Fatalf("EnsureBootstrap second call: %v", err)
	}
	if reused != token {
		t.Fatalf("reused token = %q, want %q", reused, token)
	}
	if second != first {
		t.Fatalf("second config = %#v, want %#v", second, first)
	}
}
