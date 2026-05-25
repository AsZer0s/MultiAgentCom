package httpapi

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
	"multiagentcom/internal/service"
	"multiagentcom/internal/store"
)

func TestHTTPFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name":        "Todo HTTP Demo",
		"description": "flow test",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 列表的增删改查",
		"content":     "实现 Todo 列表的增删改查，并导出最小交付包。",
		"constraints": []string{"后端使用 Go"},
	})

	planBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)

	runBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{
		"taskId": planResult.Task.ID,
	})
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)

	deadline := time.Now().Add(3 * time.Second)
	var status service.RunStatusView
	for time.Now().Before(deadline) {
		statusBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/runs/"+runEnvelope.Run.ID+"/status")
		decodeResponse(t, statusBody, &status)
		if status.Run.Status == domain.RunStatusSucceeded {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run to succeed, got %s", status.Run.Status)
	}

	exportBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/delivery/export", map[string]any{
		"runId": runEnvelope.Run.ID,
	})

	var exportResponse struct {
		Artifact     domain.Artifact `json:"artifact"`
		DownloadPath string          `json:"downloadPath"`
	}
	decodeResponse(t, exportBody, &exportResponse)

	resp, err := server.Client().Get(server.URL + exportResponse.DownloadPath)
	if err != nil {
		t.Fatalf("download artifact: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected download status 200, got %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read artifact payload: %v", err)
	}
	assertZipPayloadContains(t, payload,
		"README.md",
		"docker-compose.yml",
		"generated-app/go.mod",
		"generated-app/main.go",
		"generated-app/Dockerfile",
		"web-app/package.json",
		"web-app/server.js",
		"web-app/index.html",
		"web-app/Dockerfile",
		"metadata/manifest.json",
		"metadata/release-gate.json",
	)
}

func TestHTTPSecurityHeaders(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-security-headers",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected nosniff header, got %s", resp.Header.Get("X-Content-Type-Options"))
	}
	if resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("expected no-referrer header, got %s", resp.Header.Get("Referrer-Policy"))
	}
}

func TestHTTPReadyReportsReady(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-ready",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DataRoot:     t.TempDir(),
		APIToken:     "secret-token",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSON(t, server.Client(), server.URL+"/ready")
	var ready struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	decodeResponse(t, body, &ready)
	if ready.Status != "ready" {
		t.Fatalf("expected ready status, got %+v", ready)
	}
	if len(ready.Checks) == 0 {
		t.Fatal("expected readiness checks")
	}
	for _, check := range ready.Checks {
		if check.Status != "ok" {
			t.Fatalf("expected readiness check ok, got %+v", ready.Checks)
		}
	}
}

func TestHTTPReadyChecksGitWorkspace(t *testing.T) {
	repoPath := initHTTPTempGitRepo(t)
	cfg := config.Config{
		Address:              ":0",
		ServiceName:          "test-http-ready-git",
		ArtifactRoot:         t.TempDir(),
		SandboxRoot:          t.TempDir(),
		WorkspaceProvider:    "git",
		WorkspaceGitRepoPath: repoPath,
		WorkspaceGitBaseRef:  "main",
		RuntimeProvider:      "local",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSON(t, server.Client(), server.URL+"/ready")
	assertContainsBody(t, body, `"name":"gitWorkspace"`)
	assertContainsBody(t, body, `"status":"ready"`)
}

func TestHTTPReadyClonesRemoteGitWorkspace(t *testing.T) {
	remotePath := initHTTPBareGitRemote(t)
	repoPath := filepath.Join(t.TempDir(), "workspace-repo")
	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-http-ready-git-remote-clone",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		WorkspaceProvider:          "git",
		WorkspaceGitRepoPath:       repoPath,
		WorkspaceGitRemoteURL:      remotePath,
		WorkspaceGitBaseRef:        "origin/main",
		WorkspaceGitFetchBeforeUse: true,
		RuntimeProvider:            "local",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSON(t, server.Client(), server.URL+"/ready")
	assertContainsBody(t, body, `"name":"gitWorkspace"`)
	assertContainsBody(t, body, `"status":"ready"`)
	if out := runHTTPTestGit(t, repoPath, "rev-parse", "--is-inside-work-tree"); strings.TrimSpace(out) != "true" {
		t.Fatalf("expected cloned repo, got %q", out)
	}
}

func TestHTTPReadyFetchesRemoteGitWorkspace(t *testing.T) {
	remotePath := initHTTPBareGitRemote(t)
	repoPath := filepath.Join(t.TempDir(), "workspace-repo")
	runHTTPTestGit(t, t.TempDir(), "clone", remotePath, repoPath)
	remoteWork := cloneHTTPBareRemote(t, remotePath)
	if err := os.WriteFile(filepath.Join(remoteWork, "REMOTE.md"), []byte("new base\n"), 0o644); err != nil {
		t.Fatalf("write remote file: %v", err)
	}
	runHTTPTestGit(t, remoteWork, "add", "REMOTE.md")
	runHTTPTestGit(t, remoteWork, "commit", "-m", "remote update")
	runHTTPTestGit(t, remoteWork, "push", "origin", "main")

	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-http-ready-git-remote-fetch",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		WorkspaceProvider:          "git",
		WorkspaceGitRepoPath:       repoPath,
		WorkspaceGitRemoteURL:      remotePath,
		WorkspaceGitBaseRef:        "origin/main",
		WorkspaceGitFetchBeforeUse: true,
		RuntimeProvider:            "local",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSON(t, server.Client(), server.URL+"/ready")
	assertContainsBody(t, body, `"status":"ready"`)
	remoteRef := strings.TrimSpace(runHTTPTestGit(t, repoPath, "rev-parse", "origin/main"))
	remoteMain := strings.TrimSpace(runHTTPTestGit(t, remoteWork, "rev-parse", "main"))
	if remoteRef != remoteMain {
		t.Fatalf("origin/main = %s, want %s", remoteRef, remoteMain)
	}
}

func TestHTTPReadyChecksContainerRuntimeBinary(t *testing.T) {
	cfg := config.Config{
		Address:                        ":0",
		ServiceName:                    "test-http-ready-container",
		ArtifactRoot:                   t.TempDir(),
		SandboxRoot:                    t.TempDir(),
		RuntimeProvider:                "container",
		RuntimeContainerImage:          "multiagent-runtime:test",
		RuntimeContainerBinary:         "/bin/sh",
		RuntimeContainerNetwork:        "none",
		RuntimeContainerReadonlyRootFS: true,
		RuntimeContainerWorkdir:        "/workspace",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSON(t, server.Client(), server.URL+"/ready")
	assertContainsBody(t, body, `"name":"runtime"`)
	assertContainsBody(t, body, `"status":"ready"`)
}

func TestHTTPReadyRejectsMissingContainerRuntimeBinary(t *testing.T) {
	cfg := config.Config{
		Address:                        ":0",
		ServiceName:                    "test-http-ready-container-missing",
		ArtifactRoot:                   t.TempDir(),
		SandboxRoot:                    t.TempDir(),
		RuntimeProvider:                "container",
		RuntimeContainerImage:          "multiagent-runtime:test",
		RuntimeContainerBinary:         "definitely-missing-container-runtime-binary",
		RuntimeContainerNetwork:        "none",
		RuntimeContainerReadonlyRootFS: true,
		RuntimeContainerWorkdir:        "/workspace",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSONExpectStatus(t, server.Client(), server.URL+"/ready", http.StatusServiceUnavailable)
	assertContainsBody(t, body, `"status":"not_ready"`)
	assertContainsBody(t, body, `"name":"runtime"`)
}

func TestHTTPReadyRejectsInvalidContainerRuntimeHardeningConfig(t *testing.T) {
	cfg := config.Config{
		Address:                        ":0",
		ServiceName:                    "test-http-ready-container-invalid-hardening",
		ArtifactRoot:                   t.TempDir(),
		SandboxRoot:                    t.TempDir(),
		RuntimeProvider:                "container",
		RuntimeContainerImage:          "multiagent-runtime:test",
		RuntimeContainerBinary:         "/bin/sh",
		RuntimeContainerNetwork:        "none",
		RuntimeContainerReadonlyRootFS: true,
		RuntimeContainerWorkdir:        "/workspace",
		RuntimeContainerCPUs:           "zero",
		RuntimeContainerTmpfs:          "tmp:rw",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSONExpectStatus(t, server.Client(), server.URL+"/ready", http.StatusServiceUnavailable)
	assertContainsBody(t, body, `"status":"not_ready"`)
	assertContainsBody(t, body, `"name":"config"`)
}

func TestHTTPReadyReportsConfigFailure(t *testing.T) {
	cfg := config.Config{
		Address:         ":0",
		ServiceName:     "test-http-ready-failure",
		ArtifactRoot:    t.TempDir(),
		SandboxRoot:     t.TempDir(),
		RuntimeProvider: "http",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSONExpectStatus(t, server.Client(), server.URL+"/ready", http.StatusServiceUnavailable)
	assertContainsBody(t, body, `"status":"not_ready"`)
	assertContainsBody(t, body, `"name":"config"`)
}

func TestHTTPReadyAcceptsMissingFileStoreState(t *testing.T) {
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-http-ready-file-missing",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "file",
		DataRoot:      t.TempDir(),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSON(t, server.Client(), server.URL+"/ready")
	assertContainsBody(t, body, `"name":"fileStoreState"`)
	assertContainsBody(t, body, `"status":"ready"`)
}

func TestHTTPReadyRejectsCorruptedFileStoreState(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.WriteFile(store.ServiceStatePath(dataRoot), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write corrupted state: %v", err)
	}
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-http-ready-file-corrupted",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "file",
		DataRoot:      dataRoot,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSONExpectStatus(t, server.Client(), server.URL+"/ready", http.StatusServiceUnavailable)
	assertContainsBody(t, body, `"name":"fileStoreState"`)
	assertContainsBody(t, body, `"status":"failed"`)
}

func TestHTTPReadyRejectsUnsupportedFileStoreStateVersion(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.WriteFile(store.ServiceStatePath(dataRoot), []byte(`{"version":2}`), 0o644); err != nil {
		t.Fatalf("write unsupported state: %v", err)
	}
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-http-ready-file-version",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "file",
		DataRoot:      dataRoot,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSONExpectStatus(t, server.Client(), server.URL+"/ready", http.StatusServiceUnavailable)
	assertContainsBody(t, body, `"name":"fileStoreState"`)
	assertContainsBody(t, body, `unsupported service state version`)
}

func TestHTTPReadyRejectsPostgresStoreWhenUnavailable(t *testing.T) {
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-http-ready-postgres-unavailable",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "postgres",
		PostgresDSN:   "postgres://multiagent@127.0.0.1:1/multiagentcom_test?sslmode=disable",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSONExpectStatus(t, server.Client(), server.URL+"/ready", http.StatusServiceUnavailable)
	assertContainsBody(t, body, `"name":"postgresStore"`)
}

func TestHTTPReadyAcceptsPostgresStore(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	resetHTTPPostgresStateForTest(t, context.Background(), dsn)
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-http-ready-postgres",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "postgres",
		PostgresDSN:   dsn,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSON(t, server.Client(), server.URL+"/ready")
	assertContainsBody(t, body, `"name":"postgresStore"`)
	assertContainsBody(t, body, `"status":"ready"`)
}

func TestHTTPReadyRejectsPostgresUnsupportedLegacyVersion(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	resetHTTPPostgresStateForTest(t, ctx, dsn)
	if err := store.NewPostgresStore(dsn).Save(ctx, []byte(`{"version":2}`)); err != nil {
		t.Fatalf("seed unsupported state: %v", err)
	}
	cfg := config.Config{Address: ":0", ServiceName: "test-http-ready-postgres-version", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), StoreProvider: "postgres", PostgresDSN: dsn}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := getJSONExpectStatus(t, server.Client(), server.URL+"/ready", http.StatusServiceUnavailable)
	assertContainsBody(t, body, `unsupported service state version`)
}

func resetHTTPPostgresStateForTest(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	raw := store.NewPostgresStore(dsn)
	if err := raw.Migrate(ctx); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}
	for _, table := range []string{"artifact_order", "artifacts", "run_order", "agent_runs", "task_order", "tasks", "contracts", "plans", "requirements", "projects", "service_state"} {
		if _, err := raw.DB().ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}

func TestHTTPRejectsOversizedJSONBody(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-body-limit",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	body := strings.NewReader(`{"name":"` + strings.Repeat("x", int(maxJSONBodyBytes)+1) + `"}`)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/projects", body)
	if err != nil {
		t.Fatalf("new oversized request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("send oversized request: %v", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read oversized response: %v", err)
	}

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", resp.StatusCode, string(payload))
	}
	assertContainsBody(t, payload, `"code":"REQUEST_TOO_LARGE"`)
}

func TestHTTPContractFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-contracts",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Contracts Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并生成契约。",
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})

	contractBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	var contract domain.Contract
	decodeResponse(t, contractBody, &contract)

	if contract.Version != 1 {
		t.Fatalf("expected contract version 1, got %d", contract.Version)
	}

	listBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/contracts")
	var listResponse struct {
		Items []domain.Contract `json:"items"`
		Count int               `json:"count"`
	}
	decodeResponse(t, listBody, &listResponse)

	if listResponse.Count != 1 || len(listResponse.Items) != 1 {
		t.Fatalf("expected one contract, got count=%d len=%d", listResponse.Count, len(listResponse.Items))
	}

	getBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/contracts/"+contract.ID)
	var stored domain.Contract
	decodeResponse(t, getBody, &stored)

	if len(stored.Endpoints) == 0 {
		t.Fatal("expected stored contract endpoints")
	}
	if stored.Endpoints[0].Method != http.MethodGet {
		t.Fatalf("expected first endpoint method GET, got %s", stored.Endpoints[0].Method)
	}
}

