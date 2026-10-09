package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/panel"
)

type apiError struct {
	status   int
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Problems panel.Problems `json:"problems,omitempty"`
}

func (e *apiError) Error() string   { return e.Message }
func (e *apiError) StatusCode() int { return e.status }

func newAPIError(status int, code, message string) *apiError {
	return &apiError{status: status, Code: code, Message: message}
}

func defaultErrorCode(status int) string {
	switch status {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 405:
		return "method_not_allowed"
	case 409:
		return "conflict"
	case 413:
		return "payload_too_large"
	case 415:
		return "unsupported_media_type"
	case 422:
		return "unprocessable"
	case 429:
		return "rate_limited"
	case 502:
		return "bad_gateway"
	case 503:
		return "unavailable"
	case 504:
		return "timeout"
	default:
		if status >= 400 && status < 500 {
			return "bad_request"
		}
		return "internal"
	}
}

// HTTPErrorHandler is the product error boundary, including router failures.
// Protocol handlers and operational probes write their own responses.
func HTTPErrorHandler(c *echo.Context, err error) {
	if response, _ := echo.UnwrapResponse(c.Response()); response != nil && response.Committed {
		return
	}
	switch c.Path() {
	case "/healthz", "/readyz", "/metrics":
		c.Response().Header().Set("Cache-Control", "no-store")
		echo.DefaultHTTPErrorHandler(false)(c, err)
		return
	}
	status := http.StatusInternalServerError
	var statusError echo.HTTPStatusCoder
	if errors.As(err, &statusError) && statusError.StatusCode() >= 400 && statusError.StatusCode() <= 599 {
		status = statusError.StatusCode()
	}
	// Handlers write client-safe messages and wrap the cause. Any other error
	// is unexpected, so its text stays in the log.
	body := newAPIError(status, defaultErrorCode(status), "The request could not be completed. Please try again.")
	var specific *apiError
	var httpError *echo.HTTPError
	if errors.As(err, &specific) {
		body.Code, body.Message, body.Problems = specific.Code, specific.Message, specific.Problems
	} else if errors.As(err, &httpError) && httpError.Message != "" {
		body.Message = httpError.Message
	} else if status < 500 {
		body.Message = http.StatusText(status)
	}
	if status >= 500 {
		slog.Error("HTTP request failed", "route", c.Path(), "error", err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	var writeErr error
	if c.Request().Method == http.MethodHead {
		writeErr = c.NoContent(status)
	} else {
		writeErr = c.JSON(status, body)
	}
	if writeErr != nil {
		slog.Error("HTTP error response failed", "route", c.Path(), "error", writeErr)
	}
}
