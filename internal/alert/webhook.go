package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"text/template"
	"time"
)

// webhookClient is shared across all webhook deliveries.
var webhookClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

const defaultTemplate = `{"rule":"{{.Rule.Name}}","service":"{{.Alert.Service}}","state":"{{.Alert.State}}","event":"{{.Event}}","value":{{.Alert.Value}}}`

// RenderTemplate renders tmplStr against ctx using Go text/template.
// If tmplStr is empty, the defaultTemplate is used.
func RenderTemplate(tmplStr string, ctx ActionContext) (string, error) {
	if strings.TrimSpace(tmplStr) == "" {
		tmplStr = defaultTemplate
	}
	t, err := template.New("webhook").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("webhook: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("webhook: render template: %w", err)
	}
	return buf.String(), nil
}

// FireWebhook sends the webhook for the given rule. Returns "skipped" if no
// URL is configured, "success" on 2xx, or "failed" after exhausting retries.
//
// Custom headers are parsed from rule.WebhookHeaders as a JSON object
// (e.g. {"Authorization":"Bearer token"}).
//
// Retries: up to 3 attempts with linear backoff (0s, 2s, 4s) for 5xx responses.
// 4xx responses are not retried.
func FireWebhook(rule Rule, ctx ActionContext) (string, error) {
	if strings.TrimSpace(rule.WebhookURL) == "" {
		return "skipped", nil
	}

	body, err := RenderTemplate(rule.WebhookTemplate, ctx)
	if err != nil {
		return "failed", fmt.Errorf("webhook: render: %w", err)
	}

	headers := map[string]string{"Content-Type": "application/json"}
	if rule.WebhookHeaders != "" {
		var custom map[string]string
		if err := json.Unmarshal([]byte(rule.WebhookHeaders), &custom); err != nil {
			return "failed", fmt.Errorf("webhook: invalid headers JSON: %w", err)
		}
		for k, v := range custom {
			headers[k] = v
		}
	}

	const maxAttempts = 3
	backoff := []time.Duration{0, 2 * time.Second, 4 * time.Second}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff[attempt])
		}

		req, err := http.NewRequest(http.MethodPost, rule.WebhookURL, strings.NewReader(body))
		if err != nil {
			return "failed", fmt.Errorf("webhook: build request: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := webhookClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("webhook: attempt %d: %w", attempt+1, err)
			slog.Warn("alert: webhook delivery failed", "rule", rule.ID, "attempt", attempt+1, "err", err)
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			slog.Info("alert: webhook delivered", "rule", rule.ID, "status", resp.StatusCode)
			return "success", nil
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// Client errors are not retried.
			slog.Warn("alert: webhook 4xx, no retry", "rule", rule.ID, "status", resp.StatusCode)
			return "failed", fmt.Errorf("webhook: server returned %d", resp.StatusCode)
		}

		// 5xx — retry.
		lastErr = fmt.Errorf("webhook: attempt %d: server returned %d", attempt+1, resp.StatusCode)
		slog.Warn("alert: webhook 5xx, will retry", "rule", rule.ID, "attempt", attempt+1, "status", resp.StatusCode)
	}

	if lastErr != nil {
		return "failed", lastErr
	}
	return "failed", fmt.Errorf("webhook: all attempts failed")
}
