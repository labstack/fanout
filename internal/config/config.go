package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	appauth "github.com/labstack/fanout/internal/auth"
)

type Config struct {
	HTTPAddr     string `koanf:"server.http_addr" env:"FANOUT_HTTP_ADDR" default:":7520"`
	OTLPGRPCAddr string `koanf:"ingest.otlp_grpc_addr" env:"FANOUT_OTLP_GRPC_ADDR" default:"127.0.0.1:4317"`
	// IngestEndpoint is the OTLP endpoint the UI advertises in its "collector
	// configuration" hint (e.g. "https://ingest.example.com"). It may be
	// public or private — it's just the externally-reachable address, distinct
	// from OTLPGRPCAddr (the bind/listen address). Empty → derive host:port from
	// the browser request + OTLPGRPCAddr (best-effort, dev-friendly).
	IngestEndpoint string `koanf:"ingest.public_endpoint" env:"FANOUT_INGEST_ENDPOINT"`
	DataDir        string `koanf:"storage.data_dir" env:"FANOUT_DATA_DIR" default:"./data"`
	FlushSeconds   int    `koanf:"ingest.flush_seconds" env:"FANOUT_FLUSH_SECONDS" default:"15"`
	FlushBatchSize int    `koanf:"ingest.flush_batch_size" env:"FANOUT_FLUSH_BATCH_SIZE" default:"50000"`
	RollupEvery    int    `koanf:"storage.rollup_every_seconds" env:"FANOUT_ROLLUP_EVERY_SECONDS" default:"60"`
	MCPEnabled     bool   `koanf:"mcp.enabled" env:"FANOUT_MCP_ENABLED" default:"true"`
	// MCPPublicURL is the canonical externally reachable MCP resource URI used
	// for OAuth discovery and token audience binding. It must be stable across
	// restarts and include the /mcp path.
	MCPPublicURL  string `koanf:"mcp.public_url" env:"FANOUT_MCP_PUBLIC_URL" default:"https://localhost:7520/mcp"`
	RetentionDays int    `koanf:"storage.retention_days" env:"FANOUT_RETENTION_DAYS" default:"30"`
	// MaintenanceEverySeconds throttles the DuckLake maintenance cycle (retention
	// deletes + compaction). Default 3600 (hourly). Lower it to compact more
	// aggressively, or for soak tests that need to observe file-count staying
	// bounded within minutes rather than hours.
	MaintenanceEverySeconds int `koanf:"storage.maintenance_every_seconds" env:"FANOUT_MAINTENANCE_EVERY_SECONDS" default:"3600"`
	// MergeEverySeconds is the cadence for the cheap, frequent DuckLake file
	// compaction pass (ducklake_merge_adjacent_files only — it consolidates the
	// newest small parquet files and deletes nothing). Run often (default 60s) it
	// keeps the queryable file count continuously low, which is what bounds
	// rollup/query scan latency — WITHOUT the churn, deletion race, or catalog
	// cost of the full hourly maintenance pass (expire + cleanup). 0 disables it.
	MergeEverySeconds int `koanf:"storage.merge_every_seconds" env:"FANOUT_MERGE_EVERY_SECONDS" default:"60"`
	// RollupSkipToLatest, set once at boot, advances every rollup watermark to the
	// current max ingested timestamp so existing data is treated as already-rolled-up
	// instead of aggregated as a backlog. Stands up a large pre-seeded historical
	// dataset (benchmarks, restores) without a multi-minute first-rollup catch-up that
	// holds the write gate and starves ingest. Off in normal operation.
	RollupSkipToLatest bool   `koanf:"storage.rollup_skip_to_latest" env:"FANOUT_ROLLUP_SKIP_TO_LATEST" default:"false"`
	DefaultNS          string `koanf:"ingest.default_namespace" env:"FANOUT_DEFAULT_NAMESPACE" default:"default"`
	// PprofEnabled exposes Go's net/http/pprof handlers at /debug/pprof/* for
	// CPU/heap/mutex/goroutine profiling under load. Off by default; the routes
	// require an admin browser session with operations:read.
	PprofEnabled bool `koanf:"server.pprof_enabled" env:"FANOUT_PPROF_ENABLED" default:"false"`
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
	DuckDBMemory string `koanf:"storage.duckdb.memory" env:"FANOUT_DUCKDB_MEMORY"`
	// DuckDBThreads caps DuckDB's global query worker pool. Zero leaves
	// DuckDB's own default in place (one worker per core). Set it to leave
	// cores free for ingest on a query-heavy co-tenant host.
	DuckDBThreads int `koanf:"storage.duckdb.threads" env:"FANOUT_DUCKDB_THREADS"`
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
	DuckDBMaxConns        int           `koanf:"storage.duckdb.max_connections" env:"FANOUT_DUCKDB_MAX_CONNECTIONS" default:"0"`
	AlertEnabled          bool          `koanf:"alerts.enabled" env:"FANOUT_ALERTS_ENABLED" default:"true"`
	AlertEvalInterval     int           `koanf:"alerts.evaluation_interval_seconds" env:"FANOUT_ALERTS_EVALUATION_INTERVAL_SECONDS" default:"30"`
	AlertHistoryDays      int           `koanf:"alerts.history_days" env:"FANOUT_ALERTS_HISTORY_DAYS" default:"7"`
	AIProvider            string        `koanf:"agent.provider" env:"FANOUT_AI_PROVIDER" default:"anthropic"`
	AIAPIKey              string        `koanf:"agent.api_key" env:"FANOUT_AI_API_KEY"`
	AIModel               string        `koanf:"agent.model" env:"FANOUT_AI_MODEL"`
	AIBaseURL             string        `koanf:"agent.base_url" env:"FANOUT_AI_BASE_URL"`
	SMTPHost              string        `koanf:"smtp.host" env:"FANOUT_SMTP_HOST"`
	SMTPPort              int           `koanf:"smtp.port" env:"FANOUT_SMTP_PORT" default:"587"`
	SMTPUser              string        `koanf:"smtp.username" env:"FANOUT_SMTP_USERNAME"`
	SMTPPass              string        `koanf:"smtp.password" env:"FANOUT_SMTP_PASSWORD"`
	SMTPFrom              string        `koanf:"smtp.from" env:"FANOUT_SMTP_FROM"`
	AuthMode              string        `koanf:"auth.mode" env:"FANOUT_AUTH_MODE" default:"local"`
	PublicURL             string        `koanf:"server.public_url" env:"FANOUT_PUBLIC_URL"`
	AuthCodeSecret        string        `koanf:"auth.code_secret" env:"FANOUT_AUTH_CODE_SECRET"`
	SessionIdleTTL        time.Duration `koanf:"auth.session_idle_ttl" env:"FANOUT_SESSION_IDLE_TTL" default:"12h"`
	SessionAbsoluteTTL    time.Duration `koanf:"auth.session_absolute_ttl" env:"FANOUT_SESSION_ABSOLUTE_TTL" default:"168h"`
	OIDCIssuerURL         string        `koanf:"auth.oidc.issuer_url" env:"FANOUT_OIDC_ISSUER_URL"`
	OIDCClientID          string        `koanf:"auth.oidc.client_id" env:"FANOUT_OIDC_CLIENT_ID"`
	OIDCClientSecret      string        `koanf:"auth.oidc.client_secret" env:"FANOUT_OIDC_CLIENT_SECRET"`
	OIDCEmailClaim        string        `koanf:"auth.oidc.email_claim" env:"FANOUT_OIDC_EMAIL_CLAIM" default:"email"`
	OIDCEmailVerification string        `koanf:"auth.oidc.email_verification" env:"FANOUT_OIDC_EMAIL_VERIFICATION" default:"required"`
	OIDCAutoProvision     bool          `koanf:"auth.oidc.auto_provision" env:"FANOUT_OIDC_AUTO_PROVISION" default:"false"`
	OIDCAllowedGroups     string        `koanf:"auth.oidc.allowed_groups" env:"FANOUT_OIDC_ALLOWED_GROUPS"`
	OIDCAllowedDomains    string        `koanf:"auth.oidc.allowed_domains" env:"FANOUT_OIDC_ALLOWED_DOMAINS"`
	OIDCDefaultRole       string        `koanf:"auth.oidc.default_role" env:"FANOUT_OIDC_DEFAULT_ROLE" default:"viewer"`
	OIDCOperatorGroups    string        `koanf:"auth.oidc.operator_groups" env:"FANOUT_OIDC_OPERATOR_GROUPS"`
	OIDCAdminGroups       string        `koanf:"auth.oidc.admin_groups" env:"FANOUT_OIDC_ADMIN_GROUPS"`
	MetricsToken          string        `koanf:"metrics.token" env:"FANOUT_METRICS_TOKEN"`
	MetricsPublic         bool          `koanf:"metrics.public" env:"FANOUT_METRICS_PUBLIC" default:"false"`
	TrustedProxyCIDRs     string        `koanf:"server.trusted_proxy_cidrs" env:"FANOUT_TRUSTED_PROXY_CIDRS"`
	TLSCertFile           string        `koanf:"server.tls.cert_file" env:"FANOUT_TLS_CERT_FILE"`
	TLSKeyFile            string        `koanf:"server.tls.key_file" env:"FANOUT_TLS_KEY_FILE"`
	resolvedSizing        sizingSource
}

