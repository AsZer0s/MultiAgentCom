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

func TestLoadWorkspaceProviderDefaults(t *testing.T) {
	t.Setenv("MULTI_AGENT_WORKSPACE_PROVIDER", "")

	cfg := Load()

	if cfg.WorkspaceProvider != "directory" {
		t.Fatalf("WorkspaceProvider = %q, want directory", cfg.WorkspaceProvider)
	}
}

func TestLoadWorkspaceProviderOverrides(t *testing.T) {
	repoPath := t.TempDir()
	t.Setenv("MULTI_AGENT_WORKSPACE_PROVIDER", "git")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_REPO_PATH", repoPath)
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_BASE_REF", "main")

	cfg := Load()

	if cfg.WorkspaceProvider != "git" {
		t.Fatalf("WorkspaceProvider = %q, want git", cfg.WorkspaceProvider)
	}
	if cfg.WorkspaceGitRepoPath != repoPath {
		t.Fatalf("WorkspaceGitRepoPath = %q, want %q", cfg.WorkspaceGitRepoPath, repoPath)
	}
	if cfg.WorkspaceGitBaseRef != "main" {
		t.Fatalf("WorkspaceGitBaseRef = %q, want main", cfg.WorkspaceGitBaseRef)
	}
}

func TestLoadWorkspaceGitRemoteOverrides(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "git-token")
	if err := os.WriteFile(tokenFile, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_REMOTE_URL", "file:///tmp/remote.git")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_REMOTE_NAME", "upstream")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_FETCH_BEFORE_USE", "true")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_PUSH_ENABLED", "true")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_AUTH_TOKEN_FILE", tokenFile)
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_AUTH_USERNAME", "git-user")

	cfg := Load()

	if cfg.WorkspaceGitRemoteURL != "file:///tmp/remote.git" || cfg.WorkspaceGitRemoteName != "upstream" {
		t.Fatalf("unexpected remote config: %+v", cfg)
	}
	if !cfg.WorkspaceGitFetchBeforeUse || !cfg.WorkspaceGitPushEnabled {
		t.Fatalf("expected fetch/push enabled: %+v", cfg)
	}
	if cfg.WorkspaceGitAuthTokenFile != tokenFile || cfg.WorkspaceGitAuthUsername != "git-user" {
		t.Fatalf("unexpected auth config: %+v", cfg)
	}
}

func TestLoadWorkspaceGitRemoteDefaults(t *testing.T) {
	cfg := Load()

	if cfg.WorkspaceGitRemoteName != "origin" {
		t.Fatalf("WorkspaceGitRemoteName = %q, want origin", cfg.WorkspaceGitRemoteName)
	}
	if cfg.WorkspaceGitAuthUsername != "x-access-token" {
		t.Fatalf("WorkspaceGitAuthUsername = %q, want x-access-token", cfg.WorkspaceGitAuthUsername)
	}
	if cfg.WorkspaceGitFetchBeforeUse || cfg.WorkspaceGitPushEnabled {
		t.Fatalf("expected remote fetch/push disabled by default: %+v", cfg)
	}
}

func TestLoadWorkspaceGitCleanupDefaults(t *testing.T) {
	cfg := Load()

	if !cfg.WorkspaceGitCleanupEnabled {
		t.Fatal("expected git cleanup to be enabled by default")
	}
	if cfg.WorkspaceGitCleanupDeleteBranches {
		t.Fatal("expected git cleanup branch deletion to be disabled by default")
	}
	if cfg.WorkspaceGitCleanupFailedEnabled {
		t.Fatal("expected failed sandbox cleanup to be disabled by default")
	}
	if cfg.WorkspaceGitCleanupMinAge != 0 {
		t.Fatalf("WorkspaceGitCleanupMinAge = %s, want 0", cfg.WorkspaceGitCleanupMinAge)
	}
}

