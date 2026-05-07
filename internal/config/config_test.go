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
}

func TestLoadRuntimeOverrides(t *testing.T) {
	t.Setenv("MULTI_AGENT_RUNTIME_PROVIDER", "http")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_ENDPOINT", "https://runtime.example.com")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", "45s")

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
}

func TestLoadRuntimeTimeoutInvalidFallback(t *testing.T) {
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", "not-a-duration")

	cfg := Load()

	if cfg.RuntimeTimeout != 30*time.Second {
		t.Fatalf("RuntimeTimeout = %s, want %s when env is invalid", cfg.RuntimeTimeout, 30*time.Second)
	}
}
