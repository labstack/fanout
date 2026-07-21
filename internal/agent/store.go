package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
)

var ErrThreadNotFound = errors.New("agent thread not found")

type Thread struct {
	ThreadID string            `json:"threadId"`
	Messages []agtypes.Message `json:"messages"`
	Updated  string            `json:"updatedAt"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Thread(ctx context.Context, ownerID, threadID string) (Thread, error) {
	var raw, updated string
	err := s.db.QueryRowContext(ctx,
		`SELECT messages_json, updated_at FROM agui_threads WHERE thread_id = ? AND owner_id = ?`,
		threadID, ownerID,
	).Scan(&raw, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, ErrThreadNotFound
	}
	if err != nil {
		return Thread{}, fmt.Errorf("load thread: %w", err)
	}
	var messages []agtypes.Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return Thread{}, fmt.Errorf("decode thread messages: %w", err)
	}
	return Thread{ThreadID: threadID, Messages: messages, Updated: updated}, nil
}

// StartRun creates or updates the caller's thread and records the run as
// running. The SELECT-then-INSERT runs under BEGIN IMMEDIATE on a pinned
// connection so two concurrent first-runs on the same thread serialize
// instead of racing into a unique-constraint error.
func (s *Store) StartRun(ctx context.Context, ownerID string, input agtypes.RunAgentInput) error {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode run input: %w", err)
	}
	messagesJSON, err := json.Marshal(input.Messages)
	if err != nil {
		return fmt.Errorf("encode messages: %w", err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open run connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin run: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var existingOwner string
	err = conn.QueryRowContext(ctx, `SELECT owner_id FROM agui_threads WHERE thread_id = ?`, input.ThreadID).Scan(&existingOwner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO agui_threads (thread_id, owner_id, messages_json) VALUES (?, ?, ?)`,
			input.ThreadID, ownerID, string(messagesJSON),
		); err != nil {
			return fmt.Errorf("create thread: %w", err)
		}
	case err != nil:
		return fmt.Errorf("check thread owner: %w", err)
	case existingOwner != ownerID:
		return ErrThreadNotFound
	default:
		if _, err := conn.ExecContext(ctx,
			`UPDATE agui_threads SET messages_json = ?, updated_at = datetime('now') WHERE thread_id = ?`,
			string(messagesJSON), input.ThreadID,
		); err != nil {
			return fmt.Errorf("update thread input: %w", err)
		}
	}
	var parent any
	if input.ParentRunID != nil {
		parent = *input.ParentRunID
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO agui_runs (run_id, thread_id, parent_run_id, input_json, status) VALUES (?, ?, ?, ?, 'running')`,
		input.RunID, input.ThreadID, parent, string(inputJSON),
	); err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit run: %w", err)
	}
	committed = true
	return nil
}

// FinishRun persists the final conversation and the run outcome. The thread's
// messages_json is written wholesale: if two runs on the same thread finish
// concurrently, the later commit wins (last-write-wins) — concurrent runs on
// one thread are not a supported flow, and each run row still records its own
// events and outcome. truncated marks a run whose answer was cut off at the
// provider's token limit; it only applies when runErr is nil.
func (s *Store) FinishRun(ctx context.Context, ownerID, threadID, runID string, messages []agtypes.Message, eventJSON [][]byte, truncated bool, runErr error) error {
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("encode final messages: %w", err)
	}
	rawEvents := make([]json.RawMessage, len(eventJSON))
	for i := range eventJSON {
		rawEvents[i] = json.RawMessage(eventJSON[i])
	}
	eventsJSON, err := json.Marshal(rawEvents)
	if err != nil {
		return fmt.Errorf("encode events: %w", err)
	}
	status, errorText := "completed", ""
	switch {
	case runErr != nil:
		status, errorText = "failed", runErr.Error()
	case truncated:
		status, errorText = "truncated", "response truncated at model token limit"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("finish run transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`UPDATE agui_threads SET messages_json = ?, updated_at = datetime('now') WHERE thread_id = ? AND owner_id = ?`,
		string(messagesJSON), threadID, ownerID,
	)
	if err != nil {
		return fmt.Errorf("save final messages: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrThreadNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agui_runs SET events_json = ?, status = ?, error = ?, completed_at = datetime('now') WHERE run_id = ? AND thread_id = ?`,
		string(eventsJSON), status, errorText, runID, threadID,
	); err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return tx.Commit()
}