func TestHTTPContractValidationConflict(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-contract-validate",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Validate Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并生成契约。",
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})

	contractBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	var contract domain.Contract
	decodeResponse(t, contractBody, &contract)

	validateBody := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/validate", map[string]any{
		"contractId": contract.ID,
		"endpoints": []map[string]any{
			{"name": "ListTodo", "method": "GET", "path": "/api/todos"},
		},
		"schemas": []map[string]any{
			{
				"name": "Todo",
				"fields": []map[string]any{
					{"name": "id", "type": "string", "required": true},
					{"name": "title", "type": "number", "required": true},
				},
			},
		},
	}, http.StatusConflict)

	var result service.ContractValidationResult
	decodeResponse(t, validateBody, &result)

	if result.Passed {
		t.Fatal("expected validation result to fail")
	}
	if len(result.Conflicts) == 0 {
		t.Fatal("expected conflicts in validation result")
	}
	if result.RemediationTask == nil {
		t.Fatal("expected remediation task in validation result")
	}
}

func TestHTTPParallelDispatchFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-parallel",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Parallel Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})

	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)

	if len(dispatchResult.Tasks) != 3 {
		t.Fatalf("expected 3 dispatched tasks, got %d", len(dispatchResult.Tasks))
	}

	parallelBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/runs/parallel", map[string]any{
		"taskIds": []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	var parallelResult service.ParallelRunResult
	decodeResponse(t, parallelBody, &parallelResult)

	if len(parallelResult.Started) != 2 {
		t.Fatalf("expected 2 started runs, got %d", len(parallelResult.Started))
	}

	for _, started := range parallelResult.Started {
		waitForHTTPRun(t, server, project.ID, started.Run.ID)
	}

	listBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/tasks")
	var taskList struct {
		Items []domain.Task `json:"items"`
		Count int           `json:"count"`
	}
	decodeResponse(t, listBody, &taskList)

	if taskList.Count < 4 {
		t.Fatalf("expected at least 4 tasks including sprint1 seed and dispatched tasks, got %d", taskList.Count)
	}
}

func TestHTTPTaskContextFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-context",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Context Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})

	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)

	contextBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+dispatchResult.Tasks[0].ID+"/context/generate", map[string]any{})
	var generated service.TaskContextEnvelope
	decodeResponse(t, contextBody, &generated)

	if generated.Context.Version != 1 {
		t.Fatalf("expected context version 1, got %d", generated.Context.Version)
	}
	if generated.Context.Role != "backend" {
		t.Fatalf("expected backend role, got %s", generated.Context.Role)
	}

	latestBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/tasks/"+dispatchResult.Tasks[0].ID+"/context")
	var latest service.TaskContextEnvelope
	decodeResponse(t, latestBody, &latest)

	if latest.Context.ID != generated.Context.ID {
		t.Fatalf("expected latest context id %s, got %s", generated.Context.ID, latest.Context.ID)
	}
	if len(latest.Context.Sources) != 3 {
		t.Fatalf("expected 3 context sources, got %d", len(latest.Context.Sources))
	}
}

