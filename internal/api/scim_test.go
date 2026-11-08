package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	appstore "github.com/labstack/fanout/internal/store"
)

const (
	testSCIMToken  = "scim-test-token-0123456789abcdef0123"
	testSCIMIssuer = "https://issuer.example"
)

func scimTestConfig() config.Config {
	return config.Config{AuthMode: "oidc", OIDCIssuerURL: testSCIMIssuer, PublicURL: "https://fanout.example.com", SCIMToken: testSCIMToken}
}

type scimServer struct {
	e          *echo.Echo
	users      *auth.UserStore
	identities *auth.IdentityStore
	db         *appstore.SQLite
}

func newSCIMServer(t *testing.T) *scimServer {
	t.Helper()
	cfg := scimTestConfig()
	e, users, _, db := newOAuthTestServerAndDB(t, cfg)
	identities := auth.NewIdentityStore(db.DB)
	RegisterSCIMRoutes(e, cfg, users, identities)
	return &scimServer{e: e, users: users, identities: identities, db: db}
}

func scimHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testSCIMToken, "Content-Type": scimContentType}
}

func (s *scimServer) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, s.e, method, target, body, scimHeaders())
}

func scimUser(externalID, email, role string) string {
	body, _ := json.Marshal(map[string]any{
		"schemas": []string{scimUserSchema}, "externalId": externalID, "userName": email,
		"roles": []map[string]any{{"value": role, "primary": true}},
	})
	return string(body)
}

func scimPatchBody(operations string) string {
	return `{"schemas":["` + scimPatchSchema + `"],"Operations":[` + operations + `]}`
}

func decodeSCIMResource(t *testing.T, rec *httptest.ResponseRecorder) scimUserResource {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != scimContentType {
		t.Fatalf("Content-Type = %q, want %q", got, scimContentType)
	}
	var resource scimUserResource
	if err := json.Unmarshal(rec.Body.Bytes(), &resource); err != nil {
		t.Fatalf("decode resource: %v in %s", err, rec.Body.String())
	}
	return resource
}

func assertSCIMError(t *testing.T, rec *httptest.ResponseRecorder, status int, scimType string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), status)
	}
	if got := rec.Header().Get("Content-Type"); got != scimContentType {
		t.Fatalf("Content-Type = %q, want %q", got, scimContentType)
	}
	var body struct {
		Schemas  []string `json:"schemas"`
		ScimType string   `json:"scimType"`
		Detail   string   `json:"detail"`
		Status   string   `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v in %s", err, rec.Body.String())
	}
	if len(body.Schemas) != 1 || body.Schemas[0] != scimErrorSchema || body.Status != strconv.Itoa(status) || body.Detail == "" || body.ScimType != scimType {
		t.Fatalf("error body = %+v, want status %d scimType %q", body, status, scimType)
	}
}

func (s *scimServer) seed(t *testing.T, externalID, email, role string) scimUserResource {
	t.Helper()
	rec := s.do(t, http.MethodPost, scimUsersPath, scimUser(externalID, email, role))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed %s = %d %s", email, rec.Code, rec.Body.String())
	}
	return decodeSCIMResource(t, rec)
}

func (s *scimServer) auditCount(t *testing.T, eventType, targetID string) int {
	t.Helper()
	var count int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = ? AND target_id = ?`, eventType, targetID).Scan(&count); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	return count
}

