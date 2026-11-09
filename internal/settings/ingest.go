package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/db/generated"
	appid "github.com/labstack/fanout/internal/id"
	"github.com/labstack/fanout/internal/secret"
	appstore "github.com/labstack/fanout/internal/store"
)

const (
	// MaxActiveIngestTokens caps the tokens that can authenticate at once.
	MaxActiveIngestTokens = 50
	// MaxIngestTokenNameLength is measured in characters after trimming.
	MaxIngestTokenNameLength = 64

	// ingestTokenLastUsedFlushInterval bounds how often last-used times reach
	// SQLite. A token listing can lag real use by up to this long.
	ingestTokenLastUsedFlushInterval = time.Minute
	ingestTokenLastUsedFinalFlush    = 5 * time.Second

	ingestTokenHintLength = 4

	setupIngestTokenName      = "default"
	configuredIngestTokenName = "configuration"

	ingestTokenSourceAPI           = "api"
	ingestTokenSourceSetup         = "setup"
	ingestTokenSourceConfiguration = "configuration"
)

var (
	ErrInvalidIngestTokenName  = errors.New("settings: invalid ingest token name")
	ErrIngestTokenNameTaken    = errors.New("settings: ingest token name already in use")
	ErrIngestTokenLimitReached = errors.New("settings: active ingest token limit reached")
	ErrIngestTokenNotFound     = errors.New("settings: ingest token not found")
)

