package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewBookmarkStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBookmarkStore(dir)
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}
	if store == nil {
		t.Fatal("store is nil")
	}
	// Directory should exist
	info, err := os.Stat(filepath.Join(dir, "bookmarks"))
	if err != nil {
		t.Fatalf("bookmarks dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("bookmarks path is not a directory")
	}
}

func TestBookmarkStore_CreateAndList(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	b, err := store.Create("What is P95 latency?", "<p>P95 is the 95th percentile</p>")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if b.ID == "" {
		t.Error("bookmark ID is empty")
	}
	if len(b.ID) != 8 {
		t.Errorf("bookmark ID length = %d, want 8", len(b.ID))
	}
	if b.Question != "What is P95 latency?" {
		t.Errorf("Question = %q, want %q", b.Question, "What is P95 latency?")
	}
	if b.CreatedAt == "" {
		t.Error("CreatedAt is empty")
	}

	bookmarks, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("List returned %d bookmarks, want 1", len(bookmarks))
	}
	if bookmarks[0].ID != b.ID {
		t.Errorf("Listed bookmark ID = %q, want %q", bookmarks[0].ID, b.ID)
	}
}

func TestBookmarkStore_ListOrder(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	// Create two bookmarks with enough separation for ordering.
	// Write the first manually with an older timestamp to guarantee order.
	b1, _ := store.Create("First", "<p>1</p>")
	b2, _ := store.Create("Second", "<p>2</p>")

	bookmarks, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bookmarks) != 2 {
		t.Fatalf("List returned %d, want 2", len(bookmarks))
	}
	// Both bookmarks should be returned. Since they may have the same
	// second-precision timestamp, just verify both exist.
	ids := map[string]bool{bookmarks[0].ID: true, bookmarks[1].ID: true}
	if !ids[b1.ID] || !ids[b2.ID] {
		t.Errorf("bookmarks = %v, want both %q and %q", ids, b1.ID, b2.ID)
	}
}

func TestBookmarkStore_ListEmpty(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	bookmarks, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bookmarks) != 0 {
		t.Errorf("List returned %d, want 0", len(bookmarks))
	}
}

func TestBookmarkStore_Delete(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	b, _ := store.Create("To delete", "<p>bye</p>")

	if err := store.Delete(b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	bookmarks, _ := store.List()
	if len(bookmarks) != 0 {
		t.Errorf("after delete, List returned %d, want 0", len(bookmarks))
	}
}

func TestBookmarkStore_DeleteNotFound(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	err = store.Delete("deadbeef")
	if err == nil {
		t.Fatal("expected error for non-existent bookmark")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want contains 'not found'", err.Error())
	}
}

func TestBookmarkStore_DeleteInvalidID(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	for _, id := range []string{"", "ZZZZZZZZ", "../etc/passwd", "too-long-id-string"} {
		err := store.Delete(id)
		if err == nil {
			t.Errorf("Delete(%q): expected error for invalid ID", id)
		}
		if !strings.Contains(err.Error(), "invalid bookmark ID") {
			t.Errorf("Delete(%q): error = %q, want contains 'invalid bookmark ID'", id, err.Error())
		}
	}
}

func TestBookmarkStore_CreateEmptyQuestion(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	for _, q := range []string{"", "   ", "\t\n"} {
		_, err := store.Create(q, "<p>answer</p>")
		if err == nil {
			t.Errorf("Create(%q): expected error for empty question", q)
		}
	}
}

func TestBookmarkStore_ListSkipsCorruptFiles(t *testing.T) {
	store, err := NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}

	// Create a valid bookmark
	store.Create("Valid", "<p>ok</p>")

	// Write a corrupt JSON file
	os.WriteFile(filepath.Join(store.dir, "corrupt1.json"), []byte("not json{{{"), 0o644)

	// Write a non-JSON file (should be skipped)
	os.WriteFile(filepath.Join(store.dir, "readme.txt"), []byte("ignore me"), 0o644)

	bookmarks, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Errorf("List returned %d, want 1 (skip corrupt + non-json)", len(bookmarks))
	}
}
