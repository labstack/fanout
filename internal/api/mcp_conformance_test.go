package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/dashboard"
	appmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpHTTPFixture struct {
	auth      *MCPAuthorization
	app       *appmcp.Server
	server    *httptest.Server
	state     *testAuthServer
	clientID  string
	token     string
	methodsMu sync.Mutex
	methods   []string
	calls     atomic.Int32
	started   chan struct{}
	cancelled chan error
	release   chan struct{}
}

func newMCPHTTPFixture(t *testing.T) *mcpHTTPFixture {
	t.Helper()
	f := &mcpHTTPFixture{state: newTestAuthServerWith(t, config.Config{PublicURL: "https://fanout.example.com"}, auth.SMTPConfig{}), started: make(chan struct{}, 1), cancelled: make(chan error, 1), release: make(chan struct{})}
	store := auth.NewOAuthStore(f.state.db.DB)
	var err error
	f.auth, err = NewMCPAuthorization(store, f.state.users, testMCPResource)
	if err != nil {
		t.Fatal(err)
	}
	f.auth.Register(f.state.e)
	f.clientID = registerOAuthClient(t, f.state.e).ClientID
	f.token = f.issueToken(t, "interop@example.com", f.clientID)
	f.app = appmcp.NewWithIntelligence(nil, dashboard.New(f.state.db.DB, structuralValidator{}), nil, nil, "test")
	mcp.AddTool(f.app.MCP(), &mcp.Tool{Name: "conformance_probe", Description: "Test probe"}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Wait bool `json:"wait,omitempty"`
	}) (*mcp.CallToolResult, any, error) {
		f.calls.Add(1)
		if input.Wait {
			f.started <- struct{}{}
			select {
			case <-ctx.Done():
			case <-f.release:
				return nil, nil, fmt.Errorf("test tool released")
			}
			f.cancelled <- ctx.Err()
			return nil, nil, ctx.Err()
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	f.state.e.Any("/mcp", echo.WrapHandler(f.auth.ProtectMCP(f.app.HTTPHandler())))
	f.state.e.Any("/api/mcp", f.auth.ProtectBrowserMCP(f.state.sessions, f.app.HTTPHandler()))
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.methodsMu.Lock()
		f.methods = append(f.methods, r.Method+" "+r.Header.Get("Mcp-Method"))
		f.methodsMu.Unlock()
		f.state.e.ServeHTTP(w, r)
	}))
	t.Cleanup(f.server.Close)
	// Release a stalled probe before Close waits for handlers if a cancellation
	// assertion fails. This is cleanup only; success requires ctx cancellation.
	t.Cleanup(func() { close(f.release) })
	return f
}

