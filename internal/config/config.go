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
	MaxRows        int       // 50000 per file
	APIToken       string    // bearer for API (optional)
	RollupEvery    int       // seconds
	MCPEnabled     bool      // enable MCP server
	RetentionDays  int       // days to keep data (0 = forever)
	RetentionHours int       // how often to check retention (hours)
	TenantID       uuid.UUID // tenant identifier (UUIDv7)
	DefaultNS      string    // default namespace if not set
	// AI chat
	AIProvider string // anthropic or openai
	AIAPIKey   string // LLM API key
	AIModel    string // model ID override
	AIBaseURL  string // base URL override (OpenAI-compatible)
}

func Load() Config {
	cfg := Config{
		HTTPAddr:       getenv("HTTP_ADDR", ":7520"),
		OTLPGRPCAddr:   getenv("OTLP_GRPC_ADDR", ":4317"),
		LakeDir:        getenv("LAKE_DIR", "./lake"),
		FlushSeconds:   getenvInt("FLUSH_SECONDS", 15),
		MaxRows:        getenvInt("MAX_ROWS", 50000),
		APIToken:       os.Getenv("API_TOKEN"),
		RollupEvery:    getenvInt("ROLLUP_EVERY", 60),
		MCPEnabled:     getenvBool("MCP_ENABLED", true),
		RetentionDays:  getenvInt("RETENTION_DAYS", 30),
		RetentionHours: getenvInt("RETENTION_HOURS", 1),
		TenantID:       getenvUUID("TENANT_ID"),
		DefaultNS:      getenv("DEFAULT_NAMESPACE", "default"),
		AIProvider:     getenv("AI_PROVIDER", "anthropic"),
		AIAPIKey:       os.Getenv("AI_API_KEY"),
		AIModel:        os.Getenv("AI_MODEL"),
		AIBaseURL:      os.Getenv("AI_BASE_URL"),
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
	if c.MaxRows <= 0 {
		return fmt.Errorf("MaxRows (MAX_ROWS) must be > 0, got %d", c.MaxRows)
	}
	if c.RollupEvery <= 0 {
		return fmt.Errorf("RollupEvery (ROLLUP_EVERY) must be > 0, got %d", c.RollupEvery)
	}
	if c.RetentionDays < 0 {
		return fmt.Errorf("RetentionDays (RETENTION_DAYS) must be >= 0, got %d", c.RetentionDays)
	}
	if c.RetentionHours <= 0 {
		return fmt.Errorf("RetentionHours (RETENTION_HOURS) must be > 0, got %d", c.RetentionHours)
	}
	return nil
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
