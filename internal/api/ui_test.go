package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/ai"
	"github.com/labstack/fanout/internal/config"
)

func TestFavicon(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := Favicon(c); err != nil {
		t.Fatalf("Favicon: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "max-age=86400") {
		t.Errorf("Cache-Control = %q, want max-age=86400", cc)
	}
	if rec.Body.Len() == 0 {
		t.Error("body is empty")
	}
}

func TestStreamChat_NilHandler(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"content":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	err := h.StreamChat(c)
	if err == nil {
		t.Fatal("expected error for nil sseHandler")
	}

	httpErr, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("error type = %T, want *echo.HTTPError", err)
	}
	if httpErr.Code != 503 {
		t.Errorf("error code = %d, want 503", httpErr.Code)
	}
}

func TestCancelChat_NilHandler(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/cancel", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	err := h.CancelChat(c)
	if err == nil {
		t.Fatal("expected error for nil sseHandler")
	}

	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 503 {
		t.Errorf("error code = %d, want 503", httpErr.Code)
	}
}

func TestClearChat_NilHandler(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/clear", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	err := h.ClearChat(c)
	if err == nil {
		t.Fatal("expected error for nil sseHandler")
	}

	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 503 {
		t.Errorf("error code = %d, want 503", httpErr.Code)
	}
}

func TestListBookmarks_NilStore(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/bookmarks", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	if err := h.ListBookmarks(c); err != nil {
		t.Fatalf("ListBookmarks: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var result []ai.Bookmark
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("bookmarks = %d, want 0", len(result))
	}
}

func TestListBookmarks_WithStore(t *testing.T) {
	store, err := ai.NewBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}
	store.Create("test question", "<p>answer</p>")

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/bookmarks", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{bookmarks: store}
	if err := h.ListBookmarks(c); err != nil {
		t.Fatalf("ListBookmarks: %v", err)
	}

	var result []ai.Bookmark
	json.Unmarshal(rec.Body.Bytes(), &result)
	if len(result) != 1 {
		t.Errorf("bookmarks = %d, want 1", len(result))
	}
}

func TestDashboard_NilOrchestrator(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	err := h.Dashboard(c)
	if err == nil {
		t.Fatal("expected error for nil orchestrator")
	}

	httpErr, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("error type = %T, want *echo.HTTPError", err)
	}
	if httpErr.Code != 503 {
		t.Errorf("error code = %d, want 503", httpErr.Code)
	}
}

func TestCreateBookmark_Success(t *testing.T) {
	store, _ := ai.NewBookmarkStore(t.TempDir())

	e := echo.New()
	body := `{"question":"How does tracing work?","answer_html":"<p>It traces requests</p>"}`
	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{bookmarks: store, sanitizer: newSanitizer()}
	if err := h.CreateBookmark(c); err != nil {
		t.Fatalf("CreateBookmark: %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}

	var result ai.Bookmark
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result.Question != "How does tracing work?" {
		t.Errorf("question = %q", result.Question)
	}
}

func TestCreateBookmark_EmptyQuestion(t *testing.T) {
	store, _ := ai.NewBookmarkStore(t.TempDir())

	e := echo.New()
	body := `{"question":"  ","answer_html":"<p>answer</p>"}`
	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{bookmarks: store, sanitizer: newSanitizer()}
	err := h.CreateBookmark(c)
	if err == nil {
		t.Fatal("expected error for empty question")
	}

	httpErr, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("error type = %T, want *echo.HTTPError", err)
	}
	if httpErr.Code != 400 {
		t.Errorf("error code = %d, want 400", httpErr.Code)
	}
}

func TestCreateBookmark_NilStore(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	err := h.CreateBookmark(c)
	if err == nil {
		t.Fatal("expected error for nil store")
	}

	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 503 {
		t.Errorf("error code = %d, want 503", httpErr.Code)
	}
}

func TestCreateBookmark_SanitizesXSS(t *testing.T) {
	store, _ := ai.NewBookmarkStore(t.TempDir())

	e := echo.New()
	body := `{"question":"test","answer_html":"<script>alert('xss')</script><p>safe</p>"}`
	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{bookmarks: store, sanitizer: newSanitizer()}
	h.CreateBookmark(c)

	var result ai.Bookmark
	json.Unmarshal(rec.Body.Bytes(), &result)
	if strings.Contains(result.AnswerHTML, "<script>") {
		t.Error("XSS script tag was not sanitized")
	}
	if !strings.Contains(result.AnswerHTML, "<p>safe</p>") {
		t.Error("safe HTML was incorrectly removed")
	}
}

func TestDeleteBookmark_Success(t *testing.T) {
	store, _ := ai.NewBookmarkStore(t.TempDir())
	b, _ := store.Create("to delete", "<p>bye</p>")

	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/bookmarks/"+b.ID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPathValues(echo.PathValues{{Name: "id", Value: b.ID}})

	h := &UIHandler{bookmarks: store}
	if err := h.DeleteBookmark(c); err != nil {
		t.Fatalf("DeleteBookmark: %v", err)
	}

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestDeleteBookmark_NotFound(t *testing.T) {
	store, _ := ai.NewBookmarkStore(t.TempDir())

	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/bookmarks/deadbeef", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPathValues(echo.PathValues{{Name: "id", Value: "deadbeef"}})

	h := &UIHandler{bookmarks: store}
	err := h.DeleteBookmark(c)
	if err == nil {
		t.Fatal("expected error for non-existent bookmark")
	}

	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 404 {
		t.Errorf("error code = %d, want 404", httpErr.Code)
	}
}

func TestDeleteBookmark_InvalidID(t *testing.T) {
	store, _ := ai.NewBookmarkStore(t.TempDir())

	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/bookmarks/../etc", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPathValues(echo.PathValues{{Name: "id", Value: "../etc"}})

	h := &UIHandler{bookmarks: store}
	err := h.DeleteBookmark(c)
	if err == nil {
		t.Fatal("expected error for invalid ID")
	}

	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 404 {
		t.Errorf("error code = %d, want 404", httpErr.Code)
	}
}

func TestSuggestions_NilOrch(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/suggestions", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	if err := h.Suggestions(c); err != nil {
		t.Fatalf("Suggestions: %v", err)
	}

	var result []string
	json.Unmarshal(rec.Body.Bytes(), &result)
	if len(result) != 0 {
		t.Errorf("suggestions = %d, want 0", len(result))
	}
}

func TestRegisterUIRoutes(t *testing.T) {
	e := echo.New()
	h := RegisterUIRoutes(e, config.Config{}, nil, nil, nil, nil, nil)
	if h == nil {
		t.Fatal("RegisterUIRoutes returned nil")
	}
}

func TestHome_InvalidWindow(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/home?window=abc", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	err := h.Home(c)
	if err == nil {
		t.Fatal("expected error for invalid window")
	}
	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 400 {
		t.Errorf("error code = %d, want 400", httpErr.Code)
	}
}

func TestHome_NilService(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/home", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &UIHandler{}
	err := h.Home(c)
	if err == nil {
		t.Fatal("expected error for nil service")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("error type = %T, want *echo.HTTPError", err)
	}
	if httpErr.Code != 503 {
		t.Errorf("error code = %d, want 503", httpErr.Code)
	}
}
