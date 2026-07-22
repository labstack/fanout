package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/dashboard"
)

type DashboardHandler struct{ dashboards *dashboard.Service }

func RegisterDashboardRoutes(e *echo.Echo, dashboards *dashboard.Service) {
	h := &DashboardHandler{dashboards: dashboards}
	own := RequireCapability(ManageOwnDashboards)
	e.GET("/api/dashboards", h.List, own)
	e.POST("/api/dashboards", h.Create, own)
	e.GET("/api/dashboards/:id", h.Get, own)
	e.PUT("/api/dashboards/:id", h.Put, own)
	e.DELETE("/api/dashboards/:id", h.Delete, own)

	// Legacy single-canvas endpoints, retained for API clients: they always
	// address the owner's default dashboard, while the named collection above
	// addresses dashboards individually.
	e.GET("/api/dashboard", h.GetDefault, own)
	e.PUT("/api/dashboard", h.PutDefault, own)
}

func (h *DashboardHandler) List(c *echo.Context) error {
	owner, err := dashboardOwner(c)
	if err != nil {
		return err
	}
	items, err := h.dashboards.List(c.Request().Context(), owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "dashboards unavailable").Wrap(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"dashboards": items})
}

func (h *DashboardHandler) Create(c *echo.Context) error {
	owner, err := dashboardOwner(c)
	if err != nil {
		return err
	}
	var input dashboard.CreateInput
	if err := decodeDashboard(c, &input); err != nil {
		return err
	}
	created, err := h.dashboards.Create(c.Request().Context(), owner, input)
	if err != nil {
		return mapDashboardError(err)
	}
	return c.JSON(http.StatusCreated, created)
}

func (h *DashboardHandler) Get(c *echo.Context) error {
	owner, err := dashboardOwner(c)
	if err != nil {
		return err
	}
	item, err := h.dashboards.Get(c.Request().Context(), owner, c.Param("id"))
	if err != nil {
		return mapDashboardError(err)
	}
	return c.JSON(http.StatusOK, item)
}

func (h *DashboardHandler) Put(c *echo.Context) error {
	owner, err := dashboardOwner(c)
	if err != nil {
		return err
	}
	var input dashboard.UpdateInput
	if err := decodeDashboard(c, &input); err != nil {
		return err
	}
	updated, err := h.dashboards.Update(c.Request().Context(), owner, c.Param("id"), input)
	if err != nil {
		return mapDashboardError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

func (h *DashboardHandler) Delete(c *echo.Context) error {
	owner, err := dashboardOwner(c)
	if err != nil {
		return err
	}
	if c.Request().Header.Get("X-Fanout-Confirm-Delete") != c.Param("id") {
		return echo.NewHTTPError(http.StatusPreconditionRequired, "dashboard deletion requires confirmation")
	}
	if err := h.dashboards.Delete(c.Request().Context(), owner, c.Param("id")); err != nil {
		return mapDashboardError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *DashboardHandler) GetDefault(c *echo.Context) error {
	owner, err := dashboardOwner(c)
	if err != nil {
		return err
	}
	item, err := h.dashboards.Default(c.Request().Context(), owner)
	if err != nil {
		return mapDashboardError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"state": item.State, "updated_at": item.UpdatedAt})
}

func (h *DashboardHandler) PutDefault(c *echo.Context) error {
	owner, err := dashboardOwner(c)
	if err != nil {
		return err
	}
	item, err := h.dashboards.Default(c.Request().Context(), owner)
	if err != nil {
		return mapDashboardError(err)
	}
	var state dashboard.State
	if err := decodeDashboard(c, &state); err != nil {
		return err
	}
	updated, err := h.dashboards.Update(c.Request().Context(), owner, item.ID, dashboard.UpdateInput{Name: item.Name, Description: item.Description, State: state})
	if err != nil {
		return mapDashboardError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"state": updated.State, "updated_at": updated.UpdatedAt})
}

func dashboardOwner(c *echo.Context) (string, error) {
	user := GetCurrentUser(c)
	if user == nil || user.ID == publicViewerID {
		return "", echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	return user.ID, nil
}

func decodeDashboard(c *echo.Context, value any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid dashboard document")
	}
	return nil
}

func mapDashboardError(err error) error {
	switch {
	case errors.Is(err, dashboard.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "dashboard not found")
	case errors.Is(err, dashboard.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "a dashboard with that name already exists")
	default:
		var validation *dashboard.ValidationError
		if errors.As(err, &validation) {
			return echo.NewHTTPError(http.StatusBadRequest, validation.Message)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "dashboard operation failed").Wrap(err)
	}
}