func TestSCIMCreateLinksIdentityAndRetrySucceeds(t *testing.T) {
	s := newSCIMServer(t)
	rec := s.do(t, http.MethodPost, scimUsersPath, scimUser("sub-opaque", "Owner@Example.com", "admin"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	created := decodeSCIMResource(t, rec)
	if created.ExternalID != "sub-opaque" || created.UserName != "owner@example.com" || !created.Active || created.Roles[0].Value != "admin" {
		t.Fatalf("created = %+v", created)
	}
	wantLocation := "https://fanout.example.com/scim/v2/Users/" + created.ID
	if rec.Header().Get("Location") != wantLocation || created.Meta.Location != wantLocation || created.Meta.ResourceType != "User" {
		t.Fatalf("location header %q meta %+v", rec.Header().Get("Location"), created.Meta)
	}
	identity, err := s.identities.Find(t.Context(), testSCIMIssuer, "sub-opaque")
	if err != nil || identity.UserID != created.ID {
		t.Fatalf("identity = %+v err=%v", identity, err)
	}

	retry := s.do(t, http.MethodPost, scimUsersPath, scimUser("sub-opaque", "owner@example.com", "admin"))
	if retry.Code != http.StatusOK || decodeSCIMResource(t, retry).ID != created.ID {
		t.Fatalf("retry = %d %s", retry.Code, retry.Body.String())
	}
	if got := s.auditCount(t, "user.provisioned", created.ID); got != 1 {
		t.Fatalf("user.provisioned events = %d, want 1", got)
	}
	if got := s.auditCount(t, "identity.linked", identity.ID); got != 1 {
		t.Fatalf("identity.linked events = %d, want 1", got)
	}
}

func TestSCIMCreateValidation(t *testing.T) {
	s := newSCIMServer(t)
	tests := map[string]struct {
		body     string
		scimType string
	}{
		"not json":          {`{`, "invalidSyntax"},
		"trailing content":  {scimUser("a", "a@example.com", "admin") + `{}`, "invalidSyntax"},
		"missing schema":    {`{"externalId":"a","userName":"a@example.com","roles":[{"value":"admin"}]}`, "invalidSyntax"},
		"missing external":  {`{"schemas":["` + scimUserSchema + `"],"userName":"a@example.com","roles":[{"value":"admin"}]}`, "invalidValue"},
		"long external":     {scimUser(strings.Repeat("x", 256), "a@example.com", "admin"), "invalidValue"},
		"invalid email":     {scimUser("a", "not-an-email", "admin"), "invalidValue"},
		"missing role":      {`{"schemas":["` + scimUserSchema + `"],"externalId":"a","userName":"a@example.com"}`, "invalidValue"},
		"unknown role":      {scimUser("a", "a@example.com", "owner"), "invalidValue"},
		"disagreeing email": {`{"schemas":["` + scimUserSchema + `"],"externalId":"a","userName":"a@example.com","emails":[{"value":"b@example.com"}],"roles":[{"value":"admin"}]}`, "invalidValue"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assertSCIMError(t, s.do(t, http.MethodPost, scimUsersPath, tc.body), http.StatusBadRequest, tc.scimType)
		})
	}
	headers := scimHeaders()
	headers["Content-Type"] = "text/plain"
	assertSCIMError(t, serve(t, s.e, http.MethodPost, scimUsersPath, scimUser("a", "a@example.com", "admin"), headers), http.StatusUnsupportedMediaType, "")
	assertSCIMError(t, s.do(t, http.MethodPost, scimUsersPath, `{"schemas":["`+strings.Repeat("x", userBodyLimit)+`"]}`), http.StatusRequestEntityTooLarge, "")

	headers["Content-Type"] = "application/json; charset=utf-8"
	rec := serve(t, s.e, http.MethodPost, scimUsersPath, scimUser("json", "json@example.com", "admin"), headers)
	if rec.Code != http.StatusCreated {
		t.Fatalf("application/json create = %d %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMZeroUsersOnlyAdministrator(t *testing.T) {
	s := newSCIMServer(t)
	for _, role := range []string{"viewer", "operator"} {
		assertSCIMError(t, s.do(t, http.MethodPost, scimUsersPath, scimUser("early-"+role, role+"@example.com", role)), http.StatusConflict, "")
	}
	s.seed(t, "owner", "owner@example.com", "admin")
	viewer := s.seed(t, "member", "member@example.com", "viewer")
	if viewer.Roles[0].Value != "viewer" {
		t.Fatalf("viewer role = %+v", viewer.Roles)
	}
}

func TestSCIMConcurrentAdministratorsOnEmptyDatabase(t *testing.T) {
	db, err := appstore.NewSQLite(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := scimTestConfig()
	users := auth.NewUserStore(db.DB)
	sessions := auth.NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false)
	e := echo.New()
	e.HTTPErrorHandler = HTTPErrorHandler
	RegisterAuthMiddleware(e, users, sessions, auth.NewAuditStore(db.DB), cfg)
	RegisterSCIMRoutes(e, cfg, users, auth.NewIdentityStore(db.DB))

	const requests = 10
	start := make(chan struct{})
	codes := make(chan string, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, scimUsersPath, strings.NewReader(scimUser(fmt.Sprintf("sub-%d", i), fmt.Sprintf("admin-%d@example.com", i), "admin")))
			for key, value := range scimHeaders() {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			codes <- strconv.Itoa(rec.Code) + " " + rec.Body.String()
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	for result := range codes {
		if !strings.HasPrefix(result, "201 ") {
			t.Fatalf("concurrent administrator = %s, want 201", result)
		}
	}
	count, err := users.CountActiveAdmins()
	if err != nil || count != requests {
		t.Fatalf("active administrators = %d err=%v, want %d", count, err, requests)
	}
}

func TestSCIMEmailHeldByDifferentAccountConflicts(t *testing.T) {
	s := newSCIMServer(t)
	local, err := s.users.CreateWithAudit("taken@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	assertSCIMError(t, s.do(t, http.MethodPost, scimUsersPath, scimUser("sub-1", "taken@example.com", "viewer")), http.StatusConflict, "uniqueness")
	if count, err := s.identities.CountForUser(t.Context(), local.ID); err != nil || count != 0 {
		t.Fatalf("local account linked by email: %d %v", count, err)
	}

	other := s.seed(t, "sub-2", "other@example.com", "viewer")
	body := scimPatchBody(`{"op":"replace","path":"userName","value":"taken@example.com"}`)
	assertSCIMError(t, s.do(t, http.MethodPatch, scimUsersPath+"/"+other.ID, body), http.StatusConflict, "uniqueness")
}

func TestSCIMFilterFindsAccounts(t *testing.T) {
	s := newSCIMServer(t)
	escaped := "tenant\"a\\b/é"
	owner := s.seed(t, escaped, "owner@example.com", "admin")
	local, err := s.users.CreateWithAudit("local@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	list := func(filter string) (scimListResult, int) {
		rec := s.do(t, http.MethodGet, scimUsersPath+"?filter="+url.QueryEscape(filter), "")
		if rec.Header().Get("Content-Type") != scimContentType {
			t.Fatalf("Content-Type = %q", rec.Header().Get("Content-Type"))
		}
		var out scimListResult
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out, rec.Code
	}
	tests := []struct {
		filter string
		wantID string
	}{
		{`externalId eq "tenant\"a\\b/é"`, owner.ID},
		{`externalId eq "tenant\"a\\b\/é"`, owner.ID},
		{`EXTERNALID EQ "tenant\"a\\b/é"`, owner.ID},
		{`userName eq "OWNER@example.com"`, owner.ID},
		{`userName eq "local@example.com"`, local.ID},
		{`externalId eq "missing"`, ""},
		{`externalId eq "TENANT\"A\\B/é"`, ""},
		{`userName eq "not-an-email"`, ""},
	}
	for _, tc := range tests {
		out, code := list(tc.filter)
		if code != http.StatusOK {
			t.Fatalf("filter %s = %d", tc.filter, code)
		}
		if tc.wantID == "" {
			if out.TotalResults != 0 || len(out.Resources) != 0 {
				t.Fatalf("filter %s = %+v, want empty", tc.filter, out)
			}
			continue
		}
		if out.TotalResults != 1 || len(out.Resources) != 1 || out.Resources[0].ID != tc.wantID || out.Schemas[0] != scimListSchema {
			t.Fatalf("filter %s = %+v, want %s", tc.filter, out, tc.wantID)
		}
	}
	if out, _ := list(`userName eq "local@example.com"`); out.Resources[0].ExternalID != "" {
		t.Fatalf("local account reports externalId %q", out.Resources[0].ExternalID)
	}

	rec := s.do(t, http.MethodGet, scimUsersPath+"?filter="+url.QueryEscape(`externalId eq "tenant\"a\\b/é"`)+"&count=0", "")
	var counted scimListResult
	_ = json.Unmarshal(rec.Body.Bytes(), &counted)
	if counted.TotalResults != 1 || len(counted.Resources) != 0 {
		t.Fatalf("count=0 = %+v", counted)
	}
	assertSCIMError(t, s.do(t, http.MethodGet, scimUsersPath+"?filter="+url.QueryEscape(`externalId eq "x"`)+"&count=abc", ""), http.StatusBadRequest, "invalidValue")
}

type scimListResult struct {
	Schemas      []string           `json:"schemas"`
	TotalResults int                `json:"totalResults"`
	Resources    []scimUserResource `json:"Resources"`
}

func TestSCIMRejectsUnsupportedFilters(t *testing.T) {
	s := newSCIMServer(t)
	for _, filter := range []string{
		``,
		`externalId`,
		`externalId eq`,
		`externalId ne "x"`,
		`externalId co "x"`,
		`externalId pr`,
		`emails eq "x"`,
		`id eq "x"`,
		`active eq true`,
		`externalId eq x`,
		`externalId eq "x`,
		`externalId eq "x" and userName eq "y"`,
		`externalId eq "x" or externalId eq "y"`,
		`not (externalId eq "x")`,
		`externalId eq "x"junk`,
		`externalId eq "\q"`,
		"externalId eq \"\xff\"",
	} {
		target := scimUsersPath
		if filter != "" {
			target += "?filter=" + url.QueryEscape(filter)
		}
		assertSCIMError(t, s.do(t, http.MethodGet, target, ""), http.StatusBadRequest, "invalidFilter")
	}
}

func TestSCIMParseFilterDecodesJSONString(t *testing.T) {
	attribute, value, ok := parseSCIMFilter(`externalId eq "a\"b\\cA d"`)
	if !ok || attribute != "externalid" || value != `a"b\cA d` {
		t.Fatalf("parse = %q %q %v", attribute, value, ok)
	}
}

func TestSCIMPatchUpdatesFieldsAndIsRepeatable(t *testing.T) {
	s := newSCIMServer(t)
	owner := s.seed(t, "owner", "owner@example.com", "admin")
	member := s.seed(t, "member", "member@example.com", "viewer")
	path := scimUsersPath + "/" + member.ID

	tests := []struct {
		name  string
		body  string
		check func(scimUserResource) bool
		event string
	}{
		{"deactivate", scimPatchBody(`{"op":"replace","path":"active","value":false}`), func(r scimUserResource) bool { return !r.Active }, "user.suspended"},
		{"reactivate as string", scimPatchBody(`{"op":"Replace","path":"active","value":"True"}`), func(r scimUserResource) bool { return r.Active }, "user.reactivated"},
		{"role", scimPatchBody(`{"op":"replace","path":"roles","value":[{"value":"operator","primary":true}]}`), func(r scimUserResource) bool { return r.Roles[0].Value == "operator" }, "role.changed"},
		{"email by userName", scimPatchBody(`{"op":"replace","path":"userName","value":"New@Example.com"}`), func(r scimUserResource) bool {
			return r.UserName == "new@example.com" && r.Emails[0].Value == "new@example.com"
		}, "user.updated"},
		{"email by emails", scimPatchBody(`{"op":"replace","path":"emails","value":[{"value":"newer@example.com","primary":true}]}`), func(r scimUserResource) bool { return r.UserName == "newer@example.com" }, "user.updated"},
		{"pathless", scimPatchBody(`{"op":"replace","value":{"active":false,"roles":[{"value":"viewer"}]}}`), func(r scimUserResource) bool { return !r.Active && r.Roles[0].Value == "viewer" }, "user.suspended"},
		{"urn path", scimPatchBody(`{"op":"replace","path":"urn:ietf:params:scim:schemas:core:2.0:User:active","value":true}`), func(r scimUserResource) bool { return r.Active }, "user.reactivated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := s.auditCount(t, tc.event, member.ID)
			rec := s.do(t, http.MethodPatch, path, tc.body)
			if rec.Code != http.StatusOK || !tc.check(decodeSCIMResource(t, rec)) {
				t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
			}
			if got := s.auditCount(t, tc.event, member.ID); got != before+1 {
				t.Fatalf("%s events = %d, want %d", tc.event, got, before+1)
			}
			repeat := s.do(t, http.MethodPatch, path, tc.body)
			if repeat.Code != http.StatusOK || !tc.check(decodeSCIMResource(t, repeat)) {
				t.Fatalf("repeat = %d %s", repeat.Code, repeat.Body.String())
			}
			if got := s.auditCount(t, tc.event, member.ID); got != before+1 {
				t.Fatalf("repeat wrote an audit event: %d", got)
			}
		})
	}

	identity, err := s.identities.Find(t.Context(), testSCIMIssuer, "member")
	if err != nil || identity.UserID != member.ID {
		t.Fatalf("identity lost: %+v err=%v", identity, err)
	}
	rec := s.do(t, http.MethodGet, path, "")
	if got := decodeSCIMResource(t, rec); got.ExternalID != "member" {
		t.Fatalf("GET = %+v", got)
	}
	if got := s.auditCount(t, "user.suspended", owner.ID); got != 0 {
		t.Fatalf("unrelated account audited: %d", got)
	}
}

func TestSCIMPatchValidation(t *testing.T) {
	s := newSCIMServer(t)
	s.seed(t, "owner", "owner@example.com", "admin")
	member := s.seed(t, "member", "member@example.com", "viewer")
	path := scimUsersPath + "/" + member.ID
	tests := map[string]struct {
		body     string
		status   int
		scimType string
	}{
		"missing schema":      {`{"Operations":[{"op":"replace","path":"active","value":false}]}`, 400, "invalidSyntax"},
		"no operations":       {scimPatchBody(``), 400, "invalidValue"},
		"unknown op":          {scimPatchBody(`{"op":"move","path":"active","value":false}`), 400, "invalidSyntax"},
		"remove":              {scimPatchBody(`{"op":"remove","path":"displayName"}`), 400, "mutability"},
		"unknown path":        {scimPatchBody(`{"op":"replace","path":"title","value":"x"}`), 400, "invalidPath"},
		"filtered path":       {scimPatchBody(`{"op":"replace","path":"emails[type eq \"work\"].value","value":"x@example.com"}`), 400, "invalidPath"},
		"bad active":          {scimPatchBody(`{"op":"replace","path":"active","value":"maybe"}`), 400, "invalidValue"},
		"bad role":            {scimPatchBody(`{"op":"replace","path":"roles","value":[{"value":"owner"}]}`), 400, "invalidValue"},
		"many roles":          {scimPatchBody(`{"op":"replace","path":"roles","value":[{"value":"admin"},{"value":"viewer"}]}`), 400, "invalidValue"},
		"bad email":           {scimPatchBody(`{"op":"replace","path":"userName","value":"nope"}`), 400, "invalidValue"},
		"disagreeing emails":  {scimPatchBody(`{"op":"replace","path":"userName","value":"a@example.com"},{"op":"replace","path":"emails","value":[{"value":"b@example.com"}]}`), 400, "invalidValue"},
		"external change":     {scimPatchBody(`{"op":"replace","path":"externalId","value":"other"}`), 400, "mutability"},
		"pathless non-object": {scimPatchBody(`{"op":"replace","value":false}`), 400, "invalidValue"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assertSCIMError(t, s.do(t, http.MethodPatch, path, tc.body), tc.status, tc.scimType)
		})
	}
	ok := s.do(t, http.MethodPatch, path, scimPatchBody(`{"op":"replace","path":"externalId","value":"member"}`))
	if ok.Code != http.StatusOK {
		t.Fatalf("restating externalId = %d %s", ok.Code, ok.Body.String())
	}
	assertSCIMError(t, s.do(t, http.MethodPatch, scimUsersPath+"/missing", scimPatchBody(`{"op":"replace","path":"active","value":false}`)), http.StatusNotFound, "")
	assertSCIMError(t, s.do(t, http.MethodGet, scimUsersPath+"/missing", ""), http.StatusNotFound, "")
}

func TestSCIMLastActiveAdministratorIsProtected(t *testing.T) {
	s := newSCIMServer(t)
	owner := s.seed(t, "owner", "owner@example.com", "admin")
	path := scimUsersPath + "/" + owner.ID
	assertSCIMError(t, s.do(t, http.MethodPatch, path, scimPatchBody(`{"op":"replace","path":"active","value":false}`)), http.StatusConflict, "")
	assertSCIMError(t, s.do(t, http.MethodPatch, path, scimPatchBody(`{"op":"replace","path":"roles","value":[{"value":"viewer"}]}`)), http.StatusConflict, "")
	// The final state counts: demotion plus reactivation in one request is
	// still a demotion of the last administrator.
	assertSCIMError(t, s.do(t, http.MethodPatch, path, scimPatchBody(`{"op":"replace","path":"roles","value":[{"value":"viewer"}]},{"op":"replace","path":"active","value":true}`)), http.StatusConflict, "")

	second := s.seed(t, "second", "second@example.com", "admin")
	if rec := s.do(t, http.MethodPatch, path, scimPatchBody(`{"op":"replace","path":"active","value":false}`)); rec.Code != http.StatusOK {
		t.Fatalf("deactivate with a second administrator = %d %s", rec.Code, rec.Body.String())
	}
	assertSCIMError(t, s.do(t, http.MethodPatch, scimUsersPath+"/"+second.ID, scimPatchBody(`{"op":"replace","path":"active","value":false}`)), http.StatusConflict, "")
}

func TestSCIMDeactivationEndsBrowserAndMCPAccess(t *testing.T) {
	s := newSCIMServer(t)
	s.seed(t, "owner", "owner@example.com", "admin")
	member := s.seed(t, "member", "member@example.com", "admin")
	user, err := s.users.GetByID(member.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	cookie := oauthCookieForUser(t, s.e, user)
	if rec := serve(t, s.e, http.MethodGet, "/api/auth/me", "", nil, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("session before deactivation = %d", rec.Code)
	}

	client := registerOAuthClient(t, s.e)
	verifier, challenge := pkcePair()
	code := approveAndGetCode(t, s.e, authorizeParams(client.ClientID, mcpReadScope, challenge), cookie)
	tokens := decodeTokens(t, exchangeCode(t, s.e, client.ClientID, code, testRedirectURI, verifier))
	access, refresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	bearer := map[string]string{"Authorization": "Bearer " + access}
	if rec := serve(t, s.e, http.MethodPost, "/mcp", testReadMCPCall, bearer); rec.Code != http.StatusNoContent {
		t.Fatalf("MCP before deactivation = %d %s", rec.Code, rec.Body.String())
	}
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {refresh}}.Encode()

	// A second grant is checked only after reactivation. Presenting a refresh
	// token while suspended revokes its family by itself, which would hide a
	// missing revocation at the moment of deactivation.
	secondVerifier, secondChallenge := pkcePair()
	secondCode := approveAndGetCode(t, s.e, authorizeParams(client.ClientID, mcpReadScope, secondChallenge), cookie)
	second := decodeTokens(t, exchangeCode(t, s.e, client.ClientID, secondCode, testRedirectURI, secondVerifier))
	secondBearer := map[string]string{"Authorization": "Bearer " + second["access_token"].(string)}
	secondRefreshForm := url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {second["refresh_token"].(string)}}.Encode()

	assertTokensRejected := func(stage string, accessHeaders map[string]string, form string) {
		t.Helper()
		if rec := serve(t, s.e, http.MethodPost, "/mcp", testReadMCPCall, accessHeaders); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: MCP access token = %d %s, want 401", stage, rec.Code, rec.Body.String())
		}
		rec := serve(t, s.e, http.MethodPost, "/oauth/token", form, formHeaders)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_grant") {
			t.Fatalf("%s: refresh = %d %s, want invalid_grant", stage, rec.Code, rec.Body.String())
		}
	}
	assertSessionEnded := func(stage string) {
		t.Helper()
		if rec := serve(t, s.e, http.MethodGet, "/api/auth/me", "", nil, cookie); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: session = %d, want 401", stage, rec.Code)
		}
	}

	path := scimUsersPath + "/" + member.ID
	if rec := s.do(t, http.MethodPatch, path, scimPatchBody(`{"op":"replace","path":"active","value":false}`)); rec.Code != http.StatusOK {
		t.Fatalf("deactivate = %d %s", rec.Code, rec.Body.String())
	}
	assertSessionEnded("after deactivation")
	assertTokensRejected("after deactivation", bearer, refreshForm)

	if rec := s.do(t, http.MethodPatch, path, scimPatchBody(`{"op":"replace","path":"active","value":true}`)); rec.Code != http.StatusOK {
		t.Fatalf("reactivate = %d %s", rec.Code, rec.Body.String())
	}
	assertSessionEnded("after reactivation")
	assertTokensRejected("after reactivation", bearer, refreshForm)
	assertTokensRejected("after reactivation, unused grant", secondBearer, secondRefreshForm)
	reactivated, err := s.users.GetByID(member.ID)
	if err != nil || reactivated.Status != auth.UserStatusActive {
		t.Fatalf("reactivated = %+v err=%v", reactivated, err)
	}
	if identity, err := s.identities.Find(t.Context(), testSCIMIssuer, "member"); err != nil || identity.UserID != member.ID {
		t.Fatalf("identity after reactivation: %+v err=%v", identity, err)
	}
}