func (f *mcpHTTPFixture) issueToken(t *testing.T, email, clientID string) string {
	t.Helper()
	user, err := f.state.users.CreateWithAudit(email, "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := f.auth.store.IssueTokenPair(t.Context(), clientID, user.ID, mcpReadScope, testMCPResource)
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
}

type mcpBearerTransport struct{ token string }

func (t mcpBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = "fanout.example.com"
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (f *mcpHTTPFixture) connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "conformance", Version: "test"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{Extensions: map[string]any{
		appmcp.UIExtension: map[string]any{"mimeTypes": []string{appmcp.AppMIME}},
	}}})
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerTransport{f.token}}}, &mcp.ClientSessionOptions{ProtocolVersion: appmcp.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func modernMCPBody(method, params string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},%s}}`, method, params)
}

func (f *mcpHTTPFixture) request(t *testing.T, token, method, body, version, rpcMethod, name string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "fanout.example.com"
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", version)
	r.Header.Set("Mcp-Method", rpcMethod)
	if name != "" {
		r.Header.Set("Mcp-Name", name)
	}
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestMCPModernSDKInteropWithOAuth(t *testing.T) {
	f := newMCPHTTPFixture(t)
	session := f.connect(t)
	if got := session.InitializeResult().ProtocolVersion; got != appmcp.ProtocolVersion {
		t.Fatalf("protocol = %s", got)
	}
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("tools=%+v err=%v", tools, err)
	}
	apps := false
	for _, tool := range tools.Tools {
		if tool.Meta["ui"] != nil {
			apps = true
		}
	}
	if !apps {
		t.Fatal("request-scoped Apps capabilities were lost")
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "conformance_probe"})
	if err != nil || result.IsError || result.Content[0].(*mcp.TextContent).Text != "ok" {
		t.Fatalf("call=%+v err=%v", result, err)
	}
	resource, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "ui://fanout/panels.html"})
	if err != nil || len(resource.Contents) != 1 || resource.Contents[0].MIMEType != appmcp.AppMIME || resource.Contents[0].Text == "" {
		t.Fatalf("resource=%+v err=%v", resource, err)
	}
	_ = session.Close()
	f.methodsMu.Lock()
	defer f.methodsMu.Unlock()
	if len(f.methods) != 4 || f.methods[0] != "POST server/discover" {
		t.Fatalf("methods = %v", f.methods)
	}
	for _, method := range f.methods {
		if !strings.HasPrefix(method, "POST ") || strings.Contains(method, "initialize") {
			t.Fatalf("legacy exchange: %s", method)
		}
	}
}

func TestMCPTrustedAndAbsentOriginsProceedWithAuthentication(t *testing.T) {
	f := newMCPHTTPFixture(t)
	user, err := f.state.users.GetByEmail("interop@example.com")
	if err != nil {
		t.Fatal(err)
	}
	cookie := f.state.login(t, user)
	for _, path := range []string{"/mcp", "/api/mcp"} {
		for _, origin := range []string{"", "https://fanout.example.com"} {
			headers := map[string]string{
				"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
				"MCP-Protocol-Version": appmcp.ProtocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": "conformance_probe",
				"Authorization": "Bearer " + f.token, "Fanout-Request": "1",
			}
			if origin != "" {
				headers["Origin"] = origin
			}
			rec := serve(t, f.state.e, "POST", path, modernMCPBody("tools/call", `"name":"conformance_probe"`), headers, cookie)
			if rec.Code != 200 {
				t.Fatalf("%s origin=%q status=%d body=%s", path, origin, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestMCPRejectsLegacyProtocolAndStreams(t *testing.T) {
	f := newMCPHTTPFixture(t)
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", "2027-01-01"} {
		t.Run(version, func(t *testing.T) {
			rpcBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":%q,"io.modelcontextprotocol/clientCapabilities":{}}}}`, version)
			response := f.request(t, f.token, "POST", rpcBody, version, "server/discover", "")
			var body struct {
				Error *jsonrpc.Error `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 400 || body.Error == nil || body.Error.Code != mcp.CodeUnsupportedProtocolVersion {
				t.Fatalf("status=%d body=%+v", response.StatusCode, body)
			}
			var data mcp.UnsupportedProtocolVersionData
			if err := json.Unmarshal(body.Error.Data, &data); err != nil {
				t.Fatal(err)
			}
			if len(data.Supported) != 1 || data.Supported[0] != appmcp.ProtocolVersion || data.Requested != version {
				t.Fatalf("data=%+v", data)
			}
		})
	}
	for _, method := range []string{"GET", "DELETE"} {
		response := f.request(t, f.token, method, "", appmcp.ProtocolVersion, "", "")
		if response.StatusCode != 405 || response.Header.Get("Allow") != "POST" || response.Header.Get("Mcp-Session-Id") != "" {
			t.Fatalf("%s status=%d headers=%v", method, response.StatusCode, response.Header)
		}
	}
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, modernMCPBody("tools/call", `"name":"conformance_probe"`)} {
		response := f.request(t, f.token, "POST", body, "", "tools/call", "conformance_probe")
		if response.StatusCode != 400 {
			t.Fatalf("headerless status=%d", response.StatusCode)
		}
	}
	response := f.request(t, f.token, "POST", modernMCPBody("initialize", `"protocolVersion":"2026-07-28"`), appmcp.ProtocolVersion, "initialize", "")
	if response.StatusCode != 404 {
		t.Fatalf("modern initialize status=%d", response.StatusCode)
	}
}

func TestMCPDisconnectCancelsToolContext(t *testing.T) {
	f := newMCPHTTPFixture(t)
	session := f.connect(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "conformance_probe", Arguments: map[string]any{"wait": true}})
		done <- err
	}()
	select {
	case <-f.started:
	case err := <-done:
		t.Fatalf("tool never started: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("tool never started")
	}
	cancel()
	select {
	case err := <-f.cancelled:
		if err != context.Canceled {
			t.Fatalf("tool context=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not cancel tool")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("call succeeded after cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not return")
	}
}

func TestMCPToolRateLimitAndPrincipalIsolation(t *testing.T) {
	f := newMCPHTTPFixture(t)
	f.auth.toolLimiter.now = func() time.Time { return time.Unix(100000, 0) }
	body := modernMCPBody("tools/call", `"name":"conformance_probe"`)
	for i := range mcpToolBurst {
		response := f.request(t, f.token, "POST", body, appmcp.ProtocolVersion, "tools/call", "conformance_probe")
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("normal call %d status=%d", i, response.StatusCode)
		}
	}
	limited := f.request(t, f.token, "POST", body, appmcp.ProtocolVersion, "tools/call", "conformance_probe")
	if limited.StatusCode != 429 || limited.Header.Get("Retry-After") != "1" || f.calls.Load() != mcpToolBurst {
		t.Fatalf("status=%d retry=%q executed=%d", limited.StatusCode, limited.Header.Get("Retry-After"), f.calls.Load())
	}
	other := f.issueToken(t, "other@example.com", f.clientID)
	response := f.request(t, other, "POST", body, appmcp.ProtocolVersion, "tools/call", "conformance_probe")
	if response.StatusCode != 200 {
		t.Fatalf("other principal status=%d", response.StatusCode)
	}
	// Discovery and catalogs do not consume the tool budget.
	session := f.connect(t)
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "conformance_probe"}); err == nil || !strings.Contains(err.Error(), "Too Many Requests") {
		t.Fatalf("SDK rate-limit error=%v", err)
	}
	if _, err := session.ListTools(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// The same server's in-memory agent connection has no HTTP limiter.
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := f.app.MCP().Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "fanout-agent", Version: "test"}, nil)
	internal, err := client.Connect(t.Context(), clientTransport, &mcp.ClientSessionOptions{ProtocolVersion: appmcp.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer internal.Close()
	for range mcpToolBurst + 1 {
		result, err := internal.CallTool(t.Context(), &mcp.CallToolParams{Name: "conformance_probe"})
		if err != nil || result.IsError {
			t.Fatalf("agent=%+v err=%v", result, err)
		}
	}
}

func TestMCPHeadersRejectInvalidCallsOnBothMounts(t *testing.T) {
	f := newMCPHTTPFixture(t)
	user, err := f.state.users.GetByEmail("interop@example.com")
	if err != nil {
		t.Fatal(err)
	}
	cookie := f.state.login(t, user)
	body := modernMCPBody("tools/call", `"name":"conformance_probe"`)
	for _, path := range []string{"/mcp", "/api/mcp"} {
		for _, tc := range []struct {
			name, body, method, tool, version string
		}{
			{"method mismatch", body, "tools/list", "conformance_probe", appmcp.ProtocolVersion},
			{"name mismatch", body, "tools/call", "other_probe", appmcp.ProtocolVersion},
			{"dashboard name mismatch", modernMCPBody("tools/call", `"name":"list_dashboards","NAME":"conformance_probe"`), "tools/call", "conformance_probe", appmcp.ProtocolVersion},
			{"missing method", body, "", "conformance_probe", appmcp.ProtocolVersion},
			{"missing name", body, "tools/call", "", appmcp.ProtocolVersion},
			{"shadow method with lying header", strings.Replace(body, `"method":"tools/call"`, `"method":"tools/call","METHOD":"tools/list"`, 1), "tools/list", "conformance_probe", appmcp.ProtocolVersion},
			{"shadow name with lying header", strings.Replace(body, `"name":"conformance_probe"`, `"name":"conformance_probe","NAME":"other_probe"`, 1), "tools/call", "other_probe", appmcp.ProtocolVersion},
			{"missing protocol", body, "", "", ""},
			{"older protocol", body, "", "", "2025-11-25"},
			{"notification cannot call tool", strings.Replace(body, `"id":1,`, "", 1), "", "", appmcp.ProtocolVersion},
			{"batch", "[" + body + "]", "", "", appmcp.ProtocolVersion},
			{"malformed", "{", "tools/call", "conformance_probe", appmcp.ProtocolVersion},
			{"null", "null", "tools/call", "conformance_probe", appmcp.ProtocolVersion},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				headers := map[string]string{
					"Authorization": "Bearer " + f.token, "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
					"MCP-Protocol-Version": tc.version, "Mcp-Method": tc.method, "Mcp-Name": tc.tool,
				}
				if tc.method == "" {
					delete(headers, "Mcp-Method")
				}
				if tc.tool == "" {
					delete(headers, "Mcp-Name")
				}
				if tc.version == "" {
					delete(headers, "MCP-Protocol-Version")
				}
				rec := serve(t, f.state.e, "POST", path, tc.body, headers, cookie)
				if rec.Code != http.StatusBadRequest || f.calls.Load() != 0 {
					t.Fatalf("status=%d executed=%d body=%s", rec.Code, f.calls.Load(), rec.Body.String())
				}
			})
		}
		// Prove valid calls still reach the tool with the same credentials.
		rec := serve(t, f.state.e, "POST", path, body, map[string]string{
			"Authorization": "Bearer " + f.token, "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
			"MCP-Protocol-Version": appmcp.ProtocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": "conformance_probe",
		}, cookie)
		if rec.Code != http.StatusOK || f.calls.Swap(0) != 1 {
			t.Fatalf("valid call status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestMCPShadowKeysCannotBypassScopeOnBothMounts(t *testing.T) {
	// Exercise a browser with telemetry access but no dashboard capability.
	// Current product roles grant both; narrow this test's policy to verify
	// the handler authority as well as the OAuth HTTP scope challenge.
	original := roleCapabilities[auth.RoleAdmin]
	roleCapabilities[auth.RoleAdmin] = maps.Clone(original)
	delete(roleCapabilities[auth.RoleAdmin], ManageOwnDashboards)
	t.Cleanup(func() { roleCapabilities[auth.RoleAdmin] = original })
	f := newMCPHTTPFixture(t)
	user, err := f.state.users.GetByEmail("interop@example.com")
	if err != nil {
		t.Fatal(err)
	}
	cookie := f.state.login(t, user)
	for _, path := range []string{"/mcp", "/api/mcp"} {
		for _, shadows := range []struct{ method, name string }{
			{`,"METHOD":"tools/list"`, ""},
			{"", `,"NAME":"conformance_probe"`},
			{`,"METHOD":"tools/list"`, `,"NAME":"conformance_probe"`},
		} {
			body := modernMCPBody("tools/call", `"name":"list_dashboards"`+shadows.name)
			body = strings.Replace(body, `"method":"tools/call"`, `"method":"tools/call"`+shadows.method, 1)
			rec := serve(t, f.state.e, "POST", path, body, map[string]string{
				"Authorization": "Bearer " + f.token, "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
				"MCP-Protocol-Version": appmcp.ProtocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": "list_dashboards",
			}, cookie)
			if path == "/mcp" {
				if rec.Code != http.StatusForbidden || !strings.Contains(rec.Header().Get("WWW-Authenticate"), `scope="telemetry:read dashboard:manage"`) {
					t.Fatalf("OAuth scope bypass: status=%d challenge=%q body=%s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body.String())
				}
			} else {
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"isError":true`) || !strings.Contains(rec.Body.String(), "dashboard permission is required") {
					t.Fatalf("browser scope bypass: status=%d body=%s", rec.Code, rec.Body.String())
				}
			}
		}
	}
}

