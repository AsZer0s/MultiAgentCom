package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address                           string
	ServiceName                       string
	ArtifactRoot                      string
	SandboxRoot                       string
	DefaultAgent                      string
	APIToken                          string
	AuthTokens                        string
	AuthTokensFile                    string
	AlertWebhookURL                   string
	StoreProvider                     string
	DataRoot                          string
	WorkspaceProvider                 string
	WorkspaceGitRepoPath              string
	WorkspaceGitBaseRef               string
	WorkspaceGitRemoteURL             string
	WorkspaceGitRemoteName            string
	WorkspaceGitFetchBeforeUse        bool
	WorkspaceGitPushEnabled           bool
	WorkspaceGitAuthToken             string
	WorkspaceGitAuthTokenFile         string
	WorkspaceGitAuthUsername          string
	WorkspaceGitCleanupEnabled        bool
	WorkspaceGitCleanupDeleteBranches bool
	WorkspaceGitCleanupFailedEnabled  bool
	WorkspaceGitCleanupMinAge         time.Duration
	RuntimeProvider                   string
	RuntimeEndpoint                   string
	RuntimeTimeout                    time.Duration
	RuntimeHTTPBearerToken            string
	RuntimeHTTPMaxAttempts            int
	RuntimeHTTPRetryBaseDelay         time.Duration
	TokenPromptPricePerMillion        float64
	TokenOutputPricePerMillion        float64
	TokenBudgetWarnUSD                float64
	TokenBudgetBlockUSD               float64
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
		Address:                           getenv("MULTI_AGENT_ADDR", ":8080"),
		ServiceName:                       getenv("MULTI_AGENT_SERVICE_NAME", "multiagentcom-api"),
		ArtifactRoot:                      getenv("MULTI_AGENT_ARTIFACT_ROOT", "runtime/artifacts"),
		SandboxRoot:                       getenv("MULTI_AGENT_SANDBOX_ROOT", "runtime/sandboxes"),
		DefaultAgent:                      getenv("MULTI_AGENT_DEFAULT_AGENT", "manager-agent-sprint1"),
		APIToken:                          getenv("MULTI_AGENT_API_TOKEN", ""),
		AuthTokens:                        getenv("MULTI_AGENT_AUTH_TOKENS", ""),
		AuthTokensFile:                    getenv("MULTI_AGENT_AUTH_TOKENS_FILE", ""),
		AlertWebhookURL:                   getenv("MULTI_AGENT_ALERT_WEBHOOK_URL", ""),
		StoreProvider:                     getenv("MULTI_AGENT_STORE_PROVIDER", "memory"),
		DataRoot:                          getenv("MULTI_AGENT_DATA_ROOT", filepath.Join(os.TempDir(), "multiagentcom", "data")),
		WorkspaceProvider:                 getenv("MULTI_AGENT_WORKSPACE_PROVIDER", "directory"),
		WorkspaceGitRepoPath:              getenv("MULTI_AGENT_WORKSPACE_GIT_REPO_PATH", ""),
		WorkspaceGitBaseRef:               getenv("MULTI_AGENT_WORKSPACE_GIT_BASE_REF", "HEAD"),
		WorkspaceGitRemoteURL:             getenv("MULTI_AGENT_WORKSPACE_GIT_REMOTE_URL", ""),
		WorkspaceGitRemoteName:            getenv("MULTI_AGENT_WORKSPACE_GIT_REMOTE_NAME", "origin"),
		WorkspaceGitFetchBeforeUse:        getenvBool("MULTI_AGENT_WORKSPACE_GIT_FETCH_BEFORE_USE", false),
		WorkspaceGitPushEnabled:           getenvBool("MULTI_AGENT_WORKSPACE_GIT_PUSH_ENABLED", false),
		WorkspaceGitAuthToken:             getenv("MULTI_AGENT_WORKSPACE_GIT_AUTH_TOKEN", ""),
		WorkspaceGitAuthTokenFile:         getenv("MULTI_AGENT_WORKSPACE_GIT_AUTH_TOKEN_FILE", ""),
		WorkspaceGitAuthUsername:          getenv("MULTI_AGENT_WORKSPACE_GIT_AUTH_USERNAME", "x-access-token"),
		WorkspaceGitCleanupEnabled:        getenvBool("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_ENABLED", true),
		WorkspaceGitCleanupDeleteBranches: getenvBool("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_DELETE_BRANCHES", false),
		WorkspaceGitCleanupFailedEnabled:  getenvBool("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_FAILED_ENABLED", false),
		WorkspaceGitCleanupMinAge:         getenvNonNegativeDuration("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_MIN_AGE", 0),
		RuntimeProvider:                   getenv("MULTI_AGENT_RUNTIME_PROVIDER", "local"),
		RuntimeEndpoint:                   getenv("MULTI_AGENT_RUNTIME_HTTP_ENDPOINT", ""),
		RuntimeTimeout:                    getenvDuration("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", 30*time.Second),
		RuntimeHTTPBearerToken:            getenv("MULTI_AGENT_RUNTIME_HTTP_BEARER_TOKEN", ""),
		RuntimeHTTPMaxAttempts:            getenvInt("MULTI_AGENT_RUNTIME_HTTP_MAX_ATTEMPTS", 1),
		RuntimeHTTPRetryBaseDelay:         getenvDuration("MULTI_AGENT_RUNTIME_HTTP_RETRY_BASE_DELAY", 100*time.Millisecond),
		TokenPromptPricePerMillion:        getenvFloat("MULTI_AGENT_TOKEN_PROMPT_PRICE_PER_MILLION", 1.5),
		TokenOutputPricePerMillion:        getenvFloat("MULTI_AGENT_TOKEN_OUTPUT_PRICE_PER_MILLION", 2.5),
		TokenBudgetWarnUSD:                getenvFloat("MULTI_AGENT_TOKEN_BUDGET_WARN_USD", 0),
		TokenBudgetBlockUSD:               getenvFloat("MULTI_AGENT_TOKEN_BUDGET_BLOCK_USD", 0),
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
	if strings.TrimSpace(cfg.WorkspaceProvider) == "" {
		cfg.WorkspaceProvider = "directory"
	}
	if strings.TrimSpace(cfg.WorkspaceGitBaseRef) == "" {
		cfg.WorkspaceGitBaseRef = "HEAD"
	}
	if strings.TrimSpace(cfg.WorkspaceGitRemoteName) == "" {
		cfg.WorkspaceGitRemoteName = "origin"
	}
	if strings.TrimSpace(cfg.WorkspaceGitAuthUsername) == "" {
		cfg.WorkspaceGitAuthUsername = "x-access-token"
	}
	if strings.TrimSpace(cfg.RuntimeProvider) == "" {
		cfg.RuntimeProvider = "local"
	}
	if cfg.RuntimeTimeout == 0 {
		cfg.RuntimeTimeout = 30 * time.Second
	}
	if cfg.RuntimeHTTPMaxAttempts == 0 {
		cfg.RuntimeHTTPMaxAttempts = 1
	}
	if cfg.RuntimeHTTPRetryBaseDelay == 0 {
		cfg.RuntimeHTTPRetryBaseDelay = 100 * time.Millisecond
	}
	if cfg.TokenPromptPricePerMillion == 0 {
		cfg.TokenPromptPricePerMillion = 1.5
	}
	if cfg.TokenOutputPricePerMillion == 0 {
		cfg.TokenOutputPricePerMillion = 2.5
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
	switch strings.ToLower(strings.TrimSpace(cfg.WorkspaceProvider)) {
	case "directory":
	case "git":
		if strings.TrimSpace(cfg.WorkspaceGitRepoPath) == "" {
			issues = append(issues, ValidationIssue{Field: "WorkspaceGitRepoPath", Message: "is required when WorkspaceProvider is git"})
		}
	default:
		issues = append(issues, ValidationIssue{Field: "WorkspaceProvider", Message: "must be directory or git"})
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
	if remoteURL := strings.TrimSpace(cfg.WorkspaceGitRemoteURL); remoteURL != "" {
		if err := validateWorkspaceGitRemoteURL(remoteURL); err != nil {
			issues = append(issues, ValidationIssue{Field: "WorkspaceGitRemoteURL", Message: err.Error()})
		}
	}
	if strings.TrimSpace(cfg.WorkspaceGitAuthToken) != "" && strings.TrimSpace(cfg.WorkspaceGitAuthTokenFile) != "" {
		issues = append(issues, ValidationIssue{Field: "WorkspaceGitAuthTokenFile", Message: "must not be set when WorkspaceGitAuthToken is set"})
	}
	if tokenFile := strings.TrimSpace(cfg.WorkspaceGitAuthTokenFile); tokenFile != "" {
		if info, err := os.Stat(tokenFile); err != nil {
			issues = append(issues, ValidationIssue{Field: "WorkspaceGitAuthTokenFile", Message: "must be readable"})
		} else if info.IsDir() {
			issues = append(issues, ValidationIssue{Field: "WorkspaceGitAuthTokenFile", Message: "must be a file"})
		} else if _, err := os.ReadFile(tokenFile); err != nil {
			issues = append(issues, ValidationIssue{Field: "WorkspaceGitAuthTokenFile", Message: "must be readable"})
		}
	}
	if cfg.WorkspaceGitCleanupMinAge < 0 {
		issues = append(issues, ValidationIssue{Field: "WorkspaceGitCleanupMinAge", Message: "must be non-negative"})
	}
	if cfg.RuntimeTimeout <= 0 {
		issues = append(issues, ValidationIssue{Field: "RuntimeTimeout", Message: "must be positive"})
	}
	if cfg.RuntimeHTTPMaxAttempts <= 0 {
		issues = append(issues, ValidationIssue{Field: "RuntimeHTTPMaxAttempts", Message: "must be positive"})
	}
	if cfg.RuntimeHTTPRetryBaseDelay <= 0 {
		issues = append(issues, ValidationIssue{Field: "RuntimeHTTPRetryBaseDelay", Message: "must be positive"})
	}
	if cfg.TokenPromptPricePerMillion <= 0 {
		issues = append(issues, ValidationIssue{Field: "TokenPromptPricePerMillion", Message: "must be positive"})
	}
	if cfg.TokenOutputPricePerMillion <= 0 {
		issues = append(issues, ValidationIssue{Field: "TokenOutputPricePerMillion", Message: "must be positive"})
	}
	if cfg.TokenBudgetWarnUSD < 0 {
		issues = append(issues, ValidationIssue{Field: "TokenBudgetWarnUSD", Message: "must be non-negative"})
	}
	if cfg.TokenBudgetBlockUSD < 0 {
		issues = append(issues, ValidationIssue{Field: "TokenBudgetBlockUSD", Message: "must be non-negative"})
	}
	if cfg.TokenBudgetWarnUSD > 0 && cfg.TokenBudgetBlockUSD > 0 && cfg.TokenBudgetWarnUSD > cfg.TokenBudgetBlockUSD {
		issues = append(issues, ValidationIssue{Field: "TokenBudgetWarnUSD", Message: "must be less than or equal to TokenBudgetBlockUSD"})
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

func getenvNonNegativeDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return fallback
	}
	return duration
}

func getenvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func validateWorkspaceGitRemoteURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("must be a valid remote URL")
	}
	if parsed.Scheme == "" {
		if strings.Contains(raw, "://") {
			return fmt.Errorf("must be a valid remote URL")
		}
		return nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.User != nil {
			return fmt.Errorf("must not embed credentials")
		}
	case "ssh":
		if parsed.User != nil {
			if _, ok := parsed.User.Password(); ok {
				return fmt.Errorf("must not embed credentials")
			}
		}
	case "file":
		return nil
	}
	return nil
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
