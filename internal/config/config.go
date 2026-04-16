package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
)

type Config struct {
	HTTPAddr       string    // :7520
	OTLPGRPCAddr   string    // :4317
	LakeDir        string    // ./lake
	FlushSeconds   int       // 15
	FlushBatchSize int       // 50000 rows per writer flush
	APIToken       string    // bearer for API (optional)
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
	SMTPHost   string // SMTP server host
	SMTPPort   int    // SMTP server port (default 587)
	SMTPUser   string // SMTP username
	SMTPPass   string // SMTP password
	SMTPFrom   string // Sender email address
	AdminEmail string // First admin user (created on boot)
	JWTSecret  string // HS256 signing key (auto-generated if empty)
}

func Load() Config {
	cfg := Config{
		HTTPAddr:          getenv("HTTP_ADDR", ":7520"),
		OTLPGRPCAddr:      getenv("OTLP_GRPC_ADDR", ":4317"),
		LakeDir:           getenv("LAKE_DIR", "./lake"),
		FlushSeconds:      getenvInt("FLUSH_SECONDS", 15),
		FlushBatchSize:    getenvInt("FLUSH_BATCH_SIZE", 50000),
		APIToken:          os.Getenv("API_TOKEN"),
		RollupEvery:       getenvInt("ROLLUP_EVERY", 60),
		MCPEnabled:        getenvBool("MCP_ENABLED", true),
		RetentionDays:     getenvInt("RETENTION_DAYS", 30),
		TenantID:          getenvUUID("TENANT_ID"),
		DefaultNS:         getenv("DEFAULT_NAMESPACE", "default"),
		DuckDBMemory:      getenv("DUCKDB_MEMORY", "512MB"),
		AlertEnabled:      getenvBool("ALERT_ENABLED", true),
		AlertEvalInterval: getenvInt("ALERT_EVAL_INTERVAL", 30),
		AlertHistoryDays:  getenvInt("ALERT_HISTORY_DAYS", 7),
		AIProvider:        getenv("AI_PROVIDER", "anthropic"),
		AIAPIKey:          os.Getenv("AI_API_KEY"),
		AIModel:           os.Getenv("AI_MODEL"),
		AIBaseURL:         os.Getenv("AI_BASE_URL"),
		SMTPHost:          os.Getenv("SMTP_HOST"),
		SMTPPort:          getenvInt("SMTP_PORT", 587),
		SMTPUser:          os.Getenv("SMTP_USER"),
		SMTPPass:          os.Getenv("SMTP_PASS"),
		SMTPFrom:          getenv("SMTP_FROM", "Fanout <noreply@fanout.dev>"),
		AdminEmail:        os.Getenv("ADMIN_EMAIL"),
		JWTSecret:         os.Getenv("JWT_SECRET"),
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}
	return cfg
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
	if c.AuthEnabled() {
		if c.SMTPFrom == "" {
			return fmt.Errorf("SMTP_FROM is required when auth is enabled")
		}
		if c.SMTPPort <= 0 {
			return fmt.Errorf("SMTP_PORT must be > 0 when auth is enabled")
		}
	}
	return nil
}

// AuthEnabled returns true if SMTP is configured for passwordless login.
func (c Config) AuthEnabled() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPPass != ""
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
