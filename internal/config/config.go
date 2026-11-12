package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	appauth "github.com/labstack/pitot/internal/auth"
	"github.com/labstack/pitot/internal/secret"
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

// Config is Pitot's canonical configuration schema. Custom environment names
// use PITOT_; provider credentials and endpoints use their vendor's names.
// Elapsed-time settings use time.Duration with unit-bearing values.
type Config struct {
	// Addr binds the browser, API, MCP, OTLP/gRPC and OTLP/HTTP listener.
	Addr string `group:"server" env:"PITOT_ADDR" default:"127.0.0.1:7520"`
	// DataDir stores telemetry, query state, and the control database.
	DataDir string `group:"storage" env:"PITOT_DATA_DIR" default:"./data"`
	// IngestBatchSize sets the group-commit row target; valid values are 1–50000.
	// Individual requests remain atomic and can exceed this target.
	IngestBatchSize int `group:"ingest" env:"PITOT_INGEST_BATCH_SIZE" default:"50000"`
	// IngestMaxInFlightBytes caps decoded telemetry held across concurrent requests
	// until durable publication. Zero is unbounded. A single request is admitted
	// when no other request holds budget, even if it exceeds the limit.
	IngestMaxInFlightBytes int `group:"ingest" env:"PITOT_INGEST_MAX_IN_FLIGHT_BYTES" default:"268435456"`
	// IngestToken sets the first ingest token, named configuration, when none has
	// ever existed, so an automated install accepts telemetry as soon as it starts.
	// Use foi_ plus 43 base64url characters. Only its digest is stored. It is
	// ignored once any token has existed, including a revoked one, so a restart
	// cannot bring back a revoked token.
	IngestToken string `group:"ingest" env:"PITOT_INGEST_TOKEN" secret:"true"`
	// RollupInterval schedules analytical rollups; use a Go duration of at least 1s.
	RollupInterval time.Duration `group:"storage" env:"PITOT_ROLLUP_INTERVAL" default:"1m"`
	// MCPEnabled serves the MCP endpoint at /mcp.
	MCPEnabled bool `group:"mcp" env:"PITOT_MCP_ENABLED" default:"true"`
	// RetentionDays sets telemetry retention in days; zero disables expiry.
	RetentionDays int `group:"storage" env:"PITOT_RETENTION_DAYS" default:"30"`
	// MaintenanceInterval controls Parquet retention and compaction, and
	// query-cache checkpointing.
	MaintenanceInterval time.Duration `group:"storage" env:"PITOT_MAINTENANCE_INTERVAL" default:"1h"`
	// Set once at boot, this advances the service and edge rollup
	// watermarks to the current max ingested timestamp. Existing history is not
	// aggregated into those analytical rollups. Completed-batch trace caches
	// still rebuild all retained history in bounded passes;
	// uncached files remain immediately queryable. Useful for large pre-seeded
	// datasets (benchmarks, restores); off in normal operation.
	RollupSkipToLatest bool `group:"storage" env:"PITOT_ROLLUP_SKIP_TO_LATEST" default:"false"`
	// DefaultNamespace supplies the namespace for telemetry without one.
	DefaultNamespace string `group:"ingest" env:"PITOT_DEFAULT_NAMESPACE" default:"default"`
	// PprofEnabled exposes Go's net/http/pprof handlers at /debug/pprof/* for
	// CPU/heap/mutex/goroutine profiling under load. Off by default; the routes
	// require an admin browser session with operations:read.
	PprofEnabled bool `group:"server" env:"PITOT_PPROF_ENABLED" default:"false"`
	// QueryCacheTTL is the oldest a dashboard panel, exemplar or trace
	// detail result served from memory may be, measured from the end of the
	// time window it covers (for traces and exemplars, from when their run
	// started). A result younger than half the TTL is served as is; one
	// younger than the TTL is served while one background run refreshes it;
	// an older one is rerun. A request with Cache-Control: no-cache, such as
	// a dashboard's manual refresh, always reruns. Zero disables the cache;
	// concurrent identical requests still share one execution.
	QueryCacheTTL time.Duration `group:"server" env:"PITOT_QUERY_CACHE_TTL" default:"10s"`
	// The DuckDB knobs below self-size where possible and otherwise default to
	// values validated on the reference deployment target, a small shared VM
	// (Hetzner CPX32: 4 vCPU, 8 GB RAM, 160 GB disk). There the self-sizing
	// resolves to a ~4.8 GB memory cap and 4 query threads (deterministic from
	// 8 GB / 4 vCPU). Measure the target host with an external benchmark
	// harness before setting
	// production limits; throughput and memory use vary with CPU, disk, and the
	// telemetry mix.
	//
	// Kept free-floating, separated from the field below by a blank line, so it
	// stays context for the group rather than becoming DuckDBMemory's own doc
	// comment: cmd/pitot-docgen publishes each field's comment as that
	// setting's note, and this paragraph describes three of them.

	// DuckDBMemory caps DuckDB's memory (e.g. "8GB"). Empty selects 60% of detected
	// available memory, reserving headroom for Go. If detection fails, DuckDB uses
	// its default with a process headroom adjustment. Startup logs and /readyz
	// report the resolved budget and detection status. Set an explicit cap when
	// memory limit detection is incomplete.
	DuckDBMemory string `group:"storage" env:"PITOT_DUCKDB_MEMORY"`
	// DuckDBThreads caps DuckDB's global query worker pool. Zero selects one
	// worker per CPU the process may run on (the affinity mask and cgroup
	// quota, as GOMAXPROCS resolves them) rather than DuckDB's count of the
	// machine's hardware threads. Set it to leave cores free for ingest on a
	// query-heavy co-tenant host.
	DuckDBThreads int `group:"storage" env:"PITOT_DUCKDB_THREADS" default:"0"`
	// DuckDBMaxConns caps the DuckDB read connection pool. Reads scan immutable
	// Parquet concurrently; analytical and completed-batch writes use separate
	// gates and connections. Zero selects available Go CPUs, bounded to 2–16.
	// Startup logs and /readyz report the resolved pool size.
	DuckDBMaxConns int `group:"storage" env:"PITOT_DUCKDB_MAX_CONNECTIONS" default:"0"`
	// AlertEnabled runs the alert evaluation engine.
	AlertEnabled bool `group:"alerts" env:"PITOT_ALERTS_ENABLED" default:"true"`
	// AlertEvaluationInterval schedules alert evaluation; use a Go duration of at least 1s.
	AlertEvaluationInterval time.Duration `group:"alerts" env:"PITOT_ALERTS_EVALUATION_INTERVAL" default:"30s"`
	// AlertHistoryDays sets alert history retention in days.
	AlertHistoryDays int `group:"alerts" env:"PITOT_ALERTS_HISTORY_DAYS" default:"7"`
	// AlertEmailDefaultTo is a comma-separated list of addresses that receive alerts from rules with no webhook and no email recipients of their own; requires SMTP.
	AlertEmailDefaultTo string `group:"alerts" env:"PITOT_ALERTS_EMAIL_DEFAULT_TO"`
	// AlertEmailHourlyLimit caps the alert emails sent to one address in any rolling hour, across all rules. Notifications over the limit are suppressed, not delayed, and shown on the rule; 0 suppresses all alert email.
	AlertEmailHourlyLimit int `group:"alerts" env:"PITOT_ALERTS_EMAIL_HOURLY_LIMIT" default:"30"`
	// AIProvider enables chat by selecting exactly anthropic or openai. Empty disables chat.
	AIProvider string `group:"ai" env:"PITOT_AI_PROVIDER" default:""`
	// AIModel overrides the provider's default: claude-sonnet-5-5 for Anthropic
	// and gpt-6.1-sol for OpenAI. The OpenAI provider expects a reasoning model
	// supporting reasoning.encrypted_content for stateless Responses replay.
	// A 400 rejecting encrypted content is reported as a server-side configuration
	// error; choose a compatible reasoning model with PITOT_AI_MODEL.
	AIModel string `group:"ai" env:"PITOT_AI_MODEL"`
	// AnthropicAPIKey is required when PITOT_AI_PROVIDER is anthropic.
	AnthropicAPIKey string `group:"ai" env:"ANTHROPIC_API_KEY" secret:"true"`
	// AnthropicBaseURL defaults to https://api.anthropic.com. Pitot appends
	// /v1/messages, preserving any gateway path prefix. Use an HTTP(S) base URL
	// without embedded credentials, a query, or a fragment.
	AnthropicBaseURL string `group:"ai" env:"ANTHROPIC_BASE_URL"`
	// OpenAIAPIKey is required when PITOT_AI_PROVIDER is openai.
	OpenAIAPIKey string `group:"ai" env:"OPENAI_API_KEY" secret:"true"`
	// OpenAIBaseURL defaults to https://api.openai.com/v1. Pitot appends
	// /responses, preserving the API version and any gateway path prefix.
	// Use an HTTP(S) base URL without embedded credentials, a query, or a fragment.
	OpenAIBaseURL string `group:"ai" env:"OPENAI_BASE_URL"`
	// SMTPHost selects the mail server. Host, username, password, and from must all be set to enable SMTP.
	SMTPHost string `group:"smtp" env:"PITOT_SMTP_HOST"`
	// SMTPPort sets the mail server's positive TCP port.
	SMTPPort int `group:"smtp" env:"PITOT_SMTP_PORT" default:"587"`
	// SMTPUser authenticates to the mail server; required with SMTP.
	SMTPUser string `group:"smtp" env:"PITOT_SMTP_USERNAME"`
	// SMTPPass authenticates to the mail server; required with SMTP.
	SMTPPass string `group:"smtp" env:"PITOT_SMTP_PASSWORD" secret:"true"`
	// SMTPFrom sets the email sender address; required with SMTP.
	SMTPFrom string `group:"smtp" env:"PITOT_SMTP_FROM"`
	// AuthMode selects local sign-in or oidc; each mode validates its prerequisites.
	AuthMode string `group:"auth" env:"PITOT_AUTH_MODE" default:"local"`
	// PublicURL sets the external browser origin, MCP token audience, and trusted MCP
	// request origin. Required for OIDC; HTTPS is required with MCP.
	PublicURL string `group:"server" env:"PITOT_PUBLIC_URL"`
	// AuthCodeSecret signs sign-in codes and login links; needs at least 32 characters in local mode and to issue or redeem OIDC break-glass links.
	AuthCodeSecret string `group:"auth" env:"PITOT_AUTH_CODE_SECRET" secret:"true"`
	// SelfSignup allows verified email addresses to create viewer accounts; requires local auth and SMTP.
	SelfSignup bool `group:"auth" env:"PITOT_SELF_SIGNUP" default:"false"`
	// SessionIdleTTL expires inactive sessions; use a Go duration of at least 5m.
	SessionIdleTTL time.Duration `group:"auth" env:"PITOT_SESSION_IDLE_TTL" default:"12h"`
	// SessionAbsoluteTTL limits total session lifetime; it must be at least PITOT_SESSION_IDLE_TTL.
	SessionAbsoluteTTL time.Duration `group:"auth" env:"PITOT_SESSION_ABSOLUTE_TTL" default:"168h"`
	// SCIMToken enables SCIM 2.0 user provisioning under /scim/v2 and turns off browser setup; requires OIDC mode and at least 32 characters.
	SCIMToken string `group:"auth" env:"PITOT_SCIM_TOKEN" secret:"true"`
	// OIDCIssuerURL selects the identity provider issuer; required in OIDC mode. Pitot starts while it is unreachable and retries discovery; a mismatched issuer fails startup.
	OIDCIssuerURL string `group:"auth" env:"PITOT_OIDC_ISSUER_URL"`
	// OIDCClientID identifies Pitot's client registration; required in OIDC mode.
	OIDCClientID string `group:"auth" env:"PITOT_OIDC_CLIENT_ID"`
	// OIDCClientSecret authenticates Pitot's client registration; required in OIDC mode.
	OIDCClientSecret string `group:"auth" env:"PITOT_OIDC_CLIENT_SECRET" secret:"true"`
	// OIDCEmailClaim names the claim containing the user's email address.
	OIDCEmailClaim string `group:"auth" env:"PITOT_OIDC_EMAIL_CLAIM" default:"email"`
	// OIDCEmailVerification selects required verified-email claims or issuer trust; issuer trust requires an allowlist.
	OIDCEmailVerification string `group:"auth" env:"PITOT_OIDC_EMAIL_VERIFICATION" default:"required"`
	// OIDCAutoProvision creates accounts on OIDC sign-in; requires an allowed group or domain.
	OIDCAutoProvision bool `group:"auth" env:"PITOT_OIDC_AUTO_PROVISION" default:"false"`
	// OIDCAllowedGroups lists allowed identity-provider groups, separated by commas.
	OIDCAllowedGroups string `group:"auth" env:"PITOT_OIDC_ALLOWED_GROUPS"`
	// OIDCAllowedDomains lists allowed email domains, separated by commas; wildcard * requires verified email.
	OIDCAllowedDomains string `group:"auth" env:"PITOT_OIDC_ALLOWED_DOMAINS"`
	// OIDCDefaultRole assigns viewer, operator, or admin to users without a group role mapping.
	OIDCDefaultRole string `group:"auth" env:"PITOT_OIDC_DEFAULT_ROLE" default:"viewer"`
	// OIDCOperatorGroups lists identity-provider groups mapped to operator, separated by commas.
	OIDCOperatorGroups string `group:"auth" env:"PITOT_OIDC_OPERATOR_GROUPS"`
	// OIDCAdminGroups lists identity-provider groups mapped to admin, separated by commas.
	OIDCAdminGroups string `group:"auth" env:"PITOT_OIDC_ADMIN_GROUPS"`
	// MetricsToken authorizes Prometheus scrapes with a bearer token; optional when using an admin session.
	MetricsToken string `group:"metrics" env:"PITOT_METRICS_TOKEN" secret:"true"`
	// MetricsPublic allows unauthenticated Prometheus scrapes.
	MetricsPublic bool `group:"metrics" env:"PITOT_METRICS_PUBLIC" default:"false"`
	// TrustedProxyCIDRs lists trusted proxy networks, separated by commas; empty trusts no forwarded client addresses.
	TrustedProxyCIDRs string `group:"server" env:"PITOT_TRUSTED_PROXY_CIDRS"`
	// TLSCertFile selects the TLS certificate file; requires PITOT_TLS_KEY_FILE.
	TLSCertFile string `group:"server" env:"PITOT_TLS_CERT_FILE"`
	// TLSKeyFile selects the TLS private key file; requires PITOT_TLS_CERT_FILE.
	TLSKeyFile string `group:"server" env:"PITOT_TLS_KEY_FILE"`
	// TLSClientCAFile selects a PEM CA bundle; when set, the listener requires client certificates signed by it. Requires PITOT_TLS_CERT_FILE and PITOT_TLS_KEY_FILE.
	TLSClientCAFile string `group:"server" env:"PITOT_TLS_CLIENT_CA_FILE"`
	// HealthcheckCertFile selects the client certificate pitot healthcheck presents; requires PITOT_HEALTHCHECK_KEY_FILE and PITOT_TLS_CLIENT_CA_FILE.
	HealthcheckCertFile string `group:"server" env:"PITOT_HEALTHCHECK_CERT_FILE"`
	// HealthcheckKeyFile selects the private key for PITOT_HEALTHCHECK_CERT_FILE; requires PITOT_HEALTHCHECK_CERT_FILE.
	HealthcheckKeyFile string `group:"server" env:"PITOT_HEALTHCHECK_KEY_FILE"`
	resolvedSizing     sizingSource
}

