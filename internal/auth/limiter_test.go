package auth

import (
	"testing"
	"time"
)

func TestKeyedLimiterExhaustionRefillAndIsolation(t *testing.T) {
	limiter := NewKeyedLimiter(2, 20*time.Millisecond)

	if !limiter.Allow("client-a") {
		t.Fatal("first burst token was rejected")
	}
	if !limiter.Allow("client-a") {
		t.Fatal("second burst token was rejected")
	}
	if limiter.Allow("client-a") {
		t.Fatal("exhausted key was allowed")
	}
	if !limiter.Allow("client-b") {
		t.Fatal("one key exhausted another key")
	}

	time.Sleep(12 * time.Millisecond)
	if !limiter.Allow("client-a") {
		t.Fatal("token did not refill")
	}
}

func TestKeyedLimiterBoundsAndExpiresKeys(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := NewKeyedLimiter(1, time.Minute)
	limiter.now = func() time.Time { return now }
	limiter.maxKeys = 2

	limiter.Allow("oldest")
	now = now.Add(time.Second)
	limiter.Allow("newer")
	now = now.Add(time.Second)
	limiter.Allow("newest")
	if len(limiter.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(limiter.entries))
	}
	if _, exists := limiter.entries["oldest"]; exists {
		t.Fatal("oldest entry was not evicted")
	}

	now = now.Add(limiter.expiry + time.Second)
	limiter.Allow("after-expiry")
	if len(limiter.entries) != 1 {
		t.Fatalf("expired entries retained: %d", len(limiter.entries))
	}
}
