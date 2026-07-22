package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appstore "github.com/labstack/fanout/internal/store"
)

func newSessionTestStore(t *testing.T) (*appstore.SQLite, *BrowserSessions) {
	t.Helper()
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false)
}

func TestSessionHashAddressesSCSCommittedRow(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("session@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	handler := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EstablishAuthenticatedSession(r.Context(), user); err != nil {
			t.Fatalf("EstablishAuthenticatedSession: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/login", nil))
	response := recorder.Result()
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, SessionTokenHash(cookies[0].Value)).Scan(&count); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if count != 1 {
		t.Fatalf("matching session rows = %d, want 1", count)
	}
}

func TestSessionCookieSecurityModes(t *testing.T) {
	db, _ := newSessionTestStore(t)
	secure := NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, true)
	if secure.Manager.Cookie.Name != "__Host-fanout_session" || !secure.Manager.Cookie.Secure || secure.Manager.Cookie.Path != "/" || !secure.Manager.Cookie.HttpOnly || secure.Manager.Cookie.Domain != "" {
		t.Fatalf("secure cookie configuration = %+v", secure.Manager.Cookie)
	}
	development := NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false)
	if development.Manager.Cookie.Name != "fanout_session" || development.Manager.Cookie.Secure {
		t.Fatalf("development cookie configuration = %+v", development.Manager.Cookie)
	}
}

func TestSessionWriteContextsSurviveRequestCancellation(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sessions.Store.CommitCtx(ctx, "token", []byte("data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CommitCtx with cancelled request: %v", err)
	}
	if err := sessions.Store.DeleteCtx(ctx, "token"); err != nil {
		t.Fatalf("DeleteCtx with cancelled request: %v", err)
	}
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = 'token'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("session rows = %d, want 0", count)
	}
}

func TestSessionFindHonorsRequestCancellation(t *testing.T) {
	_, sessions := newSessionTestStore(t)
	if err := sessions.Store.Commit("token", []byte("data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := sessions.Store.FindCtx(ctx, "token"); err == nil {
		t.Fatal("FindCtx with cancelled request returned nil error")
	}
}

func TestSessionWriteWaitsForSQLiteBusyWindow(t *testing.T) {
	db, err := appstore.NewSQLite(t.TempDir() + "/control.sqlite")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer db.Close()
	conn, err := db.DB.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE: %v", err)
	}
	sessions := NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false)
	done := make(chan error, 1)
	go func() {
		done <- sessions.Store.Commit("contended", []byte("data"), time.Now().Add(time.Hour))
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("contended session write: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("contended session write did not resume after lock release")
	}
}

func TestRoleChangeRevokesSessionsAndIncrementsAuthVersion(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("role@example.com", "", "viewer")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO sessions (token_hash, user_id, data, created_at, last_activity_at, absolute_expires_at) VALUES ('token', ?, X'00', 1, 1, ?)`, user.ID, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	role := "operator"
	updated, err := users.Update(user.ID, nil, nil, &role, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.AuthVersion != user.AuthVersion+1 {
		t.Fatalf("auth version = %d, want %d", updated.AuthVersion, user.AuthVersion+1)
	}
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, user.ID).Scan(&count); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("session rows after role change = %d, want 0", count)
	}
	_ = sessions
}

func TestSessionStatusAndCleanupUseIndexedMetadata(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	rows := []struct {
		token, user string
		activity    time.Time
		expires     time.Time
	}{
		{"active", "", now.Add(-time.Minute), now.Add(time.Hour)},
		{"idle", "", now.Add(-13 * time.Hour), now.Add(time.Hour)},
		{"absolute", "", now.Add(-time.Minute), now.Add(-time.Second)},
	}
	for _, row := range rows {
		if _, err := db.DB.Exec(`INSERT INTO sessions (token_hash, user_id, data, created_at, last_activity_at, absolute_expires_at) VALUES (?, NULL, X'00', ?, ?, ?)`, row.token, now.Add(-time.Hour).Unix(), row.activity.Unix(), row.expires.Unix()); err != nil {
			t.Fatalf("insert %s: %v", row.token, err)
		}
	}
	active, expired, err := sessions.Store.CountStatus(t.Context(), 12*time.Hour, now)
	if err != nil {
		t.Fatalf("CountStatus: %v", err)
	}
	if active != 1 || expired != 2 {
		t.Fatalf("session status = active %d expired %d, want 1/2", active, expired)
	}
	deleted, err := sessions.Store.CleanupExpired(t.Context(), 12*time.Hour, now)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted sessions = %d, want 2", deleted)
	}
}
