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
	return summary(fmt.Sprintf("Created dashboard %q with %d widgets.%s", item.Name, len(item.State.Widgets), s.emptyWidgetNote(ctx, item.State))), dashboardOutput{Dashboard: item}, nil
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
	return summary(fmt.Sprintf("Updated dashboard %q with %d widgets.%s", item.Name, len(item.State.Widgets), s.emptyWidgetNote(ctx, item.State))), dashboardOutput{Dashboard: item}, nil
}

// probeLimit bounds the work a single create or update can trigger. A design
// with more filtered cards than this is reported on its first few; the point is
// to catch a dashboard built entirely out of empty cards, not to audit each one.
const probeLimit = 8

// emptyWidgetNote reports which of the new dashboard's filtered cards have
// nothing to show right now.
//
// A design is composed from what the model believes is there, and a filter it
// invents can match nothing at all — a demo dashboard shipped with "Error
// logs" and "Warnings" cards that were empty on arrival and stayed empty,
// because the service in question logs no such levels. Nothing in the tool
// result said so, so the model described them to the user as working views.
// Saying it here lets the model drop the card or explain it in the same turn.
//
// Probes never fail the call: an empty note is the honest answer when a probe
// cannot run, and a dashboard that saved correctly must not be reported as an
// error because a follow-up query failed.
func (s *Server) emptyWidgetNote(ctx context.Context, state dashboard.State) string {
	if s.queries == nil {
		return ""
	}
	scope, err := s.scope(QueryInput{Window: state.Filters.Window, Namespace: state.Filters.Namespace})
	if err != nil {
		return ""
	}
	var empty []string
	probes := 0
	for _, widget := range state.Widgets {
		if probes >= probeLimit {
			break
		}
		service := widgetConfigString(widget, "service")
		switch widget.Type {
		case "logs":
			severity, search := widgetConfigString(widget, "severity"), widgetConfigString(widget, "search")
			if service == "" && severity == "" && search == "" {
				continue
			}
			probes++
			result, probeErr := s.queries.Logs(ctx, scope, service, severity, search, 1)
			if probeErr == nil && len(result.Data.Entries) == 0 {
				empty = append(empty, widget.Title)
			}
		case "trace":
			if service == "" && widgetConfigString(widget, "trace_id") == "" {
				continue
			}
			probes++
			result, probeErr := s.queries.Trace(ctx, scope, widgetConfigString(widget, "trace_id"), service, 1)
			if probeErr == nil && len(result.Data.Spans) == 0 {
				empty = append(empty, widget.Title)
			}
		}
	}
	if len(empty) == 0 {
		return ""
	}
	return fmt.Sprintf(" No data matches these cards in the dashboard's own window, so they will render empty: %s. Tell the user, or replace them.", strings.Join(empty, ", "))
}

func widgetConfigString(widget dashboard.Widget, key string) string {
	value, ok := widget.Config[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
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
