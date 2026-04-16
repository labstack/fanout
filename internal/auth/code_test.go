package auth

import (
	"testing"

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
