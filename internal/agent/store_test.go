package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
}
