package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/labstack/fanout/internal/alert"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ──────────────────────────────────────────────────────────────────────────────
// alert_rules — CRUD + test + test_webhook
// ──────────────────────────────────────────────────────────────────────────────

// AlertRulesIn holds input parameters for the alert_rules tool.
type AlertRulesIn struct {
	Action          string `json:"action"                      jsonschema:"Action: create|list|get|update|delete|enable|disable|test|test_webhook|inspect"`
	RuleID          string `json:"rule_id,omitempty"           jsonschema:"Rule ID (for get/update/delete/enable/disable/test_webhook)"`
	Name            string `json:"name,omitempty"              jsonschema:"Rule name"`
	Description     string `json:"description,omitempty"       jsonschema:"Rule description"`
	Expression      string `json:"expression,omitempty"        jsonschema:"CEL expression: error_rate > 0.05 && p95 > 1000"`
	Service         string `json:"service,omitempty"           jsonschema:"Target service, or '*' for all"`
	ForSeconds      *int   `json:"for_seconds,omitempty"       jsonschema:"Seconds condition must hold before firing (default 60)"`
	CooldownS       *int   `json:"cooldown_s,omitempty"        jsonschema:"Seconds before re-alerting after resolve (default 600)"`
	RepeatIntervalS *int   `json:"repeat_interval_s,omitempty" jsonschema:"Seconds between repeat notifications while firing (default 3600)"`
	WebhookURL      string `json:"webhook_url,omitempty"       jsonschema:"Webhook URL for notifications"`
	WebhookHeaders  string `json:"webhook_headers,omitempty"   jsonschema:"JSON object of HTTP headers"`
	WebhookTemplate string `json:"webhook_template,omitempty"  jsonschema:"Go template for webhook body"`
	NotifyOnResolve *bool  `json:"notify_on_resolve,omitempty" jsonschema:"Send webhook when alert resolves"`
}

// TestResult is the output from the test action.
type TestResult struct {
	Expression string          `json:"expression"`
	Service    string          `json:"service,omitempty"`
	Triggered  bool            `json:"triggered"`
	Env        *alert.AlertEnv `json:"env,omitempty"`
	Message    string          `json:"message,omitempty"`
}

// WebhookResult is the output from the test_webhook action.
type WebhookResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// InspectResult is the output from the inspect action.
type InspectResult struct {
	Fields   []InspectField  `json:"fields"`
	Env      *alert.AlertEnv `json:"env,omitempty"`
	Examples []string        `json:"examples"`
	Message  string          `json:"message,omitempty"`
}

// InspectField describes one available expression variable.
type InspectField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// AlertRulesOut is the response envelope for the alert_rules tool.
type AlertRulesOut struct {
	Rule    *alert.Rule    `json:"rule,omitempty"`
	Rules   []alert.Rule   `json:"rules,omitempty"`
	Test    *TestResult    `json:"test,omitempty"`
	Webhook *WebhookResult `json:"webhook,omitempty"`
	Inspect *InspectResult `json:"inspect,omitempty"`
	Message string         `json:"message,omitempty"`
}

