package agent

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"

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
