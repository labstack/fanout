package config

import (
	"os"
	"testing"
)

func TestGetenv(t *testing.T) {
	// Test default value
	result := getenv("NONEXISTENT_VAR_12345", "default_value")
	if result != "default_value" {
		t.Errorf("getenv with missing var = %q, want %q", result, "default_value")
	}

	// Test actual value
	os.Setenv("TEST_GETENV_VAR", "actual_value")
	defer os.Unsetenv("TEST_GETENV_VAR")

	result = getenv("TEST_GETENV_VAR", "default_value")
	if result != "actual_value" {
		t.Errorf("getenv with set var = %q, want %q", result, "actual_value")
	}
}

func TestGetenvInt(t *testing.T) {
	// Test default value
	result := getenvInt("NONEXISTENT_INT_VAR", 42)
	if result != 42 {
		t.Errorf("getenvInt with missing var = %d, want %d", result, 42)
	}

	// Test actual value
	os.Setenv("TEST_INT_VAR", "123")
	defer os.Unsetenv("TEST_INT_VAR")

	result = getenvInt("TEST_INT_VAR", 42)
	if result != 123 {
		t.Errorf("getenvInt with set var = %d, want %d", result, 123)
	}

	// Test invalid value (should return default)
	os.Setenv("TEST_INT_VAR", "not_a_number")
	result = getenvInt("TEST_INT_VAR", 42)
	if result != 42 {
		t.Errorf("getenvInt with invalid var = %d, want %d", result, 42)
	}
}

func TestGetenvBool(t *testing.T) {
	tests := []struct {
		value    string
		def      bool
		expected bool
	}{
		{"", true, true},   // empty returns default
		{"", false, false}, // empty returns default
		{"1", false, true},
		{"true", false, true},
		{"yes", false, true},
		{"0", true, false},
		{"false", true, false},
		{"no", true, false},
		{"anything", true, false}, // unrecognized = false
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			if tc.value != "" {
				os.Setenv("TEST_BOOL_VAR", tc.value)
				defer os.Unsetenv("TEST_BOOL_VAR")
			} else {
				os.Unsetenv("TEST_BOOL_VAR")
			}

			result := getenvBool("TEST_BOOL_VAR", tc.def)
			if result != tc.expected {
				t.Errorf("getenvBool(%q, %v) = %v, want %v", tc.value, tc.def, result, tc.expected)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	// Clear all env vars that might affect config
	vars := []string{"HTTP_ADDR", "OTLP_GRPC_ADDR", "LAKE_DIR", "FLUSH_SECONDS",
		"MAX_ROWS", "API_TOKEN", "ROLLUP_EVERY", "MCP_ENABLED",
		"RETENTION_DAYS", "RETENTION_HOURS", "TENANT_ID", "DEFAULT_NAMESPACE"}
	for _, v := range vars {
		os.Unsetenv(v)
	}

	cfg := Load()

	// Check defaults
	if cfg.HTTPAddr != ":7520" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":7520")
	}
	if cfg.OTLPGRPCAddr != ":4317" {
		t.Errorf("OTLPGRPCAddr = %q, want %q", cfg.OTLPGRPCAddr, ":4317")
	}
	if cfg.LakeDir != "./lake" {
		t.Errorf("LakeDir = %q, want %q", cfg.LakeDir, "./lake")
	}
	if cfg.FlushSeconds != 15 {
		t.Errorf("FlushSeconds = %d, want %d", cfg.FlushSeconds, 15)
	}
	if cfg.MaxRows != 50000 {
		t.Errorf("MaxRows = %d, want %d", cfg.MaxRows, 50000)
	}
	if cfg.RollupEvery != 60 {
		t.Errorf("RollupEvery = %d, want %d", cfg.RollupEvery, 60)
	}
	if !cfg.MCPEnabled {
		t.Error("MCPEnabled should be true by default")
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, 30)
	}
	if cfg.DefaultNS != "default" {
		t.Errorf("DefaultNS = %q, want %q", cfg.DefaultNS, "default")
	}
}
