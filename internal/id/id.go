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