// LogStartup reports the effective non-secret sizing and security-sensitive
// deployment choices after Load succeeds.
func (c Config) LogStartup() {
	c.logResolvedSizing(c.resolvedSizing)
	if !c.SecureCookies() {
		slog.Warn("browser session cookies are not Secure; configure server.public_url or local TLS before exposing Fanout")
	}
	if strings.TrimSpace(c.PublicURL) != "" && !c.TLSEnabled() && strings.TrimSpace(c.TrustedProxyCIDRs) == "" {
		slog.Warn("reverse-proxy client IPs are not trusted; configure server.trusted_proxy_cidrs so audit and rate limits use end-client addresses")
	}
	if c.MetricsPublic {
		slog.Warn("Prometheus metrics are publicly accessible", "path", "/-/metrics")
	}
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
// serves HTTPS on server.http_addr and OTLP gRPC accepts TLS.
func (c Config) TLSEnabled() bool {
	return strings.TrimSpace(c.TLSCertFile) != "" &&
		strings.TrimSpace(c.TLSKeyFile) != ""
}

// Validate checks that config values are sane.
func (c Config) Validate() error {
	if c.FlushSeconds <= 0 {
		return fmt.Errorf("ingest.flush_seconds must be > 0, got %d", c.FlushSeconds)
	}
	if c.FlushBatchSize <= 0 {
		return fmt.Errorf("ingest.flush_batch_size must be > 0, got %d", c.FlushBatchSize)
	}
	if c.RollupEvery <= 0 {
		return fmt.Errorf("storage.rollup_every_seconds must be > 0, got %d", c.RollupEvery)
	}
	if c.RetentionDays < 0 {
		return fmt.Errorf("storage.retention_days must be >= 0, got %d", c.RetentionDays)
	}
	if c.MCPEnabled {
		u, err := url.Parse(strings.TrimSpace(c.MCPPublicURL))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "/mcp" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("mcp.public_url must be an HTTPS URL ending in /mcp")
		}
	}
	authMode := strings.ToLower(strings.TrimSpace(c.AuthMode))
	if authMode == "" {
		authMode = "local"
	}
	if authMode != "local" && authMode != "oidc" {
		return fmt.Errorf("auth.mode must be local or oidc")
	}
	idleTTL := c.SessionIdleTTL
	absoluteTTL := c.SessionAbsoluteTTL
	if idleTTL < 5*time.Minute {
		return fmt.Errorf("auth.session_idle_ttl must be at least 5m")
	}
	if absoluteTTL <= 0 || idleTTL > absoluteTTL {
		return fmt.Errorf("auth.session_absolute_ttl must be positive and at least auth.session_idle_ttl")
	}
	if authMode == "local" {
		if !c.SMTPConfigured() {
			return fmt.Errorf("smtp.host, smtp.username, smtp.password, and smtp.from are required in local auth mode")
		}
		if c.SMTPPort <= 0 {
			return fmt.Errorf("smtp.port must be > 0")
		}
		if len(strings.TrimSpace(c.AuthCodeSecret)) < 32 {
			return fmt.Errorf("auth.code_secret must be at least 32 characters")
		}
	}
	if authMode == "oidc" {
		issuer, err := url.Parse(strings.TrimSpace(c.OIDCIssuerURL))
		if err != nil || issuer.Scheme != "https" || issuer.Host == "" {
			return fmt.Errorf("auth.oidc.issuer_url must be an HTTPS URL")
		}
		if strings.TrimSpace(c.OIDCClientID) == "" || strings.TrimSpace(c.OIDCClientSecret) == "" {
			return fmt.Errorf("auth.oidc.client_id and auth.oidc.client_secret are required in oidc mode")
		}
		publicURL, err := url.Parse(strings.TrimSpace(c.PublicURL))
		if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
			return fmt.Errorf("server.public_url must be an HTTPS URL in oidc mode")
		}
		if c.OIDCEmailVerification != "required" && c.OIDCEmailVerification != "issuer" {
			return fmt.Errorf("auth.oidc.email_verification must be required or issuer")
		}
		if strings.TrimSpace(c.OIDCEmailClaim) == "" {
			return fmt.Errorf("auth.oidc.email_claim must not be empty")
		}
		if c.OIDCEmailVerification == "issuer" && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC issuer email verification requires auth.oidc.allowed_groups or auth.oidc.allowed_domains")
		}
		if !appauth.ValidRole(c.OIDCDefaultRole) {
			return fmt.Errorf("auth.oidc.default_role must be viewer, operator, or admin")
		}
		if c.OIDCAutoProvision && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC auto-provisioning requires auth.oidc.allowed_groups or auth.oidc.allowed_domains")
		}
	}
	if strings.TrimSpace(c.AIAPIKey) == "" {
		return fmt.Errorf("agent.api_key is required")
	}
	switch strings.ToLower(strings.TrimSpace(c.AIProvider)) {
	case "", "anthropic", "openai":
	default:
		return fmt.Errorf("agent.provider must be anthropic or openai")
	}
	for _, raw := range strings.Split(c.TrustedProxyCIDRs, ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("server.trusted_proxy_cidrs contains invalid CIDR %q", value)
		}
	}
	if anySet(c.TLSCertFile, c.TLSKeyFile) && !c.TLSEnabled() {
		return fmt.Errorf("server.tls requires cert_file and key_file")
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
