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

const maxIngestBatchSize = 50_000

const (
	DefaultAddr             = "127.0.0.1:7520"
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
	DefaultOpenAIBaseURL    = "https://api.openai.com/v1"
)

type providerSettings struct {
	keyEnv, baseURLEnv, defaultBaseURL string
	key, baseURL                       func(Config) string
}

var providers = map[string]providerSettings{
	"anthropic": {
		keyEnv: "ANTHROPIC_API_KEY", baseURLEnv: "ANTHROPIC_BASE_URL", defaultBaseURL: DefaultAnthropicBaseURL,
		key: func(c Config) string { return c.AnthropicAPIKey }, baseURL: func(c Config) string { return c.AnthropicBaseURL },
	},
	"openai": {
		keyEnv: "OPENAI_API_KEY", baseURLEnv: "OPENAI_BASE_URL", defaultBaseURL: DefaultOpenAIBaseURL,
		key: func(c Config) string { return c.OpenAIAPIKey }, baseURL: func(c Config) string { return c.OpenAIBaseURL },
	},
}

func providerForEnvironment(name string) string {
	for provider, settings := range providers {
		if name == settings.keyEnv || name == settings.baseURLEnv {
			return provider
		}
	}
	return ""
}

// Config is Fanout's canonical configuration schema. Custom environment names
// use FANOUT_; provider credentials and endpoints use their vendor's names.
// Elapsed-time settings use time.Duration with unit-bearing values.
type Config struct {
	// Addr binds the browser, API, MCP, OTLP/gRPC and OTLP/HTTP listener.
	Addr string `group:"server" env:"FANOUT_ADDR" default:"127.0.0.1:7520"`
	// DataDir stores telemetry, query state, and the control database.
	DataDir string `group:"storage" env:"FANOUT_DATA_DIR" default:"./data"`
	// IngestBatchSize sets the group-commit row target; valid values are 1–50000.
	// Individual requests remain atomic and can exceed this target.
	IngestBatchSize int `group:"ingest" env:"FANOUT_INGEST_BATCH_SIZE" default:"50000"`
	// IngestMaxInFlightBytes caps decoded telemetry held across concurrent requests
	// until durable publication. Zero is unbounded. A single request is admitted
	// when no other request holds budget, even if it exceeds the limit.
	IngestMaxInFlightBytes int `group:"ingest" env:"FANOUT_INGEST_MAX_IN_FLIGHT_BYTES" default:"268435456"`
	// RollupInterval schedules analytical rollups; use a Go duration of at least 1s.
	RollupInterval time.Duration `group:"storage" env:"FANOUT_ROLLUP_INTERVAL" default:"1m"`
	// MCPEnabled serves the MCP endpoint at /mcp.
	MCPEnabled bool `group:"mcp" env:"FANOUT_MCP_ENABLED" default:"true"`
	// RetentionDays sets telemetry retention in days; zero disables expiry.
	RetentionDays int `group:"storage" env:"FANOUT_RETENTION_DAYS" default:"30"`
	// MaintenanceInterval controls Parquet retention and compaction, and
	// query-cache checkpointing.
	MaintenanceInterval time.Duration `group:"storage" env:"FANOUT_MAINTENANCE_INTERVAL" default:"1h"`
	// RollupSkipToLatest, set once at boot, advances the service and edge rollup
	// watermarks to the current max ingested timestamp. Existing history is not
	// aggregated into those analytical rollups. Completed-batch trace caches
	// still rebuild all retained history in bounded passes;
	// uncached files remain immediately queryable. Useful for large pre-seeded
	// datasets (benchmarks, restores); off in normal operation.
	RollupSkipToLatest bool `group:"storage" env:"FANOUT_ROLLUP_SKIP_TO_LATEST" default:"false"`
	// DefaultNamespace supplies the namespace for telemetry without one.
	DefaultNamespace string `group:"ingest" env:"FANOUT_DEFAULT_NAMESPACE" default:"default"`
	// PprofEnabled exposes Go's net/http/pprof handlers at /debug/pprof/* for
	// CPU/heap/mutex/goroutine profiling under load. Off by default; the routes
	// require an admin browser session with operations:read.
	PprofEnabled bool `group:"server" env:"FANOUT_PPROF_ENABLED" default:"false"`
	// The DuckDB knobs below self-size where possible and otherwise default to
	// values validated on the reference deployment target, a small shared VM
	// (Hetzner CPX32: 4 vCPU, 8 GB RAM, 160 GB disk). There the self-sizing
	// resolves to a ~4.8 GB memory cap and 4 query threads (deterministic from
	// 8 GB / 4 vCPU). Measure the target host with the external fanout-bench
	// harness before setting
	// production limits; throughput and memory use vary with CPU, disk, and the
	// telemetry mix.
	//
	// Kept free-floating, separated from the field below by a blank line, so it
	// stays context for the group rather than becoming DuckDBMemory's own doc
	// comment: cmd/fanout-docgen publishes each field's comment as that
	// setting's note, and this paragraph describes three of them.

	// DuckDBMemory caps DuckDB's memory (e.g. "8GB"). Empty selects 60% of detected
	// available memory, reserving headroom for Go. If detection fails, DuckDB uses
	// its default with a process headroom adjustment. Startup logs and /readyz
	// report the resolved budget and detection status. Set an explicit cap when
	// memory limit detection is incomplete.
	DuckDBMemory string `group:"storage" env:"FANOUT_DUCKDB_MEMORY"`
	// DuckDBThreads caps DuckDB's global query worker pool. Zero leaves
	// DuckDB's own default in place (one worker per core). Set it to leave
	// cores free for ingest on a query-heavy co-tenant host.
	DuckDBThreads int `group:"storage" env:"FANOUT_DUCKDB_THREADS" default:"0"`
	// DuckDBMaxConns caps the DuckDB read connection pool. Reads scan immutable
	// Parquet concurrently; analytical and completed-batch writes use separate
	// gates and connections. Zero selects available Go CPUs, bounded to 2–16.
	// Startup logs and /readyz report the resolved pool size.
	DuckDBMaxConns int `group:"storage" env:"FANOUT_DUCKDB_MAX_CONNECTIONS" default:"0"`
	// AlertEnabled runs the alert evaluation engine.
	AlertEnabled bool `group:"alerts" env:"FANOUT_ALERTS_ENABLED" default:"true"`
	// AlertEvaluationInterval schedules alert evaluation; use a Go duration of at least 1s.
	AlertEvaluationInterval time.Duration `group:"alerts" env:"FANOUT_ALERTS_EVALUATION_INTERVAL" default:"30s"`
	// AlertHistoryDays sets alert history retention in days.
	AlertHistoryDays int `group:"alerts" env:"FANOUT_ALERTS_HISTORY_DAYS" default:"7"`
	// AIProvider enables chat by selecting exactly anthropic or openai. Empty disables chat.
	AIProvider string `group:"ai" env:"FANOUT_AI_PROVIDER" default:""`
	// AIModel overrides the provider's default: claude-sonnet-5-5 for Anthropic
	// and gpt-6.1-sol for OpenAI. The OpenAI provider expects a reasoning model
	// supporting reasoning.encrypted_content for stateless Responses replay.
	// A 400 rejecting encrypted content is reported as a server-side configuration
	// error; choose a compatible reasoning model with FANOUT_AI_MODEL.
	AIModel string `group:"ai" env:"FANOUT_AI_MODEL"`
	// AnthropicAPIKey is required when FANOUT_AI_PROVIDER is anthropic.
	AnthropicAPIKey string `group:"ai" env:"ANTHROPIC_API_KEY" secret:"true"`
	// AnthropicBaseURL defaults to https://api.anthropic.com. Fanout appends
	// /v1/messages, preserving any gateway path prefix. Use an HTTP(S) base URL
	// without embedded credentials, a query, or a fragment.
	AnthropicBaseURL string `group:"ai" env:"ANTHROPIC_BASE_URL"`
	// OpenAIAPIKey is required when FANOUT_AI_PROVIDER is openai.
	OpenAIAPIKey string `group:"ai" env:"OPENAI_API_KEY" secret:"true"`
	// OpenAIBaseURL defaults to https://api.openai.com/v1. Fanout appends
	// /responses, preserving the API version and any gateway path prefix.
	// Use an HTTP(S) base URL without embedded credentials, a query, or a fragment.
	OpenAIBaseURL string `group:"ai" env:"OPENAI_BASE_URL"`
	// SMTPHost selects the mail server. Host, username, password, and from must all be set to enable SMTP.
	SMTPHost string `group:"smtp" env:"FANOUT_SMTP_HOST"`
	// SMTPPort sets the mail server's positive TCP port.
	SMTPPort int `group:"smtp" env:"FANOUT_SMTP_PORT" default:"587"`
	// SMTPUser authenticates to the mail server; required with SMTP.
	SMTPUser string `group:"smtp" env:"FANOUT_SMTP_USERNAME"`
	// SMTPPass authenticates to the mail server; required with SMTP.
	SMTPPass string `group:"smtp" env:"FANOUT_SMTP_PASSWORD" secret:"true"`
	// SMTPFrom sets the email sender address; required with SMTP.
	SMTPFrom string `group:"smtp" env:"FANOUT_SMTP_FROM"`
	// AuthMode selects local sign-in or oidc; each mode validates its prerequisites.
	AuthMode string `group:"auth" env:"FANOUT_AUTH_MODE" default:"local"`
	// PublicURL sets the external browser origin, MCP token audience, and trusted MCP
	// request origin. Required for OIDC; HTTPS is required with MCP.
	PublicURL string `group:"server" env:"FANOUT_PUBLIC_URL"`
	// AuthCodeSecret signs local sign-in codes; required in local mode with at least 32 characters.
	AuthCodeSecret string `group:"auth" env:"FANOUT_AUTH_CODE_SECRET" secret:"true"`
	// SelfSignup allows verified email addresses to create viewer accounts; requires local auth and SMTP.
	SelfSignup bool `group:"auth" env:"FANOUT_SELF_SIGNUP" default:"false"`
	// SessionIdleTTL expires inactive sessions; use a Go duration of at least 5m.
	SessionIdleTTL time.Duration `group:"auth" env:"FANOUT_SESSION_IDLE_TTL" default:"12h"`
	// SessionAbsoluteTTL limits total session lifetime; it must be at least FANOUT_SESSION_IDLE_TTL.
	SessionAbsoluteTTL time.Duration `group:"auth" env:"FANOUT_SESSION_ABSOLUTE_TTL" default:"168h"`
	// OIDCIssuerURL selects the identity provider issuer; required in OIDC mode.
	OIDCIssuerURL string `group:"auth" env:"FANOUT_OIDC_ISSUER_URL"`
	// OIDCClientID identifies Fanout's client registration; required in OIDC mode.
	OIDCClientID string `group:"auth" env:"FANOUT_OIDC_CLIENT_ID"`
	// OIDCClientSecret authenticates Fanout's client registration; required in OIDC mode.
	OIDCClientSecret string `group:"auth" env:"FANOUT_OIDC_CLIENT_SECRET" secret:"true"`
	// OIDCEmailClaim names the claim containing the user's email address.
	OIDCEmailClaim string `group:"auth" env:"FANOUT_OIDC_EMAIL_CLAIM" default:"email"`
	// OIDCEmailVerification selects required verified-email claims or issuer trust; issuer trust requires an allowlist.
	OIDCEmailVerification string `group:"auth" env:"FANOUT_OIDC_EMAIL_VERIFICATION" default:"required"`
	// OIDCAutoProvision creates accounts on OIDC sign-in; requires an allowed group or domain.
	OIDCAutoProvision bool `group:"auth" env:"FANOUT_OIDC_AUTO_PROVISION" default:"false"`
	// OIDCAllowedGroups lists allowed identity-provider groups, separated by commas.
	OIDCAllowedGroups string `group:"auth" env:"FANOUT_OIDC_ALLOWED_GROUPS"`
	// OIDCAllowedDomains lists allowed email domains, separated by commas; wildcard * requires verified email.
	OIDCAllowedDomains string `group:"auth" env:"FANOUT_OIDC_ALLOWED_DOMAINS"`
	// OIDCDefaultRole assigns viewer, operator, or admin to users without a group role mapping.
	OIDCDefaultRole string `group:"auth" env:"FANOUT_OIDC_DEFAULT_ROLE" default:"viewer"`
	// OIDCOperatorGroups lists identity-provider groups mapped to operator, separated by commas.
	OIDCOperatorGroups string `group:"auth" env:"FANOUT_OIDC_OPERATOR_GROUPS"`
	// OIDCAdminGroups lists identity-provider groups mapped to admin, separated by commas.
	OIDCAdminGroups string `group:"auth" env:"FANOUT_OIDC_ADMIN_GROUPS"`
	// MetricsToken authorizes Prometheus scrapes with a bearer token; optional when using an admin session.
	MetricsToken string `group:"metrics" env:"FANOUT_METRICS_TOKEN" secret:"true"`
	// MetricsPublic allows unauthenticated Prometheus scrapes.
	MetricsPublic bool `group:"metrics" env:"FANOUT_METRICS_PUBLIC" default:"false"`
	// TrustedProxyCIDRs lists trusted proxy networks, separated by commas; empty trusts no forwarded client addresses.
	TrustedProxyCIDRs string `group:"server" env:"FANOUT_TRUSTED_PROXY_CIDRS"`
	// TLSCertFile selects the TLS certificate file; requires FANOUT_TLS_KEY_FILE.
	TLSCertFile string `group:"server" env:"FANOUT_TLS_CERT_FILE"`
	// TLSKeyFile selects the TLS private key file; requires FANOUT_TLS_CERT_FILE.
	TLSKeyFile     string `group:"server" env:"FANOUT_TLS_KEY_FILE"`
	resolvedSizing sizingSource
}

