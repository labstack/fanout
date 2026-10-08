package mcp

import (
	"context"
	"encoding/json"
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

type DashboardRestoreInput struct {
	ID      string `json:"id" jsonschema:"Dashboard ID"`
	Version int    `json:"version" jsonschema:"Positive stored version to restore; restoring creates a new version"`
}

type dashboardVersionsOutput struct {
	Versions []dashboard.VersionInfo `json:"versions"`
}

// dashboardToolSummary is intentionally separate from the browser summary:
// MCP dashboard scope does not grant access to private conversation provenance.
type dashboardToolSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	Version     int    `json:"version"`
	PanelCount  int    `json:"panel_count"`
	UpdatedAt   string `json:"updated_at"`
}
type dashboardListOutput struct {
	Dashboards []dashboardToolSummary `json:"dashboards"`
}

type dashboardOutput struct {
	Dashboard dashboard.Record  `json:"dashboard"`
	Warnings  []string          `json:"warnings,omitempty"`
	Receipt   *dashboardReceipt `json:"receipt,omitempty"`
}

type savedPanelCheck struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Rows      int    `json:"rows" jsonschema:"Executed rows or nonempty text content; not a measurement when status is not_run"`
	Diagnosis string `json:"diagnosis,omitempty"`
	Error     string `json:"error,omitempty"`
	ElapsedMS *int64 `json:"elapsed_ms,omitempty" jsonschema:"Execution duration in milliseconds; omitted when the panel did not run"`
}

type saveCheck struct {
	Checked   bool              `json:"checked"`
	Reason    string            `json:"reason,omitempty"`
	ElapsedMS int64             `json:"elapsed_ms"`
	Panels    []savedPanelCheck `json:"panels"`
}

type dashboardReceipt struct {
	BaseVersion     int                     `json:"base_version"`
	Version         int                     `json:"version"`
	Changes         []dashboard.PanelChange `json:"changes"`
	LayoutChanged   bool                    `json:"layout_changed"`
	DashboardFields []string                `json:"dashboard_fields,omitempty"`
	SaveCheck       saveCheck               `json:"save_check"`
}

const (
	listDashboardsTool = iota
	getDashboardTool
	createDashboardTool
	replaceDashboardTool
	editDashboardTool
	listDashboardVersionsTool
	restoreDashboardVersionTool
)

const saveReceiptGuide = "Successful saves return a receipt for that committed version: base_version, version, changes, layout_changed, dashboard_fields and save_check. Changes describe authored panel fields and position_changed; physical packing is only layout_changed. Order chips mark the minimal moved set. save_check reports checked, reason when unchecked, total elapsed_ms and one status/rows result per saved panel with elapsed_ms when executed. checked means every panel was executed, not that error or empty panels are healthy. Text rows identify nonempty content, not telemetry. Use the structured receipt rather than prose warnings as check evidence. Create receipts list added panels without layout or dashboard edit chips. No-op saves still create a new version with no change chips. Per-panel elapsed_ms is omitted when the panel did not run. "

