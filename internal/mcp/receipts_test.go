package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type receiptExecutor struct {
	panelExecutor
	run func(context.Context, panel.RunRequest) ([]panel.Result, error)
}

func (e receiptExecutor) Run(ctx context.Context, req panel.RunRequest) ([]panel.Result, error) {
	return e.run(ctx, req)
}

func TestSaveCheckFailuresAreStructuredAndDoNotRetrySave(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []panel.Result
		err     error
		checked bool
	}{
		{name: "missing"},
		{name: "duplicate", results: []panel.Result{{ID: "notes", Status: "ok"}, {ID: "notes", Status: "ok"}}},
		{name: "unexpected", results: []panel.Result{{ID: "other", Status: "ok"}}},
		{name: "not_run", results: []panel.Result{{ID: "notes", Status: "not_run"}}},
		{name: "engine", err: errors.New("cannot open /private/data/telemetry.parquet")},
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "failing_panel", results: []panel.Result{{ID: "notes", Status: "error", Error: "cannot read /private/data/file", ElapsedMS: 17}}, checked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newToolServer(t, structural{}, nil)
			s.panels = receiptExecutor{run: func(ctx context.Context, _ panel.RunRequest) ([]panel.Result, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > saveCheckBudget {
					t.Error("missing eight-second budget")
				}
				return tc.results, tc.err
			}}
			result, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Failure")})
			if err != nil || out.Dashboard.Version != 1 || out.Receipt == nil {
				t.Fatalf("save=%+v err=%v", out, err)
			}
			check := out.Receipt.SaveCheck
			if check.Checked != tc.checked || len(check.Panels) != 1 || check.ElapsedMS < 0 {
				t.Fatal(check)
			}
			if !tc.checked && (check.Reason == "" || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "Panels were not checked:")) {
				t.Fatal(check)
			}
			if strings.Contains(check.Reason, "/private/") || strings.Contains(check.Panels[0].Error, "/private/") {
				t.Fatal("private path in receipt")
			}
			if tc.name == "failing_panel" && (check.Panels[0].Status != "error" || check.Panels[0].ElapsedMS != 17 || len(out.Warnings) != 1) {
				t.Fatal(out)
			}
			versions, err := s.dashboards.Versions(t.Context(), "owner", out.Dashboard.ID)
			if err != nil || len(versions) != 1 {
				t.Fatalf("versions=%+v err=%v", versions, err)
			}
		})
	}
	t.Run("nil_executor", func(t *testing.T) {
		s := newToolServer(t, structural{}, nil)
		_, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Nil")})
		if err != nil || out.Receipt.SaveCheck.Checked || out.Receipt.SaveCheck.Reason == "" {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	})
}

func TestSaveCheckMatchesEverySavedIDRatherThanResultOrder(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "reordered", true: "duplicate"}[duplicate], func(t *testing.T) {
			s := newToolServer(t, structural{}, nil)
			s.panels = receiptExecutor{run: func(context.Context, panel.RunRequest) ([]panel.Result, error) {
				id := "notes"
				if duplicate {
					id = "b"
				}
				return []panel.Result{{ID: "b", Status: "empty", Diagnosis: "No spans match.", ElapsedMS: 19}, {ID: id, Status: "ok", ElapsedMS: 31}}, nil
			}}
			spec := textDashboard("IDs")
			spec.Panels = append(spec.Panels, panel.Panel{ID: "b", Title: "B", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}})
			_, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
			if err != nil {
				t.Fatal(err)
			}
			check := out.Receipt.SaveCheck
			if check.Checked == duplicate || check.Panels[0].ID != "notes" || check.Panels[1].ID != "b" {
				t.Fatal(check)
			}
			if !duplicate && (check.Panels[0].ElapsedMS != 31 || check.Panels[1].ElapsedMS != 19 || check.Panels[1].Rows != 0 || check.Panels[1].Status != "empty") {
				t.Fatal(check)
			}
			if duplicate && (check.Panels[0].Status != "not_run" || check.Panels[1].Status != "not_run" || check.Reason == "") {
				t.Fatal(check)
			}
		})
	}
}

func TestSaveCheckDoesNotAcceptResultsAfterCancellation(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	mutation, err := s.dashboards.CreateWithChanges(t.Context(), "owner", textDashboard("Cancelled check"), dashboard.Author{Kind: "agent", ID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.panels = receiptExecutor{run: func(context.Context, panel.RunRequest) ([]panel.Result, error) {
		cancel()
		return []panel.Result{{ID: "notes", Status: "ok"}}, nil
	}}
	_, out, err := s.saved(ctx, "Created", mutation)
	if err != nil || out.Receipt.SaveCheck.Checked || out.Receipt.SaveCheck.Reason == "" || out.Dashboard.Version != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestFailedMutationsReturnNoReceiptOrVersion(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Valid")})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []DashboardEditInput{
		{ID: created.Dashboard.ID, BaseVersion: 1, Operations: []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"viz": "unknown"}}}},
		{ID: created.Dashboard.ID, BaseVersion: 2, Operations: []dashboard.Operation{{Op: "rename", Name: "Stale"}}},
	} {
		_, out, err := s.dashboardEdit(t.Context(), ownerRequest(), input)
		if err == nil || out.Receipt != nil || out.Dashboard.ID != "" {
			t.Fatalf("failed save=%+v err=%v", out, err)
		}
	}
	versions, err := s.dashboards.Versions(t.Context(), "owner", created.Dashboard.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	bad := textDashboard("Invalid")
	bad.Panels[0].Viz = "unknown"
	_, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: bad})
	if err == nil || out.Receipt != nil || out.Dashboard.ID != "" {
		t.Fatalf("failed create=%+v err=%v", out, err)
	}
}

