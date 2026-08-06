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
	if err := os.WriteFile(path, []byte(`server:
  http_addr: ":1111"
storage:
  merge_every_seconds: 75
mcp:
  enabled: false
metrics:
  public: true
auth:
  session_idle_ttl: 10h
`), 0o600); err != nil {
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
	if cfg.MergeEverySeconds != 75 || cfg.MCPEnabled || !cfg.MetricsPublic || cfg.SessionIdleTTL != 10*time.Hour {
		t.Fatalf("YAML values were not merged: %+v", cfg)
	}
}

func TestLoadTypedEnvironmentValues(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
		"FANOUT_MCP_ENABLED=false",
		"FANOUT_METRICS_PUBLIC=true",
		"FANOUT_MERGE_EVERY_SECONDS=75",
		"FANOUT_SESSION_IDLE_TTL=10h",
	)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPEnabled || !cfg.MetricsPublic || cfg.MergeEverySeconds != 75 || cfg.SessionIdleTTL != 10*time.Hour {
		t.Fatalf("environment values were not decoded: %+v", cfg)
	}
}

func TestLoadTreatsEmptyEnvironmentValuesAsAbsent(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
		"FANOUT_HTTP_ADDR=",
		"FANOUT_OTLP_GRPC_ADDR=",
		"FANOUT_MCP_ENABLED=",
		"FANOUT_RETENTION_DAYS=",
	)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":7520" || cfg.OTLPGRPCAddr != "127.0.0.1:4317" || !cfg.MCPEnabled || cfg.RetentionDays != 30 {
		t.Fatalf("empty environment values erased defaults: %+v", cfg)
	}
}

func TestLoadNormalizesMCPPublicURL(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
		"FANOUT_MCP_PUBLIC_URL=  https://fanout.example.com/mcp  ",
	)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPPublicURL != "https://fanout.example.com/mcp" {
		t.Fatalf("MCPPublicURL = %q, want normalized URL", cfg.MCPPublicURL)
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

func TestDockerConfigurationMatchesSchema(t *testing.T) {
	path := filepath.Join("..", "..", "fanout.docker.yaml")
	cfg, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
	if err != nil {
		t.Fatalf("Load Docker config: %v", err)
	}
	if cfg.HTTPAddr != ":7520" || cfg.OTLPGRPCAddr != ":4317" || cfg.DataDir != "/var/lib/fanout/data" {
		t.Fatalf("Docker config values = HTTP %q OTLP %q data %q", cfg.HTTPAddr, cfg.OTLPGRPCAddr, cfg.DataDir)
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

	t.Run("near platform variable", func(t *testing.T) {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_SERVICE_PORTAL=7520")})
		if err == nil || !strings.Contains(err.Error(), "FANOUT_SERVICE_PORTAL") {
			t.Fatalf("error = %v, want unknown environment variable", err)
		}
	})

	t.Run("malformed environment entry", func(t *testing.T) {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_HTTP_ADDR")})
		if err == nil || !strings.Contains(err.Error(), "NAME=value") {
			t.Fatalf("error = %v, want malformed environment error", err)
		}
	})
}

func TestLoadIgnoresKubernetesServiceEnvironment(t *testing.T) {
	environ := append(validEnvironment(),
		"FANOUT_SERVICE_HOST=10.0.0.1",
		"FANOUT_SERVICE_PORT=7520",
		"FANOUT_SERVICE_PORT_HTTP=7520",
		"FANOUT_PORT=tcp://10.0.0.1:7520",
		"FANOUT_PORT_7520_TCP_ADDR=10.0.0.1",
	)
	if _, err := Load(LoadOptions{Environ: environ}); err != nil {
		t.Fatalf("Load with Kubernetes service variables: %v", err)
	}
}

func TestLoadIgnoresEnvironmentOutsideNamespace(t *testing.T) {
	if _, err := Load(LoadOptions{Environ: append(validEnvironment(),
		"OTHER_APP_SETTING=value",
	)}); err != nil {
		t.Fatalf("Load with unrelated environment variable: %v", err)
	}
}

func TestLoadAllowsEmptyYAMLSections(t *testing.T) {
	for _, document := range []string{
		"metrics:\n",
		"auth:\n  oidc:\n    # client_id: fanout\n",
		"server: {}\n",
	} {
		path := filepath.Join(t.TempDir(), "fanout.yaml")
		if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(LoadOptions{Path: path, Environ: validEnvironment()}); err != nil {
			t.Fatalf("Load %q: %v", document, err)
		}
	}
}

func TestLoadRejectsYAMLNullValues(t *testing.T) {
	for _, key := range []struct {
		name, document string
	}{
		{"HTTP address", "server:\n  http_addr:\n"},
		{"MCP enabled", "mcp:\n  enabled:\n"},
		{"retention", "storage:\n  retention_days:\n"},
	} {
		t.Run(key.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fanout.yaml")
			if err := os.WriteFile(path, []byte(key.document), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
			if err == nil || !strings.Contains(err.Error(), "must not be null") {
				t.Fatalf("error = %v, want null rejection", err)
			}
		})
	}
}

func TestLoadRejectsInvalidFilesAndValues(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := Load(LoadOptions{Path: filepath.Join(t.TempDir(), "missing.yaml"), Environ: validEnvironment()})
		if err == nil {
			t.Fatal("expected missing file error")
		}
	})

	for _, test := range []struct {
		name, document string
	}{
		{"string as integer", "ingest:\n  flush_seconds: never\n"},
		{"fractional integer", "smtp:\n  port: 25.9\n"},
		{"integer as boolean", "mcp:\n  enabled: 2\n"},
		{"YAML keyword as boolean", "alerts:\n  enabled: off\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fanout.yaml")
			if err := os.WriteFile(path, []byte(test.document), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
			if err == nil {
				t.Fatal("expected type error")
			}
		})
	}

	t.Run("invalid merged config", func(t *testing.T) {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_FLUSH_SECONDS=0")})
		if err == nil || !strings.Contains(err.Error(), "flush") {
			t.Fatalf("error = %v, want validation error", err)
		}
	})

	t.Run("explicit empty auth mode", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fanout.yaml")
		if err := os.WriteFile(path, []byte("auth:\n  mode: \"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
		if err == nil || !strings.Contains(err.Error(), "auth.mode must not be empty") {
			t.Fatalf("error = %v, want explicit empty auth-mode rejection", err)
		}
	})

	for _, test := range []struct {
		name, assignment, key string
	}{
		{"negative max connections is not auto", "FANOUT_DUCKDB_MAX_CONNECTIONS=-3", "max_connections"},
		{"zero alert interval", "FANOUT_ALERTS_EVALUATION_INTERVAL_SECONDS=0", "evaluation_interval_seconds"},
		{"negative merge interval", "FANOUT_MERGE_EVERY_SECONDS=-5", "merge_every_seconds"},
		{"negative maintenance interval", "FANOUT_MAINTENANCE_EVERY_SECONDS=-5", "maintenance_every_seconds"},
		{"negative DuckDB threads", "FANOUT_DUCKDB_THREADS=-4", "duckdb.threads"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(LoadOptions{Environ: append(validEnvironment(), test.assignment)})
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("error = %v, want validation error for %s", err, test.key)
			}
		})
	}
}

func TestLoadIgnoresDotenvInputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("FANOUT_HTTP_ADDR=:9999\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Chdir(dir)

	cfg, err := Load(LoadOptions{Environ: validEnvironment()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":7520" {
		t.Fatalf("HTTPAddr = %q, want default", cfg.HTTPAddr)
	}
}

func TestLoadErrorsDoNotContainSecrets(t *testing.T) {
	const secret = "do-not-print-this-password"
	path := filepath.Join(t.TempDir(), "fanout.yaml")
	document := "smtp:\n  password: " + secret + "\n  port: 25.9\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
	if err == nil {
		t.Fatal("expected decode error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestLoadRequiresPrivatePermissionsForConfigSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fanout.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  api_key: secret-from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
	if err == nil || !strings.Contains(err.Error(), "must not be accessible by group or others") {
		t.Fatalf("error = %v, want file permission rejection", err)
	}
}

func TestValidate(t *testing.T) {
	valid := Config{
		HTTPAddr:                ":7520",
		OTLPGRPCAddr:            "127.0.0.1:4317",
		DataDir:                 "./data",
		FlushSeconds:            15,
		FlushBatchSize:          50000,
		RollupEvery:             60,
		RetentionDays:           30,
		MaintenanceEverySeconds: 3600,
		MergeEverySeconds:       60,
		DuckDBMaxConns:          4,
		AlertEvalInterval:       30,
		AlertHistoryDays:        7,
		AIProvider:              "anthropic",
		AIAPIKey:                "sk-test",
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
		{"FlushSeconds=0", func(c *Config) { c.FlushSeconds = 0 }},
		{"FlushSeconds=-1", func(c *Config) { c.FlushSeconds = -1 }},
		{"FlushBatchSize=0", func(c *Config) { c.FlushBatchSize = 0 }},
		{"FlushBatchSize=-1", func(c *Config) { c.FlushBatchSize = -1 }},
		{"RollupEvery=0", func(c *Config) { c.RollupEvery = 0 }},
		{"RollupEvery=-1", func(c *Config) { c.RollupEvery = -1 }},
		{"RetentionDays=-1", func(c *Config) { c.RetentionDays = -1 }},
		{"HTTPAddr empty", func(c *Config) { c.HTTPAddr = "" }},
		{"OTLPGRPCAddr empty", func(c *Config) { c.OTLPGRPCAddr = "" }},
		{"DataDir empty", func(c *Config) { c.DataDir = "" }},
		{"MaintenanceEverySeconds=0", func(c *Config) { c.MaintenanceEverySeconds = 0 }},
		{"MaintenanceEverySeconds=-1", func(c *Config) { c.MaintenanceEverySeconds = -1 }},
		{"MergeEverySeconds=-1", func(c *Config) { c.MergeEverySeconds = -1 }},
		{"DuckDBThreads=-1", func(c *Config) { c.DuckDBThreads = -1 }},
		{"DuckDBMaxConns=0", func(c *Config) { c.DuckDBMaxConns = 0 }},
		{"AlertEvalInterval=0", func(c *Config) { c.AlertEvalInterval = 0 }},
		{"AlertHistoryDays=-1", func(c *Config) { c.AlertHistoryDays = -1 }},
		{"AuthMode empty", func(c *Config) { c.AuthMode = "" }},
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

func TestFieldSpecsRejectInvalidSchemas(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{
			name: "duplicate key",
			typ: reflect.TypeOf(struct {
				A string `koanf:"same" env:"FANOUT_A"`
				B string `koanf:"same" env:"FANOUT_B"`
			}{}),
			want: "share key",
		},
		{
			name: "duplicate environment",
			typ: reflect.TypeOf(struct {
				A string `koanf:"a" env:"FANOUT_SAME"`
				B string `koanf:"b" env:"FANOUT_SAME"`
			}{}),
			want: "share environment variable",
		},
		{
			name: "missing tag",
			typ: reflect.TypeOf(struct {
				A string `koanf:"a"`
			}{}),
			want: "must declare",
		},
		{
			name: "missing prefix",
			typ: reflect.TypeOf(struct {
				A string `koanf:"a" env:"A"`
			}{}),
			want: "lacks FANOUT_ prefix",
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
