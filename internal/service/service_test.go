package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/config"
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

func TestNew(t *testing.T) {
	cfg := config.Config{
		DefaultNS: "test-ns",
		TenantID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
	svc := New(nil, cfg)

	if svc == nil {
		t.Fatal("New() returned nil")
	}
	if svc.cfg.DefaultNS != "test-ns" {
		t.Errorf("cfg.DefaultNS = %q, want %q", svc.cfg.DefaultNS, "test-ns")
	}
}

func TestDefaults(t *testing.T) {
	tenantID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	cfg := config.Config{
		DefaultNS: "default-ns",
		TenantID:  tenantID,
	}
	svc := New(nil, cfg)

	tests := []struct {
		name        string
		namespace   string
		tenantID    string
		wantNS      string
		wantTenant  string
	}{
		{
			name:       "both empty uses defaults",
			namespace:  "",
			tenantID:   "",
			wantNS:     "default-ns",
			wantTenant: tenantID.String(),
		},
		{
			name:       "custom namespace",
			namespace:  "custom-ns",
			tenantID:   "",
			wantNS:     "custom-ns",
			wantTenant: tenantID.String(),
		},
		{
			name:       "custom tenant",
			namespace:  "",
			tenantID:   "custom-tenant",
			wantNS:     "default-ns",
			wantTenant: "custom-tenant",
		},
		{
			name:       "both custom",
			namespace:  "my-ns",
			tenantID:   "my-tenant",
			wantNS:     "my-ns",
			wantTenant: "my-tenant",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns, tenant := svc.defaults(tc.namespace, tc.tenantID)
			if ns != tc.wantNS {
				t.Errorf("defaults() namespace = %q, want %q", ns, tc.wantNS)
			}
			if tenant != tc.wantTenant {
				t.Errorf("defaults() tenantID = %q, want %q", tenant, tc.wantTenant)
			}
		})
	}
}

func TestMakePlaceholders(t *testing.T) {
	tests := []struct {
		n        int
		expected string
	}{
		{0, ""},
		{-1, ""},
		{1, "?"},
		{2, "?, ?"},
		{3, "?, ?, ?"},
		{5, "?, ?, ?, ?, ?"},
	}

	for _, tc := range tests {
		result := makePlaceholders(tc.n)
		if result != tc.expected {
			t.Errorf("makePlaceholders(%d) = %q, want %q", tc.n, result, tc.expected)
		}
	}
}

func TestContainsWildcard(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"hello", false},
		{"hello*", true},
		{"*hello", true},
		{"he*lo", true},
		{"hello?", true},
		{"?ello", true},
		{"hel?o", true},
		{"*?", true},
		{"", false},
		{"no wildcards here", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := containsWildcard(tc.input)
			if result != tc.expected {
				t.Errorf("containsWildcard(%q) = %v, want %v", tc.input, result, tc.expected)
			}
		})
	}
}

func TestWildcardToLike(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello*", "hello%"},
		{"*hello", "%hello"},
		{"he*lo", "he%lo"},
		{"hello?", "hello_"},
		{"?ello", "_ello"},
		{"hel?o", "hel_o"},
		{"*?*", "%_%"},
		{"", ""},
		{"no wildcards", "no wildcards"},
		{"**", "%%"},
		{"??", "__"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := wildcardToLike(tc.input)
			if result != tc.expected {
				t.Errorf("wildcardToLike(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestParseServiceList(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected []string
	}{
		{"nil input", nil, nil},
		{"empty slice", []any{}, nil},
		{"string slice", []any{"svc1", "svc2", "svc3"}, []string{"svc1", "svc2", "svc3"}},
		{"mixed types", []any{"svc1", 123, "svc2", nil}, []string{"svc1", "svc2"}},
		{"empty strings filtered", []any{"svc1", "", "svc2"}, []string{"svc1", "svc2"}},
		{"non-slice input", "not a slice", nil},
		{"int input", 42, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseServiceList(tc.input)
			if len(result) != len(tc.expected) {
				t.Errorf("parseServiceList() len = %d, want %d", len(result), len(tc.expected))
				return
			}
			for i, v := range result {
				if v != tc.expected[i] {
					t.Errorf("parseServiceList()[%d] = %q, want %q", i, v, tc.expected[i])
				}
			}
		})
	}
}
