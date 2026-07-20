package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
)

type DashboardHandler struct{ db *sql.DB }

type DashboardState struct {
	Layout  []DashboardLayout `json:"layout"`
	Widgets []DashboardWidget `json:"widgets"`
	Filters DashboardFilters  `json:"filters"`
}

type DashboardLayout struct {
	I    string `json:"i"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	MinW int    `json:"minW,omitempty"`
	MinH int    `json:"minH,omitempty"`
}

type DashboardWidget struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Enabled bool   `json:"enabled"`
}

type DashboardFilters struct {
	Window    string `json:"window"`
	Namespace string `json:"namespace"`
}

func RegisterDashboardRoutes(e *echo.Echo, db *sql.DB) {
	h := &DashboardHandler{db: db}
	e.GET("/api/dashboard", h.Get)
	e.PUT("/api/dashboard", h.Put)
}

func (h *DashboardHandler) Get(c *echo.Context) error {
	user := GetCurrentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var raw, updated string
	err := h.db.QueryRowContext(c.Request().Context(), `SELECT state_json, updated_at FROM dashboard_state WHERE owner_id = ?`, user.ID).Scan(&raw, &updated)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusOK, map[string]any{"state": defaultDashboardState(), "updated_at": time.Now().UTC().Format(time.RFC3339)})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "dashboard state unavailable")
	}
	var state DashboardState
	if json.Unmarshal([]byte(raw), &state) != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "dashboard state is invalid")
	}
	return c.JSON(http.StatusOK, map[string]any{"state": state, "updated_at": updated})
}

func (h *DashboardHandler) Put(c *echo.Context) error {
	user := GetCurrentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var state DashboardState
	dec := json.NewDecoder(io.LimitReader(c.Request().Body, 64<<10))
	if err := dec.Decode(&state); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid dashboard state")
	}
	if err := validateDashboardState(state); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	raw, _ := json.Marshal(state)
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := h.db.ExecContext(c.Request().Context(), `INSERT INTO dashboard_state (owner_id,state_json,updated_at) VALUES (?,?,?) ON CONFLICT(owner_id) DO UPDATE SET state_json=excluded.state_json, updated_at=excluded.updated_at`, user.ID, raw, updated)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "dashboard state could not be saved")
	}
	return c.JSON(http.StatusOK, map[string]any{"state": state, "updated_at": updated})
}

func validateDashboardState(state DashboardState) error {
	if len(state.Layout) > 32 || len(state.Widgets) > 32 {
		return echo.NewHTTPError(http.StatusBadRequest, "dashboard is limited to 32 widgets")
	}
	ids := make(map[string]bool, len(state.Widgets))
	for _, widget := range state.Widgets {
		if strings.TrimSpace(widget.ID) == "" || ids[widget.ID] {
			return echo.NewHTTPError(http.StatusBadRequest, "widget ids must be unique")
		}
		ids[widget.ID] = true
		if widget.Type != "overview" && widget.Type != "topology" && widget.Type != "activity" && widget.Type != "assistant" {
			return echo.NewHTTPError(http.StatusBadRequest, "unsupported widget type")
		}
	}
	for _, item := range state.Layout {
		if !ids[item.I] || item.W < 1 || item.H < 1 || item.X < 0 || item.Y < 0 || item.X+item.W > 12 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid widget layout")
		}
	}
	if state.Filters.Window == "" {
		state.Filters.Window = "1h"
	}
	if state.Filters.Window != "15m" && state.Filters.Window != "1h" && state.Filters.Window != "6h" && state.Filters.Window != "24h" {
		return echo.NewHTTPError(http.StatusBadRequest, "unsupported dashboard window")
	}
	return nil
}

func defaultDashboardState() DashboardState {
	return DashboardState{
		Widgets: []DashboardWidget{{ID: "health", Type: "overview", Title: "System health", Enabled: true}, {ID: "topology", Type: "topology", Title: "Service map", Enabled: true}, {ID: "activity", Type: "activity", Title: "Recent activity", Enabled: true}, {ID: "assistant", Type: "assistant", Title: "Ask Fanout", Enabled: true}},
		Layout:  []DashboardLayout{{I: "health", X: 0, Y: 0, W: 4, H: 3, MinW: 3, MinH: 2}, {I: "topology", X: 4, Y: 0, W: 8, H: 6, MinW: 4, MinH: 4}, {I: "activity", X: 0, Y: 3, W: 4, H: 3, MinW: 3, MinH: 2}, {I: "assistant", X: 0, Y: 6, W: 12, H: 3, MinW: 4, MinH: 2}},
		Filters: DashboardFilters{Window: "1h"},
	}
}
