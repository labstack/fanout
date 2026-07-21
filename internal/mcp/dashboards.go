package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type DashboardIDInput struct {
	ID string `json:"id" jsonschema:"Dashboard ID returned by dashboard_list or dashboard_create"`
}

type DashboardCreateInput struct {
	Name        string          `json:"name" jsonschema:"Short, unique dashboard name"`
	Description string          `json:"description,omitempty" jsonschema:"Concise purpose of this dashboard"`
	State       dashboard.State `json:"state" jsonschema:"Complete widget registry, 12-column layout, and shared filters"`
}

type DashboardUpdateInput struct {
	ID          string          `json:"id" jsonschema:"Dashboard ID to update"`
	Name        string          `json:"name" jsonschema:"Short, unique dashboard name"`
	Description string          `json:"description,omitempty" jsonschema:"Concise purpose of this dashboard"`
	State       dashboard.State `json:"state" jsonschema:"Complete replacement widget registry, 12-column layout, and shared filters"`
}

type dashboardListOutput struct {
	Dashboards []dashboard.Summary `json:"dashboards"`
}

type dashboardOutput struct {
	Dashboard dashboard.Dashboard `json:"dashboard"`
}

func (s *Server) registerDashboardTools() {
	if s.dashboards == nil {
		return
	}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	additive := &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
	replacement := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: boolPtr(false)}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "dashboard_list", Title: "List dashboards",
		Description: "List the authenticated user's named dashboards and widget counts before creating or changing one.", Annotations: readOnly,
	}, s.dashboardList)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "dashboard_get", Title: "Get dashboard",
		Description: "Read one named dashboard, including its widgets, filters, and 12-column layout.", Annotations: readOnly,
	}, s.dashboardGet)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "dashboard_create", Title: "Create dashboard",
		Description: "Create a complete named dashboard for the authenticated user. This is additive and does not alter existing dashboards.", Annotations: additive,
	}, s.dashboardCreate)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "dashboard_update", Title: "Replace dashboard design",
		Description: "Replace an existing dashboard's name, widgets, shared filters, and layout. Only call after the user explicitly asks to change that dashboard.", Annotations: replacement,
	}, s.dashboardUpdate)
}

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
	item, err := s.dashboards.Get(ctx, owner, strings.TrimSpace(input.ID))
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Loaded %q with %d widgets.", item.Name, len(item.State.Widgets))), dashboardOutput{Dashboard: item}, nil
}

func (s *Server) dashboardCreate(ctx context.Context, req *mcp.CallToolRequest, input DashboardCreateInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	item, err := s.dashboards.Create(ctx, owner, dashboard.CreateInput{Name: input.Name, Description: input.Description, State: input.State})
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Created dashboard %q with %d widgets.", item.Name, len(item.State.Widgets))), dashboardOutput{Dashboard: item}, nil
}

func (s *Server) dashboardUpdate(ctx context.Context, req *mcp.CallToolRequest, input DashboardUpdateInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	item, err := s.dashboards.Update(ctx, owner, strings.TrimSpace(input.ID), dashboard.UpdateInput{Name: input.Name, Description: input.Description, State: input.State})
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Updated dashboard %q with %d widgets.", item.Name, len(item.State.Widgets))), dashboardOutput{Dashboard: item}, nil
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
	switch {
	case errors.Is(err, dashboard.ErrNotFound):
		return errors.New("dashboard not found")
	case errors.Is(err, dashboard.ErrConflict):
		return errors.New("a dashboard with that name already exists")
	default:
		var validation *dashboard.ValidationError
		if errors.As(err, &validation) {
			return validation
		}
		// MCP tool errors bypass the HTTP request logger, so this is the only
		// place the underlying storage/tx failure gets recorded.
		slog.Error("dashboard tool operation failed", "error", err)
		return errors.New("dashboard operation failed")
	}
}
