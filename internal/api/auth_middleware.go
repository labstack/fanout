package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
)

const authUserKey = "auth_user"

func RegisterAuthMiddleware(e *echo.Echo, users *auth.UserStore, jwtSecret string) {
	e.Use(AuthMiddleware(users, jwtSecret))
}

func AuthMiddleware(users *auth.UserStore, jwtSecret string) echo.MiddlewareFunc {
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

func authenticateBearer(users *auth.UserStore, jwtSecret, bearer string) (auth.User, error) {
	if strings.HasPrefix(bearer, "fo_") {
		user, err := users.GetByAPIKey(bearer)
		if err != nil || !user.Active {
			return auth.User{}, echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
		}
		return user, nil
	}

	claims, err := auth.VerifyAccess(jwtSecret, bearer)
	if err != nil {
		return auth.User{}, err
	}

	user, err := users.GetByID(claims.Subject)
	if err != nil || !user.Active {
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
	isPublicAuth := strings.HasPrefix(path, "/api/auth/") &&
		path != "/api/auth/me" &&
		!strings.HasPrefix(path, "/api/auth/api-key")

	return path == "/healthz" || path == "/readyz" || path == "/api/health" || path == "/-/metrics" ||
		path == "/favicon.ico" || path == "/favicon.svg" ||
		isPublicAuth ||
		(!strings.HasPrefix(path, "/api/") && path != "/mcp")
}
