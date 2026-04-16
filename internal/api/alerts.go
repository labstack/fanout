package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/alert"
)

// RegisterAlertRoutes registers alert management REST endpoints.
func RegisterAlertRoutes(e *echo.Echo, store *alert.Store, engine *alert.Engine) {
	h := &alertHandler{store: store, engine: engine}

	e.GET("/api/alerts", h.ListAlerts)
	e.GET("/api/alerts/summary", h.AlertSummary)
	e.GET("/api/alert-rules", h.ListRules)
	e.POST("/api/alert-rules", h.CreateRule)
	e.PUT("/api/alert-rules/:id", h.UpdateRule)
	e.DELETE("/api/alert-rules/:id", h.DeleteRule)
	e.POST("/api/alert-rules/:id/test", h.TestRule)
}

type alertHandler struct {
	store  *alert.Store
	engine *alert.Engine
}

// ListAlerts returns alerts with optional state/service/ruleID filters.
func (h *alertHandler) ListAlerts(c *echo.Context) error {
	if h.store == nil {
		return c.JSON(200, []alert.Alert{})
	}
	state := c.QueryParam("state")
	service := c.QueryParam("service")
	ruleID := c.QueryParam("rule_id")

	alerts, err := h.store.ListAlerts(state, service, ruleID)
	if err != nil {
		slog.Error("list alerts failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list alerts")
	}
	if alerts == nil {
		alerts = []alert.Alert{}
	}
	return c.JSON(200, alerts)
}

// AlertSummary returns firing/pending/resolved counts.
func (h *alertHandler) AlertSummary(c *echo.Context) error {
	if h.store == nil {
		return c.JSON(200, alert.AlertSummary{})
	}
	summary, err := h.store.AlertSummary()
	if err != nil {
		slog.Error("alert summary failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get alert summary")
	}
	return c.JSON(200, summary)
}

// ListRules returns all alert rules.
func (h *alertHandler) ListRules(c *echo.Context) error {
	if h.store == nil {
		return c.JSON(200, []alert.Rule{})
	}
	rules, err := h.store.ListRules()
	if err != nil {
		slog.Error("list rules failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list rules")
	}
	if rules == nil {
		rules = []alert.Rule{}
	}
	return c.JSON(200, rules)
}

// CreateRule creates a new alert rule.
func (h *alertHandler) CreateRule(c *echo.Context) error {
	if h.store == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "alerts not configured")
	}

	var req alert.Rule
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Expression) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name and expression are required")
	}

	// Validate expression compiles
	if _, err := alert.CompileExpression(req.Expression); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid expression: "+err.Error())
	}

	created, err := h.store.CreateRule(req)
	if err != nil {
		slog.Error("create rule failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create rule")
	}

	// Compile in the engine so it starts evaluating immediately
	if h.engine != nil {
		if err := h.engine.RecompileRule(created.ID, created.Expression); err != nil {
			slog.Error("alert rule created but failed to compile in engine", "rule", created.ID, "err", err)
		}
	}

	return c.JSON(201, created)
}

// UpdateRule updates an existing alert rule.
func (h *alertHandler) UpdateRule(c *echo.Context) error {
	if h.store == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "alerts not configured")
	}

	id := c.Param("id")
	var req alert.Rule
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.ID = id

	if strings.TrimSpace(req.Expression) != "" {
		if _, err := alert.CompileExpression(req.Expression); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid expression: "+err.Error())
		}
	}

	updated, err := h.store.UpdateRule(req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return echo.NewHTTPError(http.StatusNotFound, "rule not found")
		}
		slog.Error("update rule failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update rule")
	}

	if h.engine != nil {
		if updated.Enabled {
			if err := h.engine.RecompileRule(updated.ID, updated.Expression); err != nil {
				slog.Error("alert rule updated but failed to compile in engine", "rule", updated.ID, "err", err)
			}
		} else {
			h.engine.RemoveRule(updated.ID)
			// Clear active alerts so they don't linger indefinitely
			if activeAlerts, err := h.store.ListAlerts("", "", updated.ID); err == nil {
				for _, a := range activeAlerts {
					if a.State == "firing" || a.State == "pending" {
						_ = h.store.DeleteAlert(a.ID)
					}
				}
			}
		}
	}

	return c.JSON(200, updated)
}

// DeleteRule deletes an alert rule and its associated alerts.
func (h *alertHandler) DeleteRule(c *echo.Context) error {
	if h.store == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "alerts not configured")
	}

	id := c.Param("id")
	if err := h.store.DeleteRule(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return echo.NewHTTPError(http.StatusNotFound, "rule not found")
		}
		slog.Error("delete rule failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete rule")
	}

	if h.engine != nil {
		h.engine.RemoveRule(id)
	}

	return c.NoContent(204)
}

// TestRule dry-runs a rule's expression against live data.
func (h *alertHandler) TestRule(c *echo.Context) error {
	if h.engine == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "alert engine not configured")
	}

	id := c.Param("id")
	rule, err := h.store.GetRule(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return echo.NewHTTPError(http.StatusNotFound, "rule not found")
		}
		slog.Error("get rule failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get rule")
	}

	prog, compileErr := alert.CompileExpression(rule.Expression)
	if compileErr != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "expression compile error: "+compileErr.Error())
	}

	envs := h.engine.BuildAllEnvs(c.Request().Context())

	type testResult struct {
		Service   string         `json:"service"`
		Triggered bool           `json:"triggered"`
		Error     string         `json:"error,omitempty"`
		Env       alert.AlertEnv `json:"env"`
	}

	var results []testResult
	services := resolveTestServices(rule, envs)
	for _, svc := range services {
		env := envs[svc]
		triggered, evalErr := alert.SafeEval(prog, env)
		tr := testResult{
			Service:   svc,
			Triggered: triggered,
			Env:       env,
		}
		if evalErr != nil {
			tr.Error = evalErr.Error()
		}
		results = append(results, tr)
	}

	return c.JSON(200, map[string]any{
		"rule":    rule,
		"results": results,
	})
}

func resolveTestServices(rule alert.Rule, envs map[string]alert.AlertEnv) []string {
	if rule.Service == "" || rule.Service == "*" {
		svcs := make([]string, 0, len(envs))
		for svc := range envs {
			svcs = append(svcs, svc)
		}
		return svcs
	}
	return []string{rule.Service}
}
