package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAuthTokenConfig(t *testing.T) {
	t.Setenv("MULTI_AGENT_AUTH_TOKENS", `[{"tokenHash":"abc","actor":"ops","roles":["operator"]}]`)
	t.Setenv("MULTI_AGENT_AUTH_TOKENS_FILE", "/tmp/tokens.json")

	cfg := Load()

	if cfg.AuthTokens == "" {
		t.Fatal("expected AuthTokens env value")
	}
	if cfg.AuthTokensFile != "/tmp/tokens.json" {
		t.Fatalf("AuthTokensFile = %q, want /tmp/tokens.json", cfg.AuthTokensFile)
	}
}

func TestLoadStoreDefaults(t *testing.T) {
	t.Setenv("MULTI_AGENT_STORE_PROVIDER", "")
	t.Setenv("MULTI_AGENT_DATA_ROOT", "")

	cfg := Load()

	if cfg.StoreProvider != "memory" {
		t.Fatalf("StoreProvider = %q, want %q", cfg.StoreProvider, "memory")
	}
	if cfg.DataRoot != filepath.Join(os.TempDir(), "multiagentcom", "data") {
		t.Fatalf("DataRoot = %q, want temp data root", cfg.DataRoot)
	}
}

func TestLoadStoreOverrides(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("MULTI_AGENT_STORE_PROVIDER", "file")
	t.Setenv("MULTI_AGENT_DATA_ROOT", dataRoot)

	cfg := Load()

	if cfg.StoreProvider != "file" {
		t.Fatalf("StoreProvider = %q, want %q", cfg.StoreProvider, "file")
	}
	if cfg.DataRoot != dataRoot {
		t.Fatalf("DataRoot = %q, want %q", cfg.DataRoot, dataRoot)
	}
}

func TestLoadRuntimeDefaults(t *testing.T) {
	t.Setenv("MULTI_AGENT_RUNTIME_PROVIDER", "")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_ENDPOINT", "")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", "")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_BEARER_TOKEN", "")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_MAX_ATTEMPTS", "")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_RETRY_BASE_DELAY", "")

	cfg := Load()

	if cfg.RuntimeProvider != "local" {
		t.Fatalf("RuntimeProvider = %q, want %q", cfg.RuntimeProvider, "local")
	}
	if cfg.RuntimeEndpoint != "" {
		t.Fatalf("RuntimeEndpoint = %q, want empty", cfg.RuntimeEndpoint)
	}
	if cfg.RuntimeTimeout != 30*time.Second {
		t.Fatalf("RuntimeTimeout = %s, want %s", cfg.RuntimeTimeout, 30*time.Second)
	}
	if cfg.RuntimeHTTPBearerToken != "" {
		t.Fatalf("RuntimeHTTPBearerToken = %q, want empty", cfg.RuntimeHTTPBearerToken)
	}
	if cfg.RuntimeHTTPMaxAttempts != 1 {
		t.Fatalf("RuntimeHTTPMaxAttempts = %d, want 1", cfg.RuntimeHTTPMaxAttempts)
	}
	if cfg.RuntimeHTTPRetryBaseDelay != 100*time.Millisecond {
		t.Fatalf("RuntimeHTTPRetryBaseDelay = %s, want %s", cfg.RuntimeHTTPRetryBaseDelay, 100*time.Millisecond)
	}
}

