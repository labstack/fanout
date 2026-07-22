package api

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
)

const (
	authUserKey    = "auth_user"
	authAuditKey   = "auth_audit_store"
	publicViewerID = "public"
)

type Capability string

const (
	ReadTelemetry       Capability = "telemetry:read"
	ManageOwnDashboards Capability = "dashboards:manage-own"
	ManageAlerts        Capability = "alerts:manage"
	RunAgent            Capability = "agent:run"
	ReadIngestMetadata  Capability = "ingest:read-metadata"
	ManageIngest        Capability = "ingest:manage"
	ManageUsers         Capability = "users:manage"
	ManageAuth          Capability = "auth:manage"
	ReadOperations      Capability = "operations:read"
)

var roleCapabilities = map[string]map[Capability]struct{}{
	"viewer": {
		ReadTelemetry: {}, ManageOwnDashboards: {}, ReadIngestMetadata: {},
	},
	"operator": {
		ReadTelemetry: {}, ManageOwnDashboards: {}, ManageAlerts: {}, RunAgent: {}, ReadIngestMetadata: {},
	},
	"admin": {
		ReadTelemetry: {}, ManageOwnDashboards: {}, ManageAlerts: {}, RunAgent: {}, ReadIngestMetadata: {},
		ManageIngest: {}, ManageUsers: {}, ManageAuth: {}, ReadOperations: {},
	},
}

var publicViewer = auth.User{ID: publicViewerID, Email: "public@demo", Role: "viewer", Active: true}

var registeredBrowserSessions sync.Map

type routePolicyKind uint8

const (
	routePolicyPublic routePolicyKind = iota + 1
	routePolicyAuthenticated
	routePolicyCapability
	routePolicyServiceCredential
	routePolicyProtocol
)

type routePolicy struct {
	kind       routePolicyKind
	capability Capability
}

func RegisterAuthMiddleware(e *echo.Echo, users *auth.UserStore, args ...any) {
	var sessions *auth.BrowserSessions
	var audit *auth.AuditStore
	var cfg env.Config
	if len(args) == 3 {
		sessions, _ = args[0].(*auth.BrowserSessions)
		audit, _ = args[1].(*auth.AuditStore)
		cfg, _ = args[2].(env.Config)
	} else if len(args) == 2 { // browser-JWT compatibility for downstream embedders
		cfg.JWTSecret, _ = args[0].(string)
		cfg.PublicRead, _ = args[1].(bool)
		cfg.SessionIdleTTL = 12 * time.Hour
		cfg.SessionAbsoluteTTL = 7 * 24 * time.Hour
		sessions = auth.NewBrowserSessions(users.DB(), cfg.SessionIdleTTL, cfg.SessionAbsoluteTTL, false)
		audit = auth.NewAuditStore(users.DB())
	}
	if sessions == nil {
		panic("api: browser sessions are required")
	}
	registeredBrowserSessions.Store(e, sessions)
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set(authAuditKey, audit)
			return next(c)
		}
	})
	e.Use(echo.WrapMiddleware(sessions.Middleware))
	e.Use(AuthMiddleware(users, sessions, cfg))
}

