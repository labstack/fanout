package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDashboardHistoryToolsAppendRestoreVersion(t *testing.T) {
	s := newPanelServer(t)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: panel.Dashboard{Name: "History", Panels: []panel.Panel{{ID: "note", Title: "Note", Viz: "text", Content: "first"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, restored, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: 1})
	if err != nil || restored.Dashboard.Version != 2 {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	_, history, err := s.dashboardVersions(t.Context(), ownerRequest(), DashboardIDInput{ID: created.Dashboard.ID})
	if err != nil || len(history.Versions) != 2 || history.Versions[0].AuthorKind != "agent" || history.Versions[0].Message != "Restored version 1" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	for _, name := range []string{"list_dashboard_versions", "restore_dashboard_version"} {
		if RequiredToolScope(name) == "" {
			t.Fatalf("missing owner scope: %s", name)
		}
	}
	if restored.Receipt == nil || restored.Receipt.BaseVersion != 1 || restored.Receipt.Version != 2 || len(restored.Receipt.Changes) != 0 || restored.Receipt.LayoutChanged || len(restored.Receipt.DashboardFields) != 0 || !restored.Receipt.SaveCheck.Checked || restored.Receipt.SaveCheck.Panels[0].Rows != 1 {
		t.Fatalf("no-op restore receipt=%+v", restored.Receipt)
	}
}

func TestDashboardRestoreReceiptUsesLatestCommittedBase(t *testing.T) {
	s := newToolServer(t, structural{}, panel.NewExecutor(nil, 30))
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("History")})
	if err != nil {
		t.Fatal(err)
	}
	_, edited, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: created.Dashboard.ID, Operations: []dashboard.Operation{
		{Op: "rename", Name: "Changed"},
		{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "second"}},
	}, BaseVersion: 1, Message: "Change text"})
	if err != nil {
		t.Fatal(err)
	}
	result, restored, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: " " + created.Dashboard.ID + " ", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	diff := dashboard.Changes(edited.Dashboard.Spec, restored.Dashboard.Spec)
	if restored.Dashboard.Version != 3 || !reflect.DeepEqual(restored.Dashboard.Spec, created.Dashboard.Spec) || restored.Receipt.BaseVersion != 2 || restored.Receipt.Version != 3 || !reflect.DeepEqual(restored.Receipt.Changes, diff.Panels) || restored.Receipt.LayoutChanged != diff.LayoutChanged || !reflect.DeepEqual(restored.Receipt.DashboardFields, diff.DashboardFields) || len(diff.Panels) != 1 || !restored.Receipt.SaveCheck.Checked {
		t.Fatalf("restore=%+v diff=%+v", restored, diff)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, `Restored "History", version 3`) {
		t.Fatal(result.Content)
	}
	// Every success appends a version, including restore-to-latest and repeats.
	for _, target := range []int{3, 1} {
		_, next, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: target})
		if err != nil || next.Dashboard.Version != restored.Dashboard.Version+1 || next.Receipt.BaseVersion != restored.Dashboard.Version || len(next.Receipt.Changes) != 0 {
			t.Fatalf("repeated restore=%+v err=%v", next, err)
		}
		restored = next
	}
	_, history, err := s.dashboardVersions(t.Context(), ownerRequest(), DashboardIDInput{ID: " " + created.Dashboard.ID + " "})
	if err != nil || len(history.Versions) != 5 || history.Versions[0].Version != 5 || history.Versions[0].AuthorID != "owner" || history.Versions[0].AuthorKind != "agent" || history.Versions[0].Message != "Restored version 1" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestDashboardHistoryOAuthIdentityAndOwnerScope(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Private")})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, owner, meta string
		scopes            []string
		want              string
	}{
		{"authenticated_owner_overrides_spoof", "owner", "other", []string{"telemetry:read", dashboard.OAuthScope}, ""},
		{"other_owner_cannot_spoof", "other", "owner", []string{"telemetry:read", dashboard.OAuthScope}, "dashboard not found"},
		{"telemetry_only", "owner", "owner", []string{"telemetry:read"}, "dashboard permission is required"},
		{"missing_identity", "", "owner", []string{dashboard.OAuthScope}, "authenticated dashboard owner is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := requestFor(test.meta)
			req.Extra = &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: test.owner, Scopes: test.scopes}}
			_, _, listErr := s.dashboardVersions(t.Context(), req, DashboardIDInput{ID: created.Dashboard.ID})
			_, _, restoreErr := s.dashboardRestore(t.Context(), req, DashboardRestoreInput{ID: created.Dashboard.ID, Version: 1})
			for name, err := range map[string]error{"list": listErr, "restore": restoreErr} {
				if test.want == "" && err != nil || test.want != "" && (err == nil || err.Error() != test.want) {
					t.Errorf("%s error=%v want=%q", name, err, test.want)
				}
			}
		})
	}
	for _, req := range []*mcp.CallToolRequest{nil, requestFor(""), requestFor("other")} {
		if _, _, err := s.dashboardVersions(t.Context(), req, DashboardIDInput{ID: created.Dashboard.ID}); err == nil {
			t.Fatal("unowned history allowed")
		}
		if _, _, err := s.dashboardRestore(t.Context(), req, DashboardRestoreInput{ID: created.Dashboard.ID, Version: 1}); err == nil {
			t.Fatal("unowned restore allowed")
		}
	}
	_, current, err := s.dashboardGet(t.Context(), ownerRequest(), DashboardIDInput{ID: created.Dashboard.ID})
	if err != nil || current.Dashboard.Version != 2 {
		t.Fatalf("unauthorized restore changed version: %+v %v", current, err)
	}
}

