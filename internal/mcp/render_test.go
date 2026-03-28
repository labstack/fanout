package mcp

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
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

	// IDs should be valid UUID v7
	parsed, err := uuid.Parse(id1)
	if err != nil {
		t.Errorf("genID() returned invalid UUID: %v", err)
	}
	if parsed.Version() != 7 {
		t.Errorf("genID() UUID version = %d, want 7", parsed.Version())
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

	id := genID()
	report := &Report{
		ID:        id,
		Query:     "service health",
		Summary:   "Test report",
		HTML:      "<div>Test</div>",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	store.Save(report)

	// Get should return the saved report
	got := store.Get(id)
	if got == nil {
		t.Fatal("Get() returned nil for saved report")
		return
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
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

	got := store.Get("00000000-0000-7000-8000-000000000000")
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
	id1, id2, id3 := genID(), genID(), genID()
	store.Save(&Report{ID: id1, CreatedAt: now.Add(-2 * time.Hour)})
	store.Save(&Report{ID: id2, CreatedAt: now.Add(-1 * time.Hour)})
	store.Save(&Report{ID: id3, CreatedAt: now})

	reports := store.List()

	if len(reports) != 3 {
		t.Errorf("List() returned %d reports, want 3", len(reports))
	}

	// Should be sorted by created_at descending (newest first)
	if reports[0].ID != id3 {
		t.Errorf("First report ID = %q, want %q (newest)", reports[0].ID, id3)
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

	id := genID()
	store.Save(&Report{ID: id, Summary: "Will be deleted"})

	// Verify it exists
	if store.Get(id) == nil {
		t.Fatal("Report should exist before deletion")
	}

	// Delete
	ok := store.Delete(id)
	if !ok {
		t.Error("Delete() should return true for existing report")
	}

	// Verify it's gone
	if store.Get(id) != nil {
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

	ok := store.Delete("00000000-0000-7000-8000-000000000000")
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
	id1, id2, id3 := genID(), genID(), genID()
	store.Save(&Report{ID: id1, ExpiresAt: now.Add(-1 * time.Hour)})
	store.Save(&Report{ID: id2, ExpiresAt: now.Add(-30 * time.Minute)})
	store.Save(&Report{ID: id3, ExpiresAt: now.Add(1 * time.Hour)})

	// Run cleanup
	deleted := store.Cleanup()

	if deleted != 2 {
		t.Errorf("Cleanup() deleted %d reports, want 2", deleted)
	}

	// Valid report should still exist
	if store.Get(id3) == nil {
		t.Error("Valid report should not be deleted")
	}

	// Expired reports should be gone
	if store.Get(id1) != nil {
		t.Error("Expired report 1 should be deleted")
	}
	if store.Get(id2) != nil {
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

func TestReportStore_PathTraversal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fanout-reports-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &ReportStore{dir: tmpDir + "/reports"}

	// Attempt to save with path traversal ID
	store.Save(&Report{ID: "../../../etc/passwd"})

	// Should not be retrievable (invalid ID rejected)
	if store.Get("../../../etc/passwd") != nil {
		t.Error("path traversal ID should be rejected")
	}

	// Valid UUID ID should work
	id := genID()
	store.Save(&Report{ID: id})
	if store.Get(id) == nil {
		t.Error("valid UUID ID should be accepted")
	}
}

func TestReport(t *testing.T) {
	now := time.Now()
	id := genID()
	r := Report{
		ID:        id,
		Query:     "status check",
		Summary:   "All systems operational",
		HTML:      "<div class='status'>OK</div>",
		CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
	}

	if r.ID != id {
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