// IngestToken is the metadata an administrator can see. It holds no digest and
// no plaintext; the plaintext exists only in the create response.
type IngestToken struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Hint       string  `json:"hint"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
}

// IngestTokenActor identifies who changed a token, for the audit trail.
type IngestTokenActor struct {
	UserID    string
	RemoteIP  string
	UserAgent string
}

// CreateIngestToken issues a token and returns its plaintext, which is never
// stored or available again. The first statement of the transaction is the
// conditional insert, so SQLite takes its write lock before counting and
// concurrent creates cannot pass the cap together.
func (s *Store) CreateIngestToken(ctx context.Context, name string, actor IngestTokenActor) (IngestToken, string, error) {
	name, err := normalizeIngestTokenName(name)
	if err != nil {
		return IngestToken{}, "", err
	}
	token, hash, err := GenerateIngestToken()
	if err != nil {
		return IngestToken{}, "", err
	}
	id, err := appid.New()
	if err != nil {
		return IngestToken{}, "", fmt.Errorf("settings: generate ingest token id: %w", err)
	}
	createdAt := s.ingestTimestamp()

	writeCtx, cancel := auth.DetachedWriteContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(writeCtx, nil)
	if err != nil {
		return IngestToken{}, "", fmt.Errorf("settings: begin ingest token create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inserted, err := generated.New(tx).InsertIngestTokenWithinLimit(writeCtx, generated.InsertIngestTokenWithinLimitParams{
		ID:        id,
		Name:      name,
		TokenHash: hash,
		Hint:      ingestTokenHint(token),
		Source:    ingestTokenSourceAPI,
		CreatedAt: createdAt,
		CreatedBy: nullableString(actor.UserID),
		MaxActive: MaxActiveIngestTokens,
	})
	if appstore.IsUniqueConstraint(err) {
		return IngestToken{}, "", ErrIngestTokenNameTaken
	}
	if err != nil {
		return IngestToken{}, "", fmt.Errorf("settings: insert ingest token: %w", err)
	}
	if inserted == 0 {
		return IngestToken{}, "", ErrIngestTokenLimitReached
	}
	if err := s.audit.RecordTx(writeCtx, tx, ingestTokenEvent("ingest_token.created", id, name, ingestTokenSourceAPI, actor)); err != nil {
		return IngestToken{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return IngestToken{}, "", fmt.Errorf("settings: commit ingest token create: %w", err)
	}
	return IngestToken{ID: id, Name: name, Hint: ingestTokenHint(token), CreatedAt: createdAt}, token, nil
}

// ListActiveIngestTokens returns at most MaxActiveIngestTokens rows, newest first.
func (s *Store) ListActiveIngestTokens(ctx context.Context) ([]IngestToken, error) {
	rows, err := s.q.ListActiveIngestTokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list ingest tokens: %w", err)
	}
	tokens := make([]IngestToken, 0, len(rows))
	for _, row := range rows {
		item := IngestToken{ID: row.ID, Name: row.Name, Hint: row.Hint, CreatedAt: row.CreatedAt}
		if row.LastUsedAt.Valid {
			lastUsed := row.LastUsedAt.String
			item.LastUsedAt = &lastUsed
		}
		tokens = append(tokens, item)
	}
	return tokens, nil
}

// HasActiveIngestToken reports whether ingest can currently authenticate anyone.
func (s *Store) HasActiveIngestToken(ctx context.Context) (bool, error) {
	exists, err := s.q.HasActiveIngestToken(ctx)
	if err != nil {
		return false, fmt.Errorf("settings: check active ingest tokens: %w", err)
	}
	return exists, nil
}

// RevokeIngestToken stops a token immediately: ingest authenticates against the
// row on every request and no credential is cached. The row is kept for the
// audit trail and for the rule that configuration seeds only a first token.
func (s *Store) RevokeIngestToken(ctx context.Context, id string, actor IngestTokenActor) error {
	writeCtx, cancel := auth.DetachedWriteContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(writeCtx, nil)
	if err != nil {
		return fmt.Errorf("settings: begin ingest token revoke: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := generated.New(tx).RevokeIngestToken(writeCtx, generated.RevokeIngestTokenParams{
		RevokedAt: nullableString(s.ingestTimestamp()),
		RevokedBy: nullableString(actor.UserID),
		ID:        id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIngestTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("settings: revoke ingest token: %w", err)
	}
	if err := s.audit.RecordTx(writeCtx, tx, ingestTokenEvent("ingest_token.revoked", row.ID, row.Name, "", actor)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings: commit ingest token revoke: %w", err)
	}
	return nil
}

// ApplyConfiguredIngestToken stores a token supplied by configuration, and
// reports whether it did. It acts only when no token row has ever existed,
// counting revoked rows, so a restart still carrying the setting cannot bring
// back a token an administrator revoked.
func (s *Store) ApplyConfiguredIngestToken(ctx context.Context, token string) (bool, error) {
	return s.insertFirstIngestToken(ctx, token, configuredIngestTokenName, ingestTokenSourceConfiguration, IngestTokenActor{})
}

// IssueFirstIngestToken creates the token handed to the first administrator
// during setup. It returns an empty string when a token has already existed.
func (s *Store) IssueFirstIngestToken(ctx context.Context, actor IngestTokenActor) (string, error) {
	token, _, err := GenerateIngestToken()
	if err != nil {
		return "", err
	}
	issued, err := s.insertFirstIngestToken(ctx, token, setupIngestTokenName, ingestTokenSourceSetup, actor)
	if err != nil || !issued {
		return "", err
	}
	return token, nil
}

func (s *Store) insertFirstIngestToken(ctx context.Context, token, name, source string, actor IngestTokenActor) (bool, error) {
	id, err := appid.New()
	if err != nil {
		return false, fmt.Errorf("settings: generate ingest token id: %w", err)
	}
	writeCtx, cancel := auth.DetachedWriteContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(writeCtx, nil)
	if err != nil {
		return false, fmt.Errorf("settings: begin first ingest token: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inserted, err := generated.New(tx).InsertFirstIngestToken(writeCtx, generated.InsertFirstIngestTokenParams{
		ID:        id,
		Name:      name,
		TokenHash: HashIngestToken(token),
		Hint:      ingestTokenHint(token),
		Source:    source,
		CreatedAt: s.ingestTimestamp(),
		CreatedBy: nullableString(actor.UserID),
	})
	if err != nil {
		return false, fmt.Errorf("settings: insert first ingest token: %w", err)
	}
	if inserted == 0 {
		return false, nil
	}
	if err := s.audit.RecordTx(writeCtx, tx, ingestTokenEvent("ingest_token.created", id, name, source, actor)); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("settings: commit first ingest token: %w", err)
	}
	return true, nil
}

// AuthenticateIngestToken costs one digest and one lookup on the unique
// token_hash index. A match is recorded in memory for the last-used flusher;
// the request path never writes to SQLite.
func (s *Store) AuthenticateIngestToken(ctx context.Context, token string) (bool, error) {
	id, err := s.q.GetActiveIngestTokenIDByHash(ctx, HashIngestToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("settings: look up ingest token: %w", err)
	}
	s.lastUsed.touch(id, s.now())
	return true, nil
}

// lastUsedBuffer coalesces last-used times between flushes. It holds one entry
// per token that authenticated since the previous flush, so it stays bounded
// by the number of tokens.
type lastUsedBuffer struct {
	mu      sync.Mutex
	pending map[string]time.Time
}

func (b *lastUsedBuffer) touch(id string, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil {
		b.pending = make(map[string]time.Time)
	}
	if current, ok := b.pending[id]; !ok || at.After(current) {
		b.pending[id] = at
	}
}

func (b *lastUsedBuffer) drain() map[string]time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := b.pending
	b.pending = nil
	return pending
}

// restore returns a failed batch to the buffer without overwriting anything
// recorded after the batch was drained.
func (b *lastUsedBuffer) restore(batch map[string]time.Time) {
	for id, at := range batch {
		b.touch(id, at)
	}
}

// flushIngestTokenLastUsed writes the buffered times in one transaction and
// keeps them for the next flush if the write fails.
func (s *Store) flushIngestTokenLastUsed(ctx context.Context) error {
	batch := s.lastUsed.drain()
	if len(batch) == 0 {
		return nil
	}
	if err := s.writeIngestTokenLastUsed(ctx, batch); err != nil {
		s.lastUsed.restore(batch)
		return err
	}
	return nil
}

func (s *Store) writeIngestTokenLastUsed(ctx context.Context, batch map[string]time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("settings: begin ingest token last-used flush: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := generated.New(tx)
	for id, at := range batch {
		if err := queries.UpdateIngestTokenLastUsed(ctx, generated.UpdateIngestTokenLastUsedParams{
			LastUsedAt: nullableString(formatIngestTimestamp(at)),
			ID:         id,
		}); err != nil {
			return fmt.Errorf("settings: update ingest token last used: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings: commit ingest token last-used flush: %w", err)
	}
	return nil
}

// StartIngestTokenLastUsedFlusher writes coalesced last-used times about once a
// minute. The returned function stops the loop and performs a final flush; call
// it after the ingest listeners have stopped and before the database closes.
func (s *Store) StartIngestTokenLastUsedFlusher() (stop func()) {
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.lastUsedFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				if err := s.flushIngestTokenLastUsed(context.Background()); err != nil {
					slog.Error("ingest: flush token last-used failed", "err", err)
				}
			}
		}
	}()
	return func() {
		close(quit)
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), ingestTokenLastUsedFinalFlush)
		defer cancel()
		if err := s.flushIngestTokenLastUsed(ctx); err != nil {
			slog.Error("ingest: flush token last-used failed", "err", err)
		}
	}
}

func ingestTokenEvent(eventType auth.AuditEventType, id, name, source string, actor IngestTokenActor) auth.AuditEvent {
	metadata := map[string]any{"name": name}
	if source != "" {
		metadata["source"] = source
	}
	return auth.AuditEvent{
		ActorUserID: actor.UserID,
		EventType:   eventType,
		Outcome:     "success",
		TargetType:  "ingest_token",
		TargetID:    id,
		RemoteIP:    actor.RemoteIP,
		UserAgent:   actor.UserAgent,
		Metadata:    metadata,
	}
}

// normalizeIngestTokenName trims surrounding space and requires 1 to 64
// printable characters, which keeps names safe to show in the UI and audit log.
func normalizeIngestTokenName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxIngestTokenNameLength {
		return "", ErrInvalidIngestTokenName
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "", ErrInvalidIngestTokenName
		}
	}
	return name, nil
}

// ingestTokenHint is the last characters of the secret, enough to tell tokens
// apart and far too few to help guess one.
func ingestTokenHint(token string) string {
	if len(token) < ingestTokenHintLength {
		return ""
	}
	return token[len(token)-ingestTokenHintLength:]
}

func (s *Store) ingestTimestamp() string {
	return formatIngestTimestamp(s.now())
}

func formatIngestTimestamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

// GenerateIngestToken returns a new token and its digest. The format is foi_
// followed by 43 base64url characters.
func GenerateIngestToken() (string, string, error) {
	raw, err := secret.NewSecret()
	if err != nil {
		return "", "", fmt.Errorf("generate ingest token: %w", err)
	}
	token := "foi_" + raw
	return token, HashIngestToken(token), nil
}

func HashIngestToken(token string) string {
	return hashToken(token)
}
