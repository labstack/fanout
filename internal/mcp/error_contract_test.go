package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/toolerror"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolFailuresCarryMatchingErrorObjectsInBothContentPaths(t *testing.T) {
	s := newPanelServer(t)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "fail_tool"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return nil, struct{}{}, errors.New("The tool could not complete.")
	})
	client := connectTestClient(t, s, nil)
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "fail_tool", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatal(result, err)
	}
	if result.StructuredContent != nil {
		t.Fatalf("structured content would break the tool's output schema: %v", result.StructuredContent)
	}
	if len(result.Content) != 1 {
		t.Fatal(result.Content)
	}
	raw := []byte(result.Content[0].(*mcp.TextContent).Text)
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "tool_failed" || body.Error.Message != "The tool could not complete." {
		t.Fatal(string(raw))
	}
}

func TestUnknownToolsKeepJSONRPCFailures(t *testing.T) {
	s := newPanelServer(t)
	client := connectTestClient(t, s, nil)
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "missing_tool", Arguments: map[string]any{}})
	if err == nil || result != nil {
		t.Fatal(result, err)
	}
}

type dashboardValidationFailure struct{ err error }

func (v *dashboardValidationFailure) Validate(ctx context.Context, spec *panel.Dashboard) error {
	if v.err != nil {
		return v.err
	}
	return (structural{}).Validate(ctx, spec)
}

func TestDashboardValidationProblemsUseInvalidSpecThroughMiddleware(t *testing.T) {
	v := &dashboardValidationFailure{}
	s := newToolServer(t, v, nil)
	spec := textDashboard("Saved")
	created, err := s.dashboards.Create(t.Context(), "owner", spec, agentAuthor("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.dashboards.Replace(t.Context(), "owner", created.ID, spec, 1, agentAuthor("owner"), ""); err != nil {
		t.Fatal(err)
	}
	problems := panel.Problems{{Path: "panels[0].query.sort", Message: "Binder Error: Unknown sort column missing", Hint: "Choose a known column"}, {Path: "name", Message: "Name required"}}
	v.err = problems
	session := connectTestClient(t, s, nil)
	for _, tc := range []struct {
		name string
		args any
	}{
		{"create_dashboard", DashboardCreateInput{Dashboard: spec}},
		{"replace_dashboard", DashboardReplaceInput{ID: created.ID, Dashboard: spec}},
		{"edit_dashboard", DashboardEditInput{ID: created.ID, Operations: []dashboard.Operation{{Op: "rename", Name: "Renamed"}}}},
		{"restore_dashboard_version", DashboardRestoreInput{ID: created.ID, Version: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
			if err != nil || !result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			for _, content := range []any{json.RawMessage(result.Content[0].(*mcp.TextContent).Text), result.StructuredContent} {
				raw, err := json.Marshal(content)
				var failure dashboardOutput
				if err != nil || json.Unmarshal(raw, &failure) != nil || failure.Error == nil || failure.Error.Code != "invalid_spec" || failure.Error.Message != panel.PresentProblems(problems).Error() || !reflect.DeepEqual(failure.Problems, panel.PresentProblems(problems)) {
					t.Fatalf("failure=%s err=%v", raw, err)
				}
			}
		})
	}
}

func TestOtherDashboardFailuresKeepTheirCodesThroughMiddleware(t *testing.T) {
	v := &dashboardValidationFailure{}
	s := newToolServer(t, v, nil)
	spec := textDashboard("Saved")
	created, err := s.dashboards.Create(t.Context(), "owner", spec, agentAuthor("owner"))
	if err != nil {
		t.Fatal(err)
	}
	session := connectTestClient(t, s, nil)
	for _, tc := range []struct {
		name, code, message string
		args                any
		cause               error
	}{
		{"create_dashboard", "tool_failed", "a dashboard with that name already exists", DashboardCreateInput{Dashboard: spec}, nil},
		{"replace_dashboard", "dashboard_version_conflict", "the dashboard changed since you read it; open it again and reapply the change", DashboardReplaceInput{ID: created.ID, Dashboard: spec, BaseVersion: 2}, nil},
		{"edit_dashboard", "dashboard_version_conflict", "the dashboard changed since you read it; open it again and reapply the change", DashboardEditInput{ID: created.ID, BaseVersion: 2, Operations: []dashboard.Operation{{Op: "rename", Name: "Renamed"}}}, nil},
		{"edit_dashboard", "tool_failed", "dashboard not found", DashboardEditInput{ID: "missing", Operations: []dashboard.Operation{{Op: "rename", Name: "Renamed"}}}, nil},
		{"create_dashboard", "tool_failed", panel.ReadFailure, DashboardCreateInput{Dashboard: textDashboard("Unavailable")}, errors.New("open /srv/private/control.sqlite: failed")},
	} {
		t.Run(tc.message, func(t *testing.T) {
			v.err = tc.cause
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
			if err != nil || !result.IsError || (result.StructuredContent != nil) != (tc.code == "dashboard_version_conflict") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var failure toolerror.Result
			if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &failure); err != nil || failure.Error == nil || failure.Error.Code != tc.code || failure.Error.Message != tc.message {
				t.Fatalf("failure=%+v err=%v", failure.Error, err)
			}
		})
	}
}
