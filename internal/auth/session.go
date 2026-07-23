package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	appstore "github.com/labstack/fanout/internal/store"
)

const (
	sessionUserIDKey      = "user_id"
	sessionAuthVersionKey = "auth_version"
	sessionLastAuthKey    = "last_authenticated_at"
)

type sessionMetadataContextKey struct{}

// SessionMetadata is queryable metadata stored alongside SCS's opaque blob.
// A pointer is placed in the request context before SCS runs so FindCtx and
// CommitCtx share it without replacing the context SCS retains for commit.
type SessionMetadata struct {
	UserID            string
	CreatedAt         time.Time
	LastActivityAt    time.Time
	AbsoluteExpiresAt time.Time
}

// sessionErrorResponseWriter suppresses a handler's success response after SCS
// has already reported a load or commit failure. Without it a failed commit can
// produce an HTTP 500 followed by the handler's success JSON in the same body.
type sessionErrorResponseWriter struct {
	http.ResponseWriter
	failed bool
}

func (w *sessionErrorResponseWriter) WriteHeader(status int) {
	if !w.failed {
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *sessionErrorResponseWriter) Write(data []byte) (int, error) {
	if w.failed {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}

func (w *sessionErrorResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *sessionErrorResponseWriter) fail() {
	if w.failed {
		return
	}
	w.failed = true
	w.ResponseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.ResponseWriter.Header().Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
	_, _ = w.ResponseWriter.Write([]byte("Internal Server Error\n"))
}

// SessionMetadataMiddleware must wrap SCS LoadAndSave.
func SessionMetadataMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadata := &SessionMetadata{}
		ctx := context.WithValue(r.Context(), sessionMetadataContextKey{}, metadata)
		next.ServeHTTP(&sessionErrorResponseWriter{ResponseWriter: w}, r.WithContext(ctx))
	})
}

func sessionMetadata(ctx context.Context) *SessionMetadata {
	metadata, _ := ctx.Value(sessionMetadataContextKey{}).(*SessionMetadata)
	return metadata
}

// SQLiteSessionStore implements both scs.Store and scs.CtxStore. Reads inherit
// request cancellation. Security writes deliberately shed request cancellation
// but remain bounded beyond SQLite's busy timeout.
type SQLiteSessionStore struct {
	db *sql.DB
}

func NewSQLiteSessionStore(db *sql.DB) *SQLiteSessionStore {
	return &SQLiteSessionStore{db: db}
}

