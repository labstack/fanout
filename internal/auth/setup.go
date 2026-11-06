package auth

import (
	"crypto/subtle"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/secret"
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
	token, err := secret.NewSecret()
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
	if !secret.ValidSecret(token, "") || subtle.ConstantTimeCompare([]byte(token), []byte(current)) != 1 {
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
