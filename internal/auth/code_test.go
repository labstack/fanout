package auth

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appstore "github.com/labstack/fanout/internal/store"
)

func newTestCodeStore(t *testing.T) *CodeStore {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return NewCodeStore(sqlite.DB, "test-secret")
}

func TestGenerateCode_SixDigits(t *testing.T) {
	code, err := GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("code = %q, want 6 digits", code)
	}
}

func TestGenerateLoginLinkTokenHas128BitsOfEntropy(t *testing.T) {
	token, err := GenerateLoginLinkToken()
	if err != nil {
		t.Fatalf("GenerateLoginLinkToken: %v", err)
	}
	if len(token) != 2*loginLinkTokenBytes {
		t.Fatalf("token length = %d, want %d hex characters", len(token), 2*loginLinkTokenBytes)
	}
}

func TestHashCode_Deterministic(t *testing.T) {
	h1 := HashCode("123456", "secret")
	h2 := HashCode("123456", "secret")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
}

func TestVerifyHash(t *testing.T) {
	hash := HashCode("123456", "secret")
	if !VerifyHash("123456", hash, "secret") {
		t.Error("valid code should verify")
	}
	if VerifyHash("000000", hash, "secret") {
		t.Error("wrong code should not verify")
	}
}

func TestCodeStore_CreateAndVerify(t *testing.T) {
	s := newTestCodeStore(t)

	code, err := s.Create("test@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ok, err := s.Verify("test@example.com", code)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("valid code should verify")
	}

	// Used code should not verify again
	ok, _ = s.Verify("test@example.com", code)
	if ok {
		t.Error("used code should not verify again")
	}
}

func TestLoginLinkIsSingleUseAndPurposeBound(t *testing.T) {
	s := newTestCodeStore(t)
	token, err := s.CreateLoginLink("admin@example.com")
	if err != nil {
		t.Fatalf("CreateLoginLink: %v", err)
	}
	var storedHash, purpose, expiresAt string
	if err := s.db.QueryRow(`SELECT code_hash, purpose, expires_at FROM verifications WHERE email = ?`, "admin@example.com").Scan(&storedHash, &purpose, &expiresAt); err != nil {
		t.Fatalf("read stored login link: %v", err)
	}
	if storedHash == token || strings.Contains(storedHash, token) || purpose != purposeLoginLink {
		t.Fatalf("stored login link = hash %q purpose %q", storedHash, purpose)
	}
	expires, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	remaining := time.Until(expires)
	if remaining < loginLinkTTL-time.Minute || remaining > loginLinkTTL+time.Minute {
		t.Fatalf("login link lifetime = %v, want %v", remaining, loginLinkTTL)
	}
	if ok, err := s.Verify("admin@example.com", token); err != nil || ok {
		t.Fatalf("Verify(email code with login token) = (%v, %v), want (false, nil)", ok, err)
	}
	email, ok, err := s.VerifyLoginLink(token)
	if err != nil || !ok || email != "admin@example.com" {
		t.Fatalf("VerifyLoginLink = (%q, %v, %v)", email, ok, err)
	}
	if _, ok, err := s.VerifyLoginLink(token); err != nil || ok {
		t.Fatalf("second VerifyLoginLink = (%v, %v), want (false, nil)", ok, err)
	}

	code, err := s.Create("admin@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok, err := s.VerifyLoginLink(code); err != nil || ok {
		t.Fatalf("VerifyLoginLink(email code) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestLoginLinkIsSingleUseUnderConcurrency(t *testing.T) {
	s := newTestCodeStore(t)
	token, err := s.CreateLoginLink("race-link@example.com")
	if err != nil {
		t.Fatalf("CreateLoginLink: %v", err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, verifyErr := s.VerifyLoginLink(token)
			if verifyErr != nil {
				t.Errorf("VerifyLoginLink: %v", verifyErr)
			}
			if ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful login link redemptions = %d, want 1", successes.Load())
	}
}

func TestCodeStore_WrongCode(t *testing.T) {
	s := newTestCodeStore(t)
	s.Create("test@example.com")

	ok, _ := s.Verify("test@example.com", "000000")
	if ok {
		t.Error("wrong code should not verify")
	}
}

func TestCodeStore_MaxAttempts(t *testing.T) {
	s := newTestCodeStore(t)
	code, _ := s.Create("test@example.com")

	for i := 0; i < 3; i++ {
		s.Verify("test@example.com", "000000")
	}

	ok, _ := s.Verify("test@example.com", code)
	if ok {
		t.Error("should fail after max attempts")
	}
}

func TestCodeStoreRecentCountUsesCanonicalTimestamp(t *testing.T) {
	store := newTestCodeStore(t)
	if _, err := store.Create("cooldown@example.com"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	count, err := store.RecentCount("cooldown@example.com", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("RecentCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("recent codes = %d, want 1", count)
	}
	count, err = store.RecentCount("cooldown@example.com", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("RecentCount future cutoff: %v", err)
	}
	if count != 0 {
		t.Fatalf("future recent codes = %d, want 0", count)
	}
}

func TestCodeStoreVerifyIsSingleUseUnderConcurrency(t *testing.T) {
	store := newTestCodeStore(t)
	code, err := store.Create("race@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var successes atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, verifyErr := store.Verify("race@example.com", code)
			if verifyErr != nil {
				errs <- verifyErr
				return
			}
			if ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for verifyErr := range errs {
		t.Errorf("Verify: %v", verifyErr)
	}
	if successes.Load() != 1 {
		t.Fatalf("successful verifications = %d, want 1", successes.Load())
	}
}

func TestVerifyCapsConcurrentWrongAttempts(t *testing.T) {
	s := newTestCodeStore(t)
	const email = "person@example.com"
	code, err := s.Create(email)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := s.Verify(email, wrong); ok || err != nil {
				t.Errorf("Verify(wrong) = %v, %v", ok, err)
			}
		}()
	}
	wg.Wait()

	var attempts int
	if err := s.db.QueryRow(`SELECT attempts FROM verifications WHERE email = ?`, email).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts > maxAttempts {
		t.Fatalf("attempts = %d, want at most %d: each guess must reserve its attempt before the code is compared", attempts, maxAttempts)
	}
}
