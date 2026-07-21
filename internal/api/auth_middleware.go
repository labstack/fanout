package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
)

const (
	authUserKey    = "auth_user"
	publicViewerID = "public"
)

func RegisterAuthMiddleware(e *echo.Echo, users *auth.UserStore, jwtSecret string, publicRead bool) {
	e.Use(AuthMiddleware(users, jwtSecret, publicRead))
}

// publicViewer is the synthetic identity served to unauthenticated read
// requests when PUBLIC_READ is on. Role "viewer" clears endpoints with no
// RequireRole guard while still failing RequireRole("operator"/"admin"), so
// settings and user-management routes stay locked.
var publicViewer = auth.User{ID: publicViewerID, Email: "public@demo", Role: "viewer", Active: true}

func AuthMiddleware(users *auth.UserStore, jwtSecret string, publicRead bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if isPublicRoute(c.Request().URL.Path) {
				return next(c)
			}
			bearer := bearerToken(c.Request().Header.Get("Authorization"))
			if bearer != "" {
				user, err := authenticateBearer(users, jwtSecret, bearer)
				if err == nil {
					c.Set(authUserKey, &user)
					return next(c)
				}
				// Never downgrade an explicitly authenticated request to the public
				// viewer. A 401 lets browser and MCP clients refresh or restart
				// OAuth; a DB failure surfaces as 500 instead of "invalid token".
				return err
			}

			// Public demo mode: serve unauthenticated reads as a viewer. Gated to
			// GET/HEAD so writes (POST/DELETE) still require real auth regardless
			// of a route's RequireRole, keeping "public" strictly read-only.
			if publicRead {
				switch c.Request().Method {
				case http.MethodGet, http.MethodHead:
					viewer := publicViewer
					c.Set(authUserKey, &viewer)
					return next(c)
				}
			}

			count, err := users.CountUsers()
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "auth check failed")
			}
			if count == 0 {
				return echo.NewHTTPError(http.StatusUnauthorized, "setup required")
			}
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
		}
	}
}

func GetCurrentUser(c *echo.Context) *auth.User {
	value := c.Get(authUserKey)
	if value == nil {
		return nil
	}
	user, _ := value.(*auth.User)
	return user
}

func RequireRole(minRole string) echo.MiddlewareFunc {
	levels := map[string]int{"viewer": 0, "operator": 1, "admin": 2}
	minLevel := levels[minRole]

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			user := GetCurrentUser(c)
			if user == nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
			}
			userLevel, ok := levels[user.Role]
			if !ok || userLevel < minLevel {
				return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
			}
			return next(c)
		}
	}
}

// authenticateBearer resolves a bearer token to a user. Every error it
// returns is an *echo.HTTPError: 401 for genuinely invalid credentials
// (bad signature, unknown or inactive user) and 500 for database failures —
// an infrastructure error must never masquerade as "invalid token".
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

func isPublicRoute(path string) bool {
	isPublicAuth := strings.HasPrefix(path, "/api/auth/") && path != "/api/auth/me"

	// /debug/pprof (mounted only when PPROF_ENABLED) must NOT be public — it
	// exposes heap dumps, cmdline, and a repeatable CPU-profile DoS. Excluding it
	// here means it needs real auth on a private instance; under PUBLIC_READ a GET
	// still resolves via the synthetic viewer, which is fine for a local bench.
	if strings.HasPrefix(path, "/debug/") {
		return false
	}

	return path == "/healthz" || path == "/readyz" || path == "/api/health" || path == "/-/metrics" ||
		path == "/mcp" || strings.HasPrefix(path, "/.well-known/") || strings.HasPrefix(path, "/oauth/") ||
		path == "/favicon.ico" || path == "/favicon.svg" ||
		isPublicAuth ||
		!strings.HasPrefix(path, "/api/")
}
