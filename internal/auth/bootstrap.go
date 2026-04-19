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

const BootstrapTTL = time.Hour

// BootstrapStatus is the result of verifying a presented bootstrap token.
type BootstrapStatus int

const (
	BootstrapStatusOK BootstrapStatus = iota
	BootstrapStatusUnset
	BootstrapStatusExpired
	BootstrapStatusWrong
)

func (s BootstrapStatus) String() string {
	switch s {
	case BootstrapStatusOK:
		return "ok"
	case BootstrapStatusUnset:
		return "unset"
	case BootstrapStatusExpired:
		return "expired"
	case BootstrapStatusWrong:
		return "wrong"
	}
	return "unknown"
}

// Bootstrap holds the in-memory first-boot admin token.
type Bootstrap struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewBootstrap() *Bootstrap {
	return &Bootstrap{}
}

func (b *Bootstrap) Rotate() (string, time.Time, error) {
	token, err := generateBootstrapToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(BootstrapTTL)

	b.mu.Lock()
	b.token = token
	b.expires = expires
	b.mu.Unlock()

	return token, expires, nil
}

// Verify returns why a bootstrap token is or isn't acceptable. Comparison is constant-time.
func (b *Bootstrap) Verify(token string) BootstrapStatus {
	b.mu.Lock()
	current := b.token
	expires := b.expires
	b.mu.Unlock()

	if current == "" {
		return BootstrapStatusUnset
	}
	if time.Now().UTC().After(expires) {
		return BootstrapStatusExpired
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(current)) != 1 {
		return BootstrapStatusWrong
	}
	return BootstrapStatusOK
}

func (b *Bootstrap) Clear() {
	b.mu.Lock()
	b.token = ""
	b.expires = time.Time{}
	b.mu.Unlock()
}

// SetExpiresForTest overrides the expiry time; used only by tests that need to drive the Expired path.
func (b *Bootstrap) SetExpiresForTest(expires time.Time) {
	b.mu.Lock()
	b.expires = expires
	b.mu.Unlock()
}

func generateBootstrapToken() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate bootstrap token: %w", err)
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
	return "fanout-bootstrap-" + strings.Join(parts, "-"), nil
}
