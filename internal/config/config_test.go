package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
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
	if cfg.OTLPHTTPAddr != "127.0.0.1:4318" {
		t.Errorf("OTLPHTTPAddr = %q, want %q", cfg.OTLPHTTPAddr, "127.0.0.1:4318")
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "./data")
	}
	if cfg.FlushInterval != 15*time.Second {
		t.Errorf("FlushInterval = %s, want %s", cfg.FlushInterval, 15*time.Second)
	}
	if cfg.FlushBatchSize != 50000 {
		t.Errorf("FlushBatchSize = %d, want %d", cfg.FlushBatchSize, 50000)
	}
	if cfg.RollupInterval != time.Minute {
		t.Errorf("RollupInterval = %s, want %s", cfg.RollupInterval, time.Minute)
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, 30)
	}
	if cfg.DefaultNamespace != "default" {
		t.Errorf("DefaultNamespace = %q, want %q", cfg.DefaultNamespace, "default")
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
  merge_interval: 75s
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
	if cfg.MergeInterval != 75*time.Second || cfg.MCPEnabled || !cfg.MetricsPublic || cfg.SessionIdleTTL != 10*time.Hour {
		t.Fatalf("YAML values were not merged: %+v", cfg)
	}
}

func TestLoadTypedEnvironmentValues(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
		"FANOUT_MCP_ENABLED=false",
		"FANOUT_METRICS_PUBLIC=true",
		"FANOUT_MERGE_INTERVAL=75s",
		"FANOUT_SESSION_IDLE_TTL=10h",
	)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPEnabled || !cfg.MetricsPublic || cfg.MergeInterval != 75*time.Second || cfg.SessionIdleTTL != 10*time.Hour {
		t.Fatalf("environment values were not decoded: %+v", cfg)
	}
}

func TestLoadAdvertisedIngestEndpoint(t *testing.T) {
	const endpoint = "https://ingest.example.com"

	t.Run("environment", func(t *testing.T) {
		cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
			"FANOUT_INGEST_ADVERTISED_ENDPOINT="+endpoint,
		)})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.IngestAdvertisedEndpoint != endpoint {
			t.Fatalf("IngestAdvertisedEndpoint = %q, want %q", cfg.IngestAdvertisedEndpoint, endpoint)
		}
	})

	t.Run("YAML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fanout.yaml")
		if err := os.WriteFile(path, []byte("ingest:\n  advertised_endpoint: "+endpoint+"\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.IngestAdvertisedEndpoint != endpoint {
			t.Fatalf("IngestAdvertisedEndpoint = %q, want %q", cfg.IngestAdvertisedEndpoint, endpoint)
		}
	})
}

func TestLoadTreatsEmptyEnvironmentValuesAsAbsent(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
		"FANOUT_HTTP_ADDR=",
		"FANOUT_OTLP_GRPC_ADDR=",
		"FANOUT_OTLP_HTTP_ADDR=",
		"FANOUT_MCP_ENABLED=",
		"FANOUT_RETENTION_DAYS=",
	)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":7520" || cfg.OTLPGRPCAddr != "127.0.0.1:4317" || cfg.OTLPHTTPAddr != "127.0.0.1:4318" || !cfg.MCPEnabled || cfg.RetentionDays != 30 {
		t.Fatalf("empty environment values erased defaults: %+v", cfg)
	}
}

