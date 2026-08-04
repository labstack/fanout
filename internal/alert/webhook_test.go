package alert

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func makeTestCtx(state, service string) ActionContext {
	return ActionContext{
		Rule: Rule{
			Name: "test-rule",
		},
		Alert: Alert{
			Service: service,
			State:   state,
			Value:   0.25,
		},
		Event: "firing",
		Time:  time.Now(),
	}
}

func TestRenderTemplate_Basic(t *testing.T) {
	ctx := makeTestCtx("firing", "api")
	body, err := RenderTemplate(`{"service":"{{.Alert.Service}}","state":"{{.Alert.State}}"}`, ctx)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if !strings.Contains(body, `"api"`) {
		t.Errorf("body missing service: %q", body)
	}
	if !strings.Contains(body, `"firing"`) {
		t.Errorf("body missing state: %q", body)
	}
}

func TestRenderTemplate_Empty(t *testing.T) {
	ctx := makeTestCtx("firing", "api")
	body, err := RenderTemplate("", ctx)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if body == "" {
		t.Fatal("RenderTemplate with empty template should use default, not return empty")
	}
	// Default template contains the rule name and state.
	if !strings.Contains(body, "test-rule") {
		t.Errorf("default template body missing rule name: %q", body)
	}
}

func TestFireWebhook_NoURL(t *testing.T) {
	rule := Rule{Name: "no-url", WebhookURL: ""}
	ctx := makeTestCtx("firing", "api")

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: unexpected error: %v", err)
	}
	if status != "skipped" {
		t.Errorf("status = %q, want %q", status, "skipped")
	}
}

func TestFireWebhook_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := Rule{Name: "success-rule", WebhookURL: srv.URL}
	ctx := makeTestCtx("firing", "api")

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "success" {
		t.Errorf("status = %q, want %q", status, "success")
	}
}

func TestFireWebhook_Retries5xx(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Override backoff to 0 for test speed.
	origClient := webhookClient
	webhookClient = &http.Client{Timeout: 5 * time.Second}
	defer func() { webhookClient = origClient }()

	rule := Rule{Name: "retry-rule", WebhookURL: srv.URL}
	ctx := makeTestCtx("firing", "api")

	// Need to also override the sleep in retries. We restructure the test to
	// just verify call count by using a fast-looping server.
	// The actual backoff values are small in test; we accept them.
	status, err := FireWebhook(rule, ctx)
	// After 3 calls (2 fails + 1 success), should succeed.
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "success" {
		t.Errorf("status = %q, want %q", status, "success")
	}
}

func TestFireWebhook_NoRetry4xx(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	rule := Rule{Name: "4xx-rule", WebhookURL: srv.URL}
	ctx := makeTestCtx("firing", "api")

	status, _ := FireWebhook(rule, ctx)
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 4xx)", calls.Load())
	}
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
}

func TestFireWebhook_CustomHeaders(t *testing.T) {
	var receivedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := Rule{
		Name:           "headers-rule",
		WebhookURL:     srv.URL,
		WebhookHeaders: `{"Authorization":"Bearer secret-token"}`,
	}
	ctx := makeTestCtx("firing", "api")

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "success" {
		t.Errorf("status = %q, want %q", status, "success")
	}
	if receivedAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", receivedAuth, "Bearer secret-token")
	}
}

func TestFireWebhook_TemplateRendered(t *testing.T) {
	var receivedBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := Rule{
		Name:            "tmpl-rule",
		WebhookURL:      srv.URL,
		WebhookTemplate: `alert={{.Rule.Name}} svc={{.Alert.Service}}`,
	}
	ctx := makeTestCtx("firing", "payment-service")
	ctx.Rule = rule

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "success" {
		t.Errorf("status = %q, want %q", status, "success")
	}
	if !strings.Contains(receivedBody, "tmpl-rule") {
		t.Errorf("body missing rule name: %q", receivedBody)
	}
	if !strings.Contains(receivedBody, "payment-service") {
		t.Errorf("body missing service: %q", receivedBody)
	}
}
