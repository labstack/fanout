package env

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	envparse "github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"

	appauth "github.com/labstack/fanout/internal/auth"
)

type Config struct {
	HTTPAddr     string `env:"HTTP_ADDR" envDefault:":7520"`
	OTLPGRPCAddr string `env:"OTLP_GRPC_ADDR" envDefault:"127.0.0.1:4317"`
	// IngestEndpoint is the OTLP endpoint the UI advertises in its "collector
	// configuration" hint (e.g. "https://ingest.example.com"). It may be
	// public or private — it's just the externally-reachable address, distinct
	// from OTLPGRPCAddr (the bind/listen address). Empty → derive host:port from
	// the browser request + OTLPGRPCAddr (best-effort, dev-friendly).
	IngestEndpoint string `env:"INGEST_ENDPOINT"`
	DataDir        string `env:"DATA_DIR" envDefault:"./data"`
	FlushSeconds   int    `env:"FLUSH_SECONDS" envDefault:"15"`
	FlushBatchSize int    `env:"FLUSH_BATCH_SIZE" envDefault:"50000"`
	RollupEvery    int    `env:"ROLLUP_EVERY" envDefault:"60"`
	MCPEnabled     bool   `env:"MCP_ENABLED" envDefault:"true"`
	// MCPPublicURL is the canonical externally reachable MCP resource URI used
	// for OAuth discovery and token audience binding. It must be stable across
	// restarts and include the /mcp path.
	MCPPublicURL  string `env:"MCP_PUBLIC_URL" envDefault:"https://localhost:7520/mcp"`
	RetentionDays int    `env:"RETENTION_DAYS" envDefault:"30"`
	// MaintenanceEverySeconds throttles the DuckLake maintenance cycle (retention
	// deletes + compaction). Default 3600 (hourly). Lower it to compact more
	// aggressively, or for soak tests that need to observe file-count staying
	// bounded within minutes rather than hours.
	MaintenanceEverySeconds int `env:"DUCKLAKE_MAINTENANCE_EVERY_SECONDS" envDefault:"3600"`
	// MergeEverySeconds is the cadence for the cheap, frequent DuckLake file
	// compaction pass (ducklake_merge_adjacent_files only — it consolidates the
	// newest small parquet files and deletes nothing). Run often (default 60s) it
	// keeps the queryable file count continuously low, which is what bounds
	// rollup/query scan latency — WITHOUT the churn, deletion race, or catalog
	// cost of the full hourly maintenance pass (expire + cleanup). 0 disables it.
	MergeEverySeconds int `env:"DUCKLAKE_MERGE_EVERY_SECONDS" envDefault:"60"`
	// RollupSkipToLatest, set once at boot, advances every rollup watermark to the
	// current max ingested timestamp so existing data is treated as already-rolled-up
	// instead of aggregated as a backlog. Stands up a large pre-seeded historical
	// dataset (benchmarks, restores) without a multi-minute first-rollup catch-up that
	// holds the write gate and starves ingest. Off in normal operation.
	RollupSkipToLatest bool   `env:"ROLLUP_SKIP_TO_LATEST" envDefault:"false"`
	DefaultNS          string `env:"DEFAULT_NAMESPACE" envDefault:"default"`
	// PublicRead exposes only explicitly classified telemetry GET/HEAD routes to
	// a synthetic read-only principal. It never changes ingest authentication.
	PublicRead bool `env:"PUBLIC_READ" envDefault:"false"`
	// PublicIngest is a separate demo-only escape hatch for unauthenticated OTLP.
	PublicIngest bool `env:"PUBLIC_INGEST" envDefault:"false"`
	// PprofEnabled exposes Go's net/http/pprof handlers at /debug/pprof/* for
	// CPU/heap/mutex/goroutine profiling under load. Off by default; the routes
	// require an admin browser session with operations:read.
	PprofEnabled bool `env:"PPROF_ENABLED" envDefault:"false"`
	// The DuckDB knobs below self-size where possible and otherwise default to
	// values validated on the reference deployment target, a small shared VM
	// (Hetzner CPX32: 4 vCPU, 8 GB RAM, 160 GB disk). There the self-sizing
	// resolves to a ~6.4 GB memory cap and 4 query threads (deterministic from
	// 8 GB / 4 vCPU). For a current throughput figure run `just stress hetzner`
	// rather than trusting a number here — as of 2026-06 it handled ~55k rows/s
	// with 0 drops and ~0.4 GB RSS, but that will drift with the ingest path.
	//
	// DuckDBMemory caps DuckDB's memory (e.g. "8GB"). Empty means Fanout sizes
	// it from detected memory, reserving headroom for the Go runtime; see
	// resolveSizing. DuckDB's own default is 80% of detected RAM, which is
	// calculated as though DuckDB owned the machine and has been observed to
	// get the process OOM-killed once the Go heap is added on top.
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
	// the shared write gate (Duck.WriteGate, wired into the writer via
	// UseWriteGate in cmd/fanout/main.go, enforced at startup). Without the WAL
	// mode, pool >1 fails with "database is locked".
	// Zero means "size it from the machine" — the same spelling DuckDBThreads
	// uses for deferring to a default. Resolution happens in resolveSizing and
	// is reported in the startup configuration log.
	DuckDBMaxConns        int           `env:"DUCKDB_MAX_CONNS" envDefault:"0"`
	AlertEnabled          bool          `env:"ALERT_ENABLED" envDefault:"true"`
	AlertEvalInterval     int           `env:"ALERT_EVAL_INTERVAL" envDefault:"30"`
	AlertHistoryDays      int           `env:"ALERT_HISTORY_DAYS" envDefault:"7"`
	AIProvider            string        `env:"AI_PROVIDER" envDefault:"anthropic"`
	AIAPIKey              string        `env:"AI_API_KEY"`
	AIModel               string        `env:"AI_MODEL"`
	AIBaseURL             string        `env:"AI_BASE_URL"`
	SMTPHost              string        `env:"SMTP_HOST"`
	SMTPPort              int           `env:"SMTP_PORT" envDefault:"587"`
	SMTPUser              string        `env:"SMTP_USER"`
	SMTPPass              string        `env:"SMTP_PASS"`
	SMTPFrom              string        `env:"SMTP_FROM"`
	AuthMode              string        `env:"AUTH_MODE" envDefault:"local"`
	PublicURL             string        `env:"PUBLIC_URL"`
	AuthCodeSecret        string        `env:"AUTH_CODE_SECRET"`
	SessionIdleTTL        time.Duration `env:"SESSION_IDLE_TTL" envDefault:"12h"`
	SessionAbsoluteTTL    time.Duration `env:"SESSION_ABSOLUTE_TTL" envDefault:"168h"`
	OIDCIssuerURL         string        `env:"OIDC_ISSUER_URL"`
	OIDCClientID          string        `env:"OIDC_CLIENT_ID"`
	OIDCClientSecret      string        `env:"OIDC_CLIENT_SECRET"`
	OIDCEmailClaim        string        `env:"OIDC_EMAIL_CLAIM" envDefault:"email"`
	OIDCEmailVerification string        `env:"OIDC_EMAIL_VERIFICATION" envDefault:"required"`
	OIDCAutoProvision     bool          `env:"OIDC_AUTO_PROVISION" envDefault:"false"`
	OIDCAllowedGroups     string        `env:"OIDC_ALLOWED_GROUPS"`
	OIDCAllowedDomains    string        `env:"OIDC_ALLOWED_DOMAINS"`
	OIDCDefaultRole       string        `env:"OIDC_DEFAULT_ROLE" envDefault:"viewer"`
	OIDCOperatorGroups    string        `env:"OIDC_OPERATOR_GROUPS"`
	OIDCAdminGroups       string        `env:"OIDC_ADMIN_GROUPS"`
	MetricsToken          string        `env:"METRICS_TOKEN"`
	MetricsPublic         bool          `env:"METRICS_PUBLIC" envDefault:"false"`
	TrustedProxyCIDRs     string        `env:"TRUSTED_PROXY_CIDRS"`
	TLSCertFile           string        `env:"TLS_CERT_FILE"`
	TLSKeyFile            string        `env:"TLS_KEY_FILE"`
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
	// Size before validating: validation should judge the configuration the
	// process will actually run with, not the holes left for it to fill.
	resolved := cfg.resolveSizing()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}
	cfg.logResolvedSizing(resolved)
	if !cfg.SecureCookies() {
		slog.Warn("browser session cookies are not Secure; configure PUBLIC_URL=https://... or local TLS before exposing Fanout")
	}
	if strings.TrimSpace(cfg.PublicURL) != "" && !cfg.TLSEnabled() && strings.TrimSpace(cfg.TrustedProxyCIDRs) == "" {
		slog.Warn("reverse-proxy client IPs are not trusted; set TRUSTED_PROXY_CIDRS to the proxy network so audit and rate limits use end-client addresses")
	}
	if cfg.MetricsPublic {
		slog.Warn("Prometheus metrics are publicly accessible", "path", "/-/metrics")
	}
	if cfg.PublicIngest {
		slog.Warn("OTLP ingest authentication is disabled by PUBLIC_INGEST")
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
	if c.MCPEnabled {
		u, err := url.Parse(strings.TrimSpace(c.MCPPublicURL))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "/mcp" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("MCP_PUBLIC_URL must be an HTTPS URL ending in /mcp")
		}
	}
	authMode := strings.ToLower(strings.TrimSpace(c.AuthMode))
	if authMode == "" {
		authMode = "local"
	}
	if authMode != "local" && authMode != "oidc" {
		return fmt.Errorf("AUTH_MODE must be local or oidc")
	}
	idleTTL := c.SessionIdleTTL
	absoluteTTL := c.SessionAbsoluteTTL
	if idleTTL < 5*time.Minute {
		return fmt.Errorf("SESSION_IDLE_TTL must be at least 5m")
	}
	if absoluteTTL <= 0 || idleTTL > absoluteTTL {
		return fmt.Errorf("SESSION_ABSOLUTE_TTL must be positive and at least SESSION_IDLE_TTL")
	}
	if authMode == "local" {
		if !c.SMTPConfigured() {
			return fmt.Errorf("SMTP_HOST, SMTP_USER, SMTP_PASS, and SMTP_FROM are required in local auth mode")
		}
		if c.SMTPPort <= 0 {
			return fmt.Errorf("SMTP_PORT must be > 0")
		}
		if len(strings.TrimSpace(c.AuthCodeSecret)) < 32 {
			return fmt.Errorf("AUTH_CODE_SECRET must be at least 32 characters")
		}
	}
	if authMode == "oidc" {
		issuer, err := url.Parse(strings.TrimSpace(c.OIDCIssuerURL))
		if err != nil || issuer.Scheme != "https" || issuer.Host == "" {
			return fmt.Errorf("OIDC_ISSUER_URL must be an HTTPS URL")
		}
		if strings.TrimSpace(c.OIDCClientID) == "" || strings.TrimSpace(c.OIDCClientSecret) == "" {
			return fmt.Errorf("OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are required in oidc mode")
		}
		publicURL, err := url.Parse(strings.TrimSpace(c.PublicURL))
		if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
			return fmt.Errorf("PUBLIC_URL must be an HTTPS URL in oidc mode")
		}
		if c.OIDCEmailVerification != "required" && c.OIDCEmailVerification != "issuer" {
			return fmt.Errorf("OIDC_EMAIL_VERIFICATION must be required or issuer")
		}
		if strings.TrimSpace(c.OIDCEmailClaim) == "" {
			return fmt.Errorf("OIDC_EMAIL_CLAIM must not be empty")
		}
		if c.OIDCEmailVerification == "issuer" && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC issuer email verification requires OIDC_ALLOWED_GROUPS or OIDC_ALLOWED_DOMAINS")
		}
		if !appauth.ValidRole(c.OIDCDefaultRole) {
			return fmt.Errorf("OIDC_DEFAULT_ROLE must be viewer, operator, or admin")
		}
		if c.OIDCAutoProvision && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC auto-provisioning requires OIDC_ALLOWED_GROUPS or OIDC_ALLOWED_DOMAINS")
		}
	}
	if strings.TrimSpace(c.AIAPIKey) == "" {
		return fmt.Errorf("AI_API_KEY is required")
	}
	switch strings.ToLower(strings.TrimSpace(c.AIProvider)) {
	case "", "anthropic", "openai":
	default:
		return fmt.Errorf("AI_PROVIDER must be anthropic or openai")
	}
	for _, raw := range strings.Split(c.TrustedProxyCIDRs, ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("TRUSTED_PROXY_CIDRS contains invalid CIDR %q", value)
		}
	}
	if anySet(c.TLSCertFile, c.TLSKeyFile) && !c.TLSEnabled() {
		return fmt.Errorf("TLS requires TLS_CERT_FILE and TLS_KEY_FILE")
	}
	return nil
}

func (c Config) SecureCookies() bool {
	if c.TLSEnabled() {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(c.PublicURL))
	return err == nil && u.Scheme == "https" && u.Host != ""
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
