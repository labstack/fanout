package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/auth"
)

type alertEmailServer struct {
	*testAuthServer
	store  *alert.Store
	cookie *http.Cookie
}

func newAlertEmailServer(t *testing.T, smtp bool) *alertEmailServer {
	t.Helper()
	s := newTestAuthServer(t)
	store := alert.NewStore(s.db.DB)
	engine := alert.NewEngine(store, nil, nil, 30*time.Second, 7)
	if smtp {
		engine.EnableEmail(alert.EmailConfig{Send: func(context.Context, auth.AlertMail) error { return nil }, Defaults: []string{"oncall@example.com"}, HourlyLimit: 30})
	}
	RegisterAlertRoutes(s.e, store, engine)
	admin, err := s.users.CreateWithAudit("alert-admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	return &alertEmailServer{testAuthServer: s, store: store, cookie: s.login(t, admin)}
}

func (s *alertEmailServer) do(method, path, body string) *httptest.ResponseRecorder {
	req := sessionRequest(method, path, strings.NewReader(body), s.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func ruleBody(recipients string) string {
	return `{"name":"Errors","enabled":true,"expression":"error_rate > 0.05","email_recipients":` + recipients + `}`
}

func TestAlertRuleEmailRecipientsAreValidatedAndNormalized(t *testing.T) {
	s := newAlertEmailServer(t, true)
	rec := s.do(http.MethodPost, "/api/alerting/rules", ruleBody(`[" A@Example.com ","a@example.com","b@example.com"]`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created alert.Rule
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.EmailRecipients) != 2 || created.EmailRecipients[0] != "a@example.com" {
		t.Fatalf("recipients = %v", created.EmailRecipients)
	}

	for name, recipients := range map[string]string{
		"not an address": `["nope"]`,
		"display name":   `["Eve <e@example.com>"]`,
		"line break":     `["a@example.com\r\nBcc: x@evil.test"]`,
		"over the cap":   `["a0@e.co","a1@e.co","a2@e.co","a3@e.co","a4@e.co","a5@e.co","a6@e.co","a7@e.co","a8@e.co","a9@e.co","a10@e.co"]`,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := s.do(http.MethodPost, "/api/alerting/rules", ruleBody(recipients)); rec.Code != http.StatusBadRequest {
				t.Fatalf("create = %d %s, want 400", rec.Code, rec.Body)
			}
			if rec := s.do(http.MethodPut, "/api/alerting/rules/"+created.ID, ruleBody(recipients)); rec.Code != http.StatusBadRequest {
				t.Fatalf("replace = %d %s, want 400", rec.Code, rec.Body)
			}
		})
	}
}

func TestAlertRuleClientCannotWriteEmailStatus(t *testing.T) {
	s := newAlertEmailServer(t, true)
	rec := s.do(http.MethodPost, "/api/alerting/rules", `{"name":"Errors","enabled":true,"expression":"true","email_recipients":["a@example.com"],"email_status":"sent","email_status_detail":"forged"}`)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "forged") {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
}

func TestEmailOptionIsUnavailableWithoutSMTP(t *testing.T) {
	s := newAlertEmailServer(t, false)
	rec := s.do(http.MethodPost, "/api/alerting/rules", ruleBody(`["a@example.com"]`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create = %d %s, want 400", rec.Code, rec.Body)
	}
	var body struct{ Code, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "email_unavailable" || !strings.Contains(body.Message, "SMTP is not configured") {
		t.Fatalf("error = %+v", body)
	}

	options := s.do(http.MethodGet, "/api/alerting/email", "")
	var got alert.EmailOptions
	if err := json.Unmarshal(options.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if options.Code != http.StatusOK || got.Available || !strings.Contains(got.Message, "SMTP") || got.DefaultDestination {
		t.Fatalf("options = %d %s", options.Code, options.Body)
	}
	if rec := s.do(http.MethodPost, "/api/alerting/rules", `{"name":"Hook","enabled":true,"expression":"true","webhook_url":"https://example.com/hook"}`); rec.Code != http.StatusCreated {
		t.Fatalf("webhook rule = %d %s", rec.Code, rec.Body)
	}
}

func TestEmailOptionsReportTheDefaultDestinationWithoutItsAddress(t *testing.T) {
	s := newAlertEmailServer(t, true)
	rec := s.do(http.MethodGet, "/api/alerting/email", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "oncall@") {
		t.Fatalf("options = %d %s", rec.Code, rec.Body)
	}
	var got alert.EmailOptions
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || !got.DefaultDestination || got.MaxRecipients != auth.MaxAlertEmailRecipients || got.HourlyLimit != 30 {
		t.Fatalf("options = %+v", got)
	}
}

func TestRuleDeliverySettingsAreHiddenFromViewers(t *testing.T) {
	s := newAlertEmailServer(t, false)
	if _, err := s.store.CreateRule(alert.Rule{
		Name: "Errors", Description: "Checkout errors", Enabled: true, Service: "checkout", Namespace: "prod", Expression: "true", NotifyOnResolve: true,
		WebhookURL: "https://hooks.example.com/private", WebhookHeaders: `{"Authorization":"Bearer private-token"}`, WebhookTemplate: `{"private":"template"}`,
		EmailRecipients: []string{"private@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("alert-viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/alerting/rules", nil, s.login(t, viewer)))
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list = %d %s", rec.Code, rec.Body)
	}
	for _, hidden := range []string{"private", "webhook_url", "webhook_headers", "webhook_template", "email_recipients", "email_status_detail"} {
		if strings.Contains(rec.Body.String(), hidden) {
			t.Fatalf("viewer response holds %q: %s", hidden, rec.Body)
		}
	}

	// An exact key set, so a field added to Rule cannot reach viewers unnoticed.
	var listed struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed.Rules) != 1 {
		t.Fatalf("viewer list = %s, %v", rec.Body, err)
	}
	keys := make([]string, 0, len(listed.Rules[0]))
	for key := range listed.Rules[0] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	want := []string{"cooldown_s", "created_at", "description", "email_status", "enabled", "expression", "for_seconds", "id", "name", "namespace", "notify_on_resolve", "repeat_interval_s", "service", "updated_at"}
	if !slices.Equal(keys, want) {
		t.Fatalf("viewer keys = %v, want %v", keys, want)
	}
	if listed.Rules[0]["email_status"] != "unavailable" {
		t.Fatalf("viewer lost the failure signal: %s", rec.Body)
	}

	manager := s.do(http.MethodGet, "/api/alerting/rules", "")
	for _, shown := range []string{"webhook_url", "webhook_headers", "webhook_template", "email_recipients", "email_status_detail", "private-token"} {
		if !strings.Contains(manager.Body.String(), shown) {
			t.Fatalf("manager response lacks %q: %s", shown, manager.Body)
		}
	}
}

// A rule saved while SMTP worked stays editable and reports the failure.
func TestSavedEmailRuleWithoutSMTPShowsFailureAndStaysEditable(t *testing.T) {
	s := newAlertEmailServer(t, false)
	stored, err := s.store.CreateRule(alert.Rule{Name: "Errors", Enabled: true, Expression: "true", EmailRecipients: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	rec := s.do(http.MethodGet, "/api/alerting/rules", "")
	var listed struct{ Rules []alert.Rule }
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Rules) != 1 || listed.Rules[0].EmailStatus != "unavailable" || !strings.Contains(listed.Rules[0].EmailStatusDetail, "SMTP") {
		t.Fatalf("rules = %s", rec.Body)
	}

	toggle := `{"name":"Errors","enabled":false,"expression":"true","email_recipients":["a@example.com"]}`
	if rec := s.do(http.MethodPut, "/api/alerting/rules/"+stored.ID, toggle); rec.Code != http.StatusOK {
		t.Fatalf("toggle = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(http.MethodPut, "/api/alerting/rules/"+stored.ID, ruleBody(`["new@example.com"]`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("changed recipients = %d %s, want 400", rec.Code, rec.Body)
	}
	if rec := s.do(http.MethodPut, "/api/alerting/rules/"+stored.ID, ruleBody(`[]`)); rec.Code != http.StatusOK {
		t.Fatalf("clear recipients = %d %s", rec.Code, rec.Body)
	}
}
