package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validEnvironment() []string {
	return []string{
		"FANOUT_AUTH_CODE_SECRET=0123456789abcdef0123456789abcdef",
		"FANOUT_AI_API_KEY=sk-test",
		"FANOUT_SMTP_HOST=smtp.example.com",
		"FANOUT_SMTP_USERNAME=user",
		"FANOUT_SMTP_PASSWORD=pass",
		"FANOUT_SMTP_FROM=Fanout <noreply@example.com>",
	}
}

func TestLoadReturnsDefaults(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: validEnvironment()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

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
	// DuckDBMemory is no longer asserted to be empty: Load resolves it from
	// detected memory, and what it resolves to depends on the machine running
	// the test. On a host where detection declines it stays empty, which is the
	// documented fallback rather than a failure. The resolution rules
	// themselves are covered in sizing_test.go.
	if cfg.DuckDBThreads != 0 {
		t.Errorf("DuckDBThreads = %d, want 0 (DuckDB self-sizes to core count)", cfg.DuckDBThreads)
	}
	// Sized from the machine rather than fixed at 4, so assert the contract:
	// above the write-gate invariant and within the auto-sizing ceiling.
	if cfg.DuckDBMaxConns < minDuckDBConns || cfg.DuckDBMaxConns > maxAutoDuckDBConns {
		t.Errorf("DuckDBMaxConns = %d, want between %d and %d",
			cfg.DuckDBMaxConns, minDuckDBConns, maxAutoDuckDBConns)
	}
}