// LogStartup reports the effective non-secret sizing and security-sensitive
// deployment choices after Load succeeds.
func (c Config) LogStartup() {
	c.logResolvedSizing(c.resolvedSizing)
	if !c.SecureCookies() {
		slog.Warn("browser session cookies are not Secure; configure FANOUT_PUBLIC_URL or local TLS before exposing Fanout")
	}
	if strings.TrimSpace(c.PublicURL) != "" && !c.TLSEnabled() && strings.TrimSpace(c.TrustedProxyCIDRs) == "" {
		slog.Warn("reverse-proxy client IPs are not trusted; configure FANOUT_TRUSTED_PROXY_CIDRS so audit and rate limits use end-client addresses")
	}
	if c.MetricsPublic {
		slog.Warn("Prometheus metrics are publicly accessible", "path", "/metrics")
	}
	if c.SelfSignup {
		slog.Info("self-signup is enabled", "role", appauth.RoleViewer)
	}
}

func (c Config) TelemetryDir() string {
	return filepath.Join(c.DataDir, "telemetry")
}

func (c Config) TelemetryParquetDir() string {
	return filepath.Join(c.TelemetryDir(), "parquet")
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
// serves HTTPS and OTLP gRPC over TLS on FANOUT_ADDR.
func (c Config) TLSEnabled() bool {
	return strings.TrimSpace(c.TLSCertFile) != "" &&
		strings.TrimSpace(c.TLSKeyFile) != ""
}

// Validate checks that config values are sane.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return fmt.Errorf("FANOUT_ADDR must not be empty")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("FANOUT_DATA_DIR must not be empty")
	}
	if c.IngestBatchSize <= 0 || c.IngestBatchSize > maxIngestBatchSize {
		return fmt.Errorf("FANOUT_INGEST_BATCH_SIZE must be between 1 and %d, got %d", maxIngestBatchSize, c.IngestBatchSize)
	}
	if c.RollupInterval < time.Second {
		return fmt.Errorf("FANOUT_ROLLUP_INTERVAL must be at least 1s, got %s", c.RollupInterval)
	}
	if c.RetentionDays < 0 {
		return fmt.Errorf("FANOUT_RETENTION_DAYS must be >= 0, got %d", c.RetentionDays)
	}
	if c.MaintenanceInterval < time.Second {
		return fmt.Errorf("FANOUT_MAINTENANCE_INTERVAL must be at least 1s, got %s", c.MaintenanceInterval)
	}
	if c.DuckDBThreads < 0 {
		return fmt.Errorf("FANOUT_DUCKDB_THREADS must be >= 0, got %d", c.DuckDBThreads)
	}
	if c.DuckDBMaxConns <= 0 {
		return fmt.Errorf("FANOUT_DUCKDB_MAX_CONNECTIONS must resolve to > 0, got %d", c.DuckDBMaxConns)
	}
	if c.AlertEvaluationInterval < time.Second {
		return fmt.Errorf("FANOUT_ALERTS_EVALUATION_INTERVAL must be at least 1s, got %s", c.AlertEvaluationInterval)
	}
	if c.AlertHistoryDays < 0 {
		return fmt.Errorf("FANOUT_ALERTS_HISTORY_DAYS must be >= 0, got %d", c.AlertHistoryDays)
	}
	if c.MCPEnabled {
		u, err := url.Parse(c.MCPResourceURL())
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/mcp" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("FANOUT_PUBLIC_URL must be an HTTPS origin when MCP is enabled")
		}
	}
	authMode := strings.ToLower(strings.TrimSpace(c.AuthMode))
	if authMode == "" {
		return fmt.Errorf("FANOUT_AUTH_MODE must not be empty")
	}
	if authMode != "local" && authMode != "oidc" {
		return fmt.Errorf("FANOUT_AUTH_MODE must be local or oidc")
	}
	idleTTL := c.SessionIdleTTL
	absoluteTTL := c.SessionAbsoluteTTL
	if idleTTL < 5*time.Minute {
		return fmt.Errorf("FANOUT_SESSION_IDLE_TTL must be at least 5m")
	}
	if absoluteTTL <= 0 || idleTTL > absoluteTTL {
		return fmt.Errorf("FANOUT_SESSION_ABSOLUTE_TTL must be positive and at least FANOUT_SESSION_IDLE_TTL")
	}
	if authMode == "local" {
		if len(strings.TrimSpace(c.AuthCodeSecret)) < 32 {
			return fmt.Errorf("FANOUT_AUTH_CODE_SECRET must be at least 32 characters")
		}
	}
	if anySet(c.SMTPHost, c.SMTPUser, c.SMTPPass, c.SMTPFrom) {
		if !c.SMTPConfigured() {
			return fmt.Errorf("FANOUT_SMTP_HOST, FANOUT_SMTP_USERNAME, FANOUT_SMTP_PASSWORD, and FANOUT_SMTP_FROM must all be set when SMTP is configured")
		}
		if c.SMTPPort <= 0 {
			return fmt.Errorf("FANOUT_SMTP_PORT must be > 0")
		}
	}
	if c.SelfSignup {
		if authMode != "local" {
			return fmt.Errorf("FANOUT_SELF_SIGNUP requires FANOUT_AUTH_MODE=local")
		}
		if !c.SMTPConfigured() {
			return fmt.Errorf("FANOUT_SELF_SIGNUP requires SMTP")
		}
	}
	if authMode == "oidc" {
		issuer, err := url.Parse(strings.TrimSpace(c.OIDCIssuerURL))
		if err != nil || issuer.Scheme != "https" || issuer.Host == "" {
			return fmt.Errorf("FANOUT_OIDC_ISSUER_URL must be an HTTPS URL")
		}
		if strings.TrimSpace(c.OIDCClientID) == "" || strings.TrimSpace(c.OIDCClientSecret) == "" {
			return fmt.Errorf("FANOUT_OIDC_CLIENT_ID and FANOUT_OIDC_CLIENT_SECRET are required in oidc mode")
		}
		publicURL, err := url.Parse(strings.TrimSpace(c.PublicURL))
		if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
			return fmt.Errorf("FANOUT_PUBLIC_URL must be an HTTPS URL in oidc mode")
		}
		if c.OIDCEmailVerification != "required" && c.OIDCEmailVerification != "issuer" {
			return fmt.Errorf("FANOUT_OIDC_EMAIL_VERIFICATION must be required or issuer")
		}
		if c.OIDCEmailVerification != "required" && csvContains(c.OIDCAllowedDomains, "*") {
			return fmt.Errorf("FANOUT_OIDC_ALLOWED_DOMAINS wildcard requires FANOUT_OIDC_EMAIL_VERIFICATION=required")
		}
		if strings.TrimSpace(c.OIDCEmailClaim) == "" {
			return fmt.Errorf("FANOUT_OIDC_EMAIL_CLAIM must not be empty")
		}
		if c.OIDCEmailVerification == "issuer" && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC issuer email verification requires FANOUT_OIDC_ALLOWED_GROUPS or FANOUT_OIDC_ALLOWED_DOMAINS")
		}
		if !appauth.ValidRole(c.OIDCDefaultRole) {
			return fmt.Errorf("FANOUT_OIDC_DEFAULT_ROLE must be viewer, operator, or admin")
		}
		if c.OIDCAutoProvision && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC auto-provisioning requires FANOUT_OIDC_ALLOWED_GROUPS or FANOUT_OIDC_ALLOWED_DOMAINS")
		}
	}
	provider, supported := providers[c.AIProvider]
	if c.AIProvider != "" && !supported {
		return fmt.Errorf("FANOUT_AI_PROVIDER must be anthropic or openai")
	}
	if c.AgentConfigured() {
		if strings.TrimSpace(c.AIAPIKey()) == "" {
			return fmt.Errorf("%s is required when FANOUT_AI_PROVIDER is %s", provider.keyEnv, c.AIProvider)
		}
		baseURL := c.AIBaseURL()
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(baseURL, "#") {
			return fmt.Errorf("%s must be an HTTP(S) base URL without credentials, query, or fragment", provider.baseURLEnv)
		}
	}
	for _, raw := range strings.Split(c.TrustedProxyCIDRs, ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("FANOUT_TRUSTED_PROXY_CIDRS contains invalid CIDR %q", value)
		}
	}
	if anySet(c.TLSCertFile, c.TLSKeyFile) && !c.TLSEnabled() {
		return fmt.Errorf("FANOUT_TLS_CERT_FILE and FANOUT_TLS_KEY_FILE must both be set")
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

// MCPResourceURL returns the canonical OAuth resource URI for the MCP route.
// MCP is served by the main HTTP server, so it always shares FANOUT_PUBLIC_URL.
// The localhost value preserves zero-configuration development when no public
// origin is configured.
func (c Config) MCPResourceURL() string {
	publicURL := strings.TrimRight(strings.TrimSpace(c.PublicURL), "/")
	if publicURL == "" {
		return "https://localhost:7520/mcp"
	}
	return publicURL + "/mcp"
}

// SMTPConfigured returns true if SMTP is set up for sending email codes.
func (c Config) SMTPConfigured() bool {
	return strings.TrimSpace(c.SMTPHost) != "" &&
		strings.TrimSpace(c.SMTPUser) != "" &&
		strings.TrimSpace(c.SMTPPass) != "" &&
		strings.TrimSpace(c.SMTPFrom) != ""
}

// AgentConfigured reports whether the in-process AI agent should be started.
func (c Config) AgentConfigured() bool {
	return c.AIProvider != ""
}

// AIAPIKey returns only the explicitly selected provider's credential.
func (c Config) AIAPIKey() string {
	if provider, ok := providers[c.AIProvider]; ok {
		return provider.key(c)
	}
	return ""
}

// AIBaseURL returns the selected provider's API base, including its default.
func (c Config) AIBaseURL() string {
	provider, ok := providers[c.AIProvider]
	if !ok {
		return ""
	}
	baseURL := provider.baseURL(c)
	if strings.TrimSpace(baseURL) == "" {
		return provider.defaultBaseURL
	}
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func csvContains(value, target string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func anySet(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
