package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type Config struct {
	HTTPAddr       string    // :7520
	OTLPGRPCAddr   string    // :4317
	DataDir        string    // ./data
	FlushSeconds   int       // 15
	FlushBatchSize int       // 50000 rows per writer flush
	RollupEvery    int       // seconds
	MCPEnabled     bool      // enable MCP server
	RetentionDays  int       // days to keep data (0 = forever)
	TenantID       uuid.UUID // tenant identifier (UUIDv7)
	DefaultNS      string    // default namespace if not set
	// DuckDB
	DuckDBMemory string // memory limit (e.g. "512MB", "1GB")
	// Alerting
	AlertEnabled      bool
	AlertEvalInterval int // seconds
	AlertHistoryDays  int
	// AI chat
	AIProvider string // anthropic or openai
	AIAPIKey   string // LLM API key
	AIModel    string // model ID override
	AIBaseURL  string // base URL override (OpenAI-compatible)
	// Auth
	SMTPHost         string // SMTP server host
	SMTPPort         int    // SMTP server port (default 587)
	SMTPUser         string // SMTP username
	SMTPPass         string // SMTP password
	SMTPFrom         string // Sender email address
	SetupToken       string // First-boot admin setup token
	JWTSecret        string // HS256 access-token signing key
	JWTRefreshSecret string // HS256 refresh-token signing key
	// OTLP mTLS
	OTLPTLSCertFile     string // TLS server cert for OTLP gRPC
	OTLPTLSKeyFile      string // TLS server key for OTLP gRPC
	OTLPTLSClientCAFile string // Client CA bundle for OTLP mTLS
}