func TestHTTPStatusMatrixAndPanel(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-status-panel",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Status Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)
	parallelBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/runs/parallel", map[string]any{
		"taskIds": []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	var parallelResult service.ParallelRunResult
	decodeResponse(t, parallelBody, &parallelResult)
	for _, started := range parallelResult.Started {
		waitForHTTPRun(t, server, project.ID, started.Run.ID)
	}

	matrixBody := getJSON(t, server.Client(), server.URL+"/status/matrix?projectId="+project.ID)
	var matrix service.StatusMatrixView
	decodeResponse(t, matrixBody, &matrix)

	if len(matrix.Matrices) != 1 {
		t.Fatalf("expected 1 matrix, got %d", len(matrix.Matrices))
	}
	if matrix.SelectedProjectID != project.ID {
		t.Fatalf("expected selected project id %s, got %s", project.ID, matrix.SelectedProjectID)
	}

	resp, err := server.Client().Get(server.URL + "/status/panel")
	if err != nil {
		t.Fatalf("get status panel: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read status panel: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status panel 200, got %d", resp.StatusCode)
	}
	bodyText := string(body)
	for _, label := range []string{
		"Operations Dashboard",
		"Readiness",
		"Status Matrix",
		"Task Topology",
		"topologyPanel",
		"Failure Alerts",
		"HITL Conflicts",
		"conflictPanel",
		"Audit Trail",
		"Agent Message Log",
		"Token Cost Trend",
		"Sandboxes",
		"Snapshots",
		"Filter communications by taskId",
	} {
		if !strings.Contains(bodyText, label) {
			t.Fatalf("expected status panel html to contain %q, got %s", label, bodyText)
		}
	}
	if !strings.Contains(bodyText, "new EventSource(withAuth('/status/stream'))") {
		t.Fatalf("expected status panel html to include status stream EventSource, got %s", bodyText)
	}
	if !strings.Contains(bodyText, "setInterval(loadDashboard, 4000)") {
		t.Fatalf("expected status panel html to keep polling fallback, got %s", bodyText)
	}
	if strings.Contains(bodyText, "app.innerHTML = view.matrices.map") {
		t.Fatalf("expected status panel html to avoid old innerHTML matrix rendering")
	}
	if !strings.Contains(bodyText, "textContent") || !strings.Contains(bodyText, "replaceChildren") {
		t.Fatalf("expected status panel html to use DOM-safe rendering helpers, got %s", bodyText)
	}
	if !strings.Contains(bodyText, "renderPanelFetch") {
		t.Fatalf("expected status panel html to isolate project panel fetch failures, got %s", bodyText)
	}
	if !strings.Contains(bodyText, "renderConflictQueue") || !strings.Contains(bodyText, "/conflicts") || !strings.Contains(bodyText, "OPEN") || !strings.Contains(bodyText, "RESOLVED") {
		t.Fatalf("expected status panel html to render HITL conflict queue, got %s", bodyText)
	}
	if !strings.Contains(bodyText, "Resolve conflict") || !strings.Contains(bodyText, "postJSON") || !strings.Contains(bodyText, "Resolved from status panel") {
		t.Fatalf("expected status panel html to resolve HITL conflicts, got %s", bodyText)
	}
	if !strings.Contains(bodyText, "renderTopology") || !strings.Contains(bodyText, "createElementNS") {
		t.Fatalf("expected status panel html to render SVG task topology safely, got %s", bodyText)
	}
}

func TestHTTPStatusStream(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-status-stream",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/status/stream", nil)
	if err != nil {
		t.Fatalf("new status stream request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("get status stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status stream 200, got %d", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %s", resp.Header.Get("Content-Type"))
	}

	chunk := readStatusStreamChunk(t, resp.Body)
	if !strings.Contains(chunk, "event: status") || !strings.Contains(chunk, "data:") {
		t.Fatalf("expected stream payload to contain status event and data field, got %s", chunk)
	}
}

func TestHTTPStatusStreamTokenQueryAuth(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-status-stream-auth",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
		APIToken:     "demo-token",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	unauthorizedResp, err := server.Client().Get(server.URL + "/status/stream")
	if err != nil {
		t.Fatalf("get unauthorized status stream: %v", err)
	}
	defer unauthorizedResp.Body.Close()
	if unauthorizedResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status stream 401, got %d", unauthorizedResp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/status/stream?token=demo-token", nil)
	if err != nil {
		t.Fatalf("new authorized status stream request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("get authorized status stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected authorized status stream 200, got %d", resp.StatusCode)
	}

	chunk := readStatusStreamChunk(t, resp.Body)
	if !strings.Contains(chunk, "event: status") || !strings.Contains(chunk, "data:") {
		t.Fatalf("expected authorized stream payload to contain status event and data field, got %s", chunk)
	}
}

func TestHTTPSprintTwoAcceptanceFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-sprint2-acceptance",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name":        "Todo Sprint 2 Acceptance",
		"description": "AC-03/04/05/06/12",
	})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":           "实现 Todo 全栈功能",
		"content":         "实现 Todo 全栈功能，后端提供 API，前端提供页面，并展示状态面板。",
		"constraints":     []string{"后端使用 Go", "前端使用 Vue"},
		"acceptanceHints": []string{"双 Agent 并行执行", "可查看状态矩阵"},
	})

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})

	contractBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	var contract domain.Contract
	decodeResponse(t, contractBody, &contract)
	if contract.Version != 1 || len(contract.Endpoints) == 0 || len(contract.Schemas) == 0 {
		t.Fatalf("expected generated contract with v1 endpoints and schemas, got %+v", contract)
	}

	conflictBody := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/validate", map[string]any{
		"contractId": contract.ID,
		"endpoints": []map[string]any{
			{"name": "ListTodo", "method": "GET", "path": "/api/todos"},
		},
		"schemas": []map[string]any{
			{
				"name": "Todo",
				"fields": []map[string]any{
					{"name": "id", "type": "string", "required": true},
					{"name": "title", "type": "number", "required": true},
				},
			},
		},
	}, http.StatusConflict)
	var conflictResult service.ContractValidationResult
	decodeResponse(t, conflictBody, &conflictResult)
	if conflictResult.Passed || len(conflictResult.Conflicts) == 0 || conflictResult.RemediationTask == nil {
		t.Fatalf("expected contract validation conflict with remediation task, got %+v", conflictResult)
	}

	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)
	if len(dispatchResult.Tasks) != 3 {
		t.Fatalf("expected backend/frontend/integration tasks, got %d", len(dispatchResult.Tasks))
	}

	backendTask := dispatchResult.Tasks[0]
	frontendTask := dispatchResult.Tasks[1]
	integrationTask := dispatchResult.Tasks[2]

	backendContextBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+backendTask.ID+"/context/generate", map[string]any{})
	frontendContextBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+frontendTask.ID+"/context/generate", map[string]any{})
	var backendContext service.TaskContextEnvelope
	var frontendContext service.TaskContextEnvelope
	decodeResponse(t, backendContextBody, &backendContext)
	decodeResponse(t, frontendContextBody, &frontendContext)
	if backendContext.Context.Role != "backend" || frontendContext.Context.Role != "frontend" {
		t.Fatalf("expected backend/frontend context roles, got %s and %s", backendContext.Context.Role, frontendContext.Context.Role)
	}
	if sectionTitles(backendContext.Context.Sections) == sectionTitles(frontendContext.Context.Sections) {
		t.Fatal("expected backend and frontend context slices to differ")
	}

	parallelBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/runs/parallel", map[string]any{
		"taskIds": []string{backendTask.ID, frontendTask.ID},
	})
	var parallelResult service.ParallelRunResult
	decodeResponse(t, parallelBody, &parallelResult)
	if len(parallelResult.Started) != 2 {
		t.Fatalf("expected 2 started runs, got %d", len(parallelResult.Started))
	}
	for _, started := range parallelResult.Started {
		waitForHTTPRun(t, server, project.ID, started.Run.ID)
	}

	matrixBody := getJSON(t, server.Client(), server.URL+"/status/matrix?projectId="+project.ID)
	var matrix service.StatusMatrixView
	decodeResponse(t, matrixBody, &matrix)
	if len(matrix.Matrices) != 1 {
		t.Fatalf("expected one project matrix, got %d", len(matrix.Matrices))
	}
	if matrix.Matrices[0].ReadyTasks < 1 {
		t.Fatalf("expected at least one ready task for integration, got %d", matrix.Matrices[0].ReadyTasks)
	}
	if !containsAgentStatus(matrix.Matrices[0].AgentMatrix, "go-backend-agent", "COMPLETED") {
		t.Fatal("expected go-backend-agent to be completed in status matrix")
	}
	if !containsTaskID(matrix.Matrices[0].TaskMatrix, integrationTask.ID) {
		t.Fatal("expected integration task to appear in status matrix")
	}

	resp, err := server.Client().Get(server.URL + "/status/panel")
	if err != nil {
		t.Fatalf("get status panel: %v", err)
	}
	defer resp.Body.Close()
	panelBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read panel body: %v", err)
	}
	if !strings.Contains(string(panelBody), "Status Matrix") {
		t.Fatal("expected status panel html title")
	}
}

func TestHTTPSandboxIsolationFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-sandbox",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Sandbox Demo",
	})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})

	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)

	requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+dispatchResult.Tasks[0].ID+"/sandbox/fail", map[string]any{
		"reason": "simulated private sandbox crash",
	}, http.StatusAccepted)

	parallelBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/runs/parallel", map[string]any{
		"taskIds": []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	var parallelResult service.ParallelRunResult
	decodeResponse(t, parallelBody, &parallelResult)
	if len(parallelResult.Started) != 2 {
		t.Fatalf("expected 2 started runs, got %d", len(parallelResult.Started))
	}

	runByTask := make(map[string]domain.AgentRun, 2)
	for _, started := range parallelResult.Started {
		runByTask[started.Task.ID] = started.Run
	}

	backendStatus := waitForHTTPRunTerminal(t, server, project.ID, runByTask[dispatchResult.Tasks[0].ID].ID)
	frontendStatus := waitForHTTPRunTerminal(t, server, project.ID, runByTask[dispatchResult.Tasks[1].ID].ID)
	if backendStatus.Run.Status != domain.RunStatusFailed {
		t.Fatalf("expected backend run to fail, got %s", backendStatus.Run.Status)
	}
	if frontendStatus.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected frontend run to succeed, got %s", frontendStatus.Run.Status)
	}

	backendSandboxBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/runs/"+runByTask[dispatchResult.Tasks[0].ID].ID+"/sandbox")
	var backendSandbox service.SandboxView
	decodeResponse(t, backendSandboxBody, &backendSandbox)
	if backendSandbox.Sandbox.Status != domain.SandboxStatusFailed {
		t.Fatalf("expected backend sandbox FAILED, got %s", backendSandbox.Sandbox.Status)
	}

	sandboxesBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/sandboxes")
	var sandboxes struct {
		Items []service.SandboxView `json:"items"`
		Count int                   `json:"count"`
	}
	decodeResponse(t, sandboxesBody, &sandboxes)
	if sandboxes.Count != 2 || len(sandboxes.Items) != 2 {
		t.Fatalf("expected 2 sandboxes, got count=%d len=%d", sandboxes.Count, len(sandboxes.Items))
	}
}

func TestHTTPSharedSandboxMergeFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-shared-sandbox",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)

	mergeBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints":  contract.Endpoints,
		"schemas":    contract.Schemas,
	})

	var result service.SharedSandboxMergeResult
	decodeResponse(t, mergeBody, &result)
	if !result.Passed {
		t.Fatalf("expected merge to pass, got %+v", result)
	}
	if result.Sandbox.Scope != "SHARED" {
		t.Fatalf("expected shared sandbox scope, got %s", result.Sandbox.Scope)
	}
	if result.Sandbox.Status != domain.SandboxStatusReleased {
		t.Fatalf("expected shared sandbox RELEASED, got %s", result.Sandbox.Status)
	}

	sandboxesBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/sandboxes")
	var sandboxes struct {
		Items []service.SandboxView `json:"items"`
		Count int                   `json:"count"`
	}
	decodeResponse(t, sandboxesBody, &sandboxes)
	if sandboxes.Count != 3 || len(sandboxes.Items) != 3 {
		t.Fatalf("expected 3 sandboxes including shared gate, got count=%d len=%d", sandboxes.Count, len(sandboxes.Items))
	}
}

func TestHTTPSharedSandboxMergeConflict(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-shared-sandbox-conflict",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)

	conflictBody := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints": []map[string]any{
			{"name": "ListTodo", "method": "GET", "path": "/api/todos"},
		},
		"schemas": []map[string]any{
			{
				"name": "Todo",
				"fields": []map[string]any{
					{"name": "id", "type": "string", "required": true},
					{"name": "title", "type": "number", "required": true},
				},
			},
		},
	}, http.StatusConflict)

	var result service.SharedSandboxMergeResult
	decodeResponse(t, conflictBody, &result)
	if result.Passed {
		t.Fatal("expected merge to fail on contract conflicts")
	}
	if len(result.ContractConflicts) == 0 {
		t.Fatal("expected contract conflicts in response")
	}
	if result.RemediationTask == nil {
		t.Fatal("expected remediation task for conflicting merge")
	}
}

func TestHTTPSharedSandboxMergeIntegrationFailure(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-shared-sandbox-failure",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)

	failureBody := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":         []string{dispatched[0].ID, dispatched[1].ID},
		"contractId":      contract.ID,
		"endpoints":       contract.Endpoints,
		"schemas":         contract.Schemas,
		"simulateFailure": true,
	}, http.StatusConflict)

	var result service.SharedSandboxMergeResult
	decodeResponse(t, failureBody, &result)
	if result.Passed {
		t.Fatal("expected merge to fail on simulated integration failure")
	}
	if result.Sandbox.Status != domain.SandboxStatusFailed {
		t.Fatalf("expected shared sandbox FAILED, got %s", result.Sandbox.Status)
	}
	if len(result.ContractConflicts) != 0 {
		t.Fatalf("expected no contract conflicts, got %d", len(result.ContractConflicts))
	}
}

func TestHTTPWorkspaceCleanupDryRun(t *testing.T) {
	repoPath := initHTTPTempGitRepo(t)
	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-http-workspace-cleanup",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		DefaultAgent:               "http-manager-agent",
		WorkspaceProvider:          "git",
		WorkspaceGitRepoPath:       repoPath,
		WorkspaceGitBaseRef:        "main",
		WorkspaceGitCleanupEnabled: false,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints":  contract.Endpoints,
		"schemas":    contract.Schemas,
	})

	body := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/workspaces/cleanup", map[string]any{"dryRun": true, "scope": "PRIVATE"})
	var cleanup service.CleanupWorkspacesResult
	decodeResponse(t, body, &cleanup)
	if !cleanup.DryRun || cleanup.RemovedWorktrees != 2 || cleanup.DeletedBranches != 0 {
		t.Fatalf("expected dry-run private worktree cleanup plan, got %+v", cleanup)
	}
	worktrees := runHTTPTestGit(t, repoPath, "worktree", "list", "--porcelain")
	if strings.Count(worktrees, "multiagent/") < 3 {
		t.Fatalf("expected dry run to keep private and shared worktrees, got\n%s", worktrees)
	}
}

func TestHTTPWorkspaceRebaseDryRun(t *testing.T) {
	repoPath := initHTTPTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-http-workspace-rebase", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "http-manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "HTTP Rebase Demo"})
	var project domain.Project
	decodeResponse(t, projectBody, &project)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{"title": "实现 Todo", "content": "实现 Todo 交付包"})
	planBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)
	runBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{"taskId": planResult.Task.ID})
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)
	waitForHTTPRun(t, server, project.ID, runEnvelope.Run.ID)
	sandboxBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/runs/"+runEnvelope.Run.ID+"/sandbox")
	var sandbox service.SandboxView
	decodeResponse(t, sandboxBody, &sandbox)
	if err := os.WriteFile(filepath.Join(repoPath, "BASE.md"), []byte("new base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runHTTPTestGit(t, repoPath, "add", "BASE.md")
	runHTTPTestGit(t, repoPath, "commit", "-m", "advance base")

	body := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/workspaces/rebase", map[string]any{"dryRun": true, "sandboxIds": []string{sandbox.Sandbox.ID}, "targetRef": "main"})
	var rebase service.RebaseWorkspacesResult
	decodeResponse(t, body, &rebase)
	if !rebase.DryRun || len(rebase.Results) != 1 || rebase.Results[0].SandboxID != sandbox.Sandbox.ID || rebase.Results[0].Status != "DRY_RUN" {
		t.Fatalf("expected HTTP rebase dry-run result, got %+v", rebase)
	}
}

func TestHTTPWorkspaceRebaseValidation(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-http-workspace-rebase-validation", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()
	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "HTTP Rebase Validation"})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	missingTarget := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/workspaces/rebase", map[string]any{"all": true}, http.StatusBadRequest)
	assertContainsBody(t, missingTarget, "targetRef")
	missingSelection := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/workspaces/rebase", map[string]any{"targetRef": "main"}, http.StatusBadRequest)
	assertContainsBody(t, missingSelection, "sandboxIds")
}

