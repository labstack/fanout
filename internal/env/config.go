package env

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	envparse "github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr     string `env:"HTTP_ADDR" envDefault:":7520"`
	OTLPGRPCAddr string `env:"OTLP_GRPC_ADDR" envDefault:"127.0.0.1:4317"`
	// IngestEndpoint is the OTLP endpoint the UI advertises in its "collector
	// configuration" hint (e.g. "https://ingest.fanout.labstack.com"). It may be
	// public or private — it's just the externally-reachable address, distinct
	// from OTLPGRPCAddr (the bind/listen address). Empty → derive host:port from
	// the browser request + OTLPGRPCAddr (best-effort, dev-friendly).
	IngestEndpoint string `env:"INGEST_ENDPOINT"`
	DataDir        string `env:"DATA_DIR" envDefault:"./data"`
	FlushSeconds   int    `env:"FLUSH_SECONDS" envDefault:"15"`
	FlushBatchSize int    `env:"FLUSH_BATCH_SIZE" envDefault:"50000"`
	RollupEvery    int    `env:"ROLLUP_EVERY" envDefault:"60"`
	MCPEnabled     bool   `env:"MCP_ENABLED" envDefault:"true"`
	RetentionDays  int    `env:"RETENTION_DAYS" envDefault:"30"`
	// MaintenanceEverySeconds throttles the DuckLake maintenance cycle (retention
	// deletes + compaction). Default 3600 (hourly). Lower it to compact more
	// aggressively, or for soak tests that need to observe file-count staying
	// bounded within minutes (see scripts/soak.sh).
	MaintenanceEverySeconds int    `env:"DUCKLAKE_MAINTENANCE_EVERY_SECONDS" envDefault:"3600"`
	DefaultNS               string `env:"DEFAULT_NAMESPACE" envDefault:"default"`
	// PublicRead turns the instance into a public demo: unauthenticated GET/HEAD
	// requests are served as a read-only viewer (writes, admin routes, /mcp, and
	// the API-key routes still require real auth), and OTLP ingest is accepted
	// without a token. It exposes ALL telemetry on the instance to anyone who can
	// reach it — only enable it on an instance whose data is meant to be public
	// (e.g. the otel-demo showcase). NEVER set it where data is private.
	PublicRead bool `env:"PUBLIC_READ" envDefault:"false"`
	// PprofEnabled exposes Go's net/http/pprof handlers at /debug/pprof/* for
	// CPU/heap/mutex/goroutine profiling under load. Off by default; the routes
	// are unauthenticated (non-/api/), so only enable on localhost or a trusted
	// network (e.g. during a benchmark — see scripts/bench.sh / just bench).
	PprofEnabled bool `env:"PPROF_ENABLED" envDefault:"false"`
	// The DuckDB knobs below self-size where possible and otherwise default to
	// values validated on the reference deployment target, a small shared VM
	// (Hetzner CPX32: 4 vCPU, 8 GB RAM, 160 GB disk). There the self-sizing
	// resolves to a ~6.4 GB memory cap and 4 query threads (deterministic from
	// 8 GB / 4 vCPU). For a current throughput figure run `just stress hetzner`
	// rather than trusting a number here — as of 2026-06 it handled ~55k rows/s
	// with 0 drops and ~0.4 GB RSS, but that will drift with the ingest path.
	//
	// DuckDBMemory caps DuckDB's memory (e.g. "8GB"). Empty leaves DuckDB's
	// own default in place — 80% of detected RAM, cgroup-aware in containers —
	// so the cap scales with the deployment. Set it only to constrain an
	// instance that shares its host with other memory-hungry services.
	DuckDBMemory string `env:"DUCKDB_MEMORY"`
	// DuckDBThreads caps DuckDB's global query worker pool. Zero leaves
	// DuckDB's own default in place (one worker per core). Set it to leave
	// cores free for ingest on a query-heavy co-tenant host.
	DuckDBThreads int `env:"DUCKDB_THREADS"`
	// DuckDBMaxConns caps the DuckDB connection pool. A value of 1 serializes
	// everything through one handle; the default of 4 lets read queries run
	// concurrently with each other and with ingest flushes. Two things make >1
	// safe: the DuckLake SQLite catalog is opened in WAL mode (enableCatalogWAL),
	// so readers don't collide with the single writer and a crashed writer can't
	// leave the catalog permanently locked; and write commits are serialized by
	// the shared write mutex (Duck.WriteLock, wired into the writer via
	// UseWriteLock in cmd/fanout/main.go, enforced at startup). Without the WAL
	// mode, pool >1 fails with "database is locked".
	DuckDBMaxConns    int    `env:"DUCKDB_MAX_CONNS" envDefault:"4"`
	AlertEnabled      bool   `env:"ALERT_ENABLED" envDefault:"true"`
	AlertEvalInterval int    `env:"ALERT_EVAL_INTERVAL" envDefault:"30"`
	AlertHistoryDays  int    `env:"ALERT_HISTORY_DAYS" envDefault:"7"`
	AIProvider        string `env:"AI_PROVIDER" envDefault:"anthropic"`
	AIAPIKey          string `env:"AI_API_KEY"`
	AIModel           string `env:"AI_MODEL"`
	AIBaseURL         string `env:"AI_BASE_URL"`
	SMTPHost          string `env:"SMTP_HOST"`
	SMTPPort          int    `env:"SMTP_PORT" envDefault:"587"`
	SMTPUser          string `env:"SMTP_USER"`
	SMTPPass          string `env:"SMTP_PASS"`
	SMTPFrom          string `env:"SMTP_FROM"`
	JWTSecret         string `env:"JWT_SECRET"`
	JWTRefreshSecret  string `env:"JWT_REFRESH_SECRET"`
	TLSCertFile       string `env:"TLS_CERT_FILE"`
	TLSKeyFile        string `env:"TLS_KEY_FILE"`
}

