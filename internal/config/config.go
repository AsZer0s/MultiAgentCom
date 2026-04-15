package config

import "os"

type Config struct {
	Address         string
	ServiceName     string
	ArtifactRoot    string
	SandboxRoot     string
	DefaultAgent    string
	APIToken        string
	AlertWebhookURL string
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
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
