package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/api"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"

	controlstore "github.com/labstack/fanout/internal/store"
)

func TestStorePersistsOwnerScopedThread(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := NewStore(database.DB)
	input := agtypes.RunAgentInput{ThreadID: "thread-1", RunID: "run-1", Messages: []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "hello"}}}
	if err := store.StartRun(context.Background(), "owner-1", input); err != nil {
		t.Fatal(err)
	}
	final := append(input.Messages, agtypes.Message{ID: "assistant-1", Role: agtypes.RoleAssistant, Content: "hi"})
	if err := store.FinishRun(context.Background(), "owner-1", input.ThreadID, input.RunID, final, [][]byte{[]byte(`{"type":"RUN_FINISHED"}`)}, false, nil); err != nil {
		t.Fatal(err)
	}
	thread, err := store.Thread(context.Background(), "owner-1", input.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Messages) != 2 || thread.Messages[1].Content != "hi" {
		t.Fatalf("unexpected thread: %#v", thread)
	}
	if _, err := store.Thread(context.Background(), "owner-2", input.ThreadID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("other owner error = %v", err)
	}
}

func TestStoreListsOwnerThreadsWithSearchAndCursor(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := NewStore(database.DB)
	threads := []struct {
		id      string
		owner   string
		message string
		updated string
	}{
		{"thread-checkout", "owner-1", "Investigate checkout latency", "2026-07-23 03:00:00"},
		{"thread-payment", "owner-1", "Find PAYMENT errors", "2026-07-23 02:00:00"},
		{"thread-private", "owner-2", "Owner two secret", "2026-07-23 04:00:00"},
	}
	for index, item := range threads {
		input := agtypes.RunAgentInput{
			ThreadID: item.id,
			RunID:    "run-" + string(rune('a'+index)),
			Messages: []agtypes.Message{{ID: "message-" + item.id, Role: agtypes.RoleUser, Content: item.message}},
		}
		if err := store.StartRun(context.Background(), item.owner, input); err != nil {
			t.Fatal(err)
		}
		if _, err := database.DB.Exec(`UPDATE agui_threads SET updated_at = ? WHERE thread_id = ?`, item.updated, item.id); err != nil {
			t.Fatal(err)
		}
	}

	first, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].ThreadID != "thread-checkout" || first[0].Title != "Investigate checkout latency" {
		t.Fatalf("first page = %#v", first)
	}
	second, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{
		Limit:         2,
		BeforeUpdated: first[0].Updated,
		BeforeID:      first[0].ThreadID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ThreadID != "thread-payment" {
		t.Fatalf("second page = %#v", second)
	}
	matches, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ThreadID != "thread-payment" {
		t.Fatalf("search results = %#v", matches)
	}
	noisyMatches, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{Query: "user", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(noisyMatches) != 0 {
		t.Fatalf("message metadata search results = %#v, want none", noisyMatches)
	}
	defaultPage, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultPage) != 2 {
		t.Fatalf("default-limit page length = %d, want 2", len(defaultPage))
	}
	if _, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{BeforeUpdated: first[0].Updated}); err == nil {
		t.Fatal("half cursor succeeded, want validation error")
	}
	if err := store.RenameThread(context.Background(), "owner-1", "thread-payment", "Payment regression"); err != nil {
		t.Fatal(err)
	}
	renamed, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{Query: "regression", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(renamed) != 1 || renamed[0].Title != "Payment regression" {
		t.Fatalf("renamed search results = %#v", renamed)
	}
	if _, err := database.DB.Exec(`UPDATE agui_threads SET messages_json = 'not valid json' WHERE thread_id = 'thread-payment'`); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.Threads(context.Background(), "owner-1", ThreadListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("list summaries decoded messages_json: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries after corrupt message payload = %#v", summaries)
	}
	if err := store.RenameThread(context.Background(), "owner-2", "thread-payment", "Not allowed"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("cross-owner rename = %v, want ErrThreadNotFound", err)
	}
	if err := store.DeleteThread(context.Background(), "owner-2", "thread-payment"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("cross-owner delete = %v, want ErrThreadNotFound", err)
	}
	if err := store.DeleteThread(context.Background(), "owner-1", "thread-payment"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Thread(context.Background(), "owner-1", "thread-payment"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("deleted thread load = %v, want ErrThreadNotFound", err)
	}
}

func TestThreadTitle(t *testing.T) {
	t.Parallel()
	longUnicode := strings.Repeat("界", 73)
	tests := []struct {
		name     string
		messages []agtypes.Message
		want     string
	}{
		{
			name: "normalizes whitespace",
			messages: []agtypes.Message{
				{Role: agtypes.RoleAssistant, Content: "ignore me"},
				{Role: agtypes.RoleUser, Content: "  Investigate\n checkout\tlatency  "},
			},
			want: "Investigate checkout latency",
		},
		{
			name:     "truncates unicode by rune",
			messages: []agtypes.Message{{Role: agtypes.RoleUser, Content: longUnicode}},
			want:     strings.Repeat("界", 72) + "…",
		},
		{
			name: "skips structured content",
			messages: []agtypes.Message{
				{Role: agtypes.RoleUser, Content: map[string]any{"type": "text", "text": "structured"}},
				{Role: agtypes.RoleUser, Content: "Plain follow-up"},
			},
			want: "Plain follow-up",
		},
		{
			name:     "falls back without a plain user message",
			messages: []agtypes.Message{{Role: agtypes.RoleAssistant, Content: "assistant only"}},
			want:     "Untitled investigation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := threadTitle(tt.messages); got != tt.want {
				t.Fatalf("threadTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStoreStartRunRejectsForeignThread(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := NewStore(database.DB)
	input := agtypes.RunAgentInput{ThreadID: "thread-1", RunID: "run-1"}
	if err := store.StartRun(context.Background(), "owner-1", input); err != nil {
		t.Fatal(err)
	}
	input.RunID = "run-2"
	if err := store.StartRun(context.Background(), "owner-2", input); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("foreign owner error = %v, want ErrThreadNotFound", err)
	}
}

func TestStoreConcurrentFirstRunsSameThread(t *testing.T) {
	// File-backed so the pool allows real concurrency; two first-runs on the
	// same thread must serialize under BEGIN IMMEDIATE instead of racing the
	// SELECT-then-INSERT into a unique-constraint error.
	database, err := controlstore.NewSQLite(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := NewStore(database.DB)
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			input := agtypes.RunAgentInput{ThreadID: "thread-race", RunID: "run-" + string(rune('a'+i))}
			results[i] = store.StartRun(context.Background(), "owner-1", input)
		}(i)
	}
	wg.Wait()
	for i, err := range results {
		if err != nil {
			t.Errorf("concurrent StartRun %d: %v", i, err)
		}
	}
	var runs int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM agui_runs WHERE thread_id = 'thread-race'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Errorf("runs = %d, want 2", runs)
	}
}

func TestStoreFinishRunRecordsTruncation(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := NewStore(database.DB)
	input := agtypes.RunAgentInput{ThreadID: "thread-1", RunID: "run-1"}
	if err := store.StartRun(context.Background(), "owner-1", input); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(context.Background(), "owner-1", "thread-1", "run-1", nil, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	var status, errorText string
	if err := database.DB.QueryRow(`SELECT status, error FROM agui_runs WHERE run_id = 'run-1'`).Scan(&status, &errorText); err != nil {
		t.Fatal(err)
	}
	if status != "truncated" || errorText == "" {
		t.Errorf("status=%q error=%q, want truncated with a note", status, errorText)
	}
}

func TestThreadRouteHidesOtherOwnersThread(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	users := auth.NewUserStore(database.DB)
	ownerA, err := users.Create("thread-a@example.com", "", "operator")
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := users.Create("thread-b@example.com", "", "operator")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(database.DB)
	input := agtypes.RunAgentInput{ThreadID: "private-thread", RunID: "run-1"}
	if err := store.StartRun(context.Background(), ownerA.ID, input); err != nil {
		t.Fatal(err)
	}
	ownInput := agtypes.RunAgentInput{
		ThreadID: "own-thread",
		RunID:    "run-2",
		Messages: []agtypes.Message{{ID: "message-1", Role: agtypes.RoleUser, Content: "Investigate owner B"}},
	}
	if err := store.StartRun(context.Background(), ownerB.ID, ownInput); err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewBrowserSessions(database.DB, 12*time.Hour, 7*24*time.Hour, false)
	login := httptest.NewRecorder()
	sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EstablishAuthenticatedSession(r.Context(), ownerB); err != nil {
			t.Errorf("create session: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/login", nil))
	cookie := login.Result().Cookies()[0]

	e := echo.New()
	api.RegisterAuthMiddleware(e, users, sessions, auth.NewAuditStore(database.DB), env.Config{})
	NewRuntime(nil, nil, store).Register(e.Group("/api/agent", api.RequireCapability(api.RunAgent)))
	request := httptest.NewRequest(http.MethodGet, "/api/agent/threads/private-thread", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-owner thread read = %d, want 404", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/agent/threads?q=owner&limit=1", nil)
	request.AddCookie(cookie)
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("thread list = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Threads    []ThreadSummary `json:"threads"`
		NextCursor string          `json:"nextCursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ThreadID != "own-thread" || page.NextCursor != "" {
		t.Fatalf("owner-scoped thread page = %#v", page)
	}

	mutation := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Fanout-Request", "1")
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := mutation(http.MethodPatch, "/api/agent/threads/private-thread", `{"title":"Not allowed"}`); recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-owner rename = %d, want 404", recorder.Code)
	}
	if recorder := mutation(http.MethodDelete, "/api/agent/threads/private-thread", ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete = %d, want 404", recorder.Code)
	}
	if recorder := mutation(http.MethodPatch, "/api/agent/threads/own-thread", `{"title":"Checkout regression"}`); recorder.Code != http.StatusOK {
		t.Fatalf("own rename = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	renamed, err := store.Threads(context.Background(), ownerB.ID, ThreadListOptions{Query: "regression", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(renamed) != 1 || renamed[0].Title != "Checkout regression" {
		t.Fatalf("renamed route result = %#v", renamed)
	}
	if recorder := mutation(http.MethodDelete, "/api/agent/threads/own-thread", ""); recorder.Code != http.StatusNoContent {
		t.Fatalf("own delete = %d, want 204: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := store.Thread(context.Background(), ownerB.ID, "own-thread"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("deleted route thread load = %v, want ErrThreadNotFound", err)
	}
}
