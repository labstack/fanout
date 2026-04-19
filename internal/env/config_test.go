package env

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	vars := []string{
		"HTTP_ADDR", "OTLP_GRPC_ADDR", "DATA_DIR", "FLUSH_SECONDS",
		"FLUSH_BATCH_SIZE", "ROLLUP_EVERY",
		"RETENTION_DAYS", "TENANT_ID", "DEFAULT_NAMESPACE",
		"AI_PROVIDER", "AI_API_KEY", "AI_MODEL", "AI_BASE_URL",
		"JWT_SECRET", "JWT_REFRESH_SECRET",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM",
		"OTLP_TLS_CERT_FILE", "OTLP_TLS_KEY_FILE",
	}
	for _, v := range vars {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_REFRESH_SECRET", "abcdef0123456789abcdef0123456789")
	t.Setenv("AI_API_KEY", "sk-test")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_USER", "user")
	t.Setenv("SMTP_PASS", "pass")
	t.Setenv("SMTP_FROM", "Fanout <noreply@example.com>")

	cfg := Load()

	if cfg.HTTPAddr != ":7520" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":7520")
	}
	if cfg.OTLPGRPCAddr != "127.0.0.1:4317" {
		t.Errorf("OTLPGRPCAddr = %q, want %q", cfg.OTLPGRPCAddr, "127.0.0.1:4317")
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
		{"JWTSecret empty", func(c *Config) { c.JWTSecret = "" }},
		{"JWTSecret short", func(c *Config) { c.JWTSecret = "short" }},
		{"JWTRefreshSecret empty", func(c *Config) { c.JWTRefreshSecret = "" }},
		{"JWTRefreshSecret short", func(c *Config) { c.JWTRefreshSecret = "short" }},
		{"JWT secrets equal", func(c *Config) { c.JWTRefreshSecret = c.JWTSecret }},
		{"OTLP TLS partial cert", func(c *Config) { c.OTLPTLSCertFile = "server.pem" }},
		{"OTLP TLS partial key", func(c *Config) { c.OTLPTLSKeyFile = "server-key.pem" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.modify(&c)
			if err := c.Validate(); err == nil {
				t.Error("expected validation error")
			}
		})
	}

	t.Run("RetentionDays=0_valid", func(t *testing.T) {
		c := valid
		c.RetentionDays = 0
		if err := c.Validate(); err != nil {
			t.Errorf("RetentionDays=0 should be valid: %v", err)
		}
	})

	t.Run("OTLPTLS_valid", func(t *testing.T) {
		c := valid
		c.OTLPTLSCertFile = "server.pem"
		c.OTLPTLSKeyFile = "server-key.pem"
		if err := c.Validate(); err != nil {
			t.Errorf("OTLP TLS config should be valid: %v", err)
		}
		if !c.OTLPTLSEnabled() {
			t.Error("OTLPTLSEnabled = false, want true")
		}
	})
}
