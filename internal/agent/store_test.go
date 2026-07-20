package agent

import (
	"context"
	"errors"
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
	if err := store.FinishRun(context.Background(), "owner-1", input.ThreadID, input.RunID, final, [][]byte{[]byte(`{"type":"RUN_FINISHED"}`)}, nil); err != nil {
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