// LogStartup reports the effective non-secret sizing and security-sensitive
// deployment choices after Load succeeds.
func (c Config) LogStartup() {
	c.logResolvedSizing(c.resolvedSizing)
	if !c.SecureCookies() {
		slog.Warn("browser session cookies are not Secure; configure PITOT_PUBLIC_URL or local TLS before exposing Pitot")
	}
	if strings.TrimSpace(c.PublicURL) != "" && !c.TLSEnabled() && strings.TrimSpace(c.TrustedProxyCIDRs) == "" {
		slog.Warn("reverse-proxy client IPs are not trusted; configure PITOT_TRUSTED_PROXY_CIDRS so audit and rate limits use end-client addresses")
	}
	if c.MetricsPublic {
		slog.Warn("Prometheus metrics are publicly accessible", "path", "/metrics")
	}
	if strings.ToLower(strings.TrimSpace(c.AuthMode)) == "oidc" && c.AuthCodeSecret != "" && c.ValidateLoginLinkSecret() != nil {
		slog.Warn("PITOT_AUTH_CODE_SECRET is shorter than 32 characters; OIDC break-glass login links will be unavailable")
	}
	if c.SelfSignup {
		slog.Info("self-signup is enabled", "role", appauth.RoleViewer)
	}
	// A warning and not a validation error: an installation with this
	// combination must still start after upgrading.
	if strings.ToLower(strings.TrimSpace(c.AuthMode)) == "oidc" && c.OIDCRoleMappingConfigured() && !c.OIDCAutoProvision && c.SCIMToken == "" {
		slog.Warn("OIDC role mapping refuses browser user creation and PITOT_OIDC_AUTO_PROVISION is off, so no new user can get an account; set PITOT_OIDC_AUTO_PROVISION with an allowed group or domain, or PITOT_SCIM_TOKEN")
	}
}