// Load reads .env non-destructively (does not overwrite pre-set OS env), then
// .env.{ENV} destructively (overwrites everything — it is the per-env override).
// ENV defaults to "development". Missing files are not an error.
//
// Effective precedence (highest first):
//  1. .env.{ENV}
//  2. real OS env (e.g. exported in shell or injected by a container runtime)
//  3. .env
//  4. envDefault tags on the Config struct
func Load() Config {
	envName := os.Getenv("ENV")
	if envName == "" {
		envName = "development"
	}

	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("getwd failed, using '.'", "err", err)
		cwd = "."
	}
	if err := loadIfPresent(filepath.Join(cwd, ".env")); err != nil {
		slog.Error("load .env failed", "err", err)
		os.Exit(1)
	}
	if err := overloadIfPresent(filepath.Join(cwd, ".env."+envName)); err != nil {
		slog.Error("load .env."+envName+" failed", "err", err)
		os.Exit(1)
	}

	var cfg Config
	if err := envparse.Parse(&cfg); err != nil {
		slog.Error("parse env failed", "err", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}
	return cfg
}

func loadIfPresent(path string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return godotenv.Load(path)
}

func overloadIfPresent(path string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return godotenv.Overload(path)
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

// TLSEnabled reports whether a cert/key pair is configured. When true, HTTP
// serves HTTPS on HTTP_ADDR and OTLP gRPC accepts TLS (required for public ingest).
func (c Config) TLSEnabled() bool {
	return strings.TrimSpace(c.TLSCertFile) != "" &&
		strings.TrimSpace(c.TLSKeyFile) != ""
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
	if anySet(c.TLSCertFile, c.TLSKeyFile) && !c.TLSEnabled() {
		return fmt.Errorf("TLS requires TLS_CERT_FILE and TLS_KEY_FILE")
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

func anySet(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
