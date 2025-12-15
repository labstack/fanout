package config

import (
	"fmt"
	"os"
)

type Config struct {
	HTTPAddr       string // :7520
	OTLPGRPCAddr   string // :4317
	LakeDir        string // ./lake
	FlushSeconds   int    // 15
	MaxRows        int    // 50000 per file
	APIToken       string // bearer for API (optional)
	RollupEvery    int    // seconds
	MCPEnabled     bool   // enable MCP server
	RetentionDays  int    // days to keep data (0 = forever)
	RetentionHours int    // how often to check retention (hours)
}

func Load() Config {
	return Config{
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
	}
}

func getenvBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
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
		_, _ = fmt.Sscanf(v, "%d", &i)
		if i > 0 {
			return i
		}
	}
	return def
}