func TestHTTPSharedSandboxFailureAutoRollback(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-snapshot-auto-rollback",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+dispatched[0].ID+"/context/generate", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+dispatched[1].ID+"/context/generate", map[string]any{})

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints":  contract.Endpoints,
		"schemas":    contract.Schemas,
	})

	failureBody := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":         []string{dispatched[0].ID, dispatched[1].ID},
		"contractId":      contract.ID,
		"endpoints":       contract.Endpoints,
		"schemas":         contract.Schemas,
		"simulateFailure": true,
	}, http.StatusConflict)

	var result service.SharedSandboxMergeResult
	decodeResponse(t, failureBody, &result)
	if result.Rollback == nil {
		t.Fatal("expected rollback details in response")
	}
	if result.Rollback.ActiveBranch == "main" {
		t.Fatalf("expected auto rollback to move active branch, got %s", result.Rollback.ActiveBranch)
	}

	snapshotsBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/snapshots")
	var snapshots struct {
		Items []domain.Snapshot `json:"items"`
		Count int               `json:"count"`
	}
	decodeResponse(t, snapshotsBody, &snapshots)
	if snapshots.Count != 2 || len(snapshots.Items) != 2 {
		t.Fatalf("expected stable snapshot plus rollback snapshot, got count=%d len=%d", snapshots.Count, len(snapshots.Items))
	}
}

func TestHTTPRollbackSnapshotRestoresGitWorkspace(t *testing.T) {
	repoPath := initHTTPTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-http-snapshot-rollback-restore", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "http-manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", WorkspaceGitCleanupEnabled: true, WorkspaceGitCleanupDeleteBranches: false}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints":  contract.Endpoints,
		"schemas":    contract.Schemas,
	})

	snapshotsBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/snapshots")
	var snapshots struct {
		Items []domain.Snapshot `json:"items"`
		Count int               `json:"count"`
	}
	decodeResponse(t, snapshotsBody, &snapshots)
	if snapshots.Count != 1 || snapshots.Items[0].WorkspaceChecksum == "" {
		t.Fatalf("expected one git workspace snapshot, got %+v", snapshots)
	}
	originalHead := strings.TrimSpace(runHTTPTestGit(t, repoPath, "rev-parse", "HEAD"))

	rollbackBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/snapshots/rollback", map[string]any{
		"snapshotId": snapshots.Items[0].ID,
		"reason":     "HTTP git restore",
	})
	var rollback service.RollbackResult
	decodeResponse(t, rollbackBody, &rollback)
	if rollback.Workspace == nil || !rollback.Workspace.Restored {
		t.Fatalf("expected rollback workspace result, got %+v", rollback)
	}
	if rollback.Workspace.Sandbox.Scope != "SHARED" || rollback.Workspace.Sandbox.Status != domain.SandboxStatusReleased {
		t.Fatalf("expected released shared sandbox, got %+v", rollback.Workspace.Sandbox)
	}
	if got := strings.TrimSpace(runHTTPTestGit(t, rollback.Workspace.Sandbox.WorkspacePath, "rev-parse", "HEAD")); got != snapshots.Items[0].WorkspaceChecksum {
		t.Fatalf("expected restored head %s, got %s", snapshots.Items[0].WorkspaceChecksum, got)
	}
	if got := strings.TrimSpace(runHTTPTestGit(t, repoPath, "rev-parse", "HEAD")); got != originalHead {
		t.Fatalf("expected original shared head %s to stay unchanged, got %s", originalHead, got)
	}

	snapshotsBody = getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/snapshots")
	decodeResponse(t, snapshotsBody, &snapshots)
	latest := snapshots.Items[len(snapshots.Items)-1]
	if latest.WorkspaceChecksum != snapshots.Items[0].WorkspaceChecksum || latest.WorkspaceStateRef != rollback.Workspace.StateRef {
		t.Fatalf("expected rollback snapshot to preserve git workspace metadata, got latest=%+v rollback=%+v", latest, rollback.Workspace)
	}
}

func TestHTTPRollbackSnapshotCreatesParallelBranchTimeline(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-snapshot-rollback",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints":  contract.Endpoints,
		"schemas":    contract.Schemas,
	})

	snapshotsBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/snapshots")
	var snapshots struct {
		Items []domain.Snapshot `json:"items"`
		Count int               `json:"count"`
	}
	decodeResponse(t, snapshotsBody, &snapshots)
	if snapshots.Count != 1 {
		t.Fatalf("expected 1 initial snapshot, got %d", snapshots.Count)
	}

	rollbackBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/snapshots/rollback", map[string]any{
		"snapshotId": snapshots.Items[0].ID,
		"reason":     "manual rollback verification",
	})
	var rollback service.RollbackResult
	decodeResponse(t, rollbackBody, &rollback)
	if rollback.ActiveBranch == rollback.PreviousBranch {
		t.Fatalf("expected rollback to create a new branch, got %s", rollback.ActiveBranch)
	}

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints":  contract.Endpoints,
		"schemas":    contract.Schemas,
	})

	snapshotsBody = getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/snapshots")
	decodeResponse(t, snapshotsBody, &snapshots)
	if snapshots.Count != 3 {
		t.Fatalf("expected 3 snapshots after rollback branch checkpoint, got %d", snapshots.Count)
	}
	if snapshots.Items[0].Branch != "main" {
		t.Fatalf("expected original branch main, got %s", snapshots.Items[0].Branch)
	}
	if snapshots.Items[1].Branch != rollback.ActiveBranch || snapshots.Items[2].Branch != rollback.ActiveBranch {
		t.Fatalf("expected rollback branch %s, got %s and %s", rollback.ActiveBranch, snapshots.Items[1].Branch, snapshots.Items[2].Branch)
	}
}

func TestHTTPApplyHumanOverride(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-human-override",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Override Demo",
	})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并支持人工接管。",
	})
	planBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)

	runBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{
		"taskId": planResult.Task.ID,
	})
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)
	waitForHTTPTaskStatus(t, server, project.ID, runEnvelope.Run.ID, domain.TaskStatusInProgress)

	overrideBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/overrides", map[string]any{
		"taskId":      planResult.Task.ID,
		"operator":    "reviewer",
		"instruction": "请优先保留 README 说明并按人工要求继续执行",
		"lockScope":   "TASK",
	})
	var overrideResult service.HumanOverrideResult
	decodeResponse(t, overrideBody, &overrideResult)
	if overrideResult.Task.Status != domain.TaskStatusHumanOverride {
		t.Fatalf("expected HUMAN_OVERRIDE status, got %s", overrideResult.Task.Status)
	}

	status := waitForHTTPRunTerminal(t, server, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success after override, got %s", status.Run.Status)
	}
	if !strings.Contains(status.Run.ResultSummary, "applied human override by reviewer") {
		t.Fatalf("expected run summary to mention applied override, got %s", status.Run.ResultSummary)
	}
}

func TestHTTPApplyCodeLock(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-code-lock",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Code Lock Demo",
	})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并支持人工锁定代码。",
	})
	planBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)

	lockedSource := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\t// LOCKED BY HUMAN\n\tfmt.Println(\"human locked main\")\n}\n"
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/locks", map[string]any{
		"taskId":     planResult.Task.ID,
		"path":       "generated-app/main.go",
		"content":    lockedSource,
		"lockMode":   "go_symbol",
		"language":   "go",
		"symbolKind": "func",
		"symbolName": "main",
		"createdBy":  "reviewer",
	})

	runBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{
		"taskId": planResult.Task.ID,
	})
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)
	status := waitForHTTPRunTerminal(t, server, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s", status.Run.Status)
	}

	sandboxBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/runs/"+runEnvelope.Run.ID+"/sandbox")
	var sandbox service.SandboxView
	decodeResponse(t, sandboxBody, &sandbox)

	mainPath := filepath.Join(sandbox.Sandbox.WorkspacePath, "bundle", "generated-app", "main.go")
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read locked main.go: %v", err)
	}
	if !strings.Contains(string(data), "human locked main") || !strings.Contains(string(data), "type todo struct") {
		t.Fatalf("expected locked main function and preserved generated declarations, got:\n%s", string(data))
	}
}

func TestHTTPApplyHumanOverrideLeaseConflict(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-http-human-override-conflict", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "http-manager-agent"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "Override Conflict"})
	var project domain.Project
	decodeResponse(t, projectBody, &project)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{"title": "Todo", "content": "Support override conflicts."})
	planBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)
	runBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{"taskId": planResult.Task.ID})
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)
	waitForHTTPTaskStatus(t, server, project.ID, runEnvelope.Run.ID, domain.TaskStatusInProgress)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/overrides", map[string]any{"taskId": planResult.Task.ID, "owner": "reviewer-a", "instruction": "first", "lockScope": "TASK", "ttlSeconds": 3600})
	body := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/overrides", map[string]any{"taskId": planResult.Task.ID, "owner": "reviewer-b", "instruction": "second", "lockScope": "TASK", "ttlSeconds": 3600}, http.StatusConflict)
	var result service.HumanOverrideResult
	decodeResponse(t, body, &result)
	if result.Conflict == nil || result.Conflict.Kind != "human_override" || result.Conflict.Status != "OPEN" {
		t.Fatalf("expected human override conflict result, got %+v", result)
	}
}