func TestDashboardHistoryRejectsInvalidMissingAndPrunedVersions(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Pruned")})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{-1, 0, 2} {
		if _, _, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: version}); err == nil {
			t.Fatalf("restored invalid version %d", version)
		}
	}
	if _, _, err := s.dashboardVersions(t.Context(), ownerRequest(), DashboardIDInput{ID: "missing"}); err == nil || err.Error() != "dashboard not found" {
		t.Fatalf("missing history=%v", err)
	}
	if _, _, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: "missing", Version: 1}); err == nil || err.Error() != "dashboard not found" {
		t.Fatalf("missing dashboard=%v", err)
	}
	for version := 1; version <= 100; version++ {
		if _, err := s.dashboards.Restore(t.Context(), "owner", created.Dashboard.ID, version, dashboard.Author{Kind: "user", ID: "owner"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: 1}); err == nil || err.Error() != "dashboard not found" {
		t.Fatalf("pruned restore=%v", err)
	}
	_, history, err := s.dashboardVersions(t.Context(), ownerRequest(), DashboardIDInput{ID: created.Dashboard.ID})
	if err != nil || len(history.Versions) != 100 || history.Versions[0].Version != 101 || history.Versions[99].Version != 2 {
		t.Fatalf("history=%+v %v", history, err)
	}
}

func TestDashboardHistoryToolsExposeOwnerScopedContracts(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	session := connectTestClient(t, s, nil)
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range listed.Tools {
		if tool.Name != "list_dashboard_versions" && tool.Name != "restore_dashboard_version" {
			continue
		}
		found[tool.Name] = true
		if RequiredToolScope(tool.Name) != dashboard.OAuthScope || tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("owner-scoped contract: %+v", tool)
		}
		if tool.Name == "list_dashboard_versions" && !tool.Annotations.ReadOnlyHint {
			t.Fatal("history is not read-only")
		}
		if tool.Name == "restore_dashboard_version" && (tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || tool.Annotations.IdempotentHint) {
			t.Fatal("restore mutation hints incorrect")
		}
		raw, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(raw), "owner") {
			t.Fatalf("user-supplied owner: %s", raw)
		}
	}
	if len(found) != 2 {
		t.Fatal("history tools missing", found)
	}
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Contract")})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_dashboard_versions", "restore_dashboard_version"} {
		args := map[string]any{"id": created.Dashboard.ID}
		if name == "restore_dashboard_version" {
			args["version"] = 1
		}
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
		if err != nil || result.IsError || result.StructuredContent == nil {
			t.Fatalf("%s result=%+v err=%v", name, result, err)
		}
	}
	// An omitted target must not silently default to latest.
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "restore_dashboard_version", Arguments: map[string]any{"id": created.Dashboard.ID}, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
	if err == nil && !result.IsError {
		t.Fatal("omitted version accepted")
	}
}