func AuthMiddleware(users *auth.UserStore, sessions *auth.BrowserSessions, cfg env.Config) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			path := c.Request().URL.Path
			policy, classified := classifyRoute(c.Request().Method, path)
			if !classified {
				slog.Error("request reached an unclassified route", "method", c.Request().Method, "path", path)
				return echo.NewHTTPError(http.StatusInternalServerError, "route security policy is not configured")
			}
			if policy.kind == routePolicyPublic || policy.kind == routePolicyProtocol {
				return next(c)
			}

			// Metrics has its own service credential. Resolve it before the legacy
			// bearer parser so a valid scraper token is never treated as a bad JWT.
			if policy.kind == routePolicyServiceCredential {
				if cfg.MetricsPublic || constantTimeToken(bearerToken(c.Request().Header.Get("Authorization")), cfg.MetricsToken) {
					return next(c)
				}
				user, err := authenticateSession(c, users, sessions)
				if err != nil {
					return err
				}
				c.Set(authUserKey, &user)
				if !HasCapability(user, ReadOperations) {
					recordAuthorizationDenied(c, user)
					return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
				}
				return next(c)
			}

			if sessions.UserID(c.Request().Context()) != "" {
				user, err := authenticateSession(c, users, sessions)
				if err != nil {
					return err
				}
				c.Set(authUserKey, &user)
				if isUnsafeMethod(c.Request().Method) && !validBrowserMutation(c.Request()) {
					return echo.NewHTTPError(http.StatusForbidden, "browser request validation failed")
				}
				return next(c)
			}

			// Temporary migration compatibility for non-browser callers. The SPA no
			// longer creates or stores these tokens.
			bearer := bearerToken(c.Request().Header.Get("Authorization"))
			if bearer != "" && cfg.JWTSecret != "" {
				user, err := authenticateBearer(users, cfg.JWTSecret, bearer)
				if err != nil {
					return err
				}
				c.Set(authUserKey, &user)
				return next(c)
			}

			if cfg.PublicRead && ((policy.capability == ReadTelemetry && isPublicTelemetryRead(c.Request().Method, path)) ||
				(path == "/api/auth/me" && (c.Request().Method == http.MethodGet || c.Request().Method == http.MethodHead))) {
				viewer := publicViewer
				c.Set(authUserKey, &viewer)
				return next(c)
			}

			count, err := users.CountUsers()
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "auth check failed")
			}
			if count == 0 {
				return echo.NewHTTPError(http.StatusUnauthorized, "setup required")
			}
			return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
		}
	}
}

func authenticateSession(c *echo.Context, users *auth.UserStore, sessions *auth.BrowserSessions) (auth.User, error) {
	ctx := c.Request().Context()
	if err := sessions.EnforceActivity(ctx, time.Now().UTC()); err != nil {
		if errors.Is(err, auth.ErrSessionExpired) {
			return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "session expired")
		}
		slog.Error("auth session activity check failed", "err", err)
		return auth.User{}, echo.NewHTTPError(http.StatusInternalServerError, "auth check failed")
	}
	userID := sessions.UserID(ctx)
	if userID == "" {
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	user, err := users.GetByIDContext(ctx, userID)
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		_ = sessions.Destroy(ctx)
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid session")
	case err != nil:
		slog.Error("auth user lookup failed", "user_id", userID, "err", err)
		return auth.User{}, echo.NewHTTPError(http.StatusInternalServerError, "auth check failed")
	case !user.Active || user.AuthVersion != sessions.AuthVersion(ctx):
		_ = sessions.Destroy(ctx)
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid session")
	}
	return user, nil
}

func GetCurrentUser(c *echo.Context) *auth.User {
	value := c.Get(authUserKey)
	user, _ := value.(*auth.User)
	return user
}

func HasCapability(user auth.User, capability Capability) bool {
	if user.ID == publicViewerID {
		return capability == ReadTelemetry
	}
	_, ok := roleCapabilities[user.Role][capability]
	return ok
}

func RequireCapability(capability Capability) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			user := GetCurrentUser(c)
			if user == nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
			}
			if !HasCapability(*user, capability) {
				recordAuthorizationDenied(c, *user)
				return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
			}
			return next(c)
		}
	}
}

func recordAuthorizationDenied(c *echo.Context, user auth.User) {
	if audit, _ := c.Get(authAuditKey).(*auth.AuditStore); audit != nil {
		if err := audit.Record(c.Request().Context(), auth.AuditEvent{
			ActorUserID: user.ID, EventType: "authorization.denied", Outcome: "denied",
			TargetType: "route", TargetID: c.Request().Method + " " + c.Request().URL.Path,
			RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent(),
		}); err != nil {
			slog.Error("authorization audit write failed", "err", err)
		}
	}
}

// RequireRole remains during the API migration, but maps to a capability and
// performs no ordinal role comparison.
func RequireRole(role string) echo.MiddlewareFunc {
	switch role {
	case "admin":
		return RequireCapability(ManageUsers)
	case "operator":
		return RequireCapability(ManageAlerts)
	default:
		return RequireCapability(ReadTelemetry)
	}
}

func authenticateBearer(users *auth.UserStore, jwtSecret, bearer string) (auth.User, error) {
	claims, err := auth.VerifyAccess(jwtSecret, bearer)
	if err != nil {
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
	}
	user, err := users.GetByID(claims.Subject)
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
	case err != nil:
		slog.Error("auth user lookup failed", "user_id", claims.Subject, "err", err)
		return auth.User{}, echo.NewHTTPError(http.StatusInternalServerError, "auth check failed")
	case !user.Active:
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
	}
	return user, nil
}