func TestLoadRuntimeOverrides(t *testing.T) {
	t.Setenv("MULTI_AGENT_RUNTIME_PROVIDER", "http")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_ENDPOINT", "https://runtime.example.com")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", "45s")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_BEARER_TOKEN", "runtime-secret")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_MAX_ATTEMPTS", "3")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_RETRY_BASE_DELAY", "250ms")
	t.Setenv("MULTI_AGENT_TOKEN_PROMPT_PRICE_PER_MILLION", "3.5")
	t.Setenv("MULTI_AGENT_TOKEN_OUTPUT_PRICE_PER_MILLION", "7.25")
	t.Setenv("MULTI_AGENT_TOKEN_BUDGET_WARN_USD", "10")
	t.Setenv("MULTI_AGENT_TOKEN_BUDGET_BLOCK_USD", "12")

	cfg := Load()

	if cfg.RuntimeProvider != "http" {
		t.Fatalf("RuntimeProvider = %q, want %q", cfg.RuntimeProvider, "http")
	}
	if cfg.RuntimeEndpoint != "https://runtime.example.com" {
		t.Fatalf("RuntimeEndpoint = %q, want %q", cfg.RuntimeEndpoint, "https://runtime.example.com")
	}
	if cfg.RuntimeTimeout != 45*time.Second {
		t.Fatalf("RuntimeTimeout = %s, want %s", cfg.RuntimeTimeout, 45*time.Second)
	}
	if cfg.RuntimeHTTPBearerToken != "runtime-secret" {
		t.Fatalf("RuntimeHTTPBearerToken = %q, want runtime-secret", cfg.RuntimeHTTPBearerToken)
	}
	if cfg.RuntimeHTTPMaxAttempts != 3 {
		t.Fatalf("RuntimeHTTPMaxAttempts = %d, want 3", cfg.RuntimeHTTPMaxAttempts)
	}
	if cfg.RuntimeHTTPRetryBaseDelay != 250*time.Millisecond {
		t.Fatalf("RuntimeHTTPRetryBaseDelay = %s, want %s", cfg.RuntimeHTTPRetryBaseDelay, 250*time.Millisecond)
	}
	if cfg.TokenPromptPricePerMillion != 3.5 || cfg.TokenOutputPricePerMillion != 7.25 {
		t.Fatalf("unexpected token prices: %+v", cfg)
	}
	if cfg.TokenBudgetWarnUSD != 10 || cfg.TokenBudgetBlockUSD != 12 {
		t.Fatalf("unexpected token budgets: %+v", cfg)
	}
}

func TestLoadRuntimeInvalidFallbacks(t *testing.T) {
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", "not-a-duration")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_MAX_ATTEMPTS", "nope")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_RETRY_BASE_DELAY", "not-a-duration")

	cfg := Load()

	if cfg.RuntimeTimeout != 30*time.Second {
		t.Fatalf("RuntimeTimeout = %s, want %s when env is invalid", cfg.RuntimeTimeout, 30*time.Second)
	}
	if cfg.RuntimeHTTPMaxAttempts != 1 {
		t.Fatalf("RuntimeHTTPMaxAttempts = %d, want fallback 1", cfg.RuntimeHTTPMaxAttempts)
	}
	if cfg.RuntimeHTTPRetryBaseDelay != 100*time.Millisecond {
		t.Fatalf("RuntimeHTTPRetryBaseDelay = %s, want fallback %s", cfg.RuntimeHTTPRetryBaseDelay, 100*time.Millisecond)
	}
}

func TestValidateAcceptsDefaultConfig(t *testing.T) {
	if err := Validate(Config{}); err != nil {
		t.Fatalf("Validate default config: %v", err)
	}
}

func TestValidateRejectsInvalidProductionConfig(t *testing.T) {
	err := Validate(Config{
		StoreProvider:              "postgres",
		RuntimeProvider:            "http",
		RuntimeEndpoint:            "://bad",
		AlertWebhookURL:            "://bad",
		AuthTokens:                 `[{"actor":"ops"}]`,
		TokenBudgetWarnUSD:         2,
		TokenBudgetBlockUSD:        1,
		TokenPromptPricePerMillion: -1,
	})
	issues := ValidationIssues(err)
	if len(issues) < 4 {
		t.Fatalf("expected validation issues, got %v", issues)
	}

	fields := map[string]bool{}
	for _, issue := range issues {
		fields[issue.Field] = true
	}
	for _, field := range []string{"StoreProvider", "RuntimeEndpoint", "AlertWebhookURL", "AuthTokens[0].TokenHash"} {
		if !fields[field] {
			t.Fatalf("expected issue for %s, got %v", field, issues)
		}
	}
}

func TestValidateRejectsInvalidRuntimeRetryConfig(t *testing.T) {
	err := Validate(Config{RuntimeHTTPMaxAttempts: -1, RuntimeHTTPRetryBaseDelay: -time.Millisecond})
	issues := ValidationIssues(err)
	if len(issues) != 2 {
		t.Fatalf("expected 2 validation issues, got %v", issues)
	}

	fields := map[string]bool{}
	for _, issue := range issues {
		fields[issue.Field] = true
	}
	for _, field := range []string{"RuntimeHTTPMaxAttempts", "RuntimeHTTPRetryBaseDelay"} {
		if !fields[field] {
			t.Fatalf("expected issue for %s, got %v", field, issues)
		}
	}
}

func TestValidateRejectsMissingAuthTokenFile(t *testing.T) {
	err := Validate(Config{AuthTokensFile: filepath.Join(t.TempDir(), "missing.json")})
	issues := ValidationIssues(err)
	if len(issues) != 1 || issues[0].Field != "AuthTokensFile" {
		t.Fatalf("expected AuthTokensFile issue, got %v", issues)
	}
}
