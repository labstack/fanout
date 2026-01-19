package service

import (
	"testing"
)

func TestEscapeSQL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"'quoted'", "''quoted''"},
		{"", ""},
		{"no quotes here", "no quotes here"},
		{"multiple 'single' quotes 'here'", "multiple ''single'' quotes ''here''"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := escapeSQL(tc.input)
			if result != tc.expected {
				t.Errorf("escapeSQL(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"100%", "100\\%"},
		{"user_name", "user\\_name"},
		{"50% off_sale", "50\\% off\\_sale"},
		{"", ""},
		{"no special chars", "no special chars"},
		{"'quote' and % and _", "''quote'' and \\% and \\_"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := escapeLikePattern(tc.input)
			if result != tc.expected {
				t.Errorf("escapeLikePattern(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestDeriveHealth(t *testing.T) {
	tests := []struct {
		name      string
		errorRate float64
		p95       float64
		expected  string
	}{
		// Healthy cases
		{"healthy_low_error_low_latency", 0.001, 100, "healthy"},
		{"healthy_zero_error", 0, 500, "healthy"},
		{"healthy_at_threshold", 0.01, 1000, "healthy"},

		// Degraded cases
		{"degraded_high_error", 0.02, 500, "degraded"},
		{"degraded_high_latency", 0.005, 1500, "degraded"},
		{"degraded_both_elevated", 0.05, 2000, "degraded"},
		{"degraded_at_error_threshold", 0.1, 500, "degraded"},
		{"degraded_at_latency_threshold", 0.005, 5000, "degraded"},

		// Unhealthy cases
		{"unhealthy_very_high_error", 0.15, 500, "unhealthy"},
		{"unhealthy_very_high_latency", 0.005, 6000, "unhealthy"},
		{"unhealthy_both_critical", 0.2, 10000, "unhealthy"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := DeriveHealth(tc.errorRate, tc.p95)
			if result != tc.expected {
				t.Errorf("DeriveHealth(%v, %v) = %q, want %q", tc.errorRate, tc.p95, result, tc.expected)
			}
		})
	}
}