func TestLoadWorkspaceGitCleanupOverrides(t *testing.T) {
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_ENABLED", "false")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_DELETE_BRANCHES", "true")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_FAILED_ENABLED", "true")
	t.Setenv("MULTI_AGENT_WORKSPACE_GIT_CLEANUP_MIN_AGE", "2h")

	cfg := Load()

	if cfg.WorkspaceGitCleanupEnabled {
		t.Fatal("expected git cleanup to be disabled by env")
	}
	if !cfg.WorkspaceGitCleanupDeleteBranches {
		t.Fatal("expected branch cleanup to be enabled by env")
	}
	if !cfg.WorkspaceGitCleanupFailedEnabled {
		t.Fatal("expected failed sandbox cleanup to be enabled by env")
	}
	if cfg.WorkspaceGitCleanupMinAge != 2*time.Hour {
		t.Fatalf("WorkspaceGitCleanupMinAge = %s, want 2h", cfg.WorkspaceGitCleanupMinAge)
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
	if cfg.RuntimeContainerBinary != "docker" || cfg.RuntimeContainerNetwork != "none" || cfg.RuntimeContainerWorkdir != "/workspace" {
		t.Fatalf("unexpected container runtime defaults: %+v", cfg)
	}
	if cfg.RuntimeContainerTmpfs != "/tmp:rw,nosuid,nodev,noexec,size=64m" {
		t.Fatalf("RuntimeContainerTmpfs = %q, want default /tmp tmpfs", cfg.RuntimeContainerTmpfs)
	}
	if cfg.RuntimeContainerCPUs != "" || cfg.RuntimeContainerMemory != "" || cfg.RuntimeContainerPidsLimit != 0 || cfg.RuntimeContainerEntrypoint != "" || cfg.RuntimeContainerCommand != "" {
		t.Fatalf("expected optional container limits to default empty/zero: %+v", cfg)
	}
	if !cfg.RuntimeContainerReadonlyRootFS {
		t.Fatal("expected container readonly rootfs default")
	}
}

