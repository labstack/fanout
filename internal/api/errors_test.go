package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/panel"
)

func TestHTTPFailuresCarryStatusCodesAndSafeMessages(t *testing.T) {
	for status, code := range map[int]string{400: "bad_request", 401: "unauthorized", 403: "forbidden", 404: "not_found", 405: "method_not_allowed", 409: "conflict", 413: "payload_too_large", 415: "unsupported_media_type", 422: "unprocessable", 429: "rate_limited", 500: "internal", 502: "bad_gateway", 503: "unavailable", 504: "timeout", 418: "bad_request", 501: "internal"} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := echo.New()
			e.HTTPErrorHandler = HTTPErrorHandler
			e.GET("/api/test", func(*echo.Context) error { return echo.NewHTTPError(status, "client explanation") })
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/test", nil))
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			message := "client explanation"
			if rec.Code != status || len(body) != 2 || body["code"] != code || body["message"] != message || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %s %v", rec.Code, rec.Body, rec.Header())
			}
		})
	}
}

func TestRouterFailuresCarryProductCodes(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = HTTPErrorHandler
	e.GET("/api/known", func(c *echo.Context) error { return c.NoContent(200) })
	for _, tc := range []struct {
		method, path, code string
		status             int
	}{{"GET", "/api/missing", "not_found", 404}, {"POST", "/api/known", "method_not_allowed", 405}, {"HEAD", "/api/missing", "", 404}} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.status || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code, rec.Header())
		}
		if tc.method == "HEAD" {
			if rec.Body.Len() != 0 {
				t.Fatal(rec.Body)
			}
			continue
		}
		if !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatal(rec.Body)
		}
	}
}

func TestSpecificFailuresPreserveCodesAndSpecProblems(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = HTTPErrorHandler
	e.GET("/api/specific", func(*echo.Context) error { return newAPIError(409, "already_current", "Already current.") })
	e.GET("/api/spec", func(c *echo.Context) error {
		_, err := writeProblems(c, panel.Problems{{Path: "panels[0].viz", Message: "Unsupported."}})
		return err
	})
	for _, path := range []string{"/api/specific", "/api/spec"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var body apiError
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if path == "/api/spec" {
			if body.Code != "invalid_spec" || len(body.Problems) != 1 || body.Problems[0].Path != "panels[0].viz" {
				t.Fatal(body)
			}
		} else if body.Code != "already_current" || body.Message != "Already current." {
			t.Fatal(body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Header())
		}
	}
}

func TestInternalFailuresLogPrivateDetailsWithRouteTemplate(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	e := echo.New()
	e.HTTPErrorHandler = HTTPErrorHandler
	e.GET("/api/items/:id", func(*echo.Context) error { return errors.New("private database path") })
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/api/items/secret-id?token=secret", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"code":"internal"`) || strings.Contains(rec.Body.String(), "private") {
		t.Fatal(rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), "private database path") || !strings.Contains(logs.String(), "/api/items/:id") || strings.Contains(logs.String(), "secret") {
		t.Fatal(logs.String())
	}
}

func TestCommittedResponsesRemainUntouchedByErrors(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = HTTPErrorHandler
	e.GET("/api/committed", func(c *echo.Context) error {
		if err := c.String(202, "accepted"); err != nil {
			return err
		}
		return errors.New("late failure")
	})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/api/committed", nil))
	if rec.Code != 202 || rec.Body.String() != "accepted" || rec.Header().Get("Cache-Control") != "" {
		t.Fatal(rec.Code, rec.Body, rec.Header())
	}
}

func TestOAuthErrorsKeepProtocolFields(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = HTTPErrorHandler
	e.POST("/oauth/token", func(c *echo.Context) error { return oauthJSONError(c, 400, "invalid_grant", "The grant is invalid.") })
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("POST", "/oauth/token", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 400 || len(body) != 2 || body["error"] != "invalid_grant" || body["error_description"] != "The grant is invalid." {
		t.Fatal(rec.Code, body)
	}
}

func TestOperationalFailuresKeepTheirExistingBodies(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		e := echo.New()
		e.HTTPErrorHandler = HTTPErrorHandler
		e.GET(path, func(*echo.Context) error { return echo.NewHTTPError(401, "metrics authentication required") })
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 401 || rec.Body.String() != "{\"message\":\"metrics authentication required\"}\n" {
			t.Fatal(path, rec.Code, rec.Body)
		}
	}
}
