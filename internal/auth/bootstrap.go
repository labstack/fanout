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

// BootstrapTTL is the lifetime of a generated bootstrap token.
const BootstrapTTL = time.Hour

// Bootstrap holds the one-time first-boot token in memory.
// The token is generated once per process when no users exist, printed to
// stderr, and consumed by the initial admin setup request.
type Bootstrap struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewBootstrap returns an empty bootstrap holder.
func NewBootstrap() *Bootstrap {
	return &Bootstrap{}
}

// Rotate generates a new bootstrap token with a fresh TTL and returns it
// alongside its expiry time.
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

// Check returns true when token matches the active bootstrap value and the
// TTL has not elapsed. Comparison is constant-time.
func (b *Bootstrap) Check(token string) bool {
	b.mu.Lock()
	current := b.token
	expires := b.expires
	b.mu.Unlock()

	if current == "" || time.Now().UTC().After(expires) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(current)) == 1
}

// Expired reports whether a token exists but has passed its TTL.
func (b *Bootstrap) Expired() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token != "" && time.Now().UTC().After(b.expires)
}

// Clear zeroes the bootstrap state after successful first-admin setup.
func (b *Bootstrap) Clear() {
	b.mu.Lock()
	b.token = ""
	b.expires = time.Time{}
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