// OIDCRoleMappingConfigured reports whether a group list names at least one
// group. A list of only separators and spaces maps nothing.
func (c Config) OIDCRoleMappingConfigured() bool {
	return nameListed(c.OIDCAdminGroups) || nameListed(c.OIDCOperatorGroups)
}

func nameListed(raw string) bool {
	return len(strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })) != 0
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
	return filepath.Join(c.ControlDir(), "pitot.sqlite")
}

// TLSEnabled reports whether a cert/key pair is configured. When true, HTTP
// serves HTTPS and OTLP gRPC over TLS on PITOT_ADDR.
func (c Config) TLSEnabled() bool {
	return strings.TrimSpace(c.TLSCertFile) != "" &&
		strings.TrimSpace(c.TLSKeyFile) != ""
}

// ClientCertificatesRequired reports whether the TLS listener requires and
// verifies client certificates against PITOT_TLS_CLIENT_CA_FILE.
func (c Config) ClientCertificatesRequired() bool {
	return c.TLSEnabled() && strings.TrimSpace(c.TLSClientCAFile) != ""
}

// Validate checks that config values are sane.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return fmt.Errorf("PITOT_ADDR must not be empty")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("PITOT_DATA_DIR must not be empty")
	}
	if c.IngestBatchSize <= 0 || c.IngestBatchSize > maxIngestBatchSize {
		return fmt.Errorf("PITOT_INGEST_BATCH_SIZE must be between 1 and %d, got %d", maxIngestBatchSize, c.IngestBatchSize)
	}
	if c.IngestToken != "" && !secret.ValidSecret(c.IngestToken, "foi_") {
		return fmt.Errorf("PITOT_INGEST_TOKEN must be foi_ followed by 43 base64url characters")
	}
	if c.RollupInterval < time.Second {
		return fmt.Errorf("PITOT_ROLLUP_INTERVAL must be at least 1s, got %s", c.RollupInterval)
	}
	if c.RetentionDays < 0 {
		return fmt.Errorf("PITOT_RETENTION_DAYS must be >= 0, got %d", c.RetentionDays)
	}
	if c.MaintenanceInterval < time.Second {
		return fmt.Errorf("PITOT_MAINTENANCE_INTERVAL must be at least 1s, got %s", c.MaintenanceInterval)
	}
	if c.QueryCacheTTL < 0 {
		return fmt.Errorf("PITOT_QUERY_CACHE_TTL must be >= 0, got %s", c.QueryCacheTTL)
	}
	if c.DuckDBThreads < 0 {
		return fmt.Errorf("PITOT_DUCKDB_THREADS must be >= 0, got %d", c.DuckDBThreads)
	}
	if c.DuckDBMaxConns <= 0 {
		return fmt.Errorf("PITOT_DUCKDB_MAX_CONNECTIONS must resolve to > 0, got %d", c.DuckDBMaxConns)
	}
	if c.AlertEvaluationInterval < time.Second {
		return fmt.Errorf("PITOT_ALERTS_EVALUATION_INTERVAL must be at least 1s, got %s", c.AlertEvaluationInterval)
	}
	if c.AlertHistoryDays < 0 {
		return fmt.Errorf("PITOT_ALERTS_HISTORY_DAYS must be >= 0, got %d", c.AlertHistoryDays)
	}
	if c.AlertEmailHourlyLimit < 0 {
		return fmt.Errorf("PITOT_ALERTS_EMAIL_HOURLY_LIMIT must be >= 0, got %d", c.AlertEmailHourlyLimit)
	}
	if c.MCPEnabled {
		u, err := url.Parse(c.MCPResourceURL())
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/mcp" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("PITOT_PUBLIC_URL must be an HTTPS origin when MCP is enabled")
		}
	}
	authMode := strings.ToLower(strings.TrimSpace(c.AuthMode))
	if authMode == "" {
		return fmt.Errorf("PITOT_AUTH_MODE must not be empty")
	}
	if authMode != "local" && authMode != "oidc" {
		return fmt.Errorf("PITOT_AUTH_MODE must be local or oidc")
	}
	idleTTL := c.SessionIdleTTL
	absoluteTTL := c.SessionAbsoluteTTL
	if idleTTL < 5*time.Minute {
		return fmt.Errorf("PITOT_SESSION_IDLE_TTL must be at least 5m")
	}
	if absoluteTTL <= 0 || idleTTL > absoluteTTL {
		return fmt.Errorf("PITOT_SESSION_ABSOLUTE_TTL must be positive and at least PITOT_SESSION_IDLE_TTL")
	}
	if authMode == "local" {
		if err := c.ValidateLoginLinkSecret(); err != nil {
			return err
		}
	}
	if anySet(c.SMTPHost, c.SMTPUser, c.SMTPPass, c.SMTPFrom) {
		if !c.SMTPConfigured() {
			return fmt.Errorf("PITOT_SMTP_HOST, PITOT_SMTP_USERNAME, PITOT_SMTP_PASSWORD, and PITOT_SMTP_FROM must all be set when SMTP is configured")
		}
		if c.SMTPPort <= 0 {
			return fmt.Errorf("PITOT_SMTP_PORT must be > 0")
		}
	}
	if anySet(c.AlertEmailDefaultTo) {
		if !c.SMTPConfigured() {
			return fmt.Errorf("PITOT_ALERTS_EMAIL_DEFAULT_TO requires SMTP")
		}
		if _, err := c.AlertEmailDefaults(); err != nil {
			return fmt.Errorf("PITOT_ALERTS_EMAIL_DEFAULT_TO: %w", err)
		}
	}
	if c.SelfSignup {
		if authMode != "local" {
			return fmt.Errorf("PITOT_SELF_SIGNUP requires PITOT_AUTH_MODE=local")
		}
		if !c.SMTPConfigured() {
			return fmt.Errorf("PITOT_SELF_SIGNUP requires SMTP")
		}
	}
	if c.SCIMToken != "" {
		// SCIM links each account to (issuer, subject), which needs an issuer.
		if authMode != "oidc" {
			return fmt.Errorf("PITOT_SCIM_TOKEN requires PITOT_AUTH_MODE=oidc")
		}
		if len(c.SCIMToken) < 32 || strings.ContainsAny(c.SCIMToken, " \t\r\n") {
			return fmt.Errorf("PITOT_SCIM_TOKEN must be at least 32 characters with no whitespace")
		}
	}
	if authMode == "oidc" {
		issuer, err := url.Parse(strings.TrimSpace(c.OIDCIssuerURL))
		if err != nil || issuer.Scheme != "https" || issuer.Host == "" {
			return fmt.Errorf("PITOT_OIDC_ISSUER_URL must be an HTTPS URL")
		}
		if strings.TrimSpace(c.OIDCClientID) == "" || strings.TrimSpace(c.OIDCClientSecret) == "" {
			return fmt.Errorf("PITOT_OIDC_CLIENT_ID and PITOT_OIDC_CLIENT_SECRET are required in oidc mode")
		}
		publicURL, err := url.Parse(strings.TrimSpace(c.PublicURL))
		if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
			return fmt.Errorf("PITOT_PUBLIC_URL must be an HTTPS URL in oidc mode")
		}
		if c.OIDCEmailVerification != "required" && c.OIDCEmailVerification != "issuer" {
			return fmt.Errorf("PITOT_OIDC_EMAIL_VERIFICATION must be required or issuer")
		}
		if c.OIDCEmailVerification != "required" && csvContains(c.OIDCAllowedDomains, "*") {
			return fmt.Errorf("PITOT_OIDC_ALLOWED_DOMAINS wildcard requires PITOT_OIDC_EMAIL_VERIFICATION=required")
		}
		if strings.TrimSpace(c.OIDCEmailClaim) == "" {
			return fmt.Errorf("PITOT_OIDC_EMAIL_CLAIM must not be empty")
		}
		if c.OIDCEmailVerification == "issuer" && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC issuer email verification requires PITOT_OIDC_ALLOWED_GROUPS or PITOT_OIDC_ALLOWED_DOMAINS")
		}
		if !appauth.ValidRole(c.OIDCDefaultRole) {
			return fmt.Errorf("PITOT_OIDC_DEFAULT_ROLE must be viewer, operator, or admin")
		}
		if c.OIDCAutoProvision && strings.TrimSpace(c.OIDCAllowedGroups) == "" && strings.TrimSpace(c.OIDCAllowedDomains) == "" {
			return fmt.Errorf("OIDC auto-provisioning requires PITOT_OIDC_ALLOWED_GROUPS or PITOT_OIDC_ALLOWED_DOMAINS")
		}
	}
	provider, supported := providers[c.AIProvider]
	if c.AIProvider != "" && !supported {
		return fmt.Errorf("PITOT_AI_PROVIDER must be anthropic or openai")
	}
	if c.AgentConfigured() {
		if strings.TrimSpace(c.AIAPIKey()) == "" {
			return fmt.Errorf("%s is required when PITOT_AI_PROVIDER is %s", provider.keyEnv, c.AIProvider)
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
			return fmt.Errorf("PITOT_TRUSTED_PROXY_CIDRS contains invalid CIDR %q", value)
		}
	}
	if anySet(c.TLSCertFile, c.TLSKeyFile) && !c.TLSEnabled() {
		return fmt.Errorf("PITOT_TLS_CERT_FILE and PITOT_TLS_KEY_FILE must both be set")
	}
	if anySet(c.TLSClientCAFile) && !c.TLSEnabled() {
		return fmt.Errorf("PITOT_TLS_CLIENT_CA_FILE requires PITOT_TLS_CERT_FILE and PITOT_TLS_KEY_FILE")
	}
	if anySet(c.HealthcheckCertFile, c.HealthcheckKeyFile) {
		if !anySet(c.HealthcheckCertFile) || !anySet(c.HealthcheckKeyFile) {
			return fmt.Errorf("PITOT_HEALTHCHECK_CERT_FILE and PITOT_HEALTHCHECK_KEY_FILE must both be set")
		}
		if !anySet(c.TLSClientCAFile) {
			return fmt.Errorf("PITOT_HEALTHCHECK_CERT_FILE and PITOT_HEALTHCHECK_KEY_FILE require PITOT_TLS_CLIENT_CA_FILE")
		}
	}
	return nil
}

// ValidateLoginLinkSecret reports whether PITOT_AUTH_CODE_SECRET can protect
// login links. Local mode requires this at startup. OIDC mode checks it only
// when a break-glass link is issued or redeemed, so OIDC installs that never
// set the secret keep starting.
func (c Config) ValidateLoginLinkSecret() error {
	if len(strings.TrimSpace(c.AuthCodeSecret)) < 32 {
		return fmt.Errorf("PITOT_AUTH_CODE_SECRET must be at least 32 characters")
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
// MCP is served by the main HTTP server, so it always shares PITOT_PUBLIC_URL.
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

// AlertEmailDefaults returns the normalized default alert email destination.
func (c Config) AlertEmailDefaults() ([]string, error) {
	if !anySet(c.AlertEmailDefaultTo) {
		return nil, nil
	}
	return appauth.NormalizeRecipients(strings.Split(c.AlertEmailDefaultTo, ","))
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