func TestSCIMFirstLoginUsesLinkedIdentityWithoutEmailMatch(t *testing.T) {
	s := newSCIMServer(t)
	owner := s.seed(t, "portal-subject", "owner@example.com", "admin")
	handler := &OIDCHandler{
		cfg:        config.Config{OIDCEmailVerification: "required"},
		users:      s.users,
		identities: s.identities,
		audit:      auth.NewAuditStore(s.db.DB),
	}
	verified := true
	claims := oidcClaims{Subject: "portal-subject", Email: "different@example.com", EmailVerified: &verified}
	user, identity, err := handler.resolveUser(t.Context(), testSCIMIssuer, claims, requestSource{})
	if err != nil || user.ID != owner.ID || identity.UserID != owner.ID {
		t.Fatalf("resolveUser = %+v %+v err=%v", user, identity, err)
	}
}

func TestSCIMRequiresToken(t *testing.T) {
	s := newSCIMServer(t)
	tests := map[string]map[string]string{
		"missing":      {},
		"wrong":        {"Authorization": "Bearer wrong-token"},
		"wrong scheme": {"Authorization": "Basic " + testSCIMToken},
		"prefix":       {"Authorization": "Bearer " + testSCIMToken[:10]},
	}
	for name, headers := range tests {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, s.e, http.MethodGet, scimUsersPath+"?filter="+url.QueryEscape(`externalId eq "x"`), "", headers)
			assertSCIMError(t, rec, http.StatusUnauthorized, "")
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q", rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
	rec := serve(t, s.e, http.MethodGet, scimUsersPath+"?filter="+url.QueryEscape(`externalId eq "x"`), "", map[string]string{"Authorization": "bearer " + testSCIMToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("lowercase scheme = %d %s", rec.Code, rec.Body.String())
	}
	for name, tc := range map[string]struct {
		method, path string
		status       int
	}{
		"method":        {http.MethodDelete, scimUsersPath + "/x", http.StatusMethodNotAllowed},
		"unknown route": {http.MethodGet, scimRoot + "/Groups", http.StatusNotFound},
		"trailing path": {http.MethodGet, scimUsersPath + "/x/y", http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			assertSCIMError(t, s.do(t, tc.method, tc.path, ""), tc.status, "")
		})
	}
}

