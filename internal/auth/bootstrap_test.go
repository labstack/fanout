package auth

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBootstrapVerifyAcceptsFreshToken(t *testing.T) {
	b := NewBootstrap()
	token, expires, err := b.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !strings.HasPrefix(token, "fanout-bootstrap-") {
		t.Fatalf("token = %q, want fanout-bootstrap- prefix", token)
	}
	if time.Until(expires) <= 0 || time.Until(expires) > BootstrapTTL {
		t.Fatalf("expires %v outside TTL", expires)
	}
	if got := b.Verify(token); got != BootstrapStatusOK {
		t.Fatalf("Verify(fresh) = %v, want OK", got)
	}
}

func TestBootstrapVerifyReportsUnset(t *testing.T) {
	b := NewBootstrap()
	if got := b.Verify("anything"); got != BootstrapStatusUnset {
		t.Fatalf("Verify(unset) = %v, want Unset", got)
	}
}

func TestBootstrapVerifyReportsExpired(t *testing.T) {
	b := &Bootstrap{token: "t", expires: time.Now().UTC().Add(-time.Second)}
	if got := b.Verify("t"); got != BootstrapStatusExpired {
		t.Fatalf("Verify(expired) = %v, want Expired", got)
	}
}

func TestBootstrapVerifyReportsWrong(t *testing.T) {
	b := NewBootstrap()
	if _, _, err := b.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if got := b.Verify("not-the-token"); got != BootstrapStatusWrong {
		t.Fatalf("Verify(wrong) = %v, want Wrong", got)
	}
}

func TestBootstrapClearInvalidatesToken(t *testing.T) {
	b := NewBootstrap()
	token, _, err := b.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	b.Clear()
	if got := b.Verify(token); got != BootstrapStatusUnset {
		t.Fatalf("Verify after Clear = %v, want Unset", got)
	}
}

func TestBootstrapRotateReplacesPriorToken(t *testing.T) {
	b := NewBootstrap()
	first, _, err := b.Rotate()
	if err != nil {
		t.Fatalf("Rotate first: %v", err)
	}
	second, _, err := b.Rotate()
	if err != nil {
		t.Fatalf("Rotate second: %v", err)
	}
	if first == second {
		t.Fatal("second Rotate returned the same token")
	}
	if got := b.Verify(first); got != BootstrapStatusWrong {
		t.Fatalf("Verify(first) after re-rotate = %v, want Wrong", got)
	}
	if got := b.Verify(second); got != BootstrapStatusOK {
		t.Fatalf("Verify(second) = %v, want OK", got)
	}
}

func TestBootstrapRotateAfterClear(t *testing.T) {
	b := NewBootstrap()
	if _, _, err := b.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	b.Clear()
	token, _, err := b.Rotate()
	if err != nil {
		t.Fatalf("Rotate after Clear: %v", err)
	}
	if got := b.Verify(token); got != BootstrapStatusOK {
		t.Fatalf("Verify after Rotate-after-Clear = %v, want OK", got)
	}
}

func TestBootstrapConcurrentAccess(t *testing.T) {
	b := NewBootstrap()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _, _, _ = b.Rotate() }()
		go func() { defer wg.Done(); _ = b.Verify("probe") }()
		go func() { defer wg.Done(); b.Clear() }()
	}
	wg.Wait()
}
