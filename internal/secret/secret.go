// Package secret generates and validates opaque bearer secrets.
package secret

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// NewSecret returns 32 random bytes as unpadded base64url.
func NewSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// ValidSecret reports whether value has NewSecret's shape after prefix.
func ValidSecret(value, prefix string) bool {
	rest, ok := strings.CutPrefix(value, prefix)
	if !ok || len(rest) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil && len(decoded) == 32
}
