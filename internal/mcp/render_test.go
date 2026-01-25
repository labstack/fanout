package mcp

import (
	"os"
	"testing"
	"time"
)

func TestGenID(t *testing.T) {
	id1 := genID()
	id2 := genID()

	// IDs should be non-empty
	if id1 == "" {
		t.Error("genID() returned empty string")
	}

	// IDs should be unique
	if id1 == id2 {
		t.Error("genID() should generate unique IDs")
	}

	// IDs should be 16 characters (8 bytes hex encoded)
	if len(id1) != 16 {
		t.Errorf("genID() length = %d, want 16", len(id1))
	}
}

func TestReportStore_SaveAndGet(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	report := &Report{
		ID:        "test-report-1",
		Query:     "service health",
		Summary:   "Test report",
		HTML:      "<div>Test</div>",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	store.Save(report)

	// Get should return the saved report
	got := store.Get("test-report-1")
	if got == nil {
		t.Fatal("Get() returned nil for saved report")
	}
	if got.ID != "test-report-1" {
		t.Errorf("ID = %q, want %q", got.ID, "test-report-1")
	}
	if got.Summary != "Test report" {
		t.Errorf("Summary = %q", got.Summary)
	}
}

func TestReportStore_GetNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	got := store.Get("nonexistent")
	if got != nil {
		t.Error("Get() should return nil for non-existent report")
	}
}

func TestReportStore_List(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	// Save multiple reports
	now := time.Now()
	store.Save(&Report{ID: "r1", CreatedAt: now.Add(-2 * time.Hour)})
	store.Save(&Report{ID: "r2", CreatedAt: now.Add(-1 * time.Hour)})
	store.Save(&Report{ID: "r3", CreatedAt: now})

	reports := store.List()

	if len(reports) != 3 {
		t.Errorf("List() returned %d reports, want 3", len(reports))
	}

	// Should be sorted by created_at descending (newest first)
	if reports[0].ID != "r3" {
		t.Errorf("First report ID = %q, want %q (newest)", reports[0].ID, "r3")
	}
}

func TestReportStore_ListEmpty(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	reports := store.List()
	if len(reports) != 0 {
		t.Errorf("List() on empty store returned %d reports", len(reports))
	}
}

func TestReportStore_Delete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	store.Save(&Report{ID: "to-delete", Summary: "Will be deleted"})

	// Verify it exists
	if store.Get("to-delete") == nil {
		t.Fatal("Report should exist before deletion")
	}

	// Delete
	ok := store.Delete("to-delete")
	if !ok {
		t.Error("Delete() should return true for existing report")
	}

	// Verify it's gone
	if store.Get("to-delete") != nil {
		t.Error("Report should not exist after deletion")
	}
}

func TestReportStore_DeleteNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}
	_ = os.MkdirAll(store.dir, 0755)

	ok := store.Delete("nonexistent")
	if ok {
		t.Error("Delete() should return false for non-existent report")
	}
}

func TestReportStore_Cleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	now := time.Now()

	// Save some expired and non-expired reports
	store.Save(&Report{ID: "expired1", ExpiresAt: now.Add(-1 * time.Hour)})
	store.Save(&Report{ID: "expired2", ExpiresAt: now.Add(-30 * time.Minute)})
	store.Save(&Report{ID: "valid", ExpiresAt: now.Add(1 * time.Hour)})

	// Run cleanup
	deleted := store.Cleanup()

	if deleted != 2 {
		t.Errorf("Cleanup() deleted %d reports, want 2", deleted)
	}

	// Valid report should still exist
	if store.Get("valid") == nil {
		t.Error("Valid report should not be deleted")
	}

	// Expired reports should be gone
	if store.Get("expired1") != nil {
		t.Error("Expired report 1 should be deleted")
	}
	if store.Get("expired2") != nil {
		t.Error("Expired report 2 should be deleted")
	}
}

func TestReportStore_CleanupEmpty(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	deleted := store.Cleanup()
	if deleted != 0 {
		t.Errorf("Cleanup() on empty store deleted %d reports", deleted)
	}
}

func TestReport(t *testing.T) {
	now := time.Now()
	r := Report{
		ID:        "test-123",
		Query:     "status check",
		Summary:   "All systems operational",
		HTML:      "<div class='status'>OK</div>",
		CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
	}

	if r.ID != "test-123" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Query != "status check" {
		t.Errorf("Query = %q", r.Query)
	}
	if r.HTML == "" {
		t.Error("HTML should not be empty")
	}
	if r.ExpiresAt.Before(r.CreatedAt) {
		t.Error("ExpiresAt should be after CreatedAt")
	}
}

func TestRenderIn(t *testing.T) {
	in := RenderIn{
		Title: "Service Dashboard",
		Sections: []Section{
			{Type: "metric", Config: map[string]any{"label": "Requests", "value": "1000"}},
			{Type: "table", Title: "Top Services", Config: map[string]any{"headers": []string{"Name", "Count"}}},
		},
	}

	if in.Title != "Service Dashboard" {
		t.Errorf("Title = %q", in.Title)
	}
	if len(in.Sections) != 2 {
		t.Errorf("Sections count = %d, want 2", len(in.Sections))
	}
}

func TestSection(t *testing.T) {
	s := Section{
		Type:   "badge",
		Title:  "Status",
		Config: map[string]any{"label": "healthy", "status": "healthy"},
	}

	if s.Type != "badge" {
		t.Errorf("Type = %q", s.Type)
	}
	if s.Title != "Status" {
		t.Errorf("Title = %q", s.Title)
	}
	if s.Config["label"] != "healthy" {
		t.Errorf("Config[label] = %v", s.Config["label"])
	}
}

func TestRenderOut(t *testing.T) {
	out := RenderOut{
		HTML:     "<div>Report</div>",
		ShareURL: "/view/r/abc123",
		ReportID: "abc123",
	}

	if out.HTML == "" {
		t.Error("HTML should not be empty")
	}
	if out.ShareURL != "/view/r/abc123" {
		t.Errorf("ShareURL = %q", out.ShareURL)
	}
	if out.ReportID != "abc123" {
		t.Errorf("ReportID = %q", out.ReportID)
	}
}

func TestComponentTypes(t *testing.T) {
	types := ComponentTypes()

	if len(types) == 0 {
		t.Error("ComponentTypes() returned empty list")
	}

	// Should include common types
	found := make(map[string]bool)
	for _, typ := range types {
		found[typ] = true
	}

	expected := []string{"metric", "table", "badge", "text"}
	for _, exp := range expected {
		if !found[exp] {
			t.Errorf("ComponentTypes() missing %q", exp)
		}
	}
}

func TestComponentToolDescription(t *testing.T) {
	desc := ComponentToolDescription()

	if desc == "" {
		t.Error("ComponentToolDescription() returned empty string")
	}
}
