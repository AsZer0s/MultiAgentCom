package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Address         string
	ServiceName     string
	ArtifactRoot    string
	SandboxRoot     string
	DefaultAgent    string
	APIToken        string
	AlertWebhookURL string
	StoreProvider   string
	DataRoot        string
	RuntimeProvider string
	RuntimeEndpoint string
	RuntimeTimeout  time.Duration
}

func Load() Config {
	return Config{
		Address:         getenv("MULTI_AGENT_ADDR", ":8080"),
		ServiceName:     getenv("MULTI_AGENT_SERVICE_NAME", "multiagentcom-api"),
		ArtifactRoot:    getenv("MULTI_AGENT_ARTIFACT_ROOT", "runtime/artifacts"),
		SandboxRoot:     getenv("MULTI_AGENT_SANDBOX_ROOT", "runtime/sandboxes"),
		DefaultAgent:    getenv("MULTI_AGENT_DEFAULT_AGENT", "manager-agent-sprint1"),
		APIToken:        getenv("MULTI_AGENT_API_TOKEN", ""),
		AlertWebhookURL: getenv("MULTI_AGENT_ALERT_WEBHOOK_URL", ""),
		StoreProvider:   getenv("MULTI_AGENT_STORE_PROVIDER", "memory"),
		DataRoot:        getenv("MULTI_AGENT_DATA_ROOT", filepath.Join(os.TempDir(), "multiagentcom", "data")),
		RuntimeProvider: getenv("MULTI_AGENT_RUNTIME_PROVIDER", "local"),
		RuntimeEndpoint: getenv("MULTI_AGENT_RUNTIME_HTTP_ENDPOINT", ""),
		RuntimeTimeout:  getenvDuration("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", 30*time.Second),
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
