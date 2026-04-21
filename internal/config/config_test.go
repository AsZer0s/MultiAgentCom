package config

import (
	"testing"
	"time"
)

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
