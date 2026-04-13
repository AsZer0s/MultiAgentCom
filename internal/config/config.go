package config

import "os"

type Config struct {
	Address      string
	ServiceName  string
	ArtifactRoot string
	DefaultAgent string
}

func Load() Config {
	return Config{
		Address:      getenv("MULTI_AGENT_ADDR", ":8080"),
		ServiceName:  getenv("MULTI_AGENT_SERVICE_NAME", "multiagentcom-api"),
		ArtifactRoot: getenv("MULTI_AGENT_ARTIFACT_ROOT", "runtime/artifacts"),
		DefaultAgent: getenv("MULTI_AGENT_DEFAULT_AGENT", "manager-agent-sprint1"),
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
