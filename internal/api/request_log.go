package api

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

var tokenRedactRe = regexp.MustCompile(`token=[^&]+`)

// RequestLogger logs each request without credentials or directory lookups.
func RequestLogger() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:       true,
		LogStatus:    true,
		LogLatency:   true,
		LogMethod:    true,
		LogRemoteIP:  true,
		LogUserAgent: true,
		HandleError:  true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			uri := loggedRequestURI(c.Request().URL.Path, v.URI)
			attrs := []any{"method", v.Method, "uri", uri, "status", v.Status, "latency", v.Latency}
			if v.Error != nil {
				attrs = []any{"method", v.Method, "route", c.Path(), "status", v.Status, "latency", v.Latency, "err", v.Error}
			}
			if v.Status >= 500 {
				slog.Error("request", attrs...)
			} else {
				slog.Info("request", attrs...)
			}
			return nil
		},
	})
}

// loggedRequestURI drops the query from SCIM requests, whose filter holds an
// email address or subject, and redacts tokens elsewhere.
func loggedRequestURI(path, uri string) string {
	if isSCIMPath(path) {
		uri, _, _ = strings.Cut(uri, "?")
		return uri
	}
	if strings.Contains(uri, "token=") {
		return tokenRedactRe.ReplaceAllString(uri, "token=REDACTED")
	}
	return uri
}
