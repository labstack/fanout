package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/observability"
)

type ObservabilityQueries interface {
	Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error)
	Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error)
	Performance(context.Context, observability.Scope, string, int) (observability.Result[observability.Performance], error)
	Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error)
	Logs(context.Context, observability.Scope, string, string, string, int) (observability.Result[observability.Logs], error)
}

type ObservabilityHandler struct {
	queries ObservabilityQueries
	now     func() time.Time
}

func NewObservabilityHandler(queries ObservabilityQueries) *ObservabilityHandler {
	return &ObservabilityHandler{queries: queries, now: time.Now}
}

// Register mounts the deterministic product API. These endpoints are for
// dashboards and drill-downs; the browser does not need an agent for ordinary
// telemetry navigation.
func (h *ObservabilityHandler) Register(group *echo.Group) {
	group.GET("/overview", h.overview)
	group.GET("/topology", h.topology)
	group.GET("/performance", h.performance)
	group.GET("/trace", h.trace)
	group.GET("/logs", h.logs)
}

func (h *ObservabilityHandler) performance(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Performance(c.Request().Context(), scope, c.QueryParam("service"), limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) trace(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Trace(c.Request().Context(), scope, c.QueryParam("trace_id"), c.QueryParam("service"), limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) logs(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Logs(c.Request().Context(), scope, c.QueryParam("service"), c.QueryParam("severity"), c.QueryParam("search"), limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) overview(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Overview(c.Request().Context(), scope, limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) topology(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Topology(c.Request().Context(), scope, limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) request(c *echo.Context) (observability.Scope, int, error) {
	window := time.Hour
	if raw := strings.TrimSpace(c.QueryParam("window")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "window must be a positive duration such as 15m or 1h")
		}
		window = parsed
	}
	limit := 0
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "limit must be an integer")
		}
		limit = parsed
	}
	end := h.now().UTC()
	return observability.Scope{
		Namespace: c.QueryParam("namespace"),
		Start:     end.Add(-window),
		End:       end,
	}, limit, nil
}

func mapQueryError(err error) error {
	if errors.Is(err, observability.ErrInvalidScope) || errors.Is(err, observability.ErrInvalidLimit) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return echo.NewHTTPError(http.StatusInternalServerError, "telemetry query failed").Wrap(err)
}
