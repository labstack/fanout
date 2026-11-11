package api

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/pitot/internal/alert"
	"github.com/labstack/pitot/internal/auth"
)

// RegisterAlertRoutes registers alert management REST endpoints.
func RegisterAlertRoutes(e *echo.Echo, store *alert.Store, engine *alert.Engine) {
	h := &alertHandler{store: store, engine: engine}

	read := RequireCapability(ReadTelemetry)
	manage := RequireCapability(ManageAlerts)
	e.GET("/api/alerting/alerts", h.ListAlerts, read)
	e.GET("/api/alerting/summary", h.AlertSummary, read)
	e.GET("/api/alerting/rules", h.ListRules, read)
	e.GET("/api/alerting/email", h.EmailOptions, read)
	e.POST("/api/alerting/rules", h.CreateRule, manage)
	e.PUT("/api/alerting/rules/:id", h.ReplaceRule, manage)
	e.DELETE("/api/alerting/rules/:id", h.DeleteRule, manage)
	e.POST("/api/alerting/rules/:id/test", h.TestRule, manage)
}

type alertHandler struct {
	store  *alert.Store
	engine *alert.Engine
}

// ListAlerts returns alerts with optional state/service/ruleID filters.
func (h *alertHandler) ListAlerts(c *echo.Context) error {
	if h.store == nil {
		return c.JSON(200, map[string][]alert.Alert{"alerts": {}})
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
	return c.JSON(200, map[string][]alert.Alert{"alerts": alerts})
}

// AlertSummary returns firing/pending/resolved counts.
func (h *alertHandler) AlertSummary(c *echo.Context) error {
	if h.store == nil {
		return c.JSON(200, alert.Summary{})
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
		return c.JSON(200, map[string][]alert.Rule{"rules": {}})
	}
	rules, err := h.store.ListRules()
	if err != nil {
		slog.Error("list rules failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list rules")
	}
	if rules == nil {
		rules = []alert.Rule{}
	}
	user := GetCurrentUser(c)
	manager := user != nil && HasCapability(*user, ManageAlerts)
	for i := range rules {
		rules[i] = h.engine.WithEmailState(rules[i])
		if !manager {
			rules[i] = readerRule(rules[i])
		}
	}
	return c.JSON(200, map[string][]alert.Rule{"rules": rules})
}

// readerRule is the view of a rule for principals who cannot manage alerts. It
// is an allowlist, so a field added to Rule stays hidden until it is listed
// here. Delivery settings are left out: webhook headers can hold credentials,
// and recipients and failure detail identify people and mail servers. Readers
// still see email_status, so a failing rule is visible to them.
func readerRule(rule alert.Rule) alert.Rule {
	return alert.Rule{
		ID:              rule.ID,
		Name:            rule.Name,
		Description:     rule.Description,
		Enabled:         rule.Enabled,
		Service:         rule.Service,
		Namespace:       rule.Namespace,
		Expression:      rule.Expression,
		ForSeconds:      rule.ForSeconds,
		CooldownS:       rule.CooldownS,
		RepeatIntervalS: rule.RepeatIntervalS,
		NotifyOnResolve: rule.NotifyOnResolve,
		EmailStatus:     rule.EmailStatus,
		EmailStatusAt:   rule.EmailStatusAt,
		CreatedAt:       rule.CreatedAt,
		UpdatedAt:       rule.UpdatedAt,
	}
}

// EmailOptions reports whether rules can deliver by email on this instance.
func (h *alertHandler) EmailOptions(c *echo.Context) error {
	return c.JSON(200, h.engine.EmailOptions())
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

	if err := checkRule(req, true); err != nil {
		return err
	}
	if err := h.checkRecipients(&req); err != nil {
		return err
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

	return c.JSON(201, h.engine.WithEmailState(created))
}

// ReplaceRule replaces an existing alert rule.
func (h *alertHandler) ReplaceRule(c *echo.Context) error {
	if h.store == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "alerts not configured")
	}

	id := c.Param("id")
	var req alert.Rule
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.ID = id

	// Headers are checked only when the replace changes them, so a rule saved
	// before the check tightened can still be toggled or edited elsewhere.
	stored, err := h.store.GetRule(id)
	changed := err != nil || stored.WebhookHeaders != req.WebhookHeaders
	if err := checkRule(req, changed); err != nil {
		return err
	}
	// Likewise for recipients: a saved rule stays editable after SMTP is removed.
	if err != nil || !slices.Equal(stored.EmailRecipients, req.EmailRecipients) {
		if err := h.checkRecipients(&req); err != nil {
			return err
		}
	}

	updated, err := h.store.ReplaceRule(req)
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
			h.engine.DisableRule(updated)
		}
	}

	return c.JSON(200, h.engine.WithEmailState(updated))
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

	if _, err := alert.CompileExpression(rule.Expression); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid expression: "+err.Error())
	}
	results, err := h.engine.Test(c.Request().Context(), rule)
	if err != nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "The rule could not be tested: "+err.Error()).Wrap(err)
	}
	return c.JSON(200, map[string]any{
		"rule":    rule,
		"results": results,
	})
}

// checkRecipients normalizes a rule's email recipients in place, rejecting
// invalid addresses, too many of them, or any when email is unavailable.
func (h *alertHandler) checkRecipients(rule *alert.Rule) error {
	if len(rule.EmailRecipients) == 0 {
		return nil
	}
	if options := h.engine.EmailOptions(); !options.Available {
		return newAPIError(http.StatusBadRequest, "email_unavailable", options.Message)
	}
	recipients, err := auth.NormalizeRecipients(rule.EmailRecipients)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	rule.EmailRecipients = recipients
	return nil
}

// checkRule rejects an expression that does not compile and, when headers is
// set, webhook headers that are not a JSON object of valid HTTP headers. A
// replace may omit the expression.
func checkRule(rule alert.Rule, headers bool) error {
	if strings.TrimSpace(rule.Expression) != "" {
		if _, err := alert.CompileExpression(rule.Expression); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid expression: "+err.Error())
		}
	}
	if headers {
		if err := alert.CheckHeaders(rule.WebhookHeaders); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
	}
	return nil
}
