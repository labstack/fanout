package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, SessionTokenHash(SessionToken(cookies[0].Value))).Scan(&count); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if count != 1 {
		t.Fatalf("matching session rows = %d, want 1", count)
	}
}

func TestSessionCookieSecurityModes(t *testing.T) {
	db, _ := newSessionTestStore(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("cookie@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, tc := range []struct {
		name, cookieName string
		secure           bool
	}{
		{name: "secure", cookieName: "__Host-fanout_session", secure: true},
		{name: "development", cookieName: "fanout_session", secure: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, tc.secure)
			handler := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := sessions.EstablishAuthenticatedSession(r.Context(), user); err != nil {
					t.Fatalf("EstablishAuthenticatedSession: %v", err)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/login", nil))
			cookie := recorder.Header().Get("Set-Cookie")
			if !strings.HasPrefix(cookie, tc.cookieName+"=") || !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Lax") || !strings.Contains(cookie, "Path=/") {
				t.Fatalf("Set-Cookie = %q", cookie)
			}
			if strings.Contains(cookie, "Domain=") || strings.Contains(cookie, "Secure") != tc.secure {
				t.Fatalf("Set-Cookie security mode = %q", cookie)
			}
		})
	}
}

func sessionStoreTestContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionMetadataContextKey{}, &SessionMetadata{})
}

