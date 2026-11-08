package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func validEnvironment() []string {
	return []string{
		"FANOUT_AUTH_CODE_SECRET=0123456789abcdef0123456789abcdef",
		"ANTHROPIC_API_KEY=sk-test",
		"FANOUT_SMTP_HOST=smtp.example.com",
		"FANOUT_SMTP_USERNAME=user",
		"FANOUT_SMTP_PASSWORD=pass",
		"FANOUT_SMTP_FROM=Fanout <noreply@example.com>",
	}
}

func TestLoadReturnsDefaults(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: validEnvironment()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:7520" || cfg.DataDir != "./data" || cfg.IngestBatchSize != 50000 || cfg.RollupInterval != time.Minute || cfg.RetentionDays != 30 || cfg.DefaultNamespace != "default" || !cfg.MCPEnabled || !cfg.AlertEnabled || cfg.AIProvider != "" {
		t.Fatal("built-in defaults were not resolved")
	}
	if cfg.DuckDBThreads != runtime.GOMAXPROCS(0) || cfg.DuckDBMaxConns < minDuckDBConns || cfg.DuckDBMaxConns > maxAutoDuckDBConns {
		t.Fatal("machine sizing did not resolve the connection and thread defaults")
	}
}

func TestLoadTypedEnvironmentValues(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
		"FANOUT_ADDR=:9000", "FANOUT_MCP_ENABLED=false", "FANOUT_METRICS_PUBLIC=true",
		"FANOUT_SESSION_IDLE_TTL=10h", "FANOUT_SELF_SIGNUP=true", "FANOUT_RETENTION_DAYS=0",
		"FANOUT_ROLLUP_INTERVAL=5m", "FANOUT_MAINTENANCE_INTERVAL=2h", "FANOUT_ALERTS_EVALUATION_INTERVAL=45s",
	)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9000" || cfg.MCPEnabled || !cfg.MetricsPublic || cfg.SessionIdleTTL != 10*time.Hour || !cfg.SelfSignup || cfg.RetentionDays != 0 || cfg.RollupInterval != 5*time.Minute || cfg.MaintenanceInterval != 2*time.Hour || cfg.AlertEvaluationInterval != 45*time.Second {
		t.Fatal("environment values were not decoded")
	}
}

func TestLoadTreatsEmptyEnvironmentValuesAsAbsent(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_ADDR=", "FANOUT_MCP_ENABLED=", "FANOUT_RETENTION_DAYS=", "FANOUT_AUTH_CODE_SECRET_FILE=")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:7520" || !cfg.MCPEnabled || cfg.RetentionDays != 30 {
		t.Fatal("empty values erased defaults")
	}
}

func TestConfigurationSchemaUsesUnitBearingDurations(t *testing.T) {
	specs, err := configFieldSpecs()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if strings.HasSuffix(spec.env, "_SECONDS") || strings.HasSuffix(spec.env, "_MS") {
			t.Errorf("unit suffix in %s", spec.env)
		}
		if spec.typ == reflect.TypeOf(time.Duration(0)) && !strings.HasSuffix(spec.env, "_INTERVAL") && !strings.HasSuffix(spec.env, "_TTL") {
			t.Errorf("duration name %s must end in _INTERVAL or _TTL", spec.env)
		}
	}
}

func TestLoadRejectsUnknownAndMalformedEnvironment(t *testing.T) {
	for _, entry := range []string{"FANOUT_HTPP_ADDR=:1111", "FANOUT_SERVICE_PORTAL=7520", "FANOUT_ADDR", "FANOUT_ADDR_FILE=/unused", "FANOUT_UNKNOWN_SETTING=synthetic-private-value"} {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), entry)})
		name, _, _ := strings.Cut(entry, "=")
		if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "synthetic-private-value") {
			t.Fatalf("expected redacted error naming %s", name)
		}
	}
}

func TestLoadRejectsDuplicateEnvironmentNames(t *testing.T) {
	for _, entry := range []string{"FANOUT_ADDR=", "ANTHROPIC_API_KEY=test", "FANOUT_METRICS_TOKEN_FILE="} {
		name, _, _ := strings.Cut(entry, "=")
		_, err := Load(LoadOptions{Environ: []string{entry, entry}})
		if err == nil || !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), name) {
			t.Fatalf("expected duplicate error for %s", name)
		}
	}
}