func TestHTTPApplyCodeLockLeaseConflictAndResolve(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-http-code-lock-conflict", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "http-manager-agent"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "Code Lock Conflict"})
	var project domain.Project
	decodeResponse(t, projectBody, &project)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/locks", map[string]any{"path": "README.md", "content": "# LOCKED BY HUMAN\nfirst\n", "owner": "reviewer-a", "ttlSeconds": 3600})
	body := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/locks", map[string]any{"path": "README.md", "content": "# LOCKED BY HUMAN\nsecond\n", "owner": "reviewer-b", "ttlSeconds": 3600}, http.StatusConflict)
	var lockResult service.CodeLockResult
	decodeResponse(t, body, &lockResult)
	if lockResult.Conflict == nil || lockResult.Conflict.Kind != "code_lock" {
		t.Fatalf("expected code lock conflict result, got %+v", lockResult)
	}

	listBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/conflicts")
	var list struct {
		Items []domain.ConflictQueueEntry `json:"items"`
		Count int                         `json:"count"`
	}
	decodeResponse(t, listBody, &list)
	if list.Count != 1 || list.Items[0].Status != "OPEN" {
		t.Fatalf("expected one open conflict, got %+v", list)
	}
	resolveBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/conflicts/"+list.Items[0].ID+"/resolve", map[string]any{"resolvedBy": "lead", "resolutionNote": "accepted reviewer-a"})
	var resolved domain.ConflictQueueEntry
	decodeResponse(t, resolveBody, &resolved)
	if resolved.Status != "RESOLVED" || resolved.ResolvedBy != "lead" {
		t.Fatalf("expected resolved conflict by lead, got %+v", resolved)
	}
}

func TestHTTPApplyCodeLockRejectsMarkerOutsideSelectedSymbol(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-http-code-lock-marker", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "http-manager-agent"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "Invalid Code Lock"})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	body := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/locks", map[string]any{
		"path":       "generated-app/main.go",
		"content":    "package main\n\n// LOCKED BY HUMAN\n\nfunc main() {\n\tprintln(\"not locked\")\n}\n",
		"lockMode":   "go_symbol",
		"language":   "go",
		"symbolKind": "func",
		"symbolName": "main",
		"createdBy":  "reviewer",
	}, http.StatusBadRequest)
	assertContainsBody(t, body, "marker must be inside selected Go symbol")
}

func TestHTTPApplyCodeLockAcceptsGroupedDeclarationSymbol(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-http-code-lock-grouped-decl", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "http-manager-agent"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "Grouped Declaration Code Lock"})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/locks", map[string]any{
		"path":       "generated-app/main.go",
		"content":    "package main\n\nvar (\n\t// LOCKED BY HUMAN\n\tdefaultTitle = \"human\"\n\tignoredTitle = \"ignored\"\n)\n",
		"lockMode":   "go_symbol",
		"language":   "go",
		"symbolKind": "var",
		"symbolName": "defaultTitle",
		"createdBy":  "reviewer",
	})
}

func TestHTTPApplyCodeLockAcceptsReceiverQualifiedMethod(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-http-code-lock-method-receiver", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "http-manager-agent"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "Method Receiver Code Lock"})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/locks", map[string]any{
		"path":       "generated-app/main.go",
		"content":    "package main\n\ntype user struct{}\n\nfunc (u user) label() string {\n\t// LOCKED BY HUMAN\n\treturn \"user\"\n}\n",
		"lockMode":   "go_symbol",
		"language":   "go",
		"symbolKind": "method",
		"symbolName": "user.label",
		"createdBy":  "reviewer",
	})
}

func TestHTTPListCommunicationLogs(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-communications",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, _, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+dispatched[0].ID+"/context/generate", map[string]any{})

	body := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/communications")
	var communications struct {
		Items []domain.CommunicationLog `json:"items"`
		Count int                       `json:"count"`
	}
	decodeResponse(t, body, &communications)
	if communications.Count != len(communications.Items) {
		t.Fatalf("expected count %d to match items length %d", communications.Count, len(communications.Items))
	}
	if len(communications.Items) < 6 {
		t.Fatalf("expected at least 6 communication logs, got %d", len(communications.Items))
	}

	filteredBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/communications?taskId="+dispatched[0].ID)
	var filtered struct {
		Items []domain.CommunicationLog `json:"items"`
		Count int                       `json:"count"`
	}
	decodeResponse(t, filteredBody, &filtered)
	if filtered.Count != len(filtered.Items) {
		t.Fatalf("expected filtered count %d to match items length %d", filtered.Count, len(filtered.Items))
	}
	if len(filtered.Items) < 3 {
		t.Fatalf("expected at least 3 filtered communication logs, got %d", len(filtered.Items))
	}
	for _, item := range filtered.Items {
		if item.TaskID != dispatched[0].ID {
			t.Fatalf("expected filtered task id %s, got %s", dispatched[0].ID, item.TaskID)
		}
	}

	pagedBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/communications?limit=2&offset=1")
	var paged struct {
		Items  []domain.CommunicationLog `json:"items"`
		Count  int                       `json:"count"`
		Total  int                       `json:"total"`
		Limit  int                       `json:"limit"`
		Offset int                       `json:"offset"`
	}
	decodeResponse(t, pagedBody, &paged)
	if paged.Count != 2 || len(paged.Items) != 2 || paged.Total != communications.Count || paged.Limit != 2 || paged.Offset != 1 {
		t.Fatalf("unexpected paged communications response: %+v", paged)
	}
	if paged.Items[0].ID != communications.Items[1].ID {
		t.Fatalf("expected offset item %s, got %s", communications.Items[1].ID, paged.Items[0].ID)
	}

	invalidBody := getJSONExpectStatus(t, server.Client(), server.URL+"/projects/"+project.ID+"/communications?limit=0", http.StatusBadRequest)
	assertContainsBody(t, invalidBody, `"code":"INVALID_QUERY"`)
}

func TestHTTPGetTokenCosts(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-token-costs",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, _, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)

	body := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/token-costs")
	var trend service.TokenCostTrend
	decodeResponse(t, body, &trend)
	if len(trend.Points) != 2 {
		t.Fatalf("expected 2 token cost points, got %d", len(trend.Points))
	}
	if trend.TotalTokens <= 0 || trend.EstimatedCostUSD <= 0 {
		t.Fatalf("expected positive token totals and cost, got %+v", trend)
	}

	filteredBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/token-costs?taskId="+dispatched[0].ID)
	var filtered service.TokenCostTrend
	decodeResponse(t, filteredBody, &filtered)
	if len(filtered.Points) != 1 {
		t.Fatalf("expected 1 filtered token cost point, got %d", len(filtered.Points))
	}
	if filtered.Points[0].TaskID != dispatched[0].ID {
		t.Fatalf("expected filtered task id %s, got %s", dispatched[0].ID, filtered.Points[0].TaskID)
	}
}

func TestHTTPAuditLogs(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-audit-logs",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Audit Demo",
	}, nil)
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并导出标准交付包。",
	}, nil)
	planBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{}, nil)
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)

	runBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{
		"taskId": planResult.Task.ID,
	}, nil)
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)
	waitForHTTPRun(t, server, project.ID, runEnvelope.Run.ID)

	exportBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/delivery/export", map[string]any{
		"runId": runEnvelope.Run.ID,
	}, nil)
	var exportResponse struct {
		DownloadPath string `json:"downloadPath"`
	}
	decodeResponse(t, exportBody, &exportResponse)
	resp, err := server.Client().Get(server.URL + exportResponse.DownloadPath)
	if err != nil {
		t.Fatalf("download delivery artifact: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected download status 200, got %d", resp.StatusCode)
	}

	body := getJSONWithHeaders(t, server.Client(), server.URL+"/projects/"+project.ID+"/audit-logs", nil)
	var logs struct {
		Items []domain.AuditLog `json:"items"`
		Count int               `json:"count"`
	}
	decodeResponse(t, body, &logs)
	if logs.Count != len(logs.Items) {
		t.Fatalf("expected count %d to match items length %d", logs.Count, len(logs.Items))
	}
	if len(logs.Items) < 4 {
		t.Fatalf("expected at least 4 audit log entries, got %d", len(logs.Items))
	}
	foundExport := false
	foundDownload := false
	for _, item := range logs.Items {
		switch item.Action {
		case "DELIVERY_EXPORT":
			foundExport = true
		case "DELIVERY_DOWNLOAD":
			foundDownload = true
		}
	}
	if !foundExport || !foundDownload {
		t.Fatalf("expected DELIVERY_EXPORT and DELIVERY_DOWNLOAD audit entries, got %+v", logs.Items)
	}

	since := logs.Items[len(logs.Items)-1].Timestamp.Format(time.RFC3339Nano)
	pagedBody := getJSONWithHeaders(t, server.Client(), server.URL+"/projects/"+project.ID+"/audit-logs?limit=1&since="+since, nil)
	var paged struct {
		Items  []domain.AuditLog `json:"items"`
		Count  int               `json:"count"`
		Total  int               `json:"total"`
		Limit  int               `json:"limit"`
		Offset int               `json:"offset"`
	}
	decodeResponse(t, pagedBody, &paged)
	if paged.Count != 1 || len(paged.Items) != 1 || paged.Limit != 1 || paged.Total < 1 {
		t.Fatalf("unexpected paged audit logs response: %+v", paged)
	}
}