func TestConcurrentSaveReceiptIdentifiesItsOwnCommittedVersion(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Interleaved")})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.panels = receiptExecutor{run: func(ctx context.Context, req panel.RunRequest) ([]panel.Result, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if req.Dashboard.Panels[0].Content != "first" {
			t.Error("checked a later writer's spec")
		}
		return []panel.Result{{ID: "notes", Status: "ok", ElapsedMS: 23}}, nil
	}}
	type answer struct {
		out dashboardOutput
		err error
	}
	done := make(chan answer, 1)
	go func() {
		_, out, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: created.Dashboard.ID, BaseVersion: 1, Operations: []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "first"}}}})
		done <- answer{out, err}
	}()
	<-entered
	later, err := s.dashboards.Edit(t.Context(), "owner", created.Dashboard.ID, []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"title": "Later", "content": "second"}}}, 2, dashboard.Author{Kind: "user", ID: "owner"}, "")
	close(release)
	if err != nil || later.Version != 3 {
		t.Fatalf("later=%+v err=%v", later, err)
	}
	first := <-done
	r := first.out.Receipt
	if first.err != nil || first.out.Dashboard.Version != 2 || first.out.Dashboard.Spec.Panels[0].Content != "first" || r.BaseVersion != 1 || r.Version != 2 || !r.SaveCheck.Checked || r.SaveCheck.Panels[0].ElapsedMS != 23 || len(r.Changes) != 1 || len(r.Changes[0].Fields) != 1 || r.Changes[0].Fields[0] != "content" {
		t.Fatalf("first=%+v receipt=%+v", first, r)
	}
}

func TestRemoveReceiptPacksNeighborsWithoutAuthoredChips(t *testing.T) {
	s := newPanelServer(t)
	spec := textDashboard("Packing")
	spec.Panels = append(spec.Panels, panel.Panel{ID: "b", Title: "B", Viz: "text", Content: "b"}, panel.Panel{ID: "c", Title: "C", Viz: "text", Content: "c"})
	for i := range spec.Panels {
		spec.Panels[i].Width = 12
	}
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
	if err != nil {
		t.Fatal(err)
	}
	_, removed, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: created.Dashboard.ID, BaseVersion: 1, Operations: []dashboard.Operation{{Op: "remove_panel", ID: "notes"}}})
	if err != nil {
		t.Fatal(err)
	}
	r := removed.Receipt
	if r.BaseVersion != 1 || r.Version != 2 || !r.LayoutChanged || len(r.Changes) != 1 || r.Changes[0].PanelID != "notes" || r.Changes[0].Kind != "removed" || len(r.Changes[0].Fields) != 0 || !r.SaveCheck.Checked || len(r.SaveCheck.Panels) != 2 || *created.Dashboard.Spec.Panels[1].Grid == *removed.Dashboard.Spec.Panels[0].Grid {
		t.Fatalf("receipt=%+v", r)
	}
}

func TestSaveReceiptsCheckCommittedPanelsAndAuthoredChanges(t *testing.T) {
	s := newPanelServer(t)
	spec := panel.Dashboard{Name: "Receipt", Panels: []panel.Panel{
		{ID: "notes", Title: "Notes", Viz: "text", Content: "Deliberately empty spans below."},
		{ID: "requests", Title: "Requests", Description: "Deliberately empty on this source.", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}},
	}}
	_, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
	if err != nil {
		t.Fatal(err)
	}
	r := out.Receipt
	if r == nil || r.BaseVersion != 0 || r.Version != out.Dashboard.Version || !r.SaveCheck.Checked || r.SaveCheck.ElapsedMS < 0 || len(r.SaveCheck.Panels) != 2 {
		t.Fatalf("receipt=%+v", r)
	}
	for i, p := range r.SaveCheck.Panels {
		if p.ID != out.Dashboard.Spec.Panels[i].ID || p.ElapsedMS < 0 {
			t.Fatal(p)
		}
	}
	if r.SaveCheck.Panels[0].Rows != 1 || r.SaveCheck.Panels[0].Status != "ok" || r.SaveCheck.Panels[1].Status != "empty" || r.SaveCheck.Panels[1].Diagnosis == "" {
		t.Fatal(r)
	}
	_, edited, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: out.Dashboard.ID, BaseVersion: 1, Operations: []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "Changed note."}}}})
	if err != nil {
		t.Fatal(err)
	}
	r = edited.Receipt
	if r.BaseVersion != 1 || r.Version != 2 || r.LayoutChanged || len(r.Changes) != 1 || r.Changes[0].PanelID != "notes" || len(r.Changes[0].Fields) != 1 || r.Changes[0].Fields[0] != "content" {
		t.Fatalf("edit receipt=%+v", r)
	}
	_, read, err := s.dashboardGet(t.Context(), ownerRequest(), DashboardIDInput{ID: out.Dashboard.ID})
	if err != nil || read.Receipt != nil {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	_, preview, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: spec.Panels})
	if err != nil || preview.ElapsedMS < 0 || len(preview.Panels) != 2 || preview.Panels[0].ElapsedMS < 0 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}
