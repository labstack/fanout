// Package id centralizes identifiers generated and owned by Fanout.
package id

import "github.com/google/uuid"

// New returns a time-ordered UUIDv7 string.
func New() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

// MustNew returns a time-ordered UUIDv7 string or panics if secure randomness
// is unavailable. Use New when the caller has a useful error path.
func MustNew() string {
	return uuid.Must(uuid.NewV7()).String()
}

// IsV7 reports whether value is a canonical UUIDv7 string.
func IsV7(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && parsed.Version() == 7
}
