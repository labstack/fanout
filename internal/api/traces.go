package api

import (
	"context"
	"errors"
	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/observability"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TraceQueries interface {
	Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error)
}
type TraceHandler struct {
	queries   TraceQueries
	maxWindow time.Duration
}

func RegisterTraceRoutes(e *echo.Echo, queries TraceQueries, retentionDays int) {
	days := 30
	if retentionDays > 0 {
		days = retentionDays
	}
	h := &TraceHandler{queries: queries, maxWindow: time.Duration(days) * 24 * time.Hour}
	e.GET("/api/traces/:id", h.get, RequireCapability(ReadTelemetry))
}
func (h *TraceHandler) get(c *echo.Context) error {
	bad := func() error { return echo.NewHTTPError(http.StatusBadRequest, "invalid trace scope") }
	id := strings.TrimSpace(c.Param("id"))
	namespace := strings.TrimSpace(c.QueryParam("namespace"))
	from, e1 := time.Parse(time.RFC3339Nano, c.QueryParam("from"))
	to, e2 := time.Parse(time.RFC3339Nano, c.QueryParam("to"))
	if id == "" || len(namespace) > 200 || e1 != nil || e2 != nil || !from.Before(to) || to.Sub(from) > h.maxWindow {
		return bad()
	}
	limit := 200
	if raw := c.QueryParam("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			return bad()
		}
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 20*time.Second)
	defer cancel()
	out, err := h.queries.Trace(ctx, observability.Scope{Start: from.UTC(), End: to.UTC(), Namespace: namespace}, id, "", limit)
	if err != nil {
		return traceError(err)
	}
	return c.JSON(http.StatusOK, out)
}
func traceError(err error) error {
	switch {
	case errors.Is(err, observability.ErrInvalidScope), errors.Is(err, observability.ErrInvalidLimit):
		return echo.NewHTTPError(http.StatusBadRequest, "invalid trace scope").Wrap(err)
	case errors.Is(err, context.DeadlineExceeded):
		return echo.NewHTTPError(http.StatusGatewayTimeout, "trace query timed out").Wrap(err)
	case errors.Is(err, context.Canceled):
		return echo.NewHTTPError(499, "trace query canceled").Wrap(err)
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "trace query failed").Wrap(err)
	}
}
