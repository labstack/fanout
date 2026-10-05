package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type DashboardIDInput struct {
	ID string `json:"id" jsonschema:"Dashboard ID returned by list_dashboards or create_dashboard"`
}

type DashboardCreateInput struct {
	Dashboard panel.Dashboard `json:"dashboard" jsonschema:"Complete v1 dashboard spec"`
}

type DashboardReplaceInput struct {
	ID          string          `json:"id" jsonschema:"Dashboard ID"`
	Dashboard   panel.Dashboard `json:"dashboard" jsonschema:"Complete replacement spec; omitted panels are removed"`
	BaseVersion int             `json:"base_version,omitempty" jsonschema:"Version you read; the call fails if someone saved since"`
	Message     string          `json:"message,omitempty" jsonschema:"One line describing the change, shown in history"`
}

type DashboardEditInput struct {
	ID          string                `json:"id" jsonschema:"Dashboard ID"`
	Operations  []dashboard.Operation `json:"operations" jsonschema:"Edits applied in order, atomically"`
	BaseVersion int                   `json:"base_version,omitempty" jsonschema:"Version you read; the call fails if someone saved since"`
	Message     string                `json:"message,omitempty" jsonschema:"One line describing the change, shown in history"`
}

type dashboardListOutput struct {
	Dashboards []dashboard.Summary `json:"dashboards"`
}

type dashboardOutput struct {
	Dashboard dashboard.Record `json:"dashboard"`
	Warnings  []string         `json:"warnings,omitempty"`
}

const (
	listDashboardsTool = iota
	getDashboardTool
	createDashboardTool
	replaceDashboardTool
	editDashboardTool
)

// Registration and transport authorization use the same dashboard tool catalog.
var dashboardTools = [...]mcp.Tool{
	{
		Name: "list_dashboards", Title: "List dashboards",
		Description: "List the authenticated user's dashboards with panel counts and versions.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "get_dashboard", Title: "Get dashboard",
		Description: "Read one dashboard's complete spec and version. Read it before editing so operations name real panel ids.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "create_dashboard", Title: "Create dashboard",
		Description: "Create a dashboard for the authenticated user from a complete spec. Read get_telemetry_schema first and preview_panels until every panel is ok or deliberately empty. The result lists any panel that is still empty or failing. " + specGuide,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "replace_dashboard", Title: "Replace dashboard",
		Description: "Replace a dashboard's whole spec; omitted panels are removed. Use only for a redesign the user asked for; use edit_dashboard to change a few panels.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "edit_dashboard", Title: "Edit dashboard",
		Description: "Change a dashboard with typed operations, applied in order and saved as one version: add_panel, update_panel (set replaces the named fields), remove_panel, move_panel, set_variable, remove_variable, set_time, rename. Panels not named are left unchanged. Only edit when the user asks to change that dashboard.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
	},
}

// RequiredToolScope reports additional delegated scope needed by a tool.
// All remote MCP requests already require telemetry:read.
func RequiredToolScope(name string) string {
	for _, tool := range dashboardTools {
		if tool.Name == name {
			return dashboard.OAuthScope
		}
	}
	return ""
}

func (s *Server) registerDashboardTools() {
	if s.dashboards == nil {
		return
	}
	// AddTool copies each tool before inferring schemas, so the catalog stays
	// shared and unmodified across servers.
	mcp.AddTool(s.mcp, &dashboardTools[listDashboardsTool], s.dashboardList)
	mcp.AddTool(s.mcp, &dashboardTools[getDashboardTool], s.dashboardGet)
	mcp.AddTool(s.mcp, &dashboardTools[createDashboardTool], s.dashboardCreate)
	mcp.AddTool(s.mcp, &dashboardTools[replaceDashboardTool], s.dashboardReplace)
	mcp.AddTool(s.mcp, &dashboardTools[editDashboardTool], s.dashboardEdit)
}

func agentAuthor(owner string) dashboard.Author { return dashboard.Author{Kind: "agent", ID: owner} }

func (s *Server) dashboardList(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, dashboardListOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardListOutput{}, err
	}
	items, err := s.dashboards.List(ctx, owner)
	if err != nil {
		return nil, dashboardListOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Found %d dashboards.", len(items))), dashboardListOutput{Dashboards: items}, nil
}

func (s *Server) dashboardGet(ctx context.Context, req *mcp.CallToolRequest, input DashboardIDInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Get(ctx, owner, strings.TrimSpace(input.ID))
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Loaded %q, version %d, with %d panels.", record.Name, record.Version, len(record.Spec.Panels))), dashboardOutput{Dashboard: record}, nil
}

