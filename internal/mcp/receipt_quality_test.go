package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
)

func parallelTimingExecutor() receiptExecutor {
	return receiptExecutor{run: func(_ context.Context, req panel.RunRequest) ([]panel.Result, error) {
		results := make([]panel.Result, len(req.Dashboard.Panels))
		var wg sync.WaitGroup
		for i, p := range req.Dashboard.Panels {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				time.Sleep(30 * time.Millisecond)
				results[i] = panel.Result{ID: p.ID, Status: panel.StatusOK, ElapsedMS: time.Since(start).Milliseconds()}
			}()
		}
		wg.Wait()
		return results, nil
	}}
}

func TestSaveAndPreviewTotalsMeasureParallelWallTime(t *testing.T) {
	s := newToolServer(t, structural{}, parallelTimingExecutor())
	spec := textDashboard("Parallel timings")
	for _, id := range []string{"b", "c", "d"} {
		spec.Panels = append(spec.Panels, panel.Panel{ID: id, Title: id, Viz: "text", Content: id})
	}
	_, saved, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
	if err != nil {
		t.Fatal(err)
	}
	_, preview, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: spec.Panels})
	if err != nil {
		t.Fatal(err)
	}
	// Read serialized fields so the assertion also covers executed-only pointers.
	for _, output := range []struct {
		name  string
		value any
	}{{"save_check", saved.Receipt.SaveCheck}, {"preview", preview}} {
		t.Run(output.name, func(t *testing.T) {
			raw, _ := json.Marshal(output.value)
			var value struct {
				Elapsed int64 `json:"elapsed_ms"`
				Panels  []struct {
					Elapsed int64 `json:"elapsed_ms"`
				} `json:"panels"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			var largest, sum int64
			for _, p := range value.Panels {
				largest = max(largest, p.Elapsed)
				sum += p.Elapsed
			}
			if largest < 30 || value.Elapsed < largest || value.Elapsed >= sum {
				t.Fatalf("aggregate %d must include longest %d but be below parallel sum %d", value.Elapsed, largest, sum)
			}
		})
	}
}

func TestUnexecutedPanelRowsOmitTimingMeasurements(t *testing.T) {
	s := newPanelServer(t)
	_, preview, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: []panel.Panel{
		{ID: "valid", Title: "Valid", Viz: "text", Content: "hello"},
		{ID: "invalid", Title: "Invalid", Viz: "unknown"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.panels = nil
	_, saved, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Unchecked")})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []any{preview, saved.Receipt.SaveCheck} {
		raw, _ := json.Marshal(output)
		var value struct {
			Panels []map[string]any `json:"panels"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		for _, p := range value.Panels {
			if _, ok := p["elapsed_ms"]; ok {
				t.Errorf("unexecuted %v serializes a timing: %s", p["status"], raw)
			}
		}
	}
}

func TestSavedPanelErrorsAreRedactedWithoutLoggingAgain(t *testing.T) {
	s := newToolServer(t, structural{}, receiptExecutor{run: func(context.Context, panel.RunRequest) ([]panel.Result, error) {
		return []panel.Result{{ID: "notes", Status: panel.StatusError, Error: "cannot read /private/telemetry/file.parquet"}}, nil
	}})
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(original)
	_, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Error")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Receipt.SaveCheck.Panels[0].Error, "/private/") {
		t.Fatal("path was not redacted")
	}
	if logs.Len() != 0 {
		t.Fatalf("saved check logged executor error again: %s", logs.String())
	}
}

func TestCreateReceiptsListAddedPanelsWithoutEditChips(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Created")})
	if err != nil {
		t.Fatal(err)
	}
	r := out.Receipt
	if r.LayoutChanged || len(r.DashboardFields) != 0 || len(r.Changes) != 1 || r.Changes[0].Kind != "added" {
		t.Fatalf("create reports edits: %+v", r)
	}
}

func TestNoOpDashboardEditCreatesADocumentedVersionWithoutChips(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("No-op")})
	if err != nil {
		t.Fatal(err)
	}
	_, edited, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: created.Dashboard.ID, BaseVersion: 1, Operations: []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"title": "Notes"}}}})
	if err != nil {
		t.Fatal(err)
	}
	r := edited.Receipt
	if r.BaseVersion != 1 || r.Version != 2 || len(r.Changes) != 0 || r.LayoutChanged || len(r.DashboardFields) != 0 {
		t.Fatalf("no-op receipt=%+v", r)
	}
	if !strings.Contains(saveReceiptGuide, "No-op saves still create a new version") {
		t.Fatal("no-op saves are not documented")
	}
}
