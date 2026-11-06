package settings

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func checkToken(token, hash string) bool {
	expected := hashToken(token)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(hash)) == 1
}