func TestSCIMTokenComparison(t *testing.T) {
	if !scimTokenMatches("abc", "abc") {
		t.Fatal("equal tokens must match")
	}
	for _, presented := range []string{"", "ab", "abd", "abcd"} {
		if scimTokenMatches(presented, "abc") {
			t.Fatalf("%q must not match", presented)
		}
	}
}

func TestSCIMDoesNotLogToken(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	s := newSCIMServer(t)
	s.seed(t, "owner", "owner@example.com", "admin")
	serve(t, s.e, http.MethodGet, scimUsersPath, "", map[string]string{"Authorization": "Bearer " + testSCIMToken + "x"})
	if _, err := s.db.DB.Exec(`ALTER TABLE users RENAME TO users_offline`); err != nil {
		t.Fatalf("hide users: %v", err)
	}
	assertSCIMError(t, s.do(t, http.MethodPost, scimUsersPath, scimUser("a", "a@example.com", "admin")), http.StatusInternalServerError, "")
	if strings.Contains(logs.String(), testSCIMToken) {
		t.Fatalf("token reached the log: %s", logs.String())
	}
}

func TestSCIMRoutesAbsentWithoutToken(t *testing.T) {
	cfg := scimTestConfig()
	cfg.SCIMToken = ""
	e, users, _, db := newOAuthTestServerAndDB(t, cfg)
	RegisterSCIMRoutes(e, cfg, users, auth.NewIdentityStore(db.DB))
	e.GET("/*", func(c *echo.Context) error { return c.String(http.StatusOK, "SPA") })

	bearer := map[string]string{"Authorization": "Bearer " + testSCIMToken, "Content-Type": scimContentType}
	for _, path := range []string{scimUsersPath, scimUsersPath + "/id", scimRoot + "/Groups"} {
		rec := serve(t, e, http.MethodGet, path, "", bearer)
		if rec.Code != http.StatusOK || rec.Body.String() != "SPA" {
			t.Fatalf("GET %s = %d %s, want the SPA", path, rec.Code, rec.Body.String())
		}
		rec = serve(t, e, http.MethodPost, path, "{}", bearer)
		if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Body.String(), `"code"`) || rec.Header().Get("Content-Type") == scimContentType {
			t.Fatalf("POST %s = %d %s %q, want the product 405", path, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
		}
	}
	var created int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&created); err != nil || created != 0 {
		t.Fatalf("users = %d err=%v", created, err)
	}
}

func TestSCIMTokenDisablesBrowserSetup(t *testing.T) {
	s := newTestAuthServerWith(t, scimTestConfig(), auth.SMTPConfig{})
	body := strings.NewReader(`{"email":"first@example.com","setup_token":"` + s.setupToken + `"}`)
	req := sessionRequest(http.MethodPost, "/api/auth/setup", body, nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("setup = %d %s, want 410", rec.Code, rec.Body.String())
	}
	count, err := s.users.CountUsers()
	if err != nil || count != 0 {
		t.Fatalf("users = %d err=%v", count, err)
	}
	statusRec := httptest.NewRecorder()
	s.e.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	var status struct {
		SetupRequired bool `json:"setup_required"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil || status.SetupRequired {
		t.Fatalf("status = %s err=%v, want setup_required false", statusRec.Body.String(), err)
	}
}
