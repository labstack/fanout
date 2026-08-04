package api

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
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
	ReadOperations      Capability = "operations:read"
)

var roleCapabilities = map[auth.Role]map[Capability]struct{}{
	auth.RoleViewer: {
		ReadTelemetry: {}, ManageOwnDashboards: {}, ReadIngestMetadata: {},
	},
	auth.RoleOperator: {
		ReadTelemetry: {}, ManageOwnDashboards: {}, ManageAlerts: {}, RunAgent: {}, ReadIngestMetadata: {},
	},
	auth.RoleAdmin: {
		ReadTelemetry: {}, ManageOwnDashboards: {}, ManageAlerts: {}, RunAgent: {}, ReadIngestMetadata: {},
		ManageIngest: {}, ManageUsers: {}, ReadOperations: {},
	},
}

var publicViewer = auth.User{ID: publicViewerID, Email: "public@demo", Role: auth.RoleViewer, Active: true}

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

func RegisterAuthMiddleware(e *echo.Echo, users *auth.UserStore, sessions *auth.BrowserSessions, audit *auth.AuditStore, cfg env.Config) {
	if users == nil || sessions == nil || audit == nil {
		panic("api: auth middleware dependencies are required")
	}
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
			protectedNotFound := false
			if !classified {
				if routePathKnown(path) {
					return echo.NewHTTPError(http.StatusMethodNotAllowed, "method not allowed")
				}
				routePath := c.RouteInfo().Path
				if isProtectedPath(path) && (routePath == "" || routePath == "/*") {
					policy = routePolicy{kind: routePolicyAuthenticated}
					protectedNotFound = true
				} else {
					slog.Error("request reached an unclassified registered route", "method", c.Request().Method, "path", path, "route", routePath)
					return echo.NewHTTPError(http.StatusInternalServerError, "route security policy is not configured")
				}
			}
			if policy.kind == routePolicyPublic || policy.kind == routePolicyProtocol {
				return next(c)
			}

			// Metrics has its own non-interactive credential, with an admin browser
			// session as an operational fallback.
			if policy.kind == routePolicyServiceCredential {
				if cfg.MetricsPublic || constantTimeToken(bearerToken(c.Request().Header.Get("Authorization")), cfg.MetricsToken) {
					return next(c)
				}
				if sessions.UserID(c.Request().Context()) == "" {
					return echo.NewHTTPError(http.StatusUnauthorized, "metrics authentication required")
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

			if cfg.PublicRead && sessions.UserID(c.Request().Context()) == "" &&
				((policy.capability == ReadTelemetry && isPublicTelemetryRead(c.Request().Method, path)) ||
					(path == "/api/auth/me" && (c.Request().Method == http.MethodGet || c.Request().Method == http.MethodHead))) {
				viewer := publicViewer
				c.Set(authUserKey, &viewer)
				return next(c)
			}

			if sessions.UserID(c.Request().Context()) == "" {
				if path == "/api/auth/oauth/authorize" && c.Request().Method == http.MethodGet {
					returnTo := c.Request().URL.RequestURI()
					return c.Redirect(http.StatusFound, "/?return_to="+url.QueryEscape(returnTo))
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

			user, err := authenticateSession(c, users, sessions)
			if err != nil {
				return err
			}
			c.Set(authUserKey, &user)
			if isUnsafeMethod(c.Request().Method) && !validBrowserMutation(c.Request()) {
				return echo.NewHTTPError(http.StatusForbidden, "browser request validation failed")
			}
			if policy.kind == routePolicyCapability && !HasCapability(user, policy.capability) {
				recordAuthorizationDenied(c, user)
				return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
			}
			if protectedNotFound {
				return echo.NewHTTPError(http.StatusNotFound, "not found")
			}
			return next(c)
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
		if destroyErr := sessions.Destroy(ctx); destroyErr != nil {
			slog.Error("auth invalid-session destroy failed", "err", destroyErr)
		}
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid session")
	case err != nil:
		slog.Error("auth user lookup failed", "user_id", userID, "err", err)
		return auth.User{}, echo.NewHTTPError(http.StatusInternalServerError, "auth check failed")
	case !user.Active || user.AuthVersion != sessions.AuthVersion(ctx):
		if destroyErr := sessions.Destroy(ctx); destroyErr != nil {
			slog.Error("auth revoked-session destroy failed", "user_id", userID, "err", destroyErr)
		}
		return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid session")
	}
	return user, nil
}

func GetCurrentUser(c *echo.Context) *auth.User {
	value := c.Get(authUserKey)
	user, _ := value.(*auth.User)
	return user
}

func RequestOwner(c *echo.Context) (string, error) {
	user := GetCurrentUser(c)
	if user == nil || user.ID == publicViewerID {
		return "", echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	return user.ID, nil
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
	audit, ok := c.Get(authAuditKey).(*auth.AuditStore)
	if !ok || audit == nil {
		slog.Error("authorization audit store is unavailable")
		return
	}
	ctx, cancel := auth.DetachedWriteContext(c.Request().Context())
	defer cancel()
	if err := audit.Record(ctx, auth.AuditEvent{
		ActorUserID: user.ID, EventType: "authorization.denied", Outcome: "denied",
		TargetType: "route", TargetID: c.Request().Method + " " + c.Request().URL.Path,
		RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent(),
	}); err != nil {
		slog.Error("authorization audit write failed", "err", err)
	}
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
	case path == "/api/auth/setup" || path == "/api/auth/start" || path == "/api/auth/verify":
		return routePolicy{kind: routePolicyPublic}, method == http.MethodPost
	case path == "/api/auth/me":
		return routePolicy{kind: routePolicyAuthenticated}, read
	case path == "/api/auth/logout":
		return routePolicy{kind: routePolicyAuthenticated}, method == http.MethodPost
	case path == "/api/auth/oauth/authorize":
		return routePolicy{kind: routePolicyAuthenticated}, read || method == http.MethodPost
	case strings.HasPrefix(path, "/.well-known/"):
		return routePolicy{kind: routePolicyProtocol}, read
	case path == "/oauth/register" || path == "/oauth/token":
		return routePolicy{kind: routePolicyProtocol}, method == http.MethodPost
	case path == "/mcp":
		return routePolicy{kind: routePolicyProtocol}, true
	case path == "/api/mcp":
		return routePolicy{kind: routePolicyCapability, capability: ReadTelemetry}, true
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

func isProtectedPath(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/debug/") || path == "/-/metrics"
}

func routePathKnown(path string) bool {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if _, ok := classifyRoute(method, path); ok {
			return true
		}
	}
	return false
}

func isPublicTelemetryRead(method, path string) bool {
	return (method == http.MethodGet || method == http.MethodHead) &&
		(strings.HasPrefix(path, "/api/observability/") || path == "/api/auth/me")
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func validBrowserMutation(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
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
