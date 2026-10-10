package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/panel"
)

type fakeSystem struct {
	req      panel.SystemRequest
	err      error
	deadline time.Time
	canceled bool
	calls    int
}

func (f *fakeSystem) System(ctx context.Context, req panel.SystemRequest) (*panel.Dashboard, error) {
	f.calls++
	f.req = req
	f.deadline, _ = ctx.Deadline()
	f.canceled = ctx.Err() != nil
	return &panel.Dashboard{Name: "System", Panels: []panel.Panel{}}, f.err
}
func systemServer(t *testing.T, engine *fakeSystem) (*testAuthServer, *http.Cookie) {
	t.Helper()
	s := newTestAuthServer(t)
	user, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterSystemRoutes(s.e, engine)
	return s, s.login(t, user)
}
func TestSystemRouteParsesWindowAndSetsNoStore(t *testing.T) {
	f := &fakeSystem{}
	s, cookie := systemServer(t, f)
	for _, path := range []string{"/api/system", "/api/system?range=5m&namespace=shop", "/api/system?from=2026-10-01T12:00:00.000000001Z&to=2026-10-01T13:00:00.000000002Z"} {
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest("GET", path, nil, cookie))
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), `"name":"System"`) {
			t.Fatalf("response=%d %s", rec.Code, rec.Body)
		}
		if f.deadline.IsZero() || time.Until(f.deadline) > panelDeadline {
			t.Fatal("missing request deadline")
		}
		if path == "/api/system" && f.req.Time.Range != "1h" {
			t.Fatal("default range")
		}
		if strings.Contains(path, "namespace") && f.req.Namespace != "shop" {
			t.Fatal("namespace")
		}
		if strings.Contains(path, "from=") && (f.req.Time.From.Nanosecond() != 1 || f.req.Time.To.Nanosecond() != 2 || f.req.Time.Range != "") {
			t.Fatalf("absolute=%+v", f.req)
		}
	}
	for _, query := range []string{"range=", "from=bad&to=bad", "from=2026-10-01T12:00:00Z", "to=2026-10-01T13:00:00Z", "range=1h&from=2026-10-01T12:00:00Z&to=2026-10-01T13:00:00Z", "range=1h&range=5m"} {
		calls := f.calls
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest("GET", "/api/system?"+query, nil, cookie))
		if rec.Code != 400 || f.calls != calls || !strings.Contains(rec.Body.String(), `"code":"invalid_spec"`) {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body)
		}
	}
}
func TestSystemRouteSanitizesFailuresAndPropagatesCancellation(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		body string
	}{{errors.New("private file secret"), 500, "system unavailable"}, {context.DeadlineExceeded, 504, "timeout"}, {context.Canceled, 200, ""}, {panel.Problems{{Path: "time.range", Message: "unsupported range"}}, 400, "invalid_spec"}} {
		f := &fakeSystem{err: tc.err}
		s, cookie := systemServer(t, f)
		rec := httptest.NewRecorder()
		req := sessionRequest("GET", "/api/system", nil, cookie)

		s.e.ServeHTTP(rec, req)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.body) || strings.Contains(rec.Body.String(), "private file") {
			t.Fatalf("error=%v response=%d %s", tc.err, rec.Code, rec.Body)
		}
		if errors.Is(tc.err, context.Canceled) && (rec.Body.Len() != 0) {
			t.Fatal("cancellation not propagated")
		}
	}
}
func TestSystemRouteRequiresActiveSessionAndAllowsReadRoles(t *testing.T) {
	f := &fakeSystem{}
	s, cookie := systemServer(t, f)
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, httptest.NewRequest("GET", "/api/system", nil))
	if rec.Code != 401 {
		t.Fatalf("anonymous=%d", rec.Code)
	}
	for _, role := range []string{"viewer", "operator", "admin"} {
		user, err := s.users.CreateWithAudit(role+"-read@example.com", "", auth.Role(role), auth.AuditEvent{EventType: "user.created", Outcome: "success"})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest("GET", "/api/system", nil, s.login(t, user)))
		if rec.Code != 200 {
			t.Fatalf("%s=%d %s", role, rec.Code, rec.Body)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest(method, "/api/system", nil, cookie))
		if rec.Code != 405 {
			t.Fatalf("%s=%d", method, rec.Code)
		}
	}
	for _, change := range []string{"suspend", "role"} {
		user, err := s.users.CreateWithAudit(change+"@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
		if err != nil {
			t.Fatal(err)
		}
		cookie := s.login(t, user)
		status := auth.UserStatusSuspended
		role := auth.RoleViewer
		if change == "suspend" {
			_, err = s.users.UpdateWithAudit(user.ID, nil, nil, nil, &status, auth.AuditEvent{EventType: "user.updated", Outcome: "success"})
		} else {
			_, err = s.users.UpdateWithAudit(user.ID, nil, nil, &role, nil, auth.AuditEvent{EventType: "user.updated", Outcome: "success"})
		}
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest("GET", "/api/system", nil, cookie))
		if rec.Code != 401 {
			t.Fatalf("%s=%d", change, rec.Code)
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		policy, ok := classifyRoute(method, "/api/system")
		if !ok || policy.capability != ReadTelemetry {
			t.Fatalf("policy %s=%+v", method, policy)
		}
	}
}

func TestSystemHandlerPropagatesCanceledContext(t *testing.T) {
	f := &fakeSystem{err: context.Canceled}
	s, _ := systemServer(t, f)
	req := httptest.NewRequest("GET", "/api/system", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	c := s.e.NewContext(req, rec)
	if err := (&SystemHandler{engine: f}).get(c); err != nil || !f.canceled || rec.Body.Len() != 0 {
		t.Fatalf("cancel=%v body=%s", err, rec.Body)
	}
}
