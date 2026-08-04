package id

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewReturnsCanonicalRFC4122UUIDv7(t *testing.T) {
	for range 128 {
		value, err := New()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := uuid.Parse(value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		if parsed.Version() != 7 {
			t.Fatalf("version = %d, want 7", parsed.Version())
		}
		if parsed.Variant() != uuid.RFC4122 {
			t.Fatalf("variant = %v, want RFC4122", parsed.Variant())
		}
		if !IsV7(value) {
			t.Fatalf("IsV7(%q) = false", value)
		}
	}
}

func TestIsV7RejectsOtherValues(t *testing.T) {
	for _, value := range []string{"", "widget", uuid.NewString(), "01900000-0000-7000-8000-000000000000-extra"} {
		if IsV7(value) {
			t.Fatalf("IsV7(%q) = true", value)
		}
	}
}
