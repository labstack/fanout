package auth

import (
	"strings"
	"testing"
	"time"
)

func TestBootstrapRotateReturnsFreshToken(t *testing.T) {
	b := NewBootstrap()

	token, expires, err := b.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !strings.HasPrefix(token, "fanout-bootstrap-") {
		t.Fatalf("token = %q, want fanout-bootstrap- prefix", token)
	}
	if time.Until(expires) <= 0 || time.Until(expires) > BootstrapTTL {
		t.Fatalf("expires %v not within TTL", expires)
	}
	if !b.Check(token) {
		t.Fatal("Check rejected freshly rotated token")
	}
}

func TestBootstrapCheckRejectsWrongToken(t *testing.T) {
	b := NewBootstrap()
	if _, _, err := b.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if b.Check("not-the-token") {
		t.Fatal("Check accepted a wrong token")
	}
}

func TestBootstrapCheckRejectsWhenUnset(t *testing.T) {
	b := NewBootstrap()
	if b.Check("anything") {
		t.Fatal("Check accepted a token when bootstrap was never rotated")
	}
}

func TestBootstrapClearInvalidatesToken(t *testing.T) {
	b := NewBootstrap()
	token, _, err := b.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	b.Clear()
	if b.Check(token) {
		t.Fatal("Check accepted token after Clear")
	}
}

func TestBootstrapCheckRejectsExpiredToken(t *testing.T) {
	b := &Bootstrap{token: "expired-token", expires: time.Now().UTC().Add(-time.Second)}
	if b.Check("expired-token") {
		t.Fatal("Check accepted an expired token")
	}
}

func TestBootstrapExpiredReflectsTTL(t *testing.T) {
	b := NewBootstrap()
	if b.Expired() {
		t.Fatal("Expired() returned true for an unrotated bootstrap")
	}

	b.token = "past-token"
	b.expires = time.Now().UTC().Add(-time.Second)
	if !b.Expired() {
		t.Fatal("Expired() returned false for a token past its TTL")
	}
}