func (s *Server) alertRules(ctx context.Context, req *mcp.CallToolRequest, in AlertRulesIn) (*mcp.CallToolResult, AlertRulesOut, error) {
	if s.alerts == nil {
		return nil, AlertRulesOut{Message: "alerting is disabled"}, nil
	}
	store := s.alerts.Store()

	switch in.Action {
	case "create":
		if in.Name == "" {
			return nil, AlertRulesOut{Message: "name is required"}, nil
		}
		if in.Expression == "" {
			return nil, AlertRulesOut{Message: "expression is required"}, nil
		}
		if _, err := alert.CompileExpression(in.Expression); err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("invalid expression: %s", err)}, nil
		}
		r := alert.Rule{
			Name:            in.Name,
			Description:     in.Description,
			Enabled:         true,
			Service:         in.Service,
			Expression:      in.Expression,
			ForSeconds:      intOrDefault(in.ForSeconds, 60),
			CooldownS:       intOrDefault(in.CooldownS, 600),
			RepeatIntervalS: intOrDefault(in.RepeatIntervalS, 3600),
			WebhookURL:      in.WebhookURL,
			WebhookHeaders:  in.WebhookHeaders,
			WebhookTemplate: in.WebhookTemplate,
			NotifyOnResolve: boolOrDefault(in.NotifyOnResolve, false),
		}
		created, err := store.CreateRule(r)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("create failed: %s", err)}, nil
		}
		// Compile into the engine immediately so it evaluates on the next tick.
		if err := s.alerts.RecompileRule(created.ID, created.Expression); err != nil {
			slog.Error("alert: recompile after create", "rule", created.ID, "err", err)
			return nil, AlertRulesOut{Rule: &created, Message: "rule saved, but the engine could not start evaluating it (check the expression): " + err.Error()}, nil
		}
		return nil, AlertRulesOut{Rule: &created}, nil

	case "list":
		rules, err := store.ListRules()
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("list failed: %s", err)}, nil
		}
		if rules == nil {
			rules = []alert.Rule{}
		}
		return nil, AlertRulesOut{Rules: rules}, nil

	case "get":
		if in.RuleID == "" {
			return nil, AlertRulesOut{Message: "rule_id is required"}, nil
		}
		r, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("get failed: %s", err)}, nil
		}
		return nil, AlertRulesOut{Rule: &r}, nil

	case "update":
		if in.RuleID == "" {
			return nil, AlertRulesOut{Message: "rule_id is required"}, nil
		}
		existing, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		// Merge non-empty fields.
		if in.Name != "" {
			existing.Name = in.Name
		}
		if in.Description != "" {
			existing.Description = in.Description
		}
		if in.Service != "" {
			existing.Service = in.Service
		}
		if in.Expression != "" {
			if _, err := alert.CompileExpression(in.Expression); err != nil {
				return nil, AlertRulesOut{Message: fmt.Sprintf("invalid expression: %s", err)}, nil
			}
			existing.Expression = in.Expression
		}
		if in.ForSeconds != nil {
			existing.ForSeconds = *in.ForSeconds
		}
		if in.CooldownS != nil {
			existing.CooldownS = *in.CooldownS
		}
		if in.RepeatIntervalS != nil {
			existing.RepeatIntervalS = *in.RepeatIntervalS
		}
		if in.WebhookURL != "" {
			existing.WebhookURL = in.WebhookURL
		}
		if in.WebhookHeaders != "" {
			existing.WebhookHeaders = in.WebhookHeaders
		}
		if in.WebhookTemplate != "" {
			existing.WebhookTemplate = in.WebhookTemplate
		}
		if in.NotifyOnResolve != nil {
			existing.NotifyOnResolve = *in.NotifyOnResolve
		}
		updated, err := store.UpdateRule(existing)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("update failed: %s", err)}, nil
		}
		if err := s.alerts.RecompileRule(updated.ID, updated.Expression); err != nil {
			slog.Error("alert: recompile after update", "rule", updated.ID, "err", err)
			return nil, AlertRulesOut{Rule: &updated, Message: "rule saved, but the engine could not restart evaluating it (check the expression): " + err.Error()}, nil
		}
		return nil, AlertRulesOut{Rule: &updated}, nil

	case "delete":
		if in.RuleID == "" {
			return nil, AlertRulesOut{Message: "rule_id is required"}, nil
		}
		if err := store.DeleteRule(in.RuleID); err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("delete failed: %s", err)}, nil
		}
		s.alerts.RemoveRule(in.RuleID)
		return nil, AlertRulesOut{Message: "deleted"}, nil

	case "enable":
		if in.RuleID == "" {
			return nil, AlertRulesOut{Message: "rule_id is required"}, nil
		}
		r, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		r.Enabled = true
		updated, err := store.UpdateRule(r)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("enable failed: %s", err)}, nil
		}
		if err := s.alerts.RecompileRule(updated.ID, updated.Expression); err != nil {
			slog.Error("alert: recompile after enable", "rule", updated.ID, "err", err)
			return nil, AlertRulesOut{Rule: &updated, Message: "rule enabled, but the engine could not start evaluating it (check the expression): " + err.Error()}, nil
		}
		return nil, AlertRulesOut{Rule: &updated}, nil

	case "disable":
		if in.RuleID == "" {
			return nil, AlertRulesOut{Message: "rule_id is required"}, nil
		}
		r, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		r.Enabled = false
		updated, err := store.UpdateRule(r)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("disable failed: %s", err)}, nil
		}
		s.alerts.RemoveRule(updated.ID)
		return nil, AlertRulesOut{Rule: &updated}, nil

	case "test":
		if in.Expression == "" {
			return nil, AlertRulesOut{Message: "expression is required"}, nil
		}
		prog, err := alert.CompileExpression(in.Expression)
		if err != nil {
			return nil, AlertRulesOut{
				Test: &TestResult{
					Expression: in.Expression,
					Message:    fmt.Sprintf("compile error: %s", err),
				},
			}, nil
		}
		if in.Service != "" {
			env, ok := s.alerts.BuildEnvForService(ctx, in.Service)
			if !ok {
				return nil, AlertRulesOut{
					Test: &TestResult{
						Expression: in.Expression,
						Service:    in.Service,
						Message:    fmt.Sprintf("no live data for service %q", in.Service),
					},
				}, nil
			}
			triggered, evalErr := alert.SafeEval(prog, env)
			msg := ""
			if evalErr != nil {
				msg = fmt.Sprintf("eval error: %s", evalErr)
			}
			return nil, AlertRulesOut{
				Test: &TestResult{
					Expression: in.Expression,
					Service:    in.Service,
					Triggered:  triggered,
					Env:        &env,
					Message:    msg,
				},
			}, nil
		}
		// No service: just validate, return compile success.
		return nil, AlertRulesOut{
			Test: &TestResult{
				Expression: in.Expression,
				Message:    "expression compiled successfully (provide service for live evaluation)",
			},
		}, nil

	case "test_webhook":
		if in.RuleID == "" {
			return nil, AlertRulesOut{Message: "rule_id is required"}, nil
		}
		r, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		ctx := alert.ActionContext{
			Rule: r,
			Alert: alert.Alert{
				RuleID:  r.ID,
				Service: r.Service,
				State:   "firing",
				Value:   0.1,
				FiredAt: time.Now().Format(time.RFC3339),
			},
			Env:   alert.AlertEnv{Service: r.Service},
			Event: "test",
			Time:  time.Now(),
		}
		status, webhookErr := alert.FireWebhook(r, ctx)
		msg := ""
		if webhookErr != nil {
			msg = webhookErr.Error()
		}
		return nil, AlertRulesOut{
			Webhook: &WebhookResult{
				Status:  status,
				Message: msg,
			},
		}, nil

	case "inspect":
		fields := []InspectField{
			{Name: "error_rate", Type: "float64", Description: "Fraction of spans with error status (0–1) over the last 5 minutes"},
			{Name: "p50", Type: "float64", Description: "Median latency in milliseconds"},
			{Name: "p95", Type: "float64", Description: "P95 latency in milliseconds"},
			{Name: "throughput", Type: "float64", Description: "Total span count over the last 5 minutes"},
			{Name: "log_count", Type: "float64", Description: "Log entry count over the last 5 minutes"},
			{Name: "z_score", Type: "float64", Description: "Max absolute anomaly z-score from the intelligence detector"},
			{Name: "health_score", Type: "float64", Description: "System-wide health score (0–100) from the intelligence detector"},
			{Name: "error_rate_delta", Type: "float64", Description: "Percent change in error_rate vs the previous 5-minute window"},
			{Name: "p95_delta", Type: "float64", Description: "Percent change in p95 vs the previous 5-minute window"},
			{Name: "throughput_delta", Type: "float64", Description: "Percent change in throughput vs the previous 5-minute window"},
			{Name: "service", Type: "string", Description: "Service name"},
		}
		examples := []string{
			`error_rate > 0.05`,
			`error_rate > 0.05 && p95 > 1000`,
			`p95 > 2000`,
			`throughput < 10`,
			`z_score > 3.0`,
			`error_rate_delta > 50`,
			`p95_delta > 100 && throughput > 100`,
			`health_score < 50`,
		}
		result := InspectResult{Fields: fields, Examples: examples}
		if in.Service != "" {
			env, ok := s.alerts.BuildEnvForService(ctx, in.Service)
			if ok {
				result.Env = &env
			} else {
				result.Message = fmt.Sprintf("no live data for service %q", in.Service)
			}
		}
		return nil, AlertRulesOut{Inspect: &result}, nil

	default:
		return nil, AlertRulesOut{
			Message: fmt.Sprintf("unknown action %q — valid actions: create, list, get, update, delete, enable, disable, test, test_webhook, inspect", in.Action),
		}, nil
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// alerts — view alert state
// ──────────────────────────────────────────────────────────────────────────────

