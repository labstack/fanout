package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadIgnoresInactiveProviderSecretFiles(t *testing.T) {
	emptyPath := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"", "anthropic", "openai"} {
		for _, vendor := range []string{"anthropic", "openai"} {
			if vendor == provider {
				continue
			}
			for _, file := range []struct{ name, path string }{
				{"missing", "/missing"}, {"empty", emptyPath},
			} {
				for _, bothSet := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/both_set=%t", provider, vendor, file.name, bothSet), func(t *testing.T) {
						env := []string{"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32), "FANOUT_AI_PROVIDER=" + provider}
						if provider != "" {
							env = append(env, strings.ToUpper(provider)+"_API_KEY=selected-key")
						}
						name := strings.ToUpper(vendor)
						env = append(env, name+"_API_KEY_FILE="+file.path, name+"_BASE_URL=unused-invalid-url")
						if bothSet {
							env = append(env, name+"_API_KEY=unused-key")
						}
						cfg, err := Load(LoadOptions{Environ: env})
						if err != nil {
							t.Fatal(err)
						}
						if vendor == "anthropic" && (cfg.AnthropicAPIKey != "" || cfg.AnthropicBaseURL != "") ||
							vendor == "openai" && (cfg.OpenAIAPIKey != "" || cfg.OpenAIBaseURL != "") {
							t.Fatal("inactive provider settings must be ignored")
						}
					})
				}
			}
		}
	}
}

func TestLoadRejectsSelectedProviderSecretFileErrors(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai"} {
		for _, bothSet := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/both_set=%t", provider, bothSet), func(t *testing.T) {
				name := strings.ToUpper(provider) + "_API_KEY"
				path := filepath.Join(t.TempDir(), "missing")
				env := []string{
					"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32), "FANOUT_AI_PROVIDER=" + provider,
					name + "_FILE=" + path,
				}
				if bothSet {
					env = append(env, name+"=synthetic-private-value")
				}
				_, err := Load(LoadOptions{Environ: env})
				if err == nil || !strings.Contains(err.Error(), name+"_FILE") || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "synthetic-private-value") {
					t.Fatalf("expected redacted file error naming variable and path, got %v", err)
				}
				if bothSet && !strings.Contains(err.Error(), "must not both be set") {
					t.Fatalf("expected selected provider conflict, got %v", err)
				}
			})
		}
	}
}

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

func TestLoadIgnoresOtherVendorEnvironment(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: []string{
		"FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("x", 32),
		"FANOUT_AI_PROVIDER=openai", "OPENAI_API_KEY=openai-env",
		"OPENAI_BASE_URL=https://gateway.example/api/v1/", "FANOUT_AI_MODEL=configured-model",
		"ANTHROPIC_API_KEY=", "ANTHROPIC_BASE_URL=",
		"OPENAI_MODEL=unrelated-tool", "ANTHROPIC_MODEL=unrelated-tool",
		"OPENAI_PROJECT_ID=another-consumer", "ANTHROPIC_AUTH_TOKEN=another-consumer",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AIAPIKey() != "openai-env" || cfg.AIBaseURL() != "https://gateway.example/api/v1" || cfg.AIModel != "configured-model" || cfg.AnthropicAPIKey != "" || cfg.AnthropicBaseURL != "" {
		t.Fatal("provider environment or shared consumer isolation is incorrect")
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
		if err == nil || !strings.Contains(err.Error(), "FANOUT_AI_PROVIDER") {
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
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("expected missing key error naming the environment variable, got %v", err)
			}
		}
	}
}
