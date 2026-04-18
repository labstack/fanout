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
	vars := []string{"HTTP_ADDR", "OTLP_GRPC_ADDR", "DATA_DIR", "FLUSH_SECONDS",
		"FLUSH_BATCH_SIZE", "ROLLUP_EVERY",
		"RETENTION_DAYS", "TENANT_ID", "DEFAULT_NAMESPACE",
		"AI_PROVIDER", "AI_API_KEY", "AI_MODEL", "AI_BASE_URL", "SETUP_TOKEN", "JWT_SECRET", "JWT_REFRESH_SECRET",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM",
		"OTLP_TLS_CERT_FILE", "OTLP_TLS_KEY_FILE", "OTLP_TLS_CLIENT_CA_FILE"}
	for _, v := range vars {
		os.Unsetenv(v)
	}
	os.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	os.Setenv("JWT_REFRESH_SECRET", "abcdef0123456789abcdef0123456789")
	os.Setenv("AI_API_KEY", "sk-test")
	os.Setenv("SMTP_HOST", "smtp.example.com")
	os.Setenv("SMTP_USER", "user")
	os.Setenv("SMTP_PASS", "pass")
	os.Setenv("SMTP_FROM", "Fanout <noreply@example.com>")
	os.Setenv("SETUP_TOKEN", "setup-token-012345")
	defer os.Unsetenv("JWT_SECRET")
	defer os.Unsetenv("JWT_REFRESH_SECRET")
	defer os.Unsetenv("AI_API_KEY")
	defer os.Unsetenv("SMTP_HOST")
	defer os.Unsetenv("SMTP_USER")
	defer os.Unsetenv("SMTP_PASS")
	defer os.Unsetenv("SMTP_FROM")
	defer os.Unsetenv("SETUP_TOKEN")

	cfg := Load()

	// Check defaults
	if cfg.HTTPAddr != ":7520" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":7520")
	}
	if cfg.OTLPGRPCAddr != ":4317" {
		t.Errorf("OTLPGRPCAddr = %q, want %q", cfg.OTLPGRPCAddr, ":4317")
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "./data")
	}
	if cfg.FlushSeconds != 15 {
		t.Errorf("FlushSeconds = %d, want %d", cfg.FlushSeconds, 15)
	}
	if cfg.FlushBatchSize != 50000 {
		t.Errorf("FlushBatchSize = %d, want %d", cfg.FlushBatchSize, 50000)
	}
	if cfg.RollupEvery != 60 {
		t.Errorf("RollupEvery = %d, want %d", cfg.RollupEvery, 60)
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
		FlushSeconds:     15,
		FlushBatchSize:   50000,
		RollupEvery:      60,
		RetentionDays:    30,
		AIProvider:       "anthropic",
		AIAPIKey:         "sk-test",
		SMTPHost:         "smtp.example.com",
		SMTPPort:         587,
		SMTPUser:         "user",
		SMTPPass:         "pass",
		SMTPFrom:         "Fanout <noreply@example.com>",
		SetupToken:       "setup-token-012345",
		JWTSecret:        "0123456789abcdef0123456789abcdef",
		JWTRefreshSecret: "abcdef0123456789abcdef0123456789",
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
		{"FlushBatchSize=0", func(c *Config) { c.FlushBatchSize = 0 }},
		{"FlushBatchSize=-1", func(c *Config) { c.FlushBatchSize = -1 }},
		{"RollupEvery=0", func(c *Config) { c.RollupEvery = 0 }},
		{"RollupEvery=-1", func(c *Config) { c.RollupEvery = -1 }},
		{"RetentionDays=-1", func(c *Config) { c.RetentionDays = -1 }},
		{"AI API key missing", func(c *Config) { c.AIAPIKey = "" }},
		{"AI provider invalid", func(c *Config) { c.AIProvider = "invalid" }},
		{"SMTP missing host", func(c *Config) { c.SMTPHost = "" }},
		{"SMTP missing user", func(c *Config) { c.SMTPUser = "" }},
		{"SMTP missing pass", func(c *Config) { c.SMTPPass = "" }},
		{"SMTP missing from", func(c *Config) { c.SMTPFrom = "" }},
		{"SMTP invalid port", func(c *Config) { c.SMTPPort = 0 }},
		{"SETUP_TOKEN empty", func(c *Config) { c.SetupToken = "" }},
		{"SETUP_TOKEN short", func(c *Config) { c.SetupToken = "short-token" }},
		{"JWTSecret empty", func(c *Config) { c.JWTSecret = "" }},
		{"JWTSecret short", func(c *Config) { c.JWTSecret = "short" }},
		{"JWTRefreshSecret empty", func(c *Config) { c.JWTRefreshSecret = "" }},
		{"JWTRefreshSecret short", func(c *Config) { c.JWTRefreshSecret = "short" }},
		{"JWT secrets equal", func(c *Config) { c.JWTRefreshSecret = c.JWTSecret }},
		{"OTLP mTLS partial cert", func(c *Config) { c.OTLPTLSCertFile = "server.pem" }},
		{"OTLP mTLS partial key", func(c *Config) { c.OTLPTLSKeyFile = "server-key.pem" }},
		{"OTLP mTLS partial client ca", func(c *Config) { c.OTLPTLSClientCAFile = "ca.pem" }},
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

	t.Run("OTLPMTLS_valid", func(t *testing.T) {
		c := valid
		c.OTLPTLSCertFile = "server.pem"
		c.OTLPTLSKeyFile = "server-key.pem"
		c.OTLPTLSClientCAFile = "ca.pem"
		if err := c.Validate(); err != nil {
			t.Errorf("OTLP mTLS config should be valid: %v", err)
		}
		if !c.OTLPMTLSEnabled() {
			t.Error("OTLPMTLSEnabled = false, want true")
		}
	})
}
