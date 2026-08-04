package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

const SetupTTL = time.Hour

// SetupStatus is the result of verifying a presented setup token.
type SetupStatus int

const (
	SetupStatusOK SetupStatus = iota
	SetupStatusUnset
	SetupStatusExpired
	SetupStatusWrong
)

func (s SetupStatus) String() string {
	switch s {
	case SetupStatusOK:
		return "ok"
	case SetupStatusUnset:
		return "unset"
	case SetupStatusExpired:
		return "expired"
	case SetupStatusWrong:
		return "wrong"
	}
	return "unknown"
}

// Setup holds the in-memory first-boot admin token.
type Setup struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewSetup() *Setup {
	return &Setup{}
}

func (s *Setup) Rotate() (string, time.Time, error) {
	token, err := generateSetupToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(SetupTTL)

	s.mu.Lock()
	s.token = token
	s.expires = expires
	s.mu.Unlock()

	return token, expires, nil
}

// Verify returns why a setup token is or isn't acceptable. Comparison is constant-time.
func (s *Setup) Verify(token string) SetupStatus {
	s.mu.Lock()
	current := s.token
	expires := s.expires
	s.mu.Unlock()

	if current == "" {
		return SetupStatusUnset
	}
	if time.Now().UTC().After(expires) {
		return SetupStatusExpired
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(current)) != 1 {
		return SetupStatusWrong
	}
	return SetupStatusOK
}

func (s *Setup) Clear() {
	s.mu.Lock()
	s.token = ""
	s.expires = time.Time{}
	s.mu.Unlock()
}

// SetExpiresForTest overrides the expiry to drive the Expired path in tests.
// Do not call from production code — no compiler gate enforces this because the helper lives in a non-test file.
func (s *Setup) SetExpiresForTest(expires time.Time) {
	s.mu.Lock()
	s.expires = expires
	s.mu.Unlock()
}

func generateSetupToken() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate setup token: %w", err)
	}
	raw := hex.EncodeToString(buf)
	parts := make([]string, 0, (len(raw)+3)/4)
	for len(raw) > 0 {
		end := 4
		if end > len(raw) {
			end = len(raw)
		}
		parts = append(parts, raw[:end])
		raw = raw[end:]
	}
	return strings.Join(parts, "-"), nil
}
