package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
)

type DashboardHandler struct{ dashboards *dashboard.Service }

func RegisterDashboardRoutes(e *echo.Echo, dashboards *dashboard.Service) {
	h := &DashboardHandler{dashboards: dashboards}
	own := RequireCapability(ManageOwnDashboards)
	e.GET("/api/dashboards", h.list, own)
	e.POST("/api/dashboards", h.create, own)
	e.GET("/api/dashboards/:id", h.get, own)
	e.PUT("/api/dashboards/:id", h.replace, own)
	e.PATCH("/api/dashboards/:id", h.edit, own)
	e.DELETE("/api/dashboards/:id", h.delete, own)
	e.GET("/api/dashboards/:id/versions", h.versions, own)
	e.GET("/api/dashboards/:id/versions/:version", h.version, own)
	e.POST("/api/dashboards/:id/versions/:version/restore", h.restore, own)
}

const dashboardBodyLimit = 512 << 10

type createDashboardBody struct {
	Spec panel.Dashboard `json:"spec"`
}

type replaceDashboardBody struct {
	Spec        panel.Dashboard `json:"spec"`
	BaseVersion int             `json:"base_version,omitempty"`
	Message     string          `json:"message,omitempty"`
}

type editDashboardBody struct {
	Operations  []dashboard.Operation `json:"operations"`
	BaseVersion int                   `json:"base_version,omitempty"`
	Message     string                `json:"message,omitempty"`
}

func userAuthor(owner string) dashboard.Author { return dashboard.Author{Kind: "user", ID: owner} }

func (h *DashboardHandler) list(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	items, err := h.dashboards.List(c.Request().Context(), owner)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"dashboards": items})
}

func (h *DashboardHandler) create(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	var body createDashboardBody
	if err := decodeStrict(c, &body, dashboardBodyLimit); err != nil {
		return err
	}
	record, err := h.dashboards.Create(c.Request().Context(), owner, body.Spec, userAuthor(owner))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusCreated, record)
}

func (h *DashboardHandler) get(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	record, err := h.dashboards.Get(c.Request().Context(), owner, c.Param("id"))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func (h *DashboardHandler) replace(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	var body replaceDashboardBody
	if err := decodeStrict(c, &body, dashboardBodyLimit); err != nil {
		return err
	}
	record, err := h.dashboards.Replace(c.Request().Context(), owner, c.Param("id"), body.Spec, body.BaseVersion, userAuthor(owner), body.Message)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func (h *DashboardHandler) edit(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	var body editDashboardBody
	if err := decodeStrict(c, &body, dashboardBodyLimit); err != nil {
		return err
	}
	record, err := h.dashboards.Edit(c.Request().Context(), owner, c.Param("id"), body.Operations, body.BaseVersion, userAuthor(owner), body.Message)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func (h *DashboardHandler) delete(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	if c.Request().Header.Get("Fanout-Confirm-Delete") != c.Param("id") {
		return echo.NewHTTPError(http.StatusPreconditionRequired, "dashboard deletion requires confirmation")
	}
	if err := h.dashboards.Delete(c.Request().Context(), owner, c.Param("id")); err != nil {
		return dashboardError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *DashboardHandler) versions(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	versions, err := h.dashboards.Versions(c.Request().Context(), owner, c.Param("id"))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"versions": versions})
}

func (h *DashboardHandler) restore(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "version must be a positive integer")
	}
	record, err := h.dashboards.Restore(c.Request().Context(), owner, c.Param("id"), version, userAuthor(owner))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func (h *DashboardHandler) version(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "version must be a positive integer")
	}
	record, err := h.dashboards.VersionRecord(c.Request().Context(), owner, c.Param("id"), version)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func dashboardError(c *echo.Context, err error) error {
	if handled, writeErr := writeProblems(c, err); handled {
		return writeErr
	}
	switch {
	case errors.Is(err, dashboard.ErrAlreadyCurrent):
		return newAPIError(http.StatusConflict, dashboard.AlreadyCurrentCode, err.Error())
	case errors.Is(err, dashboard.ErrVersionNotFound):
		return newAPIError(http.StatusNotFound, dashboard.VersionNotFoundCode, err.Error())
	case errors.Is(err, dashboard.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "dashboard not found")
	case errors.Is(err, dashboard.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "a dashboard with that name already exists")
	case errors.Is(err, dashboard.ErrStale):
		return newAPIError(http.StatusConflict, "dashboard_version_conflict", "The dashboard changed since you opened it. Reload to see the latest version.")
	case errors.Is(err, context.DeadlineExceeded):
		return echo.NewHTTPError(http.StatusGatewayTimeout, "Checking the dashboard took too long. Try again, or narrow its panels.").Wrap(err)
	case errors.Is(err, context.Canceled):
		// The client is gone; there is nobody to answer.
		return nil
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "dashboard operation failed").Wrap(err)
	}
}