func (s *Server) dashboardCreate(ctx context.Context, req *mcp.CallToolRequest, input DashboardCreateInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Create(ctx, owner, input.Dashboard, agentAuthor(owner))
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Created", record)
}

func (s *Server) dashboardReplace(ctx context.Context, req *mcp.CallToolRequest, input DashboardReplaceInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Replace(ctx, owner, strings.TrimSpace(input.ID), input.Dashboard, input.BaseVersion, agentAuthor(owner), input.Message)
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Replaced", record)
}

func (s *Server) dashboardEdit(ctx context.Context, req *mcp.CallToolRequest, input DashboardEditInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Edit(ctx, owner, strings.TrimSpace(input.ID), input.Operations, input.BaseVersion, agentAuthor(owner), input.Message)
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Updated", record)
}

// saveCheckBudget bounds the post-save run that reports empty or failing
// panels. The dashboard is already saved; a slow check is reported as
// unchecked, and the result says so, rather than holding the answer.
const saveCheckBudget = 8 * time.Second

func (s *Server) saved(ctx context.Context, verb string, record dashboard.Record) (*mcp.CallToolResult, dashboardOutput, error) {
	out := dashboardOutput{Dashboard: record}
	unchecked := ""
	if s.panels != nil {
		checkCtx, cancel := context.WithTimeout(ctx, saveCheckBudget)
		results, err := s.panels.Run(checkCtx, panel.RunRequest{Dashboard: record.Spec})
		cancel()
		if err != nil {
			reason := panel.SafeError(err)
			if errors.Is(err, context.DeadlineExceeded) {
				reason = fmt.Sprintf("the check took longer than %d seconds", int(saveCheckBudget/time.Second))
			}
			unchecked = " Panels were not checked: " + reason + "."
		} else {
			for _, r := range results {
				switch r.Status {
				case panel.StatusEmpty:
					out.Warnings = append(out.Warnings, fmt.Sprintf("%s is empty: %s", r.ID, r.Diagnosis))
				case panel.StatusError:
					out.Warnings = append(out.Warnings, fmt.Sprintf("%s failed: %s", r.ID, r.Error))
				}
			}
		}
	}
	text := fmt.Sprintf("%s %q, version %d, with %d panels.", verb, record.Name, record.Version, len(record.Spec.Panels))
	if len(out.Warnings) > 0 {
		text += " Needs attention: " + strings.Join(out.Warnings, " ") + " Fix these with edit_dashboard or tell the user why they are empty."
	}
	return summary(text + unchecked), out, nil
}

// dashboardOwner resolves the authenticated owner for a dashboard tool call.
//
// Trust model: OAuth TokenInfo, when present, is always authoritative — the
// client-suppliable _meta owner key is never consulted alongside it, so a
// remote client cannot spoof another owner. The _meta fallback is safe only
// because ProtectMCP (internal/api/oauth.go) attaches TokenInfo to every
// HTTP-transport request, leaving the fallback reachable solely via the
// in-process transport, where the agent runtime (internal/agent/tools.go)
// injects the already-authenticated user's ID. See dashboard.OwnerMetaKey.
func dashboardOwner(req *mcp.CallToolRequest) (string, error) {
	if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil && strings.TrimSpace(req.Extra.TokenInfo.UserID) != "" {
		if !slices.Contains(req.Extra.TokenInfo.Scopes, dashboard.OAuthScope) {
			return "", errors.New("dashboard permission is required")
		}
		return req.Extra.TokenInfo.UserID, nil
	}
	if req != nil && req.Params != nil {
		if owner, ok := req.Params.Meta[dashboard.OwnerMetaKey].(string); ok && strings.TrimSpace(owner) != "" {
			return owner, nil
		}
	}
	return "", errors.New("authenticated dashboard owner is required")
}

func dashboardToolError(err error) error {
	var problems panel.Problems
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return safePanelToolError(err)
	case errors.As(err, &problems):
		return problems
	case errors.Is(err, dashboard.ErrNotFound):
		return errors.New("dashboard not found")
	case errors.Is(err, dashboard.ErrConflict):
		return errors.New("a dashboard with that name already exists")
	case errors.Is(err, dashboard.ErrStale):
		return errors.New("the dashboard changed since you read it; get_dashboard again and reapply the change")
	default:
		// MCP tool errors bypass the HTTP request logger, so this is the only
		// place the underlying storage failure gets recorded.
		slog.Error("dashboard tool operation failed", "error", err)
		return errors.New("dashboard operation failed")
	}
}
