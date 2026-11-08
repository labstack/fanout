package auth

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"
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

// MaxAlertEmailRecipients caps the addresses on one alert rule and on the
// default destination.
const MaxAlertEmailRecipients = 10

// NormalizeRecipients validates a list of bare email addresses, returning them
// normalized and deduplicated. It rejects display-name forms, control
// characters and lists over MaxAlertEmailRecipients.
func NormalizeRecipients(raw []string) ([]string, error) {
	seen := make(map[string]bool, len(raw))
	recipients := make([]string, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if strings.IndexFunc(entry, unicode.IsControl) >= 0 || len(entry) > maxEmailLength {
			return nil, fmt.Errorf("invalid email address %q", entry)
		}
		address, err := NormalizeEmail(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid email address %q", entry)
		}
		if seen[address] {
			continue
		}
		seen[address] = true
		recipients = append(recipients, address)
		if len(recipients) > MaxAlertEmailRecipients {
			return nil, fmt.Errorf("at most %d email recipients are allowed", MaxAlertEmailRecipients)
		}
	}
	return recipients, nil
}

// maxEmailLength is the longest address SMTP can carry (RFC 5321).
const maxEmailLength = 254