func TestLoadIgnoresPlatformAndUnrelatedEnvironment(t *testing.T) {
	env := append(validEnvironment(), "FANOUT_SERVICE_HOST=10.0.0.1", "FANOUT_SERVICE_PORT=7520", "FANOUT_SERVICE_PORT_HTTP=7520", "FANOUT_PORT=tcp://10.0.0.1:7520", "FANOUT_PORT_7520_TCP_ADDR=10.0.0.1", "OTHER_APP_SETTING=value", "OPENAI_MODEL=other-model")
	if _, err := Load(LoadOptions{Environ: env}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsInvalidTypedValuesWithoutLeakingValues(t *testing.T) {
	for _, entry := range []string{
		"FANOUT_ROLLUP_INTERVAL=private-value", "FANOUT_SMTP_PORT=25.9", "FANOUT_MCP_ENABLED=off", "FANOUT_RETENTION_DAYS=999999999999999999999999999",
		"FANOUT_INGEST_BATCH_SIZE=0", "FANOUT_DUCKDB_MAX_CONNECTIONS=-3", "FANOUT_DUCKDB_THREADS=-4",
		"FANOUT_ROLLUP_INTERVAL=999ms", "FANOUT_MAINTENANCE_INTERVAL=-5s", "FANOUT_ALERTS_EVALUATION_INTERVAL=0s",
	} {
		env := validEnvironment()
		name, _, _ := strings.Cut(entry, "=")
		for i, existing := range env {
			if strings.HasPrefix(existing, name+"=") {
				env = append(env[:i], env[i+1:]...)
				break
			}
		}
		_, err := Load(LoadOptions{Environ: append(env, entry)})
		if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("expected redacted validation error for %s", name)
		}
	}
}

func TestLoadIgnoresDotenvInputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("FANOUT_ADDR=:9999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfg, err := Load(LoadOptions{Environ: validEnvironment()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:7520" {
		t.Fatal("binary loaded dotenv")
	}
}

func TestLoadSecretFiles(t *testing.T) {
	names := []string{"FANOUT_AUTH_CODE_SECRET", "FANOUT_OIDC_CLIENT_SECRET", "FANOUT_SMTP_PASSWORD", "FANOUT_METRICS_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"}
	specs, err := configFieldSpecs()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		for _, tc := range []struct{ name, suffix, retained string }{
			{"no newline", "", ""},
			{"LF", "\n", ""},
			{"CRLF", "\r\n", ""},
			{"two LF", "\n\n", "\n"},
			{"two CRLF", "\r\n\r\n", "\r\n"},
			{"bare CR", "\r", "\r"},
			{"spaces", "  \n", "  "},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				secret := strings.Repeat("x", 32)
				path := filepath.Join(t.TempDir(), "secret")
				if err := os.WriteFile(path, []byte(secret+tc.suffix), 0o644); err != nil {
					t.Fatal(err)
				}
				env := []string{}
				for _, entry := range validEnvironment() {
					if !strings.HasPrefix(entry, name+"=") {
						env = append(env, entry)
					}
				}
				env = append(env, name+"=", name+"_FILE="+path)
				if provider := providerForEnvironment(name); provider != "" {
					env = append(env, "FANOUT_AI_PROVIDER="+provider)
				}
				cfg, err := Load(LoadOptions{Environ: env})
				if err != nil {
					t.Fatal(err)
				}
				want := secret + tc.retained
				for _, spec := range specs {
					if spec.env == name && reflect.ValueOf(cfg).Field(spec.index).String() != want {
						t.Fatalf("%s did not preserve exact file contents", name)
					}
				}
			})
		}
	}
}

func TestLoadRejectsConflictingOrInvalidSecretFiles(t *testing.T) {
	const name = "FANOUT_METRICS_TOKEN"
	const secret = "synthetic-private-value"
	path := filepath.Join(t.TempDir(), "secret")
	for _, content := range []string{"", "\n", "\r\n", secret} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		env := append(validEnvironment(), name+"_FILE="+path)
		if content == secret {
			env = append(env, name+"="+secret)
		}
		_, err := Load(LoadOptions{Environ: env})
		if err == nil || !strings.Contains(err.Error(), name+"_FILE") || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), secret) {
			t.Fatal("expected redacted file error naming variable and path")
		}
	}
	for _, invalidPath := range []string{path + "-missing", filepath.Dir(path)} {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"_FILE="+invalidPath)})
		if err == nil || !strings.Contains(err.Error(), name+"_FILE") || !strings.Contains(err.Error(), invalidPath) {
			t.Fatal("expected file read error naming variable and path")
		}
	}
}