// Registration and transport authorization use the same dashboard tool catalog.
var dashboardTools = [...]mcp.Tool{
	{
		Name: "list_dashboards", Title: "List dashboards",
		Description: "List the authenticated user's dashboards with panel counts and versions. See create_dashboard for the spec guide.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "get_dashboard", Title: "Get dashboard",
		Description: "Read one dashboard's complete spec and version. Read it before editing so operations name real panel ids. See create_dashboard for the spec guide.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "create_dashboard", Title: "Create dashboard",
		Description: "Create a dashboard for the authenticated user from a complete spec. Read get_telemetry_schema first and preview_panels until every panel is ok or deliberately empty. The result lists any panel that is still empty or failing. " + saveReceiptGuide + specGuide,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "replace_dashboard", Title: "Replace dashboard",
		Description: "Replace a dashboard's whole spec; omitted panels are removed. Use only for a redesign the user asked for; use edit_dashboard to change a few panels. " + saveReceiptGuide + "See create_dashboard for the spec guide.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "edit_dashboard", Title: "Edit dashboard",
		Description: "Change a dashboard with typed operations, applied in order and saved as one version: add_panel, update_panel (set replaces the named fields), remove_panel, move_panel, set_variable, remove_variable, set_time, rename. Panels not named are left unchanged. Only edit when the user asks to change that dashboard. " + saveReceiptGuide + "See create_dashboard for the spec guide.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "list_dashboard_versions", Title: "List dashboard versions",
		Description: "List the authenticated user's saved versions of one dashboard, newest first, with author and message. The historical version identifies a restore target; restore_dashboard_version saves it as a new version.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "restore_dashboard_version", Title: "Restore dashboard version",
		Description: "Restore a saved historical version of the authenticated user's dashboard only on an explicit user request. The input version selects the historical spec; the result's version is the newly saved version. Every successful call appends another version, even when restoring the latest spec. " + saveReceiptGuide,
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
	mcp.AddTool(s.mcp, &dashboardTools[listDashboardVersionsTool], s.dashboardVersions)
	mcp.AddTool(s.mcp, &dashboardTools[restoreDashboardVersionTool], s.dashboardRestore)
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
	summaries := make([]dashboardToolSummary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, dashboardToolSummary{ID: item.ID, Name: item.Name, Description: item.Description, IsDefault: item.IsDefault, Version: item.Version, PanelCount: item.PanelCount, UpdatedAt: item.UpdatedAt})
	}
	return summary(fmt.Sprintf("Found %d dashboards.", len(items))), dashboardListOutput{Dashboards: summaries}, nil
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
	// Strip any upstream context first: only authenticated in-process metadata
	// may supply provenance. OAuth requests never accept this client-suppliable key.
	ctx = dashboard.WithBuildOrigin(ctx, dashboard.BuildOrigin{})
	if inProcess(req) {
		if value, exists := req.Params.Meta[dashboard.BuildOriginMetaKey]; exists {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, dashboardOutput{}, errors.New("invalid dashboard build origin")
			}
			var origin dashboard.BuildOrigin
			if err := json.Unmarshal(raw, &origin); err != nil || strings.TrimSpace(origin.ThreadID) == "" || strings.TrimSpace(origin.MessageID) == "" {
				return nil, dashboardOutput{}, errors.New("invalid dashboard build origin")
			}
			ctx = dashboard.WithBuildOrigin(ctx, origin)
		}
	}
	record, err := s.dashboards.CreateWithChanges(ctx, owner, input.Dashboard, agentAuthor(owner))
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
	record, err := s.dashboards.ReplaceWithChanges(ctx, owner, strings.TrimSpace(input.ID), input.Dashboard, input.BaseVersion, agentAuthor(owner), input.Message)
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
	record, err := s.dashboards.EditWithChanges(ctx, owner, strings.TrimSpace(input.ID), input.Operations, input.BaseVersion, agentAuthor(owner), input.Message)
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Updated", record)
}

func (s *Server) dashboardVersions(ctx context.Context, req *mcp.CallToolRequest, input DashboardIDInput) (*mcp.CallToolResult, dashboardVersionsOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardVersionsOutput{}, err
	}
	versions, err := s.dashboards.Versions(ctx, owner, strings.TrimSpace(input.ID))
	if err != nil {
		return nil, dashboardVersionsOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Found %d saved versions.", len(versions))), dashboardVersionsOutput{Versions: versions}, nil
}

func (s *Server) dashboardRestore(ctx context.Context, req *mcp.CallToolRequest, input DashboardRestoreInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	if input.Version <= 0 {
		return nil, dashboardOutput{}, errors.New("version must be positive")
	}
	mutation, err := s.dashboards.RestoreWithChanges(ctx, owner, strings.TrimSpace(input.ID), input.Version, agentAuthor(owner))
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Restored", mutation)
}

// saveCheckBudget bounds the post-save run that reports empty or failing
// panels. The dashboard is already saved; a slow check is reported as
// unchecked, and the result says so, rather than holding the answer.
const saveCheckBudget = 8 * time.Second