func TestLoadLayering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fanout.yaml")
	if err := os.WriteFile(path, []byte("server:\n  http_addr: ':1111'\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{
		Path:    path,
		Environ: append(validEnvironment(), "FANOUT_HTTP_ADDR=:2222"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":2222" {
		t.Fatalf("HTTPAddr = %q, want environment override", cfg.HTTPAddr)
	}
}

func TestExampleConfigurationMatchesSchema(t *testing.T) {
	path := filepath.Join("..", "..", "fanout.example.yaml")
	cfg, err := Load(LoadOptions{
		Path:    path,
		Environ: validEnvironment(),
	})
	if err != nil {
		t.Fatalf("Load example: %v", err)
	}
	if cfg.HTTPAddr != ":7520" || cfg.DataDir != "./data" {
		t.Fatalf("example defaults = HTTP %q data %q", cfg.HTTPAddr, cfg.DataDir)
	}
	defaults, err := Load(LoadOptions{Environ: validEnvironment()})
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if !reflect.DeepEqual(cfg, defaults) {
		t.Error("example values do not match built-in defaults")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	specs, err := configFieldSpecs()
	if err != nil {
		t.Fatalf("field specs: %v", err)
	}
	for _, spec := range specs {
		if !strings.Contains(string(raw), spec.env) {
			t.Errorf("example does not document %s", spec.env)
		}
	}
}

func TestLoadRejectsUnknownInputs(t *testing.T) {
	t.Run("YAML key", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fanout.yaml")
		if err := os.WriteFile(path, []byte("server:\n  htpp_addr: ':1111'\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
		if err == nil || !strings.Contains(err.Error(), "server.htpp_addr") {
			t.Fatalf("error = %v, want unknown YAML key", err)
		}
	})

	t.Run("environment variable", func(t *testing.T) {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_HTPP_ADDR=:1111")})
		if err == nil || !strings.Contains(err.Error(), "FANOUT_HTPP_ADDR") {
			t.Fatalf("error = %v, want unknown environment variable", err)
		}
	})
}

func TestLoadRejectsInvalidFilesAndValues(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := Load(LoadOptions{Path: filepath.Join(t.TempDir(), "missing.yaml"), Environ: validEnvironment()})
		if err == nil {
			t.Fatal("expected missing file error")
		}
	})

	t.Run("invalid type", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fanout.yaml")
		if err := os.WriteFile(path, []byte("ingest:\n  flush_seconds: never\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
		if err == nil {
			t.Fatal("expected type error")
		}
	})

	t.Run("invalid merged config", func(t *testing.T) {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_FLUSH_SECONDS=0")})
		if err == nil || !strings.Contains(err.Error(), "flush") {
			t.Fatalf("error = %v, want validation error", err)
		}
	})
}

func TestLoadIgnoresLegacyAndDotenvInputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("FANOUT_HTTP_ADDR=:9999\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Chdir(dir)

	environ := append(validEnvironment(), "HTTP_ADDR=:8888")
	cfg, err := Load(LoadOptions{Environ: environ})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":7520" {
		t.Fatalf("HTTPAddr = %q, want default", cfg.HTTPAddr)
	}
}

func TestLoadErrorsDoNotContainSecrets(t *testing.T) {
	const secret = "do-not-print-this-password"
	environ := []string{
		"FANOUT_AUTH_CODE_SECRET=0123456789abcdef0123456789abcdef",
		"FANOUT_AI_API_KEY=sk-test",
		"FANOUT_SMTP_PASSWORD=" + secret,
		"FANOUT_SMTP_USERNAME=user",
		"FANOUT_SMTP_FROM=noreply@example.com",
	}
	_, err := Load(LoadOptions{Environ: environ})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestValidate(t *testing.T) {
	valid := Config{
		FlushSeconds:       15,
		FlushBatchSize:     50000,
		RollupEvery:        60,
		RetentionDays:      30,
		AIProvider:         "anthropic",
		AIAPIKey:           "sk-test",
		SMTPHost:           "smtp.example.com",
		SMTPPort:           587,
		SMTPUser:           "user",
		SMTPPass:           "pass",
		SMTPFrom:           "Fanout <noreply@example.com>",
		AuthMode:           "local",
		AuthCodeSecret:     "0123456789abcdef0123456789abcdef",
		SessionIdleTTL:     12 * time.Hour,
		SessionAbsoluteTTL: 7 * 24 * time.Hour,
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
		{"SMTP missing host", func(c *Config) { c.SMTPHost = "" }},
		{"SMTP missing user", func(c *Config) { c.SMTPUser = "" }},
		{"SMTP missing pass", func(c *Config) { c.SMTPPass = "" }},
		{"SMTP missing from", func(c *Config) { c.SMTPFrom = "" }},
		{"SMTP invalid port", func(c *Config) { c.SMTPPort = 0 }},
		{"AI key empty", func(c *Config) { c.AIAPIKey = "" }},
		{"AI provider invalid", func(c *Config) { c.AIProvider = "unknown" }},
		{"auth code secret empty", func(c *Config) { c.AuthCodeSecret = "" }},
		{"auth code secret short", func(c *Config) { c.AuthCodeSecret = "short" }},
		{"session idle zero", func(c *Config) { c.SessionIdleTTL = 0 }},
		{"session idle too short", func(c *Config) { c.SessionIdleTTL = time.Minute }},
		{"session absolute zero", func(c *Config) { c.SessionAbsoluteTTL = 0 }},
		{"session absolute shorter than idle", func(c *Config) { c.SessionIdleTTL = time.Hour; c.SessionAbsoluteTTL = 30 * time.Minute }},
		{"trusted proxy invalid CIDR", func(c *Config) { c.TrustedProxyCIDRs = "private-network" }},
		{"TLS partial cert", func(c *Config) { c.TLSCertFile = "server.pem" }},
		{"TLS partial key", func(c *Config) { c.TLSKeyFile = "server-key.pem" }},
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

	t.Run("OIDC mode validates independently of SMTP", func(t *testing.T) {
		c := valid
		c.AuthMode = "oidc"
		c.AuthCodeSecret = ""
		c.SMTPHost, c.SMTPUser, c.SMTPPass, c.SMTPFrom = "", "", "", ""
		c.OIDCIssuerURL = "https://id.example.com"
		c.OIDCClientID = "fanout"
		c.OIDCClientSecret = "secret"
		c.OIDCEmailClaim = "email"
		c.OIDCEmailVerification = "required"
		c.OIDCDefaultRole = "viewer"
		c.PublicURL = "https://fanout.example.com"
		if err := c.Validate(); err != nil {
			t.Fatalf("OIDC config should pass: %v", err)
		}
	})

	t.Run("TLS_valid", func(t *testing.T) {
		c := valid
		c.TLSCertFile = "server.pem"
		c.TLSKeyFile = "server-key.pem"
		if err := c.Validate(); err != nil {
			t.Errorf("TLS config should be valid: %v", err)
		}
		if !c.TLSEnabled() {
			t.Error("TLSEnabled = false, want true")
		}
	})
}

func TestSecureCookies(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "plain HTTP", cfg: Config{}, want: false},
		{name: "external HTTPS", cfg: Config{PublicURL: "https://fanout.example.com"}, want: true},
		{name: "external HTTP", cfg: Config{PublicURL: "http://fanout.example.com"}, want: false},
		{name: "local TLS", cfg: Config{TLSCertFile: "server.pem", TLSKeyFile: "server-key.pem"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.SecureCookies(); got != tc.want {
				t.Fatalf("SecureCookies = %v, want %v", got, tc.want)
			}
		})
	}
}