func TestMCPResourceURL(t *testing.T) {
	tests := []struct {
		name      string
		publicURL string
		want      string
	}{
		{name: "local default", want: "https://localhost:7520/mcp"},
		{name: "public origin", publicURL: "https://fanout.example.com", want: "https://fanout.example.com/mcp"},
		{name: "trimmed origin", publicURL: "  https://fanout.example.com/  ", want: "https://fanout.example.com/mcp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{PublicURL: tc.publicURL}
			if got := cfg.MCPResourceURL(); got != tc.want {
				t.Fatalf("MCPResourceURL() = %q, want %q", got, tc.want)
			}
		})
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
	example := koanf.New(".")
	if err := example.Load(file.Provider(path), yaml.Parser()); err != nil {
		t.Fatalf("parse example: %v", err)
	}
	active := make(map[string]bool, len(example.Keys()))
	for _, key := range example.Keys() {
		active[key] = true
	}

	specs, err := configFieldSpecs()
	if err != nil {
		t.Fatalf("field specs: %v", err)
	}
	wantSecrets := map[string]bool{
		"ai.api_key":              true,
		"smtp.password":           true,
		"auth.code_secret":        true,
		"auth.oidc.client_secret": true,
		"metrics.token":           true,
	}
	foundSecrets := make(map[string]bool, len(wantSecrets))
	rawText := string(raw)
	for _, spec := range specs {
		if !strings.Contains(rawText, spec.env) {
			t.Errorf("example does not document %s", spec.env)
		}
		if spec.secret != wantSecrets[spec.key] {
			t.Errorf("secret classification for %s = %t, want %t", spec.key, spec.secret, wantSecrets[spec.key])
		}
		if spec.secret {
			foundSecrets[spec.key] = true
		}
		if spec.secret && active[spec.key] {
			t.Errorf("secret key %s must be documented but inactive in the example", spec.key)
		}
		if spec.secret {
			leaf := spec.key[strings.LastIndex(spec.key, ".")+1:]
			if !strings.Contains(rawText, "# "+leaf+":") {
				t.Errorf("secret key %s is not documented as a commented YAML key", spec.key)
			}
		}
		if !spec.secret && !active[spec.key] {
			t.Errorf("non-secret key %s must be active in the example", spec.key)
		}
	}
	if !reflect.DeepEqual(foundSecrets, wantSecrets) {
		t.Errorf("secret settings = %v, want exactly %v", foundSecrets, wantSecrets)
	}
}

func TestConfigurationSchemaUsesUnitBearingDurations(t *testing.T) {
	specs, err := configFieldSpecs()
	if err != nil {
		t.Fatalf("field specs: %v", err)
	}
	durationType := reflect.TypeOf(time.Duration(0))
	for _, spec := range specs {
		key := strings.ToLower(spec.key)
		env := strings.ToLower(spec.env)
		if strings.Contains(key, "_seconds") || strings.Contains(key, "_ms") ||
			strings.Contains(env, "_seconds") || strings.Contains(env, "_ms") {
			t.Errorf("elapsed-time setting uses a unit suffix: %s / %s", spec.key, spec.env)
		}
		if spec.typ == durationType && !strings.HasSuffix(key, "_interval") && !strings.HasSuffix(key, "_ttl") {
			t.Errorf("duration setting %s must end in _interval or _ttl", spec.key)
		}
	}
}

func TestLoadDurationIntervals(t *testing.T) {
	t.Run("YAML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fanout.yaml")
		document := `ingest:
  flush_interval: 30s
storage:
  rollup_interval: 5m
  merge_interval: 0s
  maintenance_interval: 1h
alerts:
  evaluation_interval: 45s
`
		if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.FlushInterval != 30*time.Second || cfg.RollupInterval != 5*time.Minute ||
			cfg.MergeInterval != 0 || cfg.MaintenanceInterval != time.Hour ||
			cfg.AlertEvaluationInterval != 45*time.Second {
			t.Fatalf("duration values were not decoded: %+v", cfg)
		}
	})

	t.Run("environment", func(t *testing.T) {
		cfg, err := Load(LoadOptions{Environ: append(validEnvironment(),
			"FANOUT_FLUSH_INTERVAL=30s",
			"FANOUT_ROLLUP_INTERVAL=5m",
			"FANOUT_MERGE_INTERVAL=0s",
			"FANOUT_MAINTENANCE_INTERVAL=1h",
			"FANOUT_ALERTS_EVALUATION_INTERVAL=45s",
		)})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.FlushInterval != 30*time.Second || cfg.RollupInterval != 5*time.Minute ||
			cfg.MergeInterval != 0 || cfg.MaintenanceInterval != time.Hour ||
			cfg.AlertEvaluationInterval != 45*time.Second {
			t.Fatalf("duration values were not decoded: %+v", cfg)
		}
	})
}