func (s *SQLiteSessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *SQLiteSessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	var (
		data              []byte
		userID            sql.NullString
		createdAt         int64
		lastActivityAt    int64
		absoluteExpiresAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT data, user_id, created_at, last_activity_at, absolute_expires_at
		FROM sessions WHERE token_hash = ?`, token,
	).Scan(&data, &userID, &createdAt, &lastActivityAt, &absoluteExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("auth: find session: %w", err)
	}
	if absoluteExpiresAt <= time.Now().Unix() {
		return nil, false, nil
	}
	if metadata := sessionMetadata(ctx); metadata != nil {
		metadata.UserID = userID.String
		metadata.CreatedAt = time.Unix(createdAt, 0).UTC()
		metadata.LastActivityAt = time.Unix(lastActivityAt, 0).UTC()
		metadata.AbsoluteExpiresAt = time.Unix(absoluteExpiresAt, 0).UTC()
	}
	return data, true, nil
}

func (s *SQLiteSessionStore) Commit(token string, data []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, data, expiry)
}

func (s *SQLiteSessionStore) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	writeCtx, cancel := DetachedWriteContext(ctx)
	defer cancel()

	metadata := sessionMetadata(ctx)
	if metadata == nil {
		return errors.New("auth: session metadata middleware is not installed")
	}
	now := time.Now().UTC()
	createdAt := now
	var userID any
	if !metadata.CreatedAt.IsZero() {
		createdAt = metadata.CreatedAt
	}
	if metadata.UserID != "" {
		userID = metadata.UserID
	}
	_, err := s.db.ExecContext(writeCtx, `
		INSERT INTO sessions
			(token_hash, user_id, data, created_at, last_activity_at, absolute_expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(token_hash) DO UPDATE SET
			user_id = excluded.user_id,
			data = excluded.data,
			last_activity_at = excluded.last_activity_at,
			absolute_expires_at = excluded.absolute_expires_at`,
		token, userID, data, createdAt.Unix(), now.Unix(), expiry.UTC().Unix(),
	)
	if err != nil {
		return fmt.Errorf("auth: commit session: %w", err)
	}
	return nil
}

func (s *SQLiteSessionStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

func (s *SQLiteSessionStore) DeleteCtx(ctx context.Context, token string) error {
	writeCtx, cancel := DetachedWriteContext(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(writeCtx, `DELETE FROM sessions WHERE token_hash = ?`, token); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// DetachedWriteContext preserves request values but prevents a disconnected
// client from cancelling a security-critical revocation, session, or audit
// write. The deadline remains bounded beyond SQLite's busy window.
func DetachedWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), appstore.ControlWriteTimeout)
}

type SessionToken string
type SessionTokenDigest string

// SessionTokenHash matches SCS v2.9 HashTokenInStore. Its compatibility is
// covered by an integration test so an upstream format change cannot silently
// orphan activity updates or revocation.
func SessionTokenHash(token SessionToken) SessionTokenDigest {
	digest := sha256.Sum256([]byte(token))
	return SessionTokenDigest(base64.RawURLEncoding.EncodeToString(digest[:]))
}

func (s *SQLiteSessionStore) Touch(ctx context.Context, rawToken SessionToken, before, now time.Time) (bool, error) {
	writeCtx, cancel := DetachedWriteContext(ctx)
	defer cancel()
	result, err := s.db.ExecContext(writeCtx, `
		UPDATE sessions SET last_activity_at = ?
		WHERE token_hash = ? AND last_activity_at < ?`,
		now.UTC().Unix(), SessionTokenHash(rawToken), before.UTC().Unix(),
	)
	if err != nil {
		return false, fmt.Errorf("auth: touch session: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *SQLiteSessionStore) DeleteUserSessions(ctx context.Context, userID string) error {
	writeCtx, cancel := DetachedWriteContext(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(writeCtx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("auth: delete user sessions: %w", err)
	}
	return nil
}

func (s *SQLiteSessionStore) CleanupExpired(ctx context.Context, idleTTL time.Duration, now time.Time) (int64, error) {
	writeCtx, cancel := DetachedWriteContext(ctx)
	defer cancel()
	result, err := s.db.ExecContext(writeCtx, `
		DELETE FROM sessions
		WHERE last_activity_at < ? OR absolute_expires_at <= ?`,
		now.Add(-idleTTL).Unix(), now.Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("auth: cleanup sessions: %w", err)
	}
	return result.RowsAffected()
}

func (s *SQLiteSessionStore) CountStatus(ctx context.Context, idleTTL time.Duration, now time.Time) (active, expired int64, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN last_activity_at >= ? AND absolute_expires_at > ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN last_activity_at < ? OR absolute_expires_at <= ? THEN 1 ELSE 0 END), 0)
		FROM sessions`,
		now.Add(-idleTTL).Unix(), now.Unix(), now.Add(-idleTTL).Unix(), now.Unix(),
	).Scan(&active, &expired)
	if err != nil {
		return 0, 0, fmt.Errorf("auth: count sessions: %w", err)
	}
	return active, expired, nil
}

// BrowserSessions owns every permitted SCS deadline and renewal operation.
type BrowserSessions struct {
	manager            *scs.SessionManager
	store              *SQLiteSessionStore
	IdleTTL            time.Duration
	AbsoluteTTL        time.Duration
	ActivityCheckpoint time.Duration
}

func NewBrowserSessions(db *sql.DB, idleTTL, absoluteTTL time.Duration, secure bool) *BrowserSessions {
	store := NewSQLiteSessionStore(db)
	manager := scs.New()
	manager.Store = store
	manager.Lifetime = absoluteTTL
	manager.IdleTimeout = 0
	manager.HashTokenInStore = true
	manager.Cookie.Name = "fanout_session"
	if secure {
		manager.Cookie.Name = "__Host-fanout_session"
	}
	manager.Cookie.Path = "/"
	manager.Cookie.Domain = ""
	manager.Cookie.HttpOnly = true
	manager.Cookie.Secure = secure
	manager.Cookie.SameSite = http.SameSiteLaxMode
	manager.Cookie.Persist = true
	manager.ErrorFunc = func(w http.ResponseWriter, _ *http.Request, err error) {
		slog.Error("browser session middleware failed", "err", err)
		if guard, ok := w.(*sessionErrorResponseWriter); ok {
			guard.fail()
			return
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}

	checkpoint := idleTTL / 10
	if checkpoint > 5*time.Minute {
		checkpoint = 5 * time.Minute
	}
	return &BrowserSessions{
		manager:            manager,
		store:              store,
		IdleTTL:            idleTTL,
		AbsoluteTTL:        absoluteTTL,
		ActivityCheckpoint: checkpoint,
	}
}

func (s *BrowserSessions) Middleware(next http.Handler) http.Handler {
	return SessionMetadataMiddleware(s.manager.LoadAndSave(next))
}

func (s *BrowserSessions) EstablishAuthenticatedSession(ctx context.Context, user User) error {
	if err := s.manager.RenewToken(ctx); err != nil {
		return fmt.Errorf("auth: renew authenticated session: %w", err)
	}
	now := time.Now().UTC()
	s.manager.Put(ctx, sessionUserIDKey, user.ID)
	s.manager.Put(ctx, sessionAuthVersionKey, user.AuthVersion)
	s.manager.Put(ctx, sessionLastAuthKey, now.Unix())
	s.manager.SetDeadline(ctx, now.Add(s.AbsoluteTTL))
	if metadata := sessionMetadata(ctx); metadata != nil {
		metadata.UserID = user.ID
		metadata.CreatedAt = now
		metadata.LastActivityAt = now
		metadata.AbsoluteExpiresAt = now.Add(s.AbsoluteTTL)
	}
	return nil
}

// BeginOIDCSession creates a short-lived pre-authentication session and stores
// the single-use values needed to validate the callback.
func (s *BrowserSessions) BeginOIDCSession(ctx context.Context, flowTTL, maximum time.Duration, state, nonce, verifier, returnTo string) error {
	if flowTTL <= 0 || flowTTL > maximum {
		return fmt.Errorf("auth: invalid pre-authentication session lifetime")
	}
	// Rotate away from any authenticated session before storing pre-auth state.
	// Clear then prevents identity data from carrying into the new token.
	if err := s.manager.RenewToken(ctx); err != nil {
		return fmt.Errorf("auth: renew pre-authentication session: %w", err)
	}
	if err := s.manager.Clear(ctx); err != nil {
		return fmt.Errorf("auth: clear pre-authentication session: %w", err)
	}
	now := time.Now().UTC()
	s.manager.SetDeadline(ctx, now.Add(flowTTL))
	if metadata := sessionMetadata(ctx); metadata != nil {
		metadata.UserID = ""
		metadata.CreatedAt = now
		metadata.LastActivityAt = now
		metadata.AbsoluteExpiresAt = now.Add(flowTTL)
	}
	s.manager.Put(ctx, "oidc_state", state)
	s.manager.Put(ctx, "oidc_nonce", nonce)
	s.manager.Put(ctx, "oidc_pkce_verifier", verifier)
	if returnTo != "" {
		s.manager.Put(ctx, "return_to", returnTo)
	}
	return nil
}

func (s *BrowserSessions) OIDCFlow(ctx context.Context) (state, nonce, verifier, returnTo string) {
	return s.manager.GetString(ctx, "oidc_state"),
		s.manager.GetString(ctx, "oidc_nonce"),
		s.manager.GetString(ctx, "oidc_pkce_verifier"),
		s.manager.GetString(ctx, "return_to")
}

func (s *BrowserSessions) ClearOIDCFlow(ctx context.Context) {
	for _, key := range []string{"oidc_state", "oidc_nonce", "oidc_pkce_verifier", "return_to"} {
		s.manager.Remove(ctx, key)
	}
}

func (s *BrowserSessions) UserID(ctx context.Context) string {
	return s.manager.GetString(ctx, sessionUserIDKey)
}

func (s *BrowserSessions) AuthVersion(ctx context.Context) int64 {
	return s.manager.GetInt64(ctx, sessionAuthVersionKey)
}

func (s *BrowserSessions) Deadline(ctx context.Context) time.Time {
	return s.manager.Deadline(ctx)
}

func (s *BrowserSessions) Destroy(ctx context.Context) error {
	return s.manager.Destroy(ctx)
}

func (s *BrowserSessions) CountStatus(ctx context.Context, now time.Time) (active, expired int64, err error) {
	return s.store.CountStatus(ctx, s.IdleTTL, now)
}

func (s *BrowserSessions) CleanupExpired(ctx context.Context, now time.Time) (int64, error) {
	return s.store.CleanupExpired(ctx, s.IdleTTL, now)
}

func (s *BrowserSessions) EnforceActivity(ctx context.Context, now time.Time) error {
	metadata := sessionMetadata(ctx)
	if metadata == nil || metadata.CreatedAt.IsZero() || metadata.LastActivityAt.IsZero() || metadata.AbsoluteExpiresAt.IsZero() {
		slog.Error("session metadata middleware is missing or incomplete")
		return ErrSessionExpired
	}
	if now.After(metadata.AbsoluteExpiresAt) || now.Sub(metadata.LastActivityAt) > s.IdleTTL {
		if err := s.Destroy(ctx); err != nil {
			return err
		}
		return ErrSessionExpired
	}
	if now.Sub(metadata.LastActivityAt) < s.ActivityCheckpoint {
		return nil
	}
	rawToken := SessionToken(s.manager.Token(ctx))
	if rawToken == "" {
		slog.Error("loaded session has no raw token")
		return ErrSessionExpired
	}
	_, err := s.store.Touch(ctx, rawToken, now.Add(-s.ActivityCheckpoint), now)
	return err
}

var ErrSessionExpired = errors.New("session expired")