func bearerToken(header string) string {
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func constantTimeToken(presented, expected string) bool {
	if presented == "" || expected == "" || len(presented) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

func classifyRoute(method, path string) (routePolicy, bool) {
	read := method == http.MethodGet || method == http.MethodHead
	unsafe := method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete

	switch {
	case path == "/healthz" || path == "/readyz" || path == "/api/health":
		return routePolicy{kind: routePolicyPublic}, read
	case path == "/api/auth/status" || path == "/api/auth/oidc/start" || path == "/api/auth/oidc/callback":
		return routePolicy{kind: routePolicyPublic}, read
	case path == "/api/auth/setup" || path == "/api/auth/start" || path == "/api/auth/verify" || path == "/api/auth/refresh":
		return routePolicy{kind: routePolicyPublic}, method == http.MethodPost
	case path == "/api/auth/me":
		return routePolicy{kind: routePolicyAuthenticated}, read
	case path == "/api/auth/logout":
		return routePolicy{kind: routePolicyAuthenticated}, method == http.MethodPost
	case path == "/api/auth/oauth/authorize":
		return routePolicy{kind: routePolicyProtocol}, read || method == http.MethodPost
	case strings.HasPrefix(path, "/.well-known/"):
		return routePolicy{kind: routePolicyProtocol}, read
	case path == "/oauth/register" || path == "/oauth/token":
		return routePolicy{kind: routePolicyProtocol}, method == http.MethodPost
	case path == "/mcp":
		return routePolicy{kind: routePolicyProtocol}, true
	case path == "/-/metrics":
		return routePolicy{kind: routePolicyServiceCredential, capability: ReadOperations}, read
	case strings.HasPrefix(path, "/debug/pprof"):
		return routePolicy{kind: routePolicyCapability, capability: ReadOperations}, read || method == http.MethodPost
	case strings.HasPrefix(path, "/api/observability/"):
		return routePolicy{kind: routePolicyCapability, capability: ReadTelemetry}, read
	case path == "/api/alerts" || path == "/api/alerts/summary" || path == "/api/rules":
		if read {
			return routePolicy{kind: routePolicyCapability, capability: ReadTelemetry}, true
		}
		if path == "/api/rules" && method == http.MethodPost {
			return routePolicy{kind: routePolicyCapability, capability: ManageAlerts}, true
		}
	case strings.HasPrefix(path, "/api/rules/"):
		return routePolicy{kind: routePolicyCapability, capability: ManageAlerts}, unsafe
	case path == "/api/dashboard" || path == "/api/dashboards" || strings.HasPrefix(path, "/api/dashboards/"):
		return routePolicy{kind: routePolicyCapability, capability: ManageOwnDashboards}, read || unsafe
	case path == "/api/agent" || strings.HasPrefix(path, "/api/agent/"):
		return routePolicy{kind: routePolicyCapability, capability: RunAgent}, read || unsafe
	case path == "/api/settings/ingest":
		return routePolicy{kind: routePolicyCapability, capability: ReadIngestMetadata}, read
	case path == "/api/settings/ingest/rotate-token":
		return routePolicy{kind: routePolicyCapability, capability: ManageIngest}, method == http.MethodPost
	case path == "/api/users" || strings.HasPrefix(path, "/api/users/"):
		return routePolicy{kind: routePolicyCapability, capability: ManageUsers}, read || unsafe
	case path == "/" || path == "/favicon.ico" || path == "/favicon.svg" ||
		(!strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/debug/") && path != "/-/metrics"):
		return routePolicy{kind: routePolicyPublic}, read
	}
	return routePolicy{}, false
}

func isPublicTelemetryRead(method, path string) bool {
	return (method == http.MethodGet || method == http.MethodHead) &&
		(strings.HasPrefix(path, "/api/observability/") || path == "/api/auth/me")
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func validBrowserMutation(r *http.Request) bool {
	if r.Header.Get("X-Fanout-Request") == "1" {
		return true
	}
	for _, raw := range []string{r.Header.Get("Origin"), r.Header.Get("Referer")} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err == nil && strings.EqualFold(u.Host, r.Host) {
			return true
		}
	}
	return false
}