func TestDockerConfigurationMatchesSchema(t *testing.T) {
	path := filepath.Join("..", "..", "fanout.docker.yaml")
	cfg, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
	if err != nil {
		t.Fatalf("Load Docker config: %v", err)
	}
	if cfg.HTTPAddr != ":7520" || cfg.OTLPGRPCAddr != ":4317" || cfg.OTLPHTTPAddr != ":4318" || cfg.DataDir != "/var/lib/fanout/data" {
		t.Fatalf("Docker config values = HTTP %q OTLP/gRPC %q OTLP/HTTP %q data %q", cfg.HTTPAddr, cfg.OTLPGRPCAddr, cfg.OTLPHTTPAddr, cfg.DataDir)
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

	t.Run("removed ingest YAML key", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fanout.yaml")
		if err := os.WriteFile(path, []byte("ingest:\n  public_endpoint: https://ingest.example.com\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
		if err == nil || !strings.Contains(err.Error(), "ingest.public_endpoint") {
			t.Fatalf("error = %v, want removed YAML key rejected", err)
		}
	})

	t.Run("removed ingest environment variable", func(t *testing.T) {
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_INGEST_ENDPOINT=https://ingest.example.com")})
		if err == nil || !strings.Contains(err.Error(), "FANOUT_INGEST_ENDPOINT") {
			t.Fatalf("error = %v, want removed environment variable rejected", err)
		}
	})

	t.Run("removed YAML keys", func(t *testing.T) {
		for _, test := range []struct {
			key, document string
		}{
			{"agent.provider", "agent:\n  provider: anthropic\n"},
			{"agent.api_key", "agent:\n  api_key: secret\n"},
			{"agent.model", "agent:\n  model: test\n"},
			{"agent.base_url", "agent:\n  base_url: https://ai.example.com\n"},
			{"ingest.flush_seconds", "ingest:\n  flush_seconds: 15\n"},
			{"storage.rollup_every_seconds", "storage:\n  rollup_every_seconds: 60\n"},
			{"storage.merge_every_seconds", "storage:\n  merge_every_seconds: 60\n"},
			{"storage.maintenance_every_seconds", "storage:\n  maintenance_every_seconds: 3600\n"},
			{"alerts.evaluation_interval_seconds", "alerts:\n  evaluation_interval_seconds: 30\n"},
			{"mcp.public_url", "mcp:\n  public_url: https://fanout.example.com/mcp\n"},
		} {
			t.Run(test.key, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "fanout.yaml")
				if err := os.WriteFile(path, []byte(test.document), 0o600); err != nil {
					t.Fatalf("write config: %v", err)
				}
				_, err := Load(LoadOptions{Path: path, Environ: validEnvironment()})
				if err == nil || !strings.Contains(err.Error(), test.key) {
					t.Fatalf("error = %v, want removed key %s rejected", err, test.key)
				}
			})
		}
	})

	t.Run("removed environment variables", func(t *testing.T) {
		for _, name := range []string{
			"FANOUT_FLUSH_SECONDS",
			"FANOUT_ROLLUP_EVERY_SECONDS",
			"FANOUT_MERGE_EVERY_SECONDS",
			"FANOUT_MAINTENANCE_EVERY_SECONDS",
			"FANOUT_ALERTS_EVALUATION_INTERVAL_SECONDS",
			"FANOUT_MCP_PUBLIC_URL",
		} {
			t.Run(name, func(t *testing.T) {
				_, err := Load(LoadOptions{Environ: append(validEnvironment(), name+"=1")})
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("error = %v, want removed environment variable %s rejected", err, name)
				}
			})
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
		{"invalid duration", "ingest:\n  flush_interval: never\n"},
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
		_, err := Load(LoadOptions{Environ: append(validEnvironment(), "FANOUT_FLUSH_INTERVAL=0s")})
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
		{"zero alert interval", "FANOUT_ALERTS_EVALUATION_INTERVAL=0s", "evaluation_interval"},
		{"negative merge interval", "FANOUT_MERGE_INTERVAL=-5s", "merge_interval"},
		{"negative maintenance interval", "FANOUT_MAINTENANCE_INTERVAL=-5s", "maintenance_interval"},
		{"negative DuckDB threads", "FANOUT_DUCKDB_THREADS=-4", "duckdb.threads"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(LoadOptions{Environ: append(validEnvironment(), test.assignment)})
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("error = %v, want validation error for %s", err, test.key)
			}
		})
	}

	for _, test := range []struct {
		name, assignment, key string
	}{
		{"subsecond flush interval", "FANOUT_FLUSH_INTERVAL=999ms", "flush_interval"},
		{"subsecond rollup interval", "FANOUT_ROLLUP_INTERVAL=999ms", "rollup_interval"},
		{"subsecond merge interval", "FANOUT_MERGE_INTERVAL=1ns", "merge_interval"},
		{"subsecond maintenance interval", "FANOUT_MAINTENANCE_INTERVAL=500ms", "maintenance_interval"},
		{"subsecond alert interval", "FANOUT_ALERTS_EVALUATION_INTERVAL=999ms", "evaluation_interval"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(LoadOptions{Environ: append(validEnvironment(), test.assignment)})
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("error = %v, want lower-bound validation for %s", err, test.key)
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
	if err := os.WriteFile(path, []byte("ai:\n  api_key: secret-from-file\n"), 0o644); err != nil {
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
		OTLPHTTPAddr:            "127.0.0.1:4318",
		DataDir:                 "./data",
		FlushInterval:           15 * time.Second,
		FlushBatchSize:          50000,
		RollupInterval:          time.Minute,
		RetentionDays:           30,
		MaintenanceInterval:     time.Hour,
		MergeInterval:           time.Minute,
		DuckDBMaxConns:          4,
		AlertEvaluationInterval: 30 * time.Second,
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
		{"FlushInterval=0", func(c *Config) { c.FlushInterval = 0 }},
		{"FlushInterval=999ms", func(c *Config) { c.FlushInterval = 999 * time.Millisecond }},
		{"FlushBatchSize=0", func(c *Config) { c.FlushBatchSize = 0 }},
		{"FlushBatchSize=-1", func(c *Config) { c.FlushBatchSize = -1 }},
		{"RollupInterval=0", func(c *Config) { c.RollupInterval = 0 }},
		{"RollupInterval=999ms", func(c *Config) { c.RollupInterval = 999 * time.Millisecond }},
		{"RetentionDays=-1", func(c *Config) { c.RetentionDays = -1 }},
		{"HTTPAddr empty", func(c *Config) { c.HTTPAddr = "" }},
		{"OTLPGRPCAddr empty", func(c *Config) { c.OTLPGRPCAddr = "" }},
		{"OTLPHTTPAddr empty", func(c *Config) { c.OTLPHTTPAddr = "" }},
		{"DataDir empty", func(c *Config) { c.DataDir = "" }},
		{"MaintenanceInterval=0", func(c *Config) { c.MaintenanceInterval = 0 }},
		{"MaintenanceInterval=999ms", func(c *Config) { c.MaintenanceInterval = 999 * time.Millisecond }},
		{"MergeInterval=-1s", func(c *Config) { c.MergeInterval = -time.Second }},
		{"MergeInterval=1ns", func(c *Config) { c.MergeInterval = time.Nanosecond }},
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

	t.Run("MergeInterval=0_valid", func(t *testing.T) {
		c := valid
		c.MergeInterval = 0
		if err := c.Validate(); err != nil {
			t.Errorf("MergeInterval=0 should disable the merge pass: %v", err)
		}
	})

	t.Run("local mode allows absent SMTP and agent", func(t *testing.T) {
		c := valid
		c.SMTPHost, c.SMTPUser, c.SMTPPass, c.SMTPFrom = "", "", "", ""
		c.AIAPIKey = ""
		c.AIProvider = "ignored-without-a-key"
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
				t.Errorf("server.public_url %q should be rejected with MCP enabled", invalid)
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