func (s *Server) saved(ctx context.Context, verb string, mutation dashboard.Mutation) (*mcp.CallToolResult, dashboardOutput, error) {
	record := mutation.Record
	diff := dashboard.Changes(mutation.Before, record.Spec)
	out := dashboardOutput{Dashboard: record, Receipt: &dashboardReceipt{
		BaseVersion: mutation.BaseVersion, Version: record.Version, Changes: diff.Panels,
		LayoutChanged: diff.LayoutChanged, DashboardFields: diff.DashboardFields,
	}}
	start := time.Now()
	check := saveCheck{Panels: make([]savedPanelCheck, len(record.Spec.Panels))}
	for i, p := range record.Spec.Panels {
		check.Panels[i] = savedPanelCheck{ID: p.ID, Status: "not_run"}
	}
	if s.panels == nil {
		check.Reason = "panels are unavailable"
	} else {
		checkCtx, cancel := context.WithTimeout(ctx, saveCheckBudget)
		results, err := s.panels.Run(checkCtx, panel.RunRequest{Dashboard: record.Spec})
		if err == nil {
			err = checkCtx.Err()
		}
		cancel()
		if err != nil {
			check.Reason = panel.SafeError(err)
			if errors.Is(err, context.DeadlineExceeded) {
				check.Reason = fmt.Sprintf("the check took longer than %d seconds", int(saveCheckBudget/time.Second))
			}
		} else {
			byID := map[string][]panel.Result{}
			expected := map[string]bool{}
			for _, p := range record.Spec.Panels {
				expected[p.ID] = true
			}
			complete := len(results) == len(record.Spec.Panels)
			for _, r := range results {
				byID[r.ID] = append(byID[r.ID], r)
				if !expected[r.ID] {
					complete = false
				}
			}
			for i, p := range record.Spec.Panels {
				matches := byID[p.ID]
				if len(matches) != 1 {
					complete = false
					continue
				}
				r := matches[0]
				if r.Status != panel.StatusOK && r.Status != panel.StatusEmpty && r.Status != panel.StatusError {
					complete = false
					continue
				}
				c := savedPanelCheck{ID: r.ID, Status: r.Status, Diagnosis: r.Diagnosis, ElapsedMS: &r.ElapsedMS}
				if r.Frame != nil {
					c.Rows = r.Frame.Rows
				} else if r.Status == panel.StatusOK && p.Viz == "text" && strings.TrimSpace(p.Content) != "" {
					c.Rows = 1
				}
				if r.Error != "" {
					c.Error = panel.RedactPaths(r.Error)
				}
				check.Panels[i] = c
				switch r.Status {
				case panel.StatusEmpty:
					out.Warnings = append(out.Warnings, fmt.Sprintf("%s is empty: %s", r.ID, c.Diagnosis))
				case panel.StatusError:
					out.Warnings = append(out.Warnings, fmt.Sprintf("%s failed: %s", r.ID, c.Error))
				}
			}
			check.Checked = complete
			if !complete {
				check.Reason = "the check did not return exactly one completed result for every saved panel"
			}
		}
	}
	check.ElapsedMS = time.Since(start).Milliseconds()
	out.Receipt.SaveCheck = check
	text := fmt.Sprintf("%s %q, version %d, with %d panels.", verb, record.Name, record.Version, len(record.Spec.Panels))
	if len(out.Warnings) > 0 {
		text += " Needs attention: " + strings.Join(out.Warnings, " ") + " Fix these with edit_dashboard or tell the user why they are empty."
	}
	if !check.Checked {
		text += " Panels were not checked: " + check.Reason + "."
	}
	return summary(text), out, nil
}

// inProcess identifies calls without a transport-authenticated OAuth identity.
func inProcess(req *mcp.CallToolRequest) bool {
	return req == nil || req.Extra == nil || req.Extra.TokenInfo == nil
}

// dashboardOwner resolves the authenticated owner. OAuth TokenInfo, when present, is always authoritative — the
// client-suppliable _meta owner key is never consulted alongside it, so a
// remote client cannot spoof another owner. The _meta fallback is safe only
// because ProtectMCP and ProtectBrowserMCP (internal/api/oauth.go) attach TokenInfo to every
// HTTP-transport request, leaving the fallback reachable solely via the
// in-process transport, where the agent runtime (internal/agent/tools.go)
// injects the already-authenticated user's ID. See dashboard.OwnerMetaKey.
func dashboardOwner(req *mcp.CallToolRequest) (string, error) {
	if !inProcess(req) {
		if strings.TrimSpace(req.Extra.TokenInfo.UserID) == "" {
			return "", errors.New("authenticated dashboard owner is required")
		}
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