func TestMCPResourceURL(t *testing.T) {
	for _, tc := range []struct{ publicURL, want string }{
		{"", "https://localhost:7520/mcp"}, {"https://fanout.example.com", "https://fanout.example.com/mcp"}, {"  https://fanout.example.com/  ", "https://fanout.example.com/mcp"},
	} {
		if got := (Config{PublicURL: tc.publicURL}).MCPResourceURL(); got != tc.want {
			t.Fatalf("MCPResourceURL = %s, want %s", got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	valid := Config{
		Addr:                    ":7520",
		DataDir:                 "./data",
		IngestBatchSize:         50000,
		RollupInterval:          time.Minute,
		RetentionDays:           30,
		MaintenanceInterval:     time.Hour,
		DuckDBMaxConns:          4,
		AlertEvaluationInterval: 30 * time.Second,
		AlertHistoryDays:        7,
		AIProvider:              "anthropic",
		AnthropicAPIKey:         "sk-test",
		SMTPHost:                "smtp.example.com",
		SMTPPort:                587,
		SMTPUser:                "user",
		SMTPPass:                "pass",
		SMTPFrom:                "Fanout <noreply@example.com>",
		AuthMode:                "local",
		AuthCodeSecret:          "0123456789abcdef0123456789abcdef",
		SessionIdleTTL:          12 * time.Hour,
		SessionAbsoluteTTL:      7 * 24 * time.Hour,
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}

	tests := []struct {
		name   string
		modify func(*Config)
	}{
		{"IngestBatchSize=0", func(c *Config) { c.IngestBatchSize = 0 }},
		{"IngestBatchSize=-1", func(c *Config) { c.IngestBatchSize = -1 }},
		{"IngestBatchSize=50001", func(c *Config) { c.IngestBatchSize = 50001 }},
		{"RollupInterval=0", func(c *Config) { c.RollupInterval = 0 }},
		{"RollupInterval=999ms", func(c *Config) { c.RollupInterval = 999 * time.Millisecond }},
		{"RetentionDays=-1", func(c *Config) { c.RetentionDays = -1 }},
		{"Addr empty", func(c *Config) { c.Addr = "" }},
		{"DataDir empty", func(c *Config) { c.DataDir = "" }},
		{"MaintenanceInterval=0", func(c *Config) { c.MaintenanceInterval = 0 }},
		{"MaintenanceInterval=999ms", func(c *Config) { c.MaintenanceInterval = 999 * time.Millisecond }},
		{"DuckDBThreads=-1", func(c *Config) { c.DuckDBThreads = -1 }},
		{"DuckDBMaxConns=0", func(c *Config) { c.DuckDBMaxConns = 0 }},
		{"AlertEvaluationInterval=0", func(c *Config) { c.AlertEvaluationInterval = 0 }},
		{"AlertEvaluationInterval=999ms", func(c *Config) { c.AlertEvaluationInterval = 999 * time.Millisecond }},
		{"AlertHistoryDays=-1", func(c *Config) { c.AlertHistoryDays = -1 }},
		{"AuthMode empty", func(c *Config) { c.AuthMode = "" }},
		{"SMTP missing host", func(c *Config) { c.SMTPHost = "" }},
		{"SMTP missing user", func(c *Config) { c.SMTPUser = "" }},
		{"SMTP missing pass", func(c *Config) { c.SMTPPass = "" }},
		{"SMTP missing from", func(c *Config) { c.SMTPFrom = "" }},
		{"SMTP invalid port", func(c *Config) { c.SMTPPort = 0 }},
		{"AI provider invalid", func(c *Config) { c.AIProvider = "unknown" }},
		{"auth code secret empty", func(c *Config) { c.AuthCodeSecret = "" }},
		{"auth code secret short", func(c *Config) { c.AuthCodeSecret = "short" }},
		{"self-signup without SMTP", func(c *Config) { c.SelfSignup = true; c.SMTPHost = "" }},
		{"local self-signup in OIDC mode", func(c *Config) {
			c.SelfSignup = true
			c.AuthMode = "oidc"
			c.OIDCIssuerURL = "https://id.example.com"
			c.OIDCClientID = "client"
			c.OIDCClientSecret = "secret"
			c.PublicURL = "https://fanout.example.com"
		}},
		{"session idle zero", func(c *Config) { c.SessionIdleTTL = 0 }},
		{"session idle too short", func(c *Config) { c.SessionIdleTTL = time.Minute }},
		{"session absolute zero", func(c *Config) { c.SessionAbsoluteTTL = 0 }},
		{"session absolute shorter than idle", func(c *Config) { c.SessionIdleTTL = time.Hour; c.SessionAbsoluteTTL = 30 * time.Minute }},
		{"trusted proxy invalid CIDR", func(c *Config) { c.TrustedProxyCIDRs = "private-network" }},
		{"TLS partial cert", func(c *Config) { c.TLSCertFile = "server.pem" }},
		{"TLS partial key", func(c *Config) { c.TLSKeyFile = "server-key.pem" }},
		{"TLS client CA without TLS", func(c *Config) { c.TLSClientCAFile = "client-ca.pem" }},
		{"TLS client CA without key", func(c *Config) { c.TLSCertFile = "server.pem"; c.TLSClientCAFile = "client-ca.pem" }},
		{"healthcheck cert without key", func(c *Config) {
			c.TLSCertFile, c.TLSKeyFile, c.TLSClientCAFile = "server.pem", "server-key.pem", "client-ca.pem"
			c.HealthcheckCertFile = "probe.pem"
		}},
		{"healthcheck key without cert", func(c *Config) {
			c.TLSCertFile, c.TLSKeyFile, c.TLSClientCAFile = "server.pem", "server-key.pem", "client-ca.pem"
			c.HealthcheckKeyFile = "probe-key.pem"
		}},
		{"healthcheck pair without client CA", func(c *Config) {
			c.TLSCertFile, c.TLSKeyFile = "server.pem", "server-key.pem"
			c.HealthcheckCertFile, c.HealthcheckKeyFile = "probe.pem", "probe-key.pem"
		}},
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

	t.Run("local mode allows absent SMTP and agent", func(t *testing.T) {
		c := valid
		c.SMTPHost, c.SMTPUser, c.SMTPPass, c.SMTPFrom = "", "", "", ""
		c.AnthropicAPIKey = ""
		c.AIProvider = ""
		if err := c.Validate(); err != nil {
			t.Fatalf("minimal local config should pass: %v", err)
		}
		if c.SMTPConfigured() || c.AgentConfigured() {
			t.Fatal("absent optional services reported as configured")
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

	t.Run("OIDC startup tolerates an unusable login-link secret", func(t *testing.T) {
		c := valid
		c.AuthMode = "oidc"
		c.OIDCIssuerURL = "https://id.example.com"
		c.OIDCClientID = "fanout"
		c.OIDCClientSecret = "secret"
		c.OIDCEmailClaim = "email"
		c.OIDCEmailVerification = "required"
		c.OIDCDefaultRole = "viewer"
		c.PublicURL = "https://fanout.example.com"
		for _, secret := range []string{"", "short"} {
			c.AuthCodeSecret = secret
			if err := c.Validate(); err != nil {
				t.Fatalf("secret %q should not fail OIDC startup: %v", secret, err)
			}
			err := c.ValidateLoginLinkSecret()
			if err == nil || !strings.Contains(err.Error(), "FANOUT_AUTH_CODE_SECRET") {
				t.Fatalf("secret %q: ValidateLoginLinkSecret = %v, want an error naming the variable", secret, err)
			}
		}
		c.AuthCodeSecret = strings.Repeat("x", 32)
		if err := c.ValidateLoginLinkSecret(); err != nil {
			t.Fatalf("32-character secret should be usable: %v", err)
		}
	})

	t.Run("OIDC verified email wildcard is explicit and valid", func(t *testing.T) {
		c := valid
		c.AuthMode = "oidc"
		c.AuthCodeSecret = ""
		c.OIDCIssuerURL = "https://id.example.com"
		c.OIDCClientID = "fanout"
		c.OIDCClientSecret = "secret"
		c.OIDCEmailClaim = "email"
		c.OIDCEmailVerification = "required"
		c.OIDCAutoProvision = true
		c.OIDCAllowedDomains = "*"
		c.OIDCDefaultRole = "viewer"
		c.PublicURL = "https://fanout.example.com"
		if err := c.Validate(); err != nil {
			t.Fatalf("verified email wildcard should pass: %v", err)
		}

		c.OIDCEmailVerification = "issuer"
		if err := c.Validate(); err == nil {
			t.Fatal("issuer-trusted email wildcard should be rejected")
		}
	})

	t.Run("MCP derives its HTTPS resource from the public origin", func(t *testing.T) {
		c := valid
		c.MCPEnabled = true
		c.PublicURL = "https://fanout.example.com"
		if err := c.Validate(); err != nil {
			t.Fatalf("HTTPS public origin should pass: %v", err)
		}
		if got := c.MCPResourceURL(); got != "https://fanout.example.com/mcp" {
			t.Fatalf("MCPResourceURL() = %q", got)
		}

		for _, invalid := range []string{
			"http://fanout.example.com",
			"https://user@fanout.example.com",
			"https://fanout.example.com/base",
			"https://fanout.example.com?tenant=one",
		} {
			c.PublicURL = invalid
			if err := c.Validate(); err == nil {
				t.Errorf("FANOUT_PUBLIC_URL %q should be rejected with MCP enabled", invalid)
			}
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
		if c.ClientCertificatesRequired() {
			t.Error("ClientCertificatesRequired = true without FANOUT_TLS_CLIENT_CA_FILE")
		}
	})

	t.Run("TLS_client_CA_valid", func(t *testing.T) {
		c := valid
		c.TLSCertFile, c.TLSKeyFile, c.TLSClientCAFile = "server.pem", "server-key.pem", "client-ca.pem"
		if err := c.Validate(); err != nil {
			t.Errorf("client CA config should be valid: %v", err)
		}
		if !c.ClientCertificatesRequired() {
			t.Error("ClientCertificatesRequired = false, want true")
		}
		c.HealthcheckCertFile, c.HealthcheckKeyFile = "probe.pem", "probe-key.pem"
		if err := c.Validate(); err != nil {
			t.Errorf("healthcheck client certificate config should be valid: %v", err)
		}
	})
}

func TestFieldSpecsRejectInvalidSchemas(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{
			name: "secret file name collision",
			typ: reflect.TypeOf(struct {
				A string `env:"FANOUT_SECRET" secret:"true"`
				B string `env:"FANOUT_SECRET_FILE"`
			}{}),
			want: "share environment variable",
		},
		{
			name: "duplicate environment",
			typ: reflect.TypeOf(struct {
				A string `env:"FANOUT_SAME"`
				B string `env:"FANOUT_SAME"`
			}{}),
			want: "share environment variable",
		},
		{
			name: "missing tag",
			typ: reflect.TypeOf(struct {
				A string
			}{}),
			want: "must declare",
		},
		{
			name: "missing prefix",
			typ: reflect.TypeOf(struct {
				A string `env:"A"`
			}{}),
			want: "outside the supported configuration contract",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fieldSpecs(test.typ)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
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

func TestLogStartupWarnsAboutUnusableBreakGlassSecret(t *testing.T) {
	const shortSecret = "not-long-enough-value"
	for _, tc := range []struct {
		name   string
		mode   string
		secret string
		want   bool
	}{
		{"oidc short", "oidc", shortSecret, true},
		{"oidc normalized mode short", " OIDC ", shortSecret, true},
		{"oidc unset", "oidc", "", false},
		{"oidc usable", "oidc", strings.Repeat("x", 32), false},
		{"local short", "local", shortSecret, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			Config{AuthMode: tc.mode, AuthCodeSecret: tc.secret}.LogStartup()

			output := logs.String()
			got := strings.Contains(output, "FANOUT_AUTH_CODE_SECRET") && strings.Contains(output, "break-glass")
			if got != tc.want {
				t.Fatalf("warning present = %v, want %v; logs: %s", got, tc.want, output)
			}
			if strings.Contains(output, shortSecret) {
				t.Fatalf("logs contain the secret value: %s", output)
			}
		})
	}
}

func TestLoadIngestToken(t *testing.T) {
	const name = "FANOUT_INGEST_TOKEN"
	valid := "foi_" + strings.Repeat("A", 43)

	t.Run("absent by default", func(t *testing.T) {
		cfg, err := Load(LoadOptions{Environ: validEnvironment()})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.IngestToken != "" {
			t.Fatal("ingest token must default to empty")
		}
	})

	t.Run("environment value", func(t *testing.T) {
		cfg, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"="+valid)})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.IngestToken != valid {
			t.Fatal("ingest token was not loaded from the environment")
		}
	})

	t.Run("file value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ingest-token")
		if err := os.WriteFile(path, []byte(valid+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"_FILE="+path)})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.IngestToken != valid {
			t.Fatal("ingest token was not loaded from its file")
		}
	})

	for _, malformed := range []string{
		"synthetic-private-value",
		"foi_" + strings.Repeat("A", 42),
		"foi_" + strings.Repeat("A", 44),
		"foi_" + strings.Repeat("A", 42) + "+",
		"fo_" + strings.Repeat("A", 43),
		strings.Repeat("A", 43),
		" " + valid,
	} {
		t.Run("rejects malformed environment value", func(t *testing.T) {
			_, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"="+malformed)})
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), malformed) {
				t.Fatal("expected redacted error naming the setting")
			}
		})
		t.Run("rejects malformed file value", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ingest-token")
			if err := os.WriteFile(path, []byte(malformed), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"_FILE="+path)})
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), malformed) {
				t.Fatal("expected redacted error naming the setting")
			}
		})
	}

	t.Run("rejects both forms", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ingest-token")
		if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"="+valid, name+"_FILE="+path)})
		if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), valid) {
			t.Fatal("expected redacted conflict error naming the setting")
		}
	})
}