func TestHTTPScopedAuthAllowsAndDeniesPrivilegedActions(t *testing.T) {
	operatorToken := "operator-token"
	viewerToken := "viewer-token"
	otherProjectToken := "other-project-token"
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-scoped-auth",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
		AuthTokens: scopedAuthTokensJSON(
			scopedAuthRecord(operatorToken, "ops-user", []string{"operator", "delivery"}, ""),
			scopedAuthRecord(viewerToken, "viewer-user", []string{"viewer"}, ""),
			scopedAuthRecord(otherProjectToken, "other-project-user", []string{"operator"}, "proj_other"),
		),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	operatorHeaders := map[string]string{"Authorization": "Bearer " + operatorToken}
	viewerHeaders := map[string]string{"Authorization": "Bearer " + viewerToken}
	otherProjectHeaders := map[string]string{"Authorization": "Bearer " + otherProjectToken}

	projectBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Scoped Auth Demo",
	}, operatorHeaders)
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo API",
		"content": "实现 Todo API 并返回交付包。",
	}, operatorHeaders)
	planBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{}, operatorHeaders)
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)
	runBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{"taskId": planResult.Task.ID}, operatorHeaders)
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)
	waitForHTTPRunStatus(t, server, project.ID, runEnvelope.Run.ID, operatorHeaders, domain.RunStatusSucceeded)

	requestJSONExpectStatusWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/delivery/export", map[string]any{
		"runId": runEnvelope.Run.ID,
	}, http.StatusOK, operatorHeaders)
	requestJSONExpectStatusWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/delivery/export", map[string]any{
		"runId": runEnvelope.Run.ID,
	}, http.StatusForbidden, viewerHeaders)
	requestJSONExpectStatusWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/overrides", map[string]any{
		"taskId":      planResult.Task.ID,
		"operator":    "ops-user",
		"instruction": "pause",
	}, http.StatusForbidden, otherProjectHeaders)
}

func TestHTTPScopedAuthTokenFile(t *testing.T) {
	token := "file-token"
	tokensPath := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(tokensPath, []byte(scopedAuthTokensJSON(scopedAuthRecord(token, "file-ops", []string{"operator"}, ""))), 0o644); err != nil {
		t.Fatalf("write auth tokens file: %v", err)
	}
	cfg := config.Config{
		Address:        ":0",
		ServiceName:    "test-http-scoped-auth-file",
		ArtifactRoot:   t.TempDir(),
		SandboxRoot:    t.TempDir(),
		DefaultAgent:   "http-manager-agent",
		AuthTokensFile: tokensPath,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	headers := map[string]string{"X-API-Key": token}
	body := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{"name": "File Token Demo"}, headers)
	var project domain.Project
	decodeResponse(t, body, &project)
	logsBody := getJSONWithHeaders(t, server.Client(), server.URL+"/projects/"+project.ID+"/audit-logs", headers)
	var logs struct {
		Items []domain.AuditLog `json:"items"`
	}
	decodeResponse(t, logsBody, &logs)
	if len(logs.Items) == 0 || logs.Items[0].Actor != "file-ops" {
		t.Fatalf("expected file token actor in audit logs, got %+v", logs.Items)
	}
}

func TestHTTPAuthMiddleware(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-auth",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
		APIToken:     "demo-token",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	healthResp, err := server.Client().Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected health 200, got %d", healthResp.StatusCode)
	}

	unauthorized := requestJSONExpectStatusWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Unauthorized Demo",
	}, http.StatusUnauthorized, nil)
	assertContainsBody(t, unauthorized, `"code":"UNAUTHORIZED"`)

	headers := map[string]string{
		"Authorization": "Bearer demo-token",
		"X-Actor":       "release-manager",
	}
	projectBody := requestJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Authorized Demo",
	}, headers)
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	auditBody := getJSONWithHeaders(t, server.Client(), server.URL+"/projects/"+project.ID+"/audit-logs", headers)
	var logs struct {
		Items []domain.AuditLog `json:"items"`
	}
	decodeResponse(t, auditBody, &logs)
	if len(logs.Items) == 0 || logs.Items[0].Actor != "release-manager" {
		t.Fatalf("expected release-manager audit actor, got %+v", logs.Items)
	}
}

func TestHTTPAlerts(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-alerts",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Alert Demo",
	})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并模拟私有沙盒失败。",
	})
	planBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)

	requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+planResult.Task.ID+"/sandbox/fail", map[string]any{
		"reason": "simulated crash",
	}, http.StatusAccepted)
	runBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{
		"taskId": planResult.Task.ID,
	})
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)
	status := waitForHTTPRunTerminal(t, server, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", status.Run.Status)
	}

	body := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/alerts")
	var alerts struct {
		Items  []domain.Alert `json:"items"`
		Count  int            `json:"count"`
		Total  int            `json:"total"`
		Limit  int            `json:"limit"`
		Offset int            `json:"offset"`
	}
	decodeResponse(t, body, &alerts)
	if alerts.Count == 0 || len(alerts.Items) == 0 {
		t.Fatal("expected alert entries")
	}
	if alerts.Items[0].Type != "RUN_FAILURE" {
		t.Fatalf("expected RUN_FAILURE alert, got %+v", alerts.Items[0])
	}
	if alerts.Total != alerts.Count || alerts.Limit != 100 || alerts.Offset != 0 {
		t.Fatalf("unexpected alert pagination metadata: %+v", alerts)
	}

	since := alerts.Items[0].Timestamp.Format(time.RFC3339Nano)
	pagedBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/alerts?limit=1&since="+since)
	var paged struct {
		Items  []domain.Alert `json:"items"`
		Count  int            `json:"count"`
		Total  int            `json:"total"`
		Limit  int            `json:"limit"`
		Offset int            `json:"offset"`
	}
	decodeResponse(t, pagedBody, &paged)
	if paged.Count != 1 || len(paged.Items) != 1 || paged.Total != 1 || paged.Limit != 1 || paged.Offset != 0 {
		t.Fatalf("unexpected paged alerts response: %+v", paged)
	}
}

func TestHTTPStartPreview(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-preview",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	project, contract, dispatched := prepareHTTPSharedSandboxMergeScenario(t, server)
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/shared-sandbox/merge", map[string]any{
		"taskIds":    []string{dispatched[0].ID, dispatched[1].ID},
		"contractId": contract.ID,
		"endpoints":  contract.Endpoints,
		"schemas":    contract.Schemas,
	})

	previewBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/preview/start", map[string]any{})
	var preview service.PreviewStartResult
	decodeResponse(t, previewBody, &preview)
	if preview.Preview.Status != "READY" {
		t.Fatalf("expected preview READY, got %s", preview.Preview.Status)
	}

	resp, err := server.Client().Get(server.URL + preview.Preview.URL)
	if err != nil {
		t.Fatalf("get preview page: %v", err)
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read preview page: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected preview page 200, got %d", resp.StatusCode)
	}
	body := string(page)
	if !strings.Contains(body, "Todo Preview Workspace") || !strings.Contains(body, "Hot reload watching") {
		t.Fatalf("expected preview page content, got %s", body)
	}

	statusBody := getJSON(t, server.Client(), server.URL+preview.Preview.URL+"/status")
	var previewStatus domain.Preview
	decodeResponse(t, statusBody, &previewStatus)
	if previewStatus.Revision == "" {
		t.Fatal("expected preview revision in status response")
	}
}

