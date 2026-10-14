package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSelectsProviderCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, provider, key, baseURL string
		credentials                  []string
	}{
		{"anthropic", "anthropic", "anthropic-test", "https://anthropic.example/gateway", []string{"ANTHROPIC_API_KEY=anthropic-test", "ANTHROPIC_BASE_URL=https://anthropic.example/gateway/", "OPENAI_API_KEY=openai-test", "OPENAI_BASE_URL=https://openai.example/v1"}},
		{"openai", "openai", "openai-test", "https://openai.example/gateway/v1", []string{"ANTHROPIC_API_KEY=anthropic-test", "ANTHROPIC_BASE_URL=not-used", "OPENAI_API_KEY=openai-test", "OPENAI_BASE_URL=https://openai.example/gateway/v1/"}},
		{"inherited credentials do not enable chat", "", "", "", []string{"ANTHROPIC_API_KEY=anthropic-test", "OPENAI_API_KEY=openai-test", "ANTHROPIC_BASE_URL=not-used", "OPENAI_BASE_URL=not-used"}},
		{"anthropic default endpoint", "anthropic", "anthropic-test", DefaultAnthropicBaseURL, []string{"ANTHROPIC_API_KEY=anthropic-test", "OPENAI_BASE_URL=not-used"}},
		{"openai default endpoint", "openai", "openai-test", DefaultOpenAIBaseURL, []string{"OPENAI_API_KEY=openai-test", "ANTHROPIC_BASE_URL=not-used"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := []string{"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32), "FANOUT_AI_PROVIDER=" + tc.provider}
			cfg, err := Load(LoadOptions{Environ: append(env, tc.credentials...)})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AIProvider != tc.provider || cfg.AIAPIKey() != tc.key || cfg.AIBaseURL() != tc.baseURL || cfg.AgentConfigured() != (tc.provider != "") {
				t.Fatal("selected provider credential, endpoint, or enabled state is incorrect")
			}
		})
	}
}

func TestLoadProviderEnvironmentOverridesYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fanout.yaml")
	document := "ai:\n  provider: openai\n  model: configured-model\n  anthropic:\n    api_key: anthropic-file\n    base_url: https://anthropic.example\n  openai:\n    api_key: openai-file\n    base_url: https://openai.example/v1\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, Environ: []string{
		"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32),
		"OPENAI_API_KEY=openai-env", "OPENAI_BASE_URL=https://gateway.example/api/v1/",
		"ANTHROPIC_API_KEY=", "ANTHROPIC_BASE_URL=", "FANOUT_AI_MODEL=",
		"OPENAI_MODEL=unrelated-tool", "ANTHROPIC_MODEL=unrelated-tool",
		"OPENAI_PROJECT_ID=another-consumer", "ANTHROPIC_AUTH_TOKEN=another-consumer",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AIAPIKey() != "openai-env" || cfg.AIBaseURL() != "https://gateway.example/api/v1" || cfg.AIModel != "configured-model" || cfg.AnthropicAPIKey != "anthropic-file" || cfg.AnthropicBaseURL != "https://anthropic.example" {
		t.Fatal("provider environment precedence or shared consumer isolation is incorrect")
	}
}

func TestLoadRejectsInvalidProviderURLWithoutLeakingValues(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai"} {
		name := strings.ToUpper(provider) + "_BASE_URL"
		for _, value := range []string{"file:///private", "https://user:private-value@example.com", "https://example.com?key=private-value", "https://example.com#private-value", "https://example.com?", "https://example.com#", "https://", "https://example.com/%zz"} {
			_, err := Load(LoadOptions{Environ: []string{"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32), "FANOUT_AI_PROVIDER=" + provider, strings.ToUpper(provider) + "_API_KEY=test-key", name + "=" + value}})
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), value) || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("expected redacted validation error naming %s", name)
			}
		}
	}
}

func TestLoadRejectsUnsupportedProviderWithoutAKey(t *testing.T) {
	for _, provider := range []string{"unsupported", "OpenAI", " OpenAI ", " openai ", "Anthropic"} {
		_, err := Load(LoadOptions{Environ: []string{"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32), "FANOUT_AI_PROVIDER=" + provider}})
		if err == nil || !strings.Contains(err.Error(), "ai.provider") {
			t.Fatal("unsupported provider must fail at startup, even without a key")
		}
	}
}

func TestLoadRequiresSelectedProviderKey(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai"} {
		otherKey := "OPENAI_API_KEY"
		if provider == "openai" {
			otherKey = "ANTHROPIC_API_KEY"
		}
		for _, value := range []string{"", "   "} {
			name := strings.ToUpper(provider) + "_API_KEY"
			_, err := Load(LoadOptions{Environ: []string{
				"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32), "FANOUT_AI_PROVIDER=" + provider,
				name + "=" + value, otherKey + "=other-provider-key",
			}})
			if err == nil || !strings.Contains(err.Error(), "ai."+provider+".api_key") || !strings.Contains(err.Error(), name) {
				t.Fatalf("expected missing key error naming both YAML and environment keys, got %v", err)
			}
		}
	}
}