func Load() Config {
	cfg := Config{
		HTTPAddr:            getenv("HTTP_ADDR", ":7520"),
		OTLPGRPCAddr:        getenv("OTLP_GRPC_ADDR", ":4317"),
		DataDir:             getenv("DATA_DIR", "./data"),
		FlushSeconds:        getenvInt("FLUSH_SECONDS", 15),
		FlushBatchSize:      getenvInt("FLUSH_BATCH_SIZE", 50000),
		RollupEvery:         getenvInt("ROLLUP_EVERY", 60),
		MCPEnabled:          getenvBool("MCP_ENABLED", true),
		RetentionDays:       getenvInt("RETENTION_DAYS", 30),
		TenantID:            getenvUUID("TENANT_ID"),
		DefaultNS:           getenv("DEFAULT_NAMESPACE", "default"),
		DuckDBMemory:        getenv("DUCKDB_MEMORY", "512MB"),
		AlertEnabled:        getenvBool("ALERT_ENABLED", true),
		AlertEvalInterval:   getenvInt("ALERT_EVAL_INTERVAL", 30),
		AlertHistoryDays:    getenvInt("ALERT_HISTORY_DAYS", 7),
		AIProvider:          getenv("AI_PROVIDER", "anthropic"),
		AIAPIKey:            os.Getenv("AI_API_KEY"),
		AIModel:             os.Getenv("AI_MODEL"),
		AIBaseURL:           os.Getenv("AI_BASE_URL"),
		SMTPHost:            os.Getenv("SMTP_HOST"),
		SMTPPort:            getenvInt("SMTP_PORT", 587),
		SMTPUser:            os.Getenv("SMTP_USER"),
		SMTPPass:            os.Getenv("SMTP_PASS"),
		SMTPFrom:            os.Getenv("SMTP_FROM"),
		SetupToken:          os.Getenv("SETUP_TOKEN"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		JWTRefreshSecret:    os.Getenv("JWT_REFRESH_SECRET"),
		OTLPTLSCertFile:     os.Getenv("OTLP_TLS_CERT_FILE"),
		OTLPTLSKeyFile:      os.Getenv("OTLP_TLS_KEY_FILE"),
		OTLPTLSClientCAFile: os.Getenv("OTLP_TLS_CLIENT_CA_FILE"),
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}
	return cfg
}

func (c Config) TelemetryDir() string {
	return filepath.Join(c.DataDir, "telemetry")
}

func (c Config) TelemetryParquetDir() string {
	return filepath.Join(c.TelemetryDir(), "parquet")
}

func (c Config) TelemetryDuckLakePath() string {
	return filepath.Join(c.TelemetryDir(), "ducklake.sqlite")
}

func (c Config) QueryDir() string {
	return filepath.Join(c.DataDir, "query")
}

func (c Config) QueryDuckDBPath() string {
	return filepath.Join(c.QueryDir(), "catalog.duckdb")
}

func (c Config) QueryTempDir() string {
	return filepath.Join(c.QueryDir(), "tmp")
}

func (c Config) ControlDir() string {
	return filepath.Join(c.DataDir, "control")
}

func (c Config) ControlSQLitePath() string {
	return filepath.Join(c.ControlDir(), "fanout.sqlite")
}

func (c Config) BookmarksDir() string {
	return filepath.Join(c.ControlDir(), "bookmarks")
}

func (c Config) ReportsDir() string {
	return filepath.Join(c.ControlDir(), "reports")
}

func (c Config) OTLPMTLSEnabled() bool {
	return strings.TrimSpace(c.OTLPTLSCertFile) != "" &&
		strings.TrimSpace(c.OTLPTLSKeyFile) != "" &&
		strings.TrimSpace(c.OTLPTLSClientCAFile) != ""
}

// Validate checks that config values are sane.
func (c Config) Validate() error {
	if c.FlushSeconds <= 0 {
		return fmt.Errorf("FlushSeconds (FLUSH_SECONDS) must be > 0, got %d", c.FlushSeconds)
	}
	if c.FlushBatchSize <= 0 {
		return fmt.Errorf("FlushBatchSize (FLUSH_BATCH_SIZE) must be > 0, got %d", c.FlushBatchSize)
	}
	if c.RollupEvery <= 0 {
		return fmt.Errorf("RollupEvery (ROLLUP_EVERY) must be > 0, got %d", c.RollupEvery)
	}
	if c.RetentionDays < 0 {
		return fmt.Errorf("RetentionDays (RETENTION_DAYS) must be >= 0, got %d", c.RetentionDays)
	}
	if !c.SMTPConfigured() {
		return fmt.Errorf("SMTP_HOST, SMTP_USER, SMTP_PASS, and SMTP_FROM are required")
	}
	if c.SMTPPort <= 0 {
		return fmt.Errorf("SMTP_PORT must be > 0")
	}
	if strings.TrimSpace(c.SetupToken) == "" {
		return fmt.Errorf("SETUP_TOKEN is required")
	}
	if len(c.SetupToken) < 16 {
		return fmt.Errorf("SETUP_TOKEN must be at least 16 characters")
	}
	if strings.TrimSpace(c.AIAPIKey) == "" {
		return fmt.Errorf("AI_API_KEY is required")
	}
	switch strings.TrimSpace(c.AIProvider) {
	case "", "anthropic", "openai":
	default:
		return fmt.Errorf("AI_PROVIDER must be anthropic or openai")
	}
	if strings.TrimSpace(c.JWTSecret) == "" {
		return fmt.Errorf("JWT_SECRET is required")
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if strings.TrimSpace(c.JWTRefreshSecret) == "" {
		return fmt.Errorf("JWT_REFRESH_SECRET is required")
	}
	if len(c.JWTRefreshSecret) < 32 {
		return fmt.Errorf("JWT_REFRESH_SECRET must be at least 32 characters")
	}
	if c.JWTSecret == c.JWTRefreshSecret {
		return fmt.Errorf("JWT_SECRET and JWT_REFRESH_SECRET must be different")
	}
	if anySet(c.OTLPTLSCertFile, c.OTLPTLSKeyFile, c.OTLPTLSClientCAFile) && !c.OTLPMTLSEnabled() {
		return fmt.Errorf("OTLP mTLS requires OTLP_TLS_CERT_FILE, OTLP_TLS_KEY_FILE, and OTLP_TLS_CLIENT_CA_FILE")
	}
	return nil
}

// SMTPConfigured returns true if SMTP is set up for sending email codes.
func (c Config) SMTPConfigured() bool {
	return strings.TrimSpace(c.SMTPHost) != "" &&
		strings.TrimSpace(c.SMTPUser) != "" &&
		strings.TrimSpace(c.SMTPPass) != "" &&
		strings.TrimSpace(c.SMTPFrom) != ""
}

func getenvUUID(k string) uuid.UUID {
	if v := os.Getenv(k); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			slog.Error("invalid UUID", "key", k, "err", err)
			os.Exit(1)
		}
		return id
	}
	return uuid.Nil // stable default
}

func getenvBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	v = strings.ToLower(v)
	return v == "1" || v == "true" || v == "yes"
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var i int
		if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
			return i
		}
		slog.Warn("invalid integer config value, using default", "key", k, "value", v, "default", def)
	}
	return def
}

func anySet(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