func prepareHTTPSharedSandboxMergeScenario(t *testing.T, server *httptest.Server) (domain.Project, domain.Contract, []domain.Task) {
	t.Helper()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Shared Sandbox Demo",
	})
	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})

	contractBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	var contract domain.Contract
	decodeResponse(t, contractBody, &contract)

	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)

	parallelBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/runs/parallel", map[string]any{
		"taskIds": []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	var parallelResult service.ParallelRunResult
	decodeResponse(t, parallelBody, &parallelResult)
	for _, started := range parallelResult.Started {
		waitForHTTPRun(t, server, project.ID, started.Run.ID)
	}

	return project, contract, dispatchResult.Tasks
}

func requestJSON(t *testing.T, client *http.Client, method, url string, payload any) []byte {
	t.Helper()
	return requestJSONWithHeaders(t, client, method, url, payload, nil)
}

func requestJSONWithHeaders(t *testing.T, client *http.Client, method, url string, payload any, headers map[string]string) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode >= 300 {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(data))
	}

	return data
}

func requestJSONExpectStatus(t *testing.T, client *http.Client, method, url string, payload any, expectedStatus int) []byte {
	t.Helper()
	return requestJSONExpectStatusWithHeaders(t, client, method, url, payload, expectedStatus, nil)
}

func requestJSONExpectStatusWithHeaders(t *testing.T, client *http.Client, method, url string, payload any, expectedStatus int, headers map[string]string) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode != expectedStatus {
		t.Fatalf("expected status %d, got %d: %s", expectedStatus, resp.StatusCode, string(data))
	}

	return data
}

func getJSON(t *testing.T, client *http.Client, url string) []byte {
	t.Helper()
	return getJSONWithHeaders(t, client, url, nil)
}

func getJSONExpectStatus(t *testing.T, client *http.Client, url string, expectedStatus int) []byte {
	t.Helper()
	return getJSONWithHeadersExpectStatus(t, client, url, expectedStatus, nil)
}

func getJSONWithHeaders(t *testing.T, client *http.Client, url string, headers map[string]string) []byte {
	t.Helper()
	return getJSONWithHeadersExpectStatus(t, client, url, http.StatusOK, headers)
}

func getJSONWithHeadersExpectStatus(t *testing.T, client *http.Client, url string, expectedStatus int, headers map[string]string) []byte {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new get request: %v", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode != expectedStatus {
		t.Fatalf("expected status %d, got %d: %s", expectedStatus, resp.StatusCode, string(data))
	}

	return data
}

func readStatusStreamChunk(t *testing.T, body io.Reader) string {
	t.Helper()

	reader := bufio.NewReader(body)
	var lines strings.Builder
	for i := 0; i < 2; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read status stream line %d: %v", i+1, err)
		}
		lines.WriteString(line)
	}

	return lines.String()
}

func assertContainsBody(t *testing.T, body []byte, fragment string) {
	t.Helper()
	if !strings.Contains(string(body), fragment) {
		t.Fatalf("expected body to contain %s, got %s", fragment, string(body))
	}
}

func initHTTPBareGitRemote(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	barePath := filepath.Join(t.TempDir(), "remote.git")
	runHTTPTestGit(t, t.TempDir(), "init", "--bare", barePath)
	workPath := cloneHTTPBareRemote(t, barePath)
	if err := os.WriteFile(filepath.Join(workPath, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runHTTPTestGit(t, workPath, "add", "README.md")
	runHTTPTestGit(t, workPath, "commit", "-m", "initial")
	runHTTPTestGit(t, workPath, "branch", "-M", "main")
	runHTTPTestGit(t, workPath, "push", "origin", "main")
	runHTTPTestGit(t, barePath, "symbolic-ref", "HEAD", "refs/heads/main")
	return barePath
}

func cloneHTTPBareRemote(t *testing.T, barePath string) string {
	t.Helper()
	workPath := filepath.Join(t.TempDir(), "work")
	runHTTPTestGit(t, t.TempDir(), "clone", barePath, workPath)
	runHTTPTestGit(t, workPath, "config", "user.name", "MultiAgentCom Test")
	runHTTPTestGit(t, workPath, "config", "user.email", "multiagentcom-test@example.invalid")
	return workPath
}

func initHTTPTempGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repoPath := t.TempDir()
	runHTTPTestGit(t, repoPath, "init")
	runHTTPTestGit(t, repoPath, "config", "user.name", "MultiAgentCom Test")
	runHTTPTestGit(t, repoPath, "config", "user.email", "multiagentcom-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runHTTPTestGit(t, repoPath, "add", "README.md")
	runHTTPTestGit(t, repoPath, "commit", "-m", "initial")
	runHTTPTestGit(t, repoPath, "branch", "-M", "main")
	return repoPath
}

func runHTTPTestGit(t *testing.T, repoPath string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", fullArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(fullArgs, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func decodeResponse(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, string(body))
	}
}

func waitForHTTPRunStatus(t *testing.T, server *httptest.Server, projectID, runID string, headers map[string]string, expected domain.RunStatus) service.RunStatusView {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var status service.RunStatusView
	for time.Now().Before(deadline) {
		statusBody := getJSONWithHeaders(t, server.Client(), server.URL+"/projects/"+projectID+"/runs/"+runID+"/status", headers)
		decodeResponse(t, statusBody, &status)
		if status.Run.Status == expected {
			return status
		}
		if status.Run.Status == domain.RunStatusFailed && expected != domain.RunStatusFailed {
			t.Fatalf("run failed before reaching %s: %s", expected, status.Run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("run %s did not reach %s before deadline", runID, expected)
	return service.RunStatusView{}
}

func waitForHTTPRun(t *testing.T, server *httptest.Server, projectID, runID string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var status service.RunStatusView
	for time.Now().Before(deadline) {
		statusBody := getJSON(t, server.Client(), server.URL+"/projects/"+projectID+"/runs/"+runID+"/status")
		decodeResponse(t, statusBody, &status)
		if status.Run.Status == domain.RunStatusSucceeded {
			return
		}
		if status.Run.Status == domain.RunStatusFailed {
			t.Fatalf("expected successful run, got failure: %s", status.Run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("run %s did not complete before deadline", runID)
}

func waitForHTTPRunTerminal(t *testing.T, server *httptest.Server, projectID, runID string) service.RunStatusView {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var status service.RunStatusView
	for time.Now().Before(deadline) {
		statusBody := getJSON(t, server.Client(), server.URL+"/projects/"+projectID+"/runs/"+runID+"/status")
		decodeResponse(t, statusBody, &status)
		if status.Run.Status == domain.RunStatusSucceeded || status.Run.Status == domain.RunStatusFailed {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("run %s did not complete before deadline", runID)
	return service.RunStatusView{}
}

func waitForHTTPTaskStatus(t *testing.T, server *httptest.Server, projectID, runID string, expected domain.TaskStatus) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var status service.RunStatusView
	for time.Now().Before(deadline) {
		statusBody := getJSON(t, server.Client(), server.URL+"/projects/"+projectID+"/runs/"+runID+"/status")
		decodeResponse(t, statusBody, &status)
		if status.Task.Status == expected {
			return
		}
		if status.Run.Status == domain.RunStatusFailed {
			t.Fatalf("run failed before reaching task status %s: %s", expected, status.Run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("run %s did not reach task status %s before deadline", runID, expected)
}

func scopedAuthRecord(token, actor string, roles []string, projectID string) map[string]any {
	sum := sha256.Sum256([]byte(token))
	return map[string]any{
		"tokenHash": hex.EncodeToString(sum[:]),
		"actor":     actor,
		"roles":     roles,
		"projectId": projectID,
	}
}

func scopedAuthTokensJSON(records ...map[string]any) string {
	payload, err := json.Marshal(records)
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func containsAgentStatus(items []service.StatusMatrixAgent, agent, status string) bool {
	for _, item := range items {
		if item.Agent == agent && item.Status == status {
			return true
		}
	}
	return false
}

func containsTaskID(items []service.StatusMatrixTask, taskID string) bool {
	for _, item := range items {
		if item.ID == taskID {
			return true
		}
	}
	return false
}

func sectionTitles(items []domain.ContextSection) string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return strings.Join(titles, "|")
}

func assertZipPayloadContains(t *testing.T, payload []byte, expected ...string) {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("open zip payload: %v", err)
	}

	entries := make(map[string]struct{}, len(reader.File))
	for _, file := range reader.File {
		entries[file.Name] = struct{}{}
	}
	for _, item := range expected {
		if _, ok := entries[item]; !ok {
			t.Fatalf("expected zip payload to contain %s", item)
		}
	}
}
