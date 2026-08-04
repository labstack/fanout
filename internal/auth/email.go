package auth

import (
	"fmt"
	"net/mail"
	"strings"
)

// NormalizeEmail trims, lowercases, and validates an email address.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", fmt.Errorf("email is required")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(addr.Address, email) {
		return "", fmt.Errorf("invalid email address")
	}
	return email, nil
}