func TestLoadRuntimeOverrides(t *testing.T) {
	t.Setenv("MULTI_AGENT_RUNTIME_PROVIDER", "http")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_ENDPOINT", "https://runtime.example.com")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_TIMEOUT", "45s")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_BEARER_TOKEN", "runtime-secret")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_MAX_ATTEMPTS", "3")
	t.Setenv("MULTI_AGENT_RUNTIME_HTTP_RETRY_BASE_DELAY", "250ms")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_BINARY", "podman")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_IMAGE", "multiagent-runtime:test")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_NETWORK", "bridge")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_USER", "1000:1000")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_READONLY_ROOTFS", "false")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_WORKDIR", "/work")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_CPUS", "1.5")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_MEMORY", "256m")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_PIDS_LIMIT", "128")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_TMPFS", "/tmp:rw,size=64m;/run:rw,size=16m")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_ENTRYPOINT", "/usr/local/bin/runtime")
	t.Setenv("MULTI_AGENT_RUNTIME_CONTAINER_COMMAND", "--mode worker")
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
	if cfg.RuntimeContainerBinary != "podman" || cfg.RuntimeContainerImage != "multiagent-runtime:test" || cfg.RuntimeContainerNetwork != "bridge" || cfg.RuntimeContainerUser != "1000:1000" || cfg.RuntimeContainerWorkdir != "/work" {
		t.Fatalf("unexpected container runtime overrides: %+v", cfg)
	}
	if cfg.RuntimeContainerCPUs != "1.5" || cfg.RuntimeContainerMemory != "256m" || cfg.RuntimeContainerPidsLimit != 128 || cfg.RuntimeContainerTmpfs != "/tmp:rw,size=64m;/run:rw,size=16m" {
		t.Fatalf("unexpected container hardening overrides: %+v", cfg)
	}
	if cfg.RuntimeContainerEntrypoint != "/usr/local/bin/runtime" || cfg.RuntimeContainerCommand != "--mode worker" {
		t.Fatalf("unexpected container entrypoint/command overrides: %+v", cfg)
	}
	if cfg.RuntimeContainerReadonlyRootFS {
		t.Fatal("expected readonly rootfs override to be false")
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

func TestValidateAcceptsContainerRuntimeConfig(t *testing.T) {
	if err := Validate(Config{RuntimeProvider: "container", RuntimeContainerImage: "multiagent-runtime:test"}); err != nil {
		t.Fatalf("Validate container runtime config: %v", err)
	}
}

func TestValidateRejectsInvalidContainerRuntimeHardeningConfig(t *testing.T) {
	err := Validate(Config{
		RuntimeProvider:                "container",
		RuntimeContainerImage:          "multiagent-runtime:test",
		RuntimeContainerCPUs:           "zero",
		RuntimeContainerMemory:         "lots",
		RuntimeContainerPidsLimit:      -1,
		RuntimeContainerTmpfs:          "tmp:rw;/workspace:rw",
		RuntimeContainerWorkdir:        "/workspace",
		RuntimeContainerBinary:         "/bin/sh",
		RuntimeContainerReadonlyRootFS: true,
	})
	issues := ValidationIssues(err)
	if len(issues) < 4 {
		t.Fatalf("expected validation issues, got %v", issues)
	}
	fields := map[string]bool{}
	for _, issue := range issues {
		fields[issue.Field] = true
	}
	for _, field := range []string{"RuntimeContainerCPUs", "RuntimeContainerMemory", "RuntimeContainerPidsLimit", "RuntimeContainerTmpfs"} {
		if !fields[field] {
			t.Fatalf("expected issue for %s, got %v", field, issues)
		}
	}
}

func TestValidateRejectsContainerRuntimeWithoutImage(t *testing.T) {
	err := Validate(Config{RuntimeProvider: "container"})
	issues := ValidationIssues(err)
	if len(issues) == 0 {
		t.Fatal("expected validation issue")
	}
	for _, issue := range issues {
		if issue.Field == "RuntimeContainerImage" {
			return
		}
	}
	t.Fatalf("expected RuntimeContainerImage issue, got %v", issues)
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

func TestValidateAcceptsGitWorkspaceProvider(t *testing.T) {
	if err := Validate(Config{WorkspaceProvider: "git", WorkspaceGitRepoPath: t.TempDir()}); err != nil {
		t.Fatalf("Validate git workspace config: %v", err)
	}
}

func TestValidateRejectsGitWorkspaceProviderWithoutRepoPath(t *testing.T) {
	err := Validate(Config{WorkspaceProvider: "git"})
	issues := ValidationIssues(err)
	if len(issues) != 1 || issues[0].Field != "WorkspaceGitRepoPath" {
		t.Fatalf("expected WorkspaceGitRepoPath issue, got %v", issues)
	}
}

func TestValidateRejectsWorkspaceGitRemoteURLWithCredentials(t *testing.T) {
	err := Validate(Config{WorkspaceProvider: "git", WorkspaceGitRepoPath: t.TempDir(), WorkspaceGitRemoteURL: "https://token@example.com/repo.git"})
	issues := ValidationIssues(err)
	if len(issues) != 1 || issues[0].Field != "WorkspaceGitRemoteURL" {
		t.Fatalf("expected WorkspaceGitRemoteURL issue, got %v", issues)
	}
}

func TestValidateAcceptsWorkspaceGitFileRemote(t *testing.T) {
	if err := Validate(Config{WorkspaceProvider: "git", WorkspaceGitRepoPath: t.TempDir(), WorkspaceGitRemoteURL: "file:///tmp/repo.git"}); err != nil {
		t.Fatalf("Validate file remote: %v", err)
	}
}

func TestValidateRejectsWorkspaceGitAuthTokenAndFileTogether(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	err := Validate(Config{WorkspaceGitAuthToken: "secret", WorkspaceGitAuthTokenFile: tokenFile})
	issues := ValidationIssues(err)
	if len(issues) != 1 || issues[0].Field != "WorkspaceGitAuthTokenFile" {
		t.Fatalf("expected WorkspaceGitAuthTokenFile issue, got %v", issues)
	}
}

func TestValidateRejectsMissingWorkspaceGitAuthTokenFile(t *testing.T) {
	err := Validate(Config{WorkspaceGitAuthTokenFile: filepath.Join(t.TempDir(), "missing")})
	issues := ValidationIssues(err)
	if len(issues) != 1 || issues[0].Field != "WorkspaceGitAuthTokenFile" {
		t.Fatalf("expected WorkspaceGitAuthTokenFile issue, got %v", issues)
	}
}

func TestValidateRejectsInvalidWorkspaceProvider(t *testing.T) {
	err := Validate(Config{WorkspaceProvider: "git-local"})
	issues := ValidationIssues(err)
	if len(issues) != 1 || issues[0].Field != "WorkspaceProvider" {
		t.Fatalf("expected WorkspaceProvider issue, got %v", issues)
	}
}

func TestValidateRejectsInvalidRuntimeRetryConfig(t *testing.T) {
	err := Validate(Config{RuntimeHTTPMaxAttempts: -1, RuntimeHTTPRetryBaseDelay: -time.Millisecond, WorkspaceGitCleanupMinAge: -time.Second})
	issues := ValidationIssues(err)
	if len(issues) != 3 {
		t.Fatalf("expected 3 validation issues, got %v", issues)
	}

	fields := map[string]bool{}
	for _, issue := range issues {
		fields[issue.Field] = true
	}
	for _, field := range []string{"RuntimeHTTPMaxAttempts", "RuntimeHTTPRetryBaseDelay", "WorkspaceGitCleanupMinAge"} {
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
