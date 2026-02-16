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
		{"True", false, true},
		{"TRUE", false, true},
		{"YES", false, true},
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

func TestValidate(t *testing.T) {
	valid := Config{
		FlushSeconds:   15,
		MaxRows:        50000,
		RollupEvery:    60,
		RetentionDays:  30,
		RetentionHours: 1,
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}

	tests := []struct {
		name   string
		modify func(*Config)
	}{
		{"FlushSeconds=0", func(c *Config) { c.FlushSeconds = 0 }},
		{"FlushSeconds=-1", func(c *Config) { c.FlushSeconds = -1 }},
		{"MaxRows=0", func(c *Config) { c.MaxRows = 0 }},
		{"MaxRows=-1", func(c *Config) { c.MaxRows = -1 }},
		{"RollupEvery=0", func(c *Config) { c.RollupEvery = 0 }},
		{"RetentionDays=-1", func(c *Config) { c.RetentionDays = -1 }},
		{"RetentionHours=0", func(c *Config) { c.RetentionHours = 0 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := valid // copy
			tc.modify(&c)
			if err := c.Validate(); err == nil {
				t.Error("expected validation error")
			}
		})
	}

	// RetentionDays=0 is valid (means "keep forever")
	t.Run("RetentionDays=0_valid", func(t *testing.T) {
		c := valid
		c.RetentionDays = 0
		if err := c.Validate(); err != nil {
			t.Errorf("RetentionDays=0 should be valid: %v", err)
		}
	})
}