func TestAlertEmailSettings(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_ALERTS_EMAIL_DEFAULT_TO=Oncall@example.com, b@example.com")})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := cfg.AlertEmailDefaults()
	if err != nil || !reflect.DeepEqual(defaults, []string{"oncall@example.com", "b@example.com"}) {
		t.Fatalf("defaults = %v, %v", defaults, err)
	}
	if cfg.AlertEmailHourlyLimit != 30 {
		t.Fatalf("hourly limit = %d, want 30", cfg.AlertEmailHourlyLimit)
	}

	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"invalid address", append(validEnvironment(), "FANOUT_ALERTS_EMAIL_DEFAULT_TO=nope"), "FANOUT_ALERTS_EMAIL_DEFAULT_TO"},
		{"negative limit", append(validEnvironment(), "FANOUT_ALERTS_EMAIL_HOURLY_LIMIT=-1"), "FANOUT_ALERTS_EMAIL_HOURLY_LIMIT"},
		{"no SMTP", []string{"FANOUT_AUTH_CODE_SECRET=0123456789abcdef0123456789abcdef", "ANTHROPIC_API_KEY=sk-test", "FANOUT_ALERTS_EMAIL_DEFAULT_TO=a@example.com"}, "requires SMTP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(LoadOptions{Environ: tc.env}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadSCIMToken(t *testing.T) {
	const name = "FANOUT_SCIM_TOKEN"
	valid := strings.Repeat("s", 32)
	oidc := append(validEnvironment(),
		"FANOUT_AUTH_MODE=oidc",
		"FANOUT_OIDC_ISSUER_URL=https://issuer.example.com",
		"FANOUT_OIDC_CLIENT_ID=client",
		"FANOUT_OIDC_CLIENT_SECRET=secret",
		"FANOUT_PUBLIC_URL=https://fanout.example.com",
	)

	t.Run("absent by default", func(t *testing.T) {
		cfg, err := Load(LoadOptions{Environ: oidc})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SCIMToken != "" {
			t.Fatal("SCIM token must default to empty")
		}
	})

	t.Run("environment value", func(t *testing.T) {
		cfg, err := Load(LoadOptions{Environ: append(slices.Clone(oidc), name+"="+valid)})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SCIMToken != valid {
			t.Fatal("SCIM token was not loaded from the environment")
		}
	})

	t.Run("file value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "scim-token")
		if err := os.WriteFile(path, []byte(valid+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(LoadOptions{Environ: append(slices.Clone(oidc), name+"_FILE="+path)})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SCIMToken != valid {
			t.Fatal("SCIM token was not loaded from its file")
		}
	})

	t.Run("requires OIDC mode", func(t *testing.T) {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"="+valid)})
		if err == nil || !strings.Contains(err.Error(), "FANOUT_AUTH_MODE=oidc") {
			t.Fatalf("Load = %v", err)
		}
	})

	for label, token := range map[string]string{"short": strings.Repeat("s", 31), "whitespace": strings.Repeat("s", 32) + " x"} {
		t.Run(label, func(t *testing.T) {
			_, err := Load(LoadOptions{Environ: append(slices.Clone(oidc), name+"="+token)})
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), token) {
				t.Fatalf("Load = %v", err)
			}
		})
	}
}
