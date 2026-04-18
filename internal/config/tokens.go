package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

func randomHexToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func checkToken(token, hash string) bool {
	expected := hashToken(token)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(hash)) == 1
}

func formatGroupedToken(prefix, raw string, groupSize int) string {
	if groupSize <= 0 {
		return prefix + raw
	}
	parts := make([]string, 0, (len(raw)+groupSize-1)/groupSize)
	for len(raw) > 0 {
		end := groupSize
		if end > len(raw) {
			end = len(raw)
		}
		parts = append(parts, raw[:end])
		raw = raw[end:]
	}
	return prefix + strings.Join(parts, "-")
}
