package settings

import (
	"crypto/sha256"
	"encoding/hex"
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
