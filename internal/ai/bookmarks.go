package ai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Bookmark stores a saved chat answer.
type Bookmark struct {
	ID         string `json:"id"`
	Question   string `json:"question"`
	AnswerHTML string `json:"answer_html"`
	CreatedAt  string `json:"created_at"`
}

// BookmarkStore manages bookmark CRUD in the configured bookmarks directory.
type BookmarkStore struct {
	mu  sync.RWMutex
	dir string
}

// NewBookmarkStore creates a store backed by the given directory.
func NewBookmarkStore(dir string) (*BookmarkStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create bookmarks dir: %w", err)
	}
	return &BookmarkStore{dir: dir}, nil
}

// Create saves a new bookmark. Returns an error if question is empty.
func (s *BookmarkStore) Create(question, answerHTML string) (*Bookmark, error) {
	if strings.TrimSpace(question) == "" {
		return nil, fmt.Errorf("bookmark question is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate bookmark id: %w", err)
	}

	b := &Bookmark{
		ID:         id.String(),
		Question:   question,
		AnswerHTML: answerHTML,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}

	// Write to temp file then rename for atomicity
	path := filepath.Join(s.dir, b.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, fmt.Errorf("write bookmark: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("rename bookmark: %w", err)
	}

	return b, nil
}

// List returns all bookmarks sorted by creation time (newest first).
func (s *BookmarkStore) List() ([]Bookmark, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Bookmark{}, nil
		}
		return nil, err
	}

	var bookmarks []Bookmark
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			slog.Error("failed to read bookmark file", "file", entry.Name(), "err", err)
			continue
		}

		var b Bookmark
		if err := json.Unmarshal(data, &b); err != nil {
			slog.Error("corrupt bookmark file", "file", entry.Name(), "err", err)
			continue
		}
		bookmarks = append(bookmarks, b)
	}

	sort.Slice(bookmarks, func(i, j int) bool {
		return bookmarks[i].CreatedAt > bookmarks[j].CreatedAt
	})

	return bookmarks, nil
}

// Delete removes a bookmark by ID.
func (s *BookmarkStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	bookmarkID, err := normalizeBookmarkID(id)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, bookmarkID+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("bookmark not found: %s", bookmarkID)
		}
		return fmt.Errorf("delete bookmark: %w", err)
	}
	return nil
}

func normalizeBookmarkID(id string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil || parsed.Version() != 7 {
		return "", fmt.Errorf("invalid bookmark ID: %s", id)
	}
	return parsed.String(), nil
}
