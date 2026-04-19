package auth

import (
	"regexp"
	"sync"
	"testing"
	"time"
)

var tokenShape = regexp.MustCompile(`^[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}$`)

func TestSetupVerifyAcceptsFreshToken(t *testing.T) {
	s := NewSetup()
	token, expires, err := s.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !tokenShape.MatchString(token) {
		t.Fatalf("token = %q, want 3 hex-4 groups separated by dashes", token)
	}
	if time.Until(expires) <= 0 || time.Until(expires) > SetupTTL {
		t.Fatalf("expires %v outside TTL", expires)
	}
	if got := s.Verify(token); got != SetupStatusOK {
		t.Fatalf("Verify(fresh) = %v, want OK", got)
	}
}

func TestSetupVerifyReportsUnset(t *testing.T) {
	s := NewSetup()
	if got := s.Verify("anything"); got != SetupStatusUnset {
		t.Fatalf("Verify(unset) = %v, want Unset", got)
	}
}

func TestSetupVerifyReportsExpired(t *testing.T) {
	s := &Setup{token: "t", expires: time.Now().UTC().Add(-time.Second)}
	if got := s.Verify("t"); got != SetupStatusExpired {
		t.Fatalf("Verify(expired) = %v, want Expired", got)
	}
}

func TestSetupVerifyReportsWrong(t *testing.T) {
	s := NewSetup()
	if _, _, err := s.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if got := s.Verify("not-the-token"); got != SetupStatusWrong {
		t.Fatalf("Verify(wrong) = %v, want Wrong", got)
	}
}

func TestSetupClearInvalidatesToken(t *testing.T) {
	s := NewSetup()
	token, _, err := s.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	s.Clear()
	if got := s.Verify(token); got != SetupStatusUnset {
		t.Fatalf("Verify after Clear = %v, want Unset", got)
	}
}

func TestSetupRotateReplacesPriorToken(t *testing.T) {
	s := NewSetup()
	first, _, err := s.Rotate()
	if err != nil {
		t.Fatalf("Rotate first: %v", err)
	}
	second, _, err := s.Rotate()
	if err != nil {
		t.Fatalf("Rotate second: %v", err)
	}
	if first == second {
		t.Fatal("second Rotate returned the same token")
	}
	if got := s.Verify(first); got != SetupStatusWrong {
		t.Fatalf("Verify(first) after re-rotate = %v, want Wrong", got)
	}
	if got := s.Verify(second); got != SetupStatusOK {
		t.Fatalf("Verify(second) = %v, want OK", got)
	}
}

func TestSetupRotateAfterClear(t *testing.T) {
	s := NewSetup()
	if _, _, err := s.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	s.Clear()
	token, _, err := s.Rotate()
	if err != nil {
		t.Fatalf("Rotate after Clear: %v", err)
	}
	if got := s.Verify(token); got != SetupStatusOK {
		t.Fatalf("Verify after Rotate-after-Clear = %v, want OK", got)
	}
}

func TestSetupConcurrentAccess(t *testing.T) {
	s := NewSetup()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _, _, _ = s.Rotate() }()
		go func() { defer wg.Done(); _ = s.Verify("probe") }()
		go func() { defer wg.Done(); s.Clear() }()
	}
	wg.Wait()
}