func TestSessionWriteContextsSurviveRequestCancellation(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sessions.store.CommitCtx(sessionStoreTestContext(ctx), "token", []byte("data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CommitCtx with cancelled request: %v", err)
	}
	if err := sessions.store.DeleteCtx(ctx, "token"); err != nil {
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
	if err := sessions.store.CommitCtx(sessionStoreTestContext(context.Background()), "token", []byte("data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := sessions.store.FindCtx(ctx, "token"); err == nil {
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
		done <- sessions.store.CommitCtx(sessionStoreTestContext(context.Background()), "contended", []byte("data"), time.Now().Add(time.Hour))
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
	role := RoleOperator
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
	active, expired, err := sessions.store.CountStatus(t.Context(), 12*time.Hour, now)
	if err != nil {
		t.Fatalf("CountStatus: %v", err)
	}
	if active != 1 || expired != 2 {
		t.Fatalf("session status = active %d expired %d, want 1/2", active, expired)
	}
	deleted, err := sessions.store.CleanupExpired(t.Context(), 12*time.Hour, now)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted sessions = %d, want 2", deleted)
	}
}

func TestEnforceActivityFailsClosedWithoutMetadata(t *testing.T) {
	_, sessions := newSessionTestStore(t)
	if err := sessions.EnforceActivity(context.Background(), time.Now()); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("EnforceActivity without metadata = %v, want ErrSessionExpired", err)
	}
}

func TestEnforceActivityExpiresIdleSessionAndCheckpointsActivity(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("activity@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	establish := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EstablishAuthenticatedSession(r.Context(), user); err != nil {
			t.Fatalf("EstablishAuthenticatedSession: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	login := httptest.NewRecorder()
	establish.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/login", nil))
	cookie := login.Result().Cookies()[0]
	digest := SessionTokenHash(SessionToken(cookie.Value))
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := db.DB.Exec(`UPDATE sessions SET last_activity_at = ? WHERE token_hash = ?`, now.Add(-10*time.Minute).Unix(), digest); err != nil {
		t.Fatalf("age session: %v", err)
	}
	enforceAt := func(at time.Time) int {
		handler := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := sessions.EnforceActivity(r.Context(), at); err != nil {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	if status := enforceAt(now); status != http.StatusNoContent {
		t.Fatalf("checkpoint request = %d, want 204", status)
	}
	var activity int64
	if err := db.DB.QueryRow(`SELECT last_activity_at FROM sessions WHERE token_hash = ?`, digest).Scan(&activity); err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if activity != now.Unix() {
		t.Fatalf("last activity = %d, want %d", activity, now.Unix())
	}

	if _, err := db.DB.Exec(`UPDATE sessions SET last_activity_at = ? WHERE token_hash = ?`, now.Add(-13*time.Hour).Unix(), digest); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if status := enforceAt(now); status != http.StatusUnauthorized {
		t.Fatalf("idle session request = %d, want 401", status)
	}
	var remaining int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, digest).Scan(&remaining); err != nil {
		t.Fatalf("count expired session: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expired session rows = %d, want 0", remaining)
	}
}

func TestEnforceActivityRejectsAbsoluteExpiry(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("absolute@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	login := httptest.NewRecorder()
	sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EstablishAuthenticatedSession(r.Context(), user); err != nil {
			t.Fatalf("EstablishAuthenticatedSession: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/login", nil))
	cookie := login.Result().Cookies()[0]
	digest := SessionTokenHash(SessionToken(cookie.Value))
	if _, err := db.DB.Exec(`UPDATE sessions SET absolute_expires_at = ? WHERE token_hash = ?`, time.Now().Add(-time.Second).Unix(), digest); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EnforceActivity(r.Context(), time.Now()); !errors.Is(err, ErrSessionExpired) {
			t.Errorf("absolute expiry = %v, want ErrSessionExpired", err)
		}
		w.WriteHeader(http.StatusUnauthorized)
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("absolute-expired session = %d, want 401", recorder.Code)
	}
}

func TestBeginOIDCSessionReplacesAuthenticatedSession(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("reauth@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EstablishAuthenticatedSession(r.Context(), user); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/login", nil))
	oldCookie := login.Result().Cookies()[0]

	started := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/oidc/start", nil)
	request.AddCookie(oldCookie)
	sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.BeginOIDCSession(r.Context(), time.Minute, time.Minute, "state", "nonce", "verifier", ""); err != nil {
			t.Fatal(err)
		}
		if sessions.UserID(r.Context()) != "" {
			t.Fatal("authenticated identity survived pre-authentication renewal")
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(started, request)
	newCookie := started.Result().Cookies()[0]
	if newCookie.Value == oldCookie.Value {
		t.Fatal("OIDC start reused the authenticated session token")
	}
	var oldRows int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, SessionTokenHash(SessionToken(oldCookie.Value))).Scan(&oldRows); err != nil {
		t.Fatal(err)
	}
	if oldRows != 0 {
		t.Fatalf("old authenticated session rows = %d, want 0", oldRows)
	}
	var linkedUsers int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ? AND user_id IS NOT NULL`, SessionTokenHash(SessionToken(newCookie.Value))).Scan(&linkedUsers); err != nil {
		t.Fatal(err)
	}
	if linkedUsers != 0 {
		t.Fatal("pre-authentication session retained user_id metadata")
	}
}

func TestOIDCFlowValuesAreSingleUse(t *testing.T) {
	_, sessions := newSessionTestStore(t)
	begin := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.BeginOIDCSession(r.Context(), time.Minute, time.Minute, "state", "nonce", "verifier", "/dashboards"); err != nil {
			t.Fatalf("BeginOIDCSession: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	started := httptest.NewRecorder()
	begin.ServeHTTP(started, httptest.NewRequest(http.MethodGet, "/start", nil))
	cookie := started.Result().Cookies()[0]

	consume := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, nonce, verifier, returnTo := sessions.OIDCFlow(r.Context())
		if state != "state" || nonce != "nonce" || verifier != "verifier" || returnTo != "/dashboards" {
			t.Fatalf("OIDC flow = %q %q %q %q", state, nonce, verifier, returnTo)
		}
		sessions.ClearOIDCFlow(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	consumed := httptest.NewRecorder()
	consumeRequest := httptest.NewRequest(http.MethodGet, "/callback", nil)
	consumeRequest.AddCookie(cookie)
	consume.ServeHTTP(consumed, consumeRequest)
	updatedCookie := consumed.Result().Cookies()[0]

	verify := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, nonce, verifier, returnTo := sessions.OIDCFlow(r.Context())
		if state != "" || nonce != "" || verifier != "" || returnTo != "" {
			t.Fatalf("consumed OIDC flow retained values: %q %q %q %q", state, nonce, verifier, returnTo)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	verified := httptest.NewRecorder()
	verifyRequest := httptest.NewRequest(http.MethodGet, "/after", nil)
	verifyRequest.AddCookie(updatedCookie)
	verify.ServeHTTP(verified, verifyRequest)
}

func TestSessionCommitFailureSuppressesHandlerSuccessBody(t *testing.T) {
	db, sessions := newSessionTestStore(t)
	users := NewUserStore(db.DB)
	user, err := users.Create("commit-failure@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	handler := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EstablishAuthenticatedSession(r.Context(), user); err != nil {
			t.Fatalf("EstablishAuthenticatedSession: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"authenticated"}`))
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/login", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("commit failure status = %d, want 500", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "authenticated") {
		t.Fatalf("commit failure leaked success body: %q", recorder.Body.String())
	}
}
