package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type restoreConflictValidator struct {
	armed   atomic.Bool
	attempt atomic.Int32
	entered chan int
	release chan struct{}
}

func (v *restoreConflictValidator) Validate(ctx context.Context, d *panel.Dashboard) error {
	if v.armed.Load() && d.Panels[0].Content == "hello" {
		attempt := int(v.attempt.Add(1))
		if attempt <= 2 {
			select {
			case v.entered <- attempt:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case <-v.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return (structural{}).Validate(ctx, d)
}

func TestRestoreLosesTwoContestedBasesWithoutReceipt(t *testing.T) {
	for _, transport := range []string{"service", "mcp"} {
		t.Run(transport, func(t *testing.T) {
			v := &restoreConflictValidator{entered: make(chan int), release: make(chan struct{})}
			s := newToolServer(t, v, nil)
			_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Two conflicts")})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.dashboards.Edit(t.Context(), "owner", created.Dashboard.ID, []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "latest"}}}, 1, agentAuthor("owner"), "")
			if err != nil {
				t.Fatal(err)
			}
			v.armed.Store(true)
			type answer struct {
				receipt *dashboardReceipt
				err     error
			}
			done := make(chan answer, 1)
			go func() {
				if transport == "service" {
					m, err := s.dashboards.RestoreWithChanges(t.Context(), "owner", created.Dashboard.ID, 1, agentAuthor("owner"))
					if m.Record.ID != "" {
						t.Error("losing restore returned a record")
					}
					done <- answer{err: err}
					return
				}
				_, out, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: 1})
				done <- answer{out.Receipt, err}
			}()
			for attempt := 1; attempt <= 2; attempt++ {
				select {
				case actual := <-v.entered:
					if actual != attempt {
						t.Fatal(actual)
					}
				case early := <-done:
					t.Fatalf("restore stopped before retry %d: %+v", attempt, early)
				case <-t.Context().Done():
					t.Fatal(t.Context().Err())
				}
				results := make(chan error, 2)
				for writer := 0; writer < 2; writer++ {
					go func() {
						_, err := s.dashboards.Edit(t.Context(), "owner", created.Dashboard.ID, []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": fmt.Sprintf("writer %d/%d", attempt, writer)}}}, attempt+1, agentAuthor("owner"), "")
						results <- err
					}()
				}
				wins := 0
				for range 2 {
					err := <-results
					if err == nil {
						wins++
					} else if !errors.Is(err, dashboard.ErrStale) {
						t.Fatal(err)
					}
				}
				if wins != 1 {
					t.Fatalf("base %d winners=%d", attempt+1, wins)
				}
				v.release <- struct{}{}
			}
			result := <-done
			if result.receipt != nil || result.err == nil || transport == "service" && !errors.Is(result.err, dashboard.ErrStale) || transport == "mcp" && !strings.Contains(result.err.Error(), "the dashboard changed since you read it") {
				t.Fatalf("restore=%+v", result)
			}
			versions, err := s.dashboards.Versions(t.Context(), "owner", created.Dashboard.ID)
			if err != nil || len(versions) != 4 || versions[0].Version != 4 {
				t.Fatal(versions, err)
			}
		})
	}
}

func TestDashboardHistoryToolsAppendRestoreVersion(t *testing.T) {
	s := newPanelServer(t)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: panel.Dashboard{Name: "History", Panels: []panel.Panel{{ID: "note", Title: "Note", Viz: "text", Content: "first"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.dashboards.Replace(t.Context(), "owner", created.Dashboard.ID, created.Dashboard.Spec, 1, agentAuthor("owner"), "Saved again"); err != nil {
		t.Fatal(err)
	}
	_, restored, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: 1})
	if err != nil || restored.Dashboard.Version != 3 {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	_, history, err := s.dashboardVersions(t.Context(), ownerRequest(), DashboardIDInput{ID: created.Dashboard.ID})
	if err != nil || len(history.Versions) != 3 || history.Versions[0].AuthorKind != "agent" || history.Versions[0].Message != "Restored version 1" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	for _, name := range []string{"list_dashboard_versions", "restore_dashboard_version"} {
		if RequiredToolScope(name) == "" {
			t.Fatalf("missing owner scope: %s", name)
		}
	}
	if restored.Receipt == nil || restored.Receipt.BaseVersion != 2 || restored.Receipt.Version != 3 || len(restored.Receipt.Changes) != 0 || restored.Receipt.LayoutChanged || len(restored.Receipt.DashboardFields) != 0 || !restored.Receipt.SaveCheck.Checked || restored.Receipt.SaveCheck.Panels[0].Rows != 1 {
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
	// The current version is rejected. A historical version with identical
	// contents still appends a version on each successful restore.
	result, rejected, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: 3})
	if err != nil || !result.IsError || rejected.ErrorCode != "already_current" || rejected.Receipt != nil || rejected.Dashboard != nil {
		t.Fatalf("current restore=%+v err=%v", rejected, err)
	}
	for range 2 {
		target := 1
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
	if _, err := s.dashboards.Replace(t.Context(), "owner", created.Dashboard.ID, created.Dashboard.Spec, 1, agentAuthor("owner"), "Saved again"); err != nil {
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
	if err != nil || current.Dashboard.Version != 3 {
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
		result, _, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: version})
		if err == nil && (result == nil || !result.IsError) {
			t.Fatalf("restored invalid version %d", version)
		}
	}
	if _, _, err := s.dashboardVersions(t.Context(), ownerRequest(), DashboardIDInput{ID: "missing"}); err == nil || err.Error() != "dashboard not found" {
		t.Fatalf("missing history=%v", err)
	}
	if _, _, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: "missing", Version: 1}); err == nil || err.Error() != "dashboard not found" {
		t.Fatalf("missing dashboard=%v", err)
	}
	if _, err := s.dashboards.Replace(t.Context(), "owner", created.Dashboard.ID, created.Dashboard.Spec, 1, agentAuthor("owner"), "Saved again"); err != nil {
		t.Fatal(err)
	}
	for version := 1; version < 100; version++ {
		if _, err := s.dashboards.Restore(t.Context(), "owner", created.Dashboard.ID, version, dashboard.Author{Kind: "user", ID: "owner"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.dashboards.Restore(t.Context(), "owner", created.Dashboard.ID, 1, dashboard.Author{Kind: "agent", ID: "owner"}); err == nil || err.Error() != fmt.Sprintf("version 1 of dashboard %s does not exist", created.Dashboard.ID) {
		t.Fatalf("pruned version error=%v", err)
	}
	result, out, err := s.dashboardRestore(t.Context(), ownerRequest(), DashboardRestoreInput{ID: created.Dashboard.ID, Version: 1})
	if err != nil || !result.IsError || out.Error != fmt.Sprintf("version 1 of dashboard %s does not exist", created.Dashboard.ID) || out.ErrorCode != "dashboard_version_not_found" || out.Dashboard != nil || out.Receipt != nil {
		t.Fatalf("pruned version result=%+v output=%+v err=%v", result, out, err)
	}
	_, history, err := s.dashboardVersions(t.Context(), ownerRequest(), DashboardIDInput{ID: created.Dashboard.ID})
	if err != nil || len(history.Versions) != 100 || history.Versions[0].Version != 101 || history.Versions[99].Version != 2 {
		t.Fatalf("history=%+v %v", history, err)
	}
}

func TestMissingDashboardVersionHasDistinctErrorCode(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Missing version")})
	if err != nil {
		t.Fatal(err)
	}
	session := connectTestClient(t, s, nil)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "restore_dashboard_version", Arguments: map[string]any{"id": created.Dashboard.ID, "version": 9}, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
	if err != nil || !result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload["error"] != fmt.Sprintf("version 9 of dashboard %s does not exist", created.Dashboard.ID) || payload["error_code"] != "dashboard_version_not_found" || payload["receipt"] != nil || payload["dashboard"] != nil {
		t.Fatalf("missing version payload=%s", raw)
	}
}

func TestCurrentDashboardVersionRestoreHasDistinctErrorCodeWithoutSave(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Current version")})
	if err != nil {
		t.Fatal(err)
	}
	session := connectTestClient(t, s, nil)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "restore_dashboard_version", Arguments: map[string]any{"id": created.Dashboard.ID, "version": 1}, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
	if err != nil || !result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload["error_code"] != "already_current" || payload["error"] != dashboard.ErrAlreadyCurrent.Error() || payload["receipt"] != nil || payload["dashboard"] != nil {
		t.Fatalf("current version payload=%s", raw)
	}
	versions, err := s.dashboards.Versions(t.Context(), "owner", created.Dashboard.ID)
	if err != nil || len(versions) != 1 || versions[0].Version != 1 {
		t.Fatal(versions, err)
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
	if _, err := s.dashboards.Replace(t.Context(), "owner", created.Dashboard.ID, created.Dashboard.Spec, 1, agentAuthor("owner"), "Saved again"); err != nil {
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