func TestMCPShadowKeysCannotBypassExhaustedBucketsOnBothMounts(t *testing.T) {
	f := newMCPHTTPFixture(t)
	user, err := f.state.users.GetByEmail("interop@example.com")
	if err != nil {
		t.Fatal(err)
	}
	cookie := f.state.login(t, user)
	f.auth.toolLimiter.now = func() time.Time { return time.Unix(100000, 0) }
	for _, client := range []string{f.clientID, ""} {
		for range mcpToolBurst {
			if ok, _ := f.auth.toolLimiter.allow(mcpToolKey{user.ID, client}); !ok {
				t.Fatal("burst rejected early")
			}
		}
	}
	for _, path := range []string{"/mcp", "/api/mcp"} {
		for _, shadows := range []struct{ method, name string }{
			{`,"METHOD":"tools/list"`, ""},
			{"", `,"NAME":"other_probe"`},
			{`,"METHOD":"tools/list"`, `,"NAME":"other_probe"`},
		} {
			body := modernMCPBody("tools/call", `"name":"conformance_probe"`+shadows.name)
			body = strings.Replace(body, `"method":"tools/call"`, `"method":"tools/call"`+shadows.method, 1)
			rec := serve(t, f.state.e, "POST", path, body, map[string]string{
				"Authorization": "Bearer " + f.token, "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
				"MCP-Protocol-Version": appmcp.ProtocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": "conformance_probe",
			}, cookie)
			if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" || f.calls.Load() != 0 {
				t.Fatalf("%s: status=%d retry=%q executed=%d body=%s", path, rec.Code, rec.Header().Get("Retry-After"), f.calls.Load(), rec.Body.String())
			}
		}
	}
}
