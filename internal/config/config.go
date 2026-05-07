package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	AuthTokens      string
	AuthTokensFile  string
	AlertWebhookURL string
	StoreProvider   string
	DataRoot        string
	RuntimeProvider string
	RuntimeEndpoint string
	RuntimeTimeout  time.Duration
}

type ValidationIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationIssue) Error() string {
	return e.Field + ": " + e.Message
}

type ValidationError struct {
	Issues []ValidationIssue
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "configuration is invalid"
	}
	return "configuration is invalid: " + e.Issues[0].Error()
}

func Load() Config {
	return WithDefaults(Config{
		Address:         getenv("MULTI_AGENT_ADDR", ":8080"),
		ServiceName:     getenv("MULTI_AGENT_SERVICE_NAME", "multiagentcom-api"),
		ArtifactRoot:    getenv("MULTI_AGENT_ARTIFACT_ROOT", "runtime/artifacts"),
		SandboxRoot:     getenv("MULTI_AGENT_SANDBOX_ROOT", "runtime/sandboxes"),
		DefaultAgent:    getenv("MULTI_AGENT_DEFAULT_AGENT", "manager-agent-sprint1"),
		APIToken:        getenv("MULTI_AGENT_API_TOKEN", ""),
		AuthTokens:      getenv("MULTI_AGENT_AUTH_TOKENS", ""),
		AuthTokensFile:  getenv("MULTI_AGENT_AUTH_TOKENS_FILE", ""),
		AlertWebhookURL: getenv("MULTI_AGENT_ALERT_WEBHOOK_URL", ""),
		StoreProvider:   getenv("MULTI_AGENT_STORE_PROVIDER", "memory"),
		DataRoot:        getenv("MULTI_AGENT_DATA_ROOT", filepath.Join(os.TempDir(), "multiagentcom", "data")),
		RuntimeProvider: getenv("MULTI_AGENT_RUNTIME_PROVIDER", "local"),
		RuntimeEndpoint: getenv("MULTI_AGENT_RUNTIME_HTTP_ENDPOINT", ""),
		RuntimeTimeout:  getenvDuration("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", 30*time.Second),
	})
}

func WithDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.Address) == "" {
		cfg.Address = ":8080"
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = "multiagentcom-api"
	}
	if strings.TrimSpace(cfg.ArtifactRoot) == "" {
		cfg.ArtifactRoot = "runtime/artifacts"
	}
	if strings.TrimSpace(cfg.SandboxRoot) == "" {
		cfg.SandboxRoot = "runtime/sandboxes"
	}
	if strings.TrimSpace(cfg.DefaultAgent) == "" {
		cfg.DefaultAgent = "manager-agent-sprint1"
	}
	if strings.TrimSpace(cfg.StoreProvider) == "" {
		cfg.StoreProvider = "memory"
	}
	if strings.TrimSpace(cfg.DataRoot) == "" {
		cfg.DataRoot = filepath.Join(os.TempDir(), "multiagentcom", "data")
	}
	if strings.TrimSpace(cfg.RuntimeProvider) == "" {
		cfg.RuntimeProvider = "local"
	}
	if cfg.RuntimeTimeout <= 0 {
		cfg.RuntimeTimeout = 30 * time.Second
	}
	return cfg
}

func Validate(cfg Config) error {
	cfg = WithDefaults(cfg)
	var issues []ValidationIssue

	switch strings.ToLower(strings.TrimSpace(cfg.StoreProvider)) {
	case "memory", "file":
	default:
		issues = append(issues, ValidationIssue{Field: "StoreProvider", Message: "must be memory or file"})
	}
	if strings.EqualFold(strings.TrimSpace(cfg.StoreProvider), "file") && strings.TrimSpace(cfg.DataRoot) == "" {
		issues = append(issues, ValidationIssue{Field: "DataRoot", Message: "is required when StoreProvider is file"})
	}
	if strings.TrimSpace(cfg.ArtifactRoot) == "" {
		issues = append(issues, ValidationIssue{Field: "ArtifactRoot", Message: "is required"})
	}
	if strings.TrimSpace(cfg.SandboxRoot) == "" {
		issues = append(issues, ValidationIssue{Field: "SandboxRoot", Message: "is required"})
	}

	switch strings.ToLower(strings.TrimSpace(cfg.RuntimeProvider)) {
	case "local":
	case "http":
		if strings.TrimSpace(cfg.RuntimeEndpoint) == "" {
			issues = append(issues, ValidationIssue{Field: "RuntimeEndpoint", Message: "is required when RuntimeProvider is http"})
		} else if parsed, err := url.ParseRequestURI(strings.TrimSpace(cfg.RuntimeEndpoint)); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			issues = append(issues, ValidationIssue{Field: "RuntimeEndpoint", Message: "must be a valid absolute URL"})
		}
	default:
		issues = append(issues, ValidationIssue{Field: "RuntimeProvider", Message: "must be local or http"})
	}
	if cfg.RuntimeTimeout <= 0 {
		issues = append(issues, ValidationIssue{Field: "RuntimeTimeout", Message: "must be positive"})
	}

	if strings.TrimSpace(cfg.AlertWebhookURL) != "" {
		if parsed, err := url.ParseRequestURI(strings.TrimSpace(cfg.AlertWebhookURL)); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			issues = append(issues, ValidationIssue{Field: "AlertWebhookURL", Message: "must be a valid absolute URL"})
		}
	}
	if strings.TrimSpace(cfg.AuthTokens) != "" {
		var records []struct {
			TokenHash string `json:"tokenHash"`
		}
		if err := json.Unmarshal([]byte(cfg.AuthTokens), &records); err != nil {
			issues = append(issues, ValidationIssue{Field: "AuthTokens", Message: "must be valid token JSON"})
		} else if len(records) == 0 {
			issues = append(issues, ValidationIssue{Field: "AuthTokens", Message: "must contain at least one token record"})
		} else {
			for idx, record := range records {
				if strings.TrimSpace(record.TokenHash) == "" {
					issues = append(issues, ValidationIssue{Field: fmt.Sprintf("AuthTokens[%d].TokenHash", idx), Message: "is required"})
				}
			}
		}
	}
	if tokensFile := strings.TrimSpace(cfg.AuthTokensFile); tokensFile != "" {
		if info, err := os.Stat(tokensFile); err != nil {
			issues = append(issues, ValidationIssue{Field: "AuthTokensFile", Message: "must be readable"})
		} else if info.IsDir() {
			issues = append(issues, ValidationIssue{Field: "AuthTokensFile", Message: "must be a file"})
		} else if payload, err := os.ReadFile(tokensFile); err != nil {
			issues = append(issues, ValidationIssue{Field: "AuthTokensFile", Message: "must be readable"})
		} else {
			var records []struct {
				TokenHash string `json:"tokenHash"`
			}
			if err := json.Unmarshal(payload, &records); err != nil {
				issues = append(issues, ValidationIssue{Field: "AuthTokensFile", Message: "must contain valid token JSON"})
			} else if len(records) == 0 {
				issues = append(issues, ValidationIssue{Field: "AuthTokensFile", Message: "must contain at least one token record"})
			} else {
				for idx, record := range records {
					if strings.TrimSpace(record.TokenHash) == "" {
						issues = append(issues, ValidationIssue{Field: fmt.Sprintf("AuthTokensFile[%d].TokenHash", idx), Message: "is required"})
					}
				}
			}
		}
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

func ValidationIssues(err error) []ValidationIssue {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Issues
	}
	return nil
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