// AlertsIn holds input parameters for the alerts tool.
type AlertsIn struct {
	State   string `json:"state,omitempty"   jsonschema:"Filter by state: firing|pending|resolved|all"`
	Service string `json:"service,omitempty" jsonschema:"Filter by service name"`
	RuleID  string `json:"rule_id,omitempty" jsonschema:"Filter by rule ID"`
}

// AlertsOut is the response envelope for the alerts tool.
type AlertsOut struct {
	Alerts  []alert.Alert      `json:"alerts"`
	Summary alert.AlertSummary `json:"summary"`
	Message string             `json:"message,omitempty"`
}

func (s *Server) alertsList(ctx context.Context, req *mcp.CallToolRequest, in AlertsIn) (*mcp.CallToolResult, AlertsOut, error) {
	if s.alerts == nil {
		return nil, AlertsOut{Alerts: []alert.Alert{}, Message: "alerting is disabled"}, nil
	}
	store := s.alerts.Store()

	// Normalize "all" state to empty string (no filter).
	state := in.State
	if state == "all" {
		state = ""
	}

	alerts, err := store.ListAlerts(state, in.Service, in.RuleID)
	if err != nil {
		return nil, AlertsOut{Alerts: []alert.Alert{}, Message: fmt.Sprintf("query failed: %s", err)}, nil
	}
	if alerts == nil {
		alerts = []alert.Alert{}
	}

	summary, err := store.AlertSummary()
	if err != nil {
		slog.Warn("alert: summary query failed", "err", err)
		return nil, AlertsOut{Alerts: alerts, Message: "summary unavailable"}, nil
	}

	return nil, AlertsOut{
		Alerts:  alerts,
		Summary: summary,
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

// intOrDefault returns *p if non-nil, otherwise def.
func intOrDefault(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

// boolOrDefault returns *p if non-nil, otherwise def.
func boolOrDefault(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}
