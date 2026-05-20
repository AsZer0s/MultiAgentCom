package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
)

func TestNewDefaultsArtifactRootWhenEmpty(t *testing.T) {
	svc := New(config.Config{SandboxRoot: t.TempDir()}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if svc.cfg.ArtifactRoot != filepath.Join(os.TempDir(), "multiagentcom", "artifacts") {
		t.Fatalf("expected temp artifact root, got %s", svc.cfg.ArtifactRoot)
	}

	customRoot := t.TempDir()
	custom := New(config.Config{ArtifactRoot: customRoot, SandboxRoot: t.TempDir()}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if custom.cfg.ArtifactRoot != customRoot {
		t.Fatalf("expected custom artifact root %s, got %s", customRoot, custom.cfg.ArtifactRoot)
	}
}

func TestFileStoreRestoresServiceState(t *testing.T) {
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-file-store-restore",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "file",
		DataRoot:      t.TempDir(),
		DefaultAgent:  "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := context.Background()
	svc := New(cfg, logger)

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	merge, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	if !merge.Passed {
		t.Fatalf("expected successful merge, got %+v", merge)
	}
	preview, err := svc.StartPreview(ctx, project.ID)
	if err != nil {
		t.Fatalf("start preview: %v", err)
	}
	artifact, err := svc.ExportDelivery(ctx, project.ID, ExportDeliveryInput{})
	if err != nil {
		t.Fatalf("export delivery: %v", err)
	}
	if _, err := svc.GetArtifact(ctx, project.ID, artifact.ID); err != nil {
		t.Fatalf("download artifact: %v", err)
	}

	restored := New(cfg, logger)
	projects, err := restored.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list restored projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != project.ID {
		t.Fatalf("expected restored project %s, got %+v", project.ID, projects)
	}
	requirements, err := restored.ListRequirements(ctx, project.ID)
	if err != nil {
		t.Fatalf("list restored requirements: %v", err)
	}
	if len(requirements) != 1 {
		t.Fatalf("expected 1 restored requirement, got %d", len(requirements))
	}
	tasks, err := restored.ListTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("list restored tasks: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("expected 4 restored tasks, got %d", len(tasks))
	}
	if _, err := restored.GetContract(ctx, project.ID, contract.ID); err != nil {
		t.Fatalf("get restored contract: %v", err)
	}
	if _, err := restored.GetPreview(ctx, project.ID, preview.Preview.ID); err != nil {
		t.Fatalf("get restored preview: %v", err)
	}
	if _, err := restored.GetArtifact(ctx, project.ID, artifact.ID); err != nil {
		t.Fatalf("get restored artifact: %v", err)
	}
	snapshots, err := restored.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list restored snapshots: %v", err)
	}
	if len(snapshots) != 1 || !snapshots[0].Stable {
		t.Fatalf("expected 1 stable restored snapshot, got %+v", snapshots)
	}
	if !strings.HasPrefix(snapshots[0].StateRef, "file://") || snapshots[0].Checksum == "" {
		t.Fatalf("expected file-backed snapshot ref and checksum, got %+v", snapshots[0])
	}
	manifestPath := filepath.Join(cfg.DataRoot, "snapshots", project.ID, snapshots[0].ID, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected snapshot manifest at %s: %v", manifestPath, err)
	}
	if _, err := restored.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshots[0].ID}); err != nil {
		t.Fatalf("rollback using restored snapshot state: %v", err)
	}
	auditLogs, err := restored.ListAuditLogs(ctx, project.ID)
	if err != nil {
		t.Fatalf("list restored audit logs: %v", err)
	}
	if len(auditLogs) == 0 {
		t.Fatal("expected restored audit logs")
	}
}

func TestSprintOneFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "test-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Todo Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo 列表的增删改查",
		Content:     "实现 Todo 列表的增删改查，并提供最小可演示交付包。",
		Constraints: []string{"后端使用 Go"},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}

	plan, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: plan.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := svc.GetRunStatus(ctx, project.ID, runEnvelope.Run.ID)
		if err != nil {
			t.Fatalf("get run status: %v", err)
		}
		if status.Run.Status == domain.RunStatusSucceeded {
			if status.Task.Status != domain.TaskStatusDone {
				t.Fatalf("expected task DONE, got %s", status.Task.Status)
			}
			if len(status.Artifacts) != 1 {
				t.Fatalf("expected 1 artifact, got %d", len(status.Artifacts))
			}
			if _, err := os.Stat(status.Artifacts[0].URI); err != nil {
				t.Fatalf("artifact file missing: %v", err)
			}
			assertZipContains(t, status.Artifacts[0].URI,
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
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("run did not complete before deadline")
}

func TestDeliveryBundleIncludesManifestAndReleaseGateV1(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-delivery-gate",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "test-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Delivery Gate Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo API", Content: "生成可校验交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	plan, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: plan.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s: %s", status.Run.Status, status.Run.Error)
	}
	artifact, err := svc.ExportDelivery(ctx, project.ID, ExportDeliveryInput{RunID: runEnvelope.Run.ID})
	if err != nil {
		t.Fatalf("export delivery: %v", err)
	}

	var manifest deliveryBundleManifest
	readZipJSON(t, artifact.URI, "metadata/manifest.json", &manifest)
	if manifest.SchemaVersion != deliveryBundleSchemaVersion || manifest.Kind != "delivery_bundle" {
		t.Fatalf("unexpected manifest identity: %+v", manifest)
	}
	if manifest.ProjectID != project.ID || manifest.TaskID != plan.Task.ID || manifest.RunID != runEnvelope.Run.ID || manifest.PlanID != plan.Plan.ID {
		t.Fatalf("manifest IDs do not match project/run: %+v", manifest)
	}
	if manifest.PlanVersion != plan.Plan.Version {
		t.Fatalf("manifest plan version = %d, want %d", manifest.PlanVersion, plan.Plan.Version)
	}
	if manifest.Entrypoints.Frontend == "" || manifest.Entrypoints.BackendHealth == "" || manifest.Entrypoints.ComposeFile != "docker-compose.yml" {
		t.Fatalf("manifest entrypoints incomplete: %+v", manifest.Entrypoints)
	}
	if manifest.ReleaseGate.Path != "metadata/release-gate.json" || manifest.ReleaseGate.Status != "PASS" {
		t.Fatalf("manifest release gate mismatch: %+v", manifest.ReleaseGate)
	}

	filesByPath := make(map[string]deliveryFileDescriptor, len(manifest.Files))
	for _, item := range manifest.Files {
		filesByPath[item.Path] = item
		if item.Path == "" || item.Role == "" || !item.Required || item.SHA256 == "" || item.SizeBytes <= 0 {
			t.Fatalf("invalid manifest file descriptor: %+v", item)
		}
	}
	for _, required := range requiredDeliveryBundleFiles {
		if _, ok := filesByPath[required.Path]; !ok {
			t.Fatalf("manifest missing required file descriptor %s", required.Path)
		}
	}
	if _, ok := filesByPath["metadata/manifest.json"]; ok {
		t.Fatal("manifest should not include self-referential metadata/manifest.json descriptor")
	}

	var gate deliveryReleaseGate
	readZipJSON(t, artifact.URI, "metadata/release-gate.json", &gate)
	if gate.SchemaVersion != deliveryGateSchemaVersion || gate.Status != "PASS" {
		t.Fatalf("unexpected release gate: %+v", gate)
	}
	if len(gate.Checks) < 3 {
		t.Fatalf("expected release gate checks, got %+v", gate.Checks)
	}
}

func TestDeliveryBundleGateRejectsMissingRequiredFile(t *testing.T) {
	bundleDir := t.TempDir()
	if err := writeFile(filepath.Join(bundleDir, "README.md"), []byte("readme")); err != nil {
		t.Fatalf("write temp readme: %v", err)
	}
	err := validateRequiredDeliveryFiles(bundleDir, []deliveryRequiredFile{{Path: "README.md", Role: "documentation"}, {Path: "missing.txt", Role: "missing"}})
	if err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("expected missing required file error, got %v", err)
	}
}

func TestRunUsesHTTPRuntimeProviderWhenConfigured(t *testing.T) {
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST runtime call, got %s", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode runtime payload: %v", err)
		}
		if payload["projectId"] == "" || payload["taskId"] == "" || payload["runId"] == "" {
			t.Fatalf("runtime payload missing IDs: %#v", payload)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":            "runtime-http-v1",
			"output":           "runtime accepted task and prepared execution plan",
			"promptTokens":     31,
			"completionTokens": 19,
			"totalTokens":      50,
		})
	}))
	defer runtimeServer.Close()

	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-runtime-http",
		ArtifactRoot:               t.TempDir(),
		DefaultAgent:               "go-backend-agent",
		RuntimeProvider:            "http",
		RuntimeEndpoint:            runtimeServer.URL,
		RuntimeTimeout:             2 * time.Second,
		TokenPromptPricePerMillion: 10,
		TokenOutputPricePerMillion: 20,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Runtime HTTP Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo API",
		Content:     "实现 Todo API 并返回可交付产物",
		Constraints: []string{"后端使用 Go"},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s (%s)", status.Run.Status, status.Run.Error)
	}
	if status.Run.Model != "runtime-http-v1" {
		t.Fatalf("expected runtime model runtime-http-v1, got %s", status.Run.Model)
	}
	if status.Run.PromptTokens != 31 || status.Run.CompletionTokens != 19 || status.Run.TotalTokens != 50 {
		t.Fatalf(
			"expected runtime tokens 31/19/50, got %d/%d/%d",
			status.Run.PromptTokens,
			status.Run.CompletionTokens,
			status.Run.TotalTokens,
		)
	}
	wantCost := (31*cfg.TokenPromptPricePerMillion + 19*cfg.TokenOutputPricePerMillion) / 1_000_000
	if status.Run.EstimatedCostUSD != wantCost {
		t.Fatalf("EstimatedCostUSD = %.9f, want %.9f", status.Run.EstimatedCostUSD, wantCost)
	}
	if !strings.Contains(status.Run.ResultSummary, "runtime output") {
		t.Fatalf("expected runtime summary in result summary, got %s", status.Run.ResultSummary)
	}
}

func TestRunUsesHTTPRuntimeProviderBearerToken(t *testing.T) {
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer runtime-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion": "runtime.http.v1",
				"error": map[string]any{
					"code":    "UNAUTHORIZED",
					"message": "missing bearer token",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": "runtime.http.v1",
			"model":           "runtime-http-v1",
			"output":          "authorized runtime execution",
			"usage": map[string]any{
				"promptTokens":     5,
				"completionTokens": 6,
				"totalTokens":      11,
			},
		})
	}))
	defer runtimeServer.Close()

	cfg := config.Config{
		Address:                ":0",
		ServiceName:            "test-runtime-bearer",
		ArtifactRoot:           t.TempDir(),
		DefaultAgent:           "go-backend-agent",
		RuntimeProvider:        "http",
		RuntimeEndpoint:        runtimeServer.URL,
		RuntimeTimeout:         2 * time.Second,
		RuntimeHTTPBearerToken: "runtime-secret",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Runtime Bearer Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo API", Content: "实现 Todo API"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s (%s)", status.Run.Status, status.Run.Error)
	}
	if status.Run.TotalTokens != 11 {
		t.Fatalf("expected provider tokens, got %+v", status.Run)
	}
}

func TestRunRetriesTransientHTTPRuntimeProviderFailure(t *testing.T) {
	var attempts int
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion": "runtime.http.v1",
				"error": map[string]any{
					"code":      "UPSTREAM_UNAVAILABLE",
					"message":   "temporary outage",
					"retryable": true,
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": "runtime.http.v1",
			"model":           "runtime-http-v1",
			"output":          "retry recovered",
			"usage": map[string]any{
				"promptTokens":     8,
				"completionTokens": 9,
				"totalTokens":      17,
			},
		})
	}))
	defer runtimeServer.Close()

	cfg := config.Config{
		Address:                   ":0",
		ServiceName:               "test-runtime-retry",
		ArtifactRoot:              t.TempDir(),
		DefaultAgent:              "go-backend-agent",
		RuntimeProvider:           "http",
		RuntimeEndpoint:           runtimeServer.URL,
		RuntimeTimeout:            2 * time.Second,
		RuntimeHTTPMaxAttempts:    2,
		RuntimeHTTPRetryBaseDelay: time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Runtime Retry Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo API", Content: "实现 Todo API"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s (%s)", status.Run.Status, status.Run.Error)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if status.Run.TotalTokens != 17 || status.Run.EstimatedCostUSD <= 0 {
		t.Fatalf("expected retried provider usage and cost, got %+v", status.Run)
	}
}

func TestRunUsesTotalOnlyRuntimeUsageForCost(t *testing.T) {
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": "runtime.http.v1",
			"model":           "runtime-http-v1",
			"output":          "runtime returned total-only usage",
			"usage": map[string]any{
				"totalTokens": 50,
			},
		})
	}))
	defer runtimeServer.Close()

	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-runtime-total-only",
		ArtifactRoot:               t.TempDir(),
		DefaultAgent:               "go-backend-agent",
		RuntimeProvider:            "http",
		RuntimeEndpoint:            runtimeServer.URL,
		RuntimeTimeout:             2 * time.Second,
		TokenPromptPricePerMillion: 10,
		TokenOutputPricePerMillion: 20,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Runtime Total Usage Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo API",
		Content: "实现 Todo API 并返回可交付产物",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s (%s)", status.Run.Status, status.Run.Error)
	}
	if status.Run.PromptTokens != 50 || status.Run.CompletionTokens != 0 || status.Run.TotalTokens != 50 {
		t.Fatalf("expected normalized total-only tokens 50/0/50, got %d/%d/%d", status.Run.PromptTokens, status.Run.CompletionTokens, status.Run.TotalTokens)
	}
	if status.Run.EstimatedCostUSD <= 0 {
		t.Fatalf("expected non-zero cost from total-only usage, got %.9f", status.Run.EstimatedCostUSD)
	}
}

func TestStartRunFailsWhenPrivateSandboxWorkspaceCannotBeCreated(t *testing.T) {
	sandboxRootFile := filepath.Join(t.TempDir(), "sandbox-root-file")
	if err := os.WriteFile(sandboxRootFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write sandbox root file: %v", err)
	}

	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-private-sandbox-create-error",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  sandboxRootFile,
		DefaultAgent: "test-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Sandbox Failure Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo API", Content: "实现 Todo API"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	_, err = svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "SANDBOX_CREATE_FAILED" {
		t.Fatalf("expected SANDBOX_CREATE_FAILED, got %v", err)
	}
}

func TestContractHubVersioning(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-contracts",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "test-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Contract Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	requirement, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo 列表的增删改查",
		Content:     "实现 Todo 列表的增删改查，并先生成 API 契约。",
		Constraints: []string{"后端使用 Go", "前端稍后补齐"},
	})
	if err != nil {
		t.Fatalf("add requirement: %v", err)
	}

	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	contractV1, err := svc.GenerateContract(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate contract v1: %v", err)
	}
	if contractV1.Version != 1 {
		t.Fatalf("expected version 1, got %d", contractV1.Version)
	}
	if contractV1.PlanID != planResult.Plan.ID {
		t.Fatalf("expected plan id %s, got %s", planResult.Plan.ID, contractV1.PlanID)
	}
	if contractV1.RequirementID != requirement.ID {
		t.Fatalf("expected requirement id %s, got %s", requirement.ID, contractV1.RequirementID)
	}
	if len(contractV1.Endpoints) < 4 {
		t.Fatalf("expected CRUD endpoints, got %d", len(contractV1.Endpoints))
	}
	if len(contractV1.Schemas) < 2 {
		t.Fatalf("expected schemas to be generated, got %d", len(contractV1.Schemas))
	}

	contractV2, err := svc.GenerateContract(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate contract v2: %v", err)
	}
	if contractV2.Version != 2 {
		t.Fatalf("expected version 2, got %d", contractV2.Version)
	}

	contracts, err := svc.ListContracts(ctx, project.ID)
	if err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	if len(contracts) != 2 {
		t.Fatalf("expected 2 contracts, got %d", len(contracts))
	}

	stored, err := svc.GetContract(ctx, project.ID, contractV2.ID)
	if err != nil {
		t.Fatalf("get contract: %v", err)
	}
	if stored.Name == "" {
		t.Fatal("expected stored contract name")
	}
	if stored.Endpoints[0].Path != "/api/todos" {
		t.Fatalf("expected todo contract path, got %s", stored.Endpoints[0].Path)
	}
}

func TestValidateContractCreatesRemediationTaskOnConflict(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-validate-contract",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "test-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Conflict Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并校验契约一致性。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}

	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	contract, err := svc.GenerateContract(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate contract: %v", err)
	}

	result, err := svc.ValidateContract(ctx, project.ID, ValidateContractInput{
		ContractID: contract.ID,
		Endpoints: []domain.ContractEndpoint{
			{Name: "ListTodo", Method: "GET", Path: "/api/todos"},
			{Name: "CreateTodo", Method: "POST", Path: "/api/todos"},
		},
		Schemas: []domain.ContractSchema{
			{
				Name: "Todo",
				Fields: []domain.ContractField{
					{Name: "id", Type: "string", Required: true},
					{Name: "title", Type: "number", Required: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("validate contract: %v", err)
	}
	if result.Passed {
		t.Fatal("expected validation to fail")
	}
	if len(result.Conflicts) == 0 {
		t.Fatal("expected conflicts to be returned")
	}
	if result.RemediationTask == nil {
		t.Fatal("expected remediation task to be created")
	}
	if result.RemediationTask.Type != "CONTRACT_REWORK" {
		t.Fatalf("expected remediation task type CONTRACT_REWORK, got %s", result.RemediationTask.Type)
	}
	if result.RemediationTask.InputRef != "contract://"+contract.ID {
		t.Fatalf("expected remediation task input ref to point to contract, got %s", result.RemediationTask.InputRef)
	}
}

func TestValidateContractPassesForMatchingCandidate(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-validate-contract-pass",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "test-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Valid Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并校验契约一致性。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}

	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	contract, err := svc.GenerateContract(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate contract: %v", err)
	}

	result, err := svc.ValidateContract(ctx, project.ID, ValidateContractInput{
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	if err != nil {
		t.Fatalf("validate contract: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected validation to pass, got conflicts: %+v", result.Conflicts)
	}
	if result.RemediationTask != nil {
		t.Fatal("did not expect remediation task on successful validation")
	}
}

func TestDispatchTasksAndParallelRun(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-parallel-run",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Parallel Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo 全栈功能",
		Content:     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		Constraints: []string{"后端使用 Go", "前端使用 Vue"},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if _, err := svc.GenerateContract(ctx, project.ID); err != nil {
		t.Fatalf("generate contract: %v", err)
	}

	dispatchResult, err := svc.DispatchTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("dispatch tasks: %v", err)
	}
	if len(dispatchResult.Tasks) != 3 {
		t.Fatalf("expected 3 dispatched tasks, got %d", len(dispatchResult.Tasks))
	}
	if len(dispatchResult.Tasks[2].DependsOn) != 2 {
		t.Fatalf("expected integration task to depend on 2 tasks, got %d", len(dispatchResult.Tasks[2].DependsOn))
	}

	parallelResult, err := svc.StartParallelRun(ctx, project.ID, ParallelRunInput{
		TaskIDs: []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	if err != nil {
		t.Fatalf("start parallel run: %v", err)
	}
	if len(parallelResult.Started) != 2 {
		t.Fatalf("expected 2 started runs, got %d", len(parallelResult.Started))
	}

	for _, started := range parallelResult.Started {
		waitForSucceededRun(t, svc, project.ID, started.Run.ID)
	}

	integrationRun, err := svc.StartParallelRun(ctx, project.ID, ParallelRunInput{
		TaskIDs: []string{dispatchResult.Tasks[2].ID},
	})
	if err != nil {
		t.Fatalf("start integration run: %v", err)
	}
	if len(integrationRun.Started) != 1 {
		t.Fatalf("expected 1 integration run, got %d", len(integrationRun.Started))
	}
	waitForSucceededRun(t, svc, project.ID, integrationRun.Started[0].Run.ID)
}

func TestRetryTaskCreatesIndependentRetry(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-retry-task",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Retry Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 全栈功能",
		Content: "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if _, err := svc.GenerateContract(ctx, project.ID); err != nil {
		t.Fatalf("generate contract: %v", err)
	}
	dispatchResult, err := svc.DispatchTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("dispatch tasks: %v", err)
	}

	targetTaskID := dispatchResult.Tasks[1].ID
	svc.mu.Lock()
	task := svc.tasks[targetTaskID]
	now := time.Now().UTC()
	if err := task.TransitionTo(domain.TaskStatusInProgress, "test start", now); err != nil {
		svc.mu.Unlock()
		t.Fatalf("transition to in progress: %v", err)
	}
	if err := task.TransitionTo(domain.TaskStatusFailed, "test fail", now.Add(time.Millisecond)); err != nil {
		svc.mu.Unlock()
		t.Fatalf("transition to failed: %v", err)
	}
	svc.mu.Unlock()

	retryTask, err := svc.RetryTask(ctx, project.ID, targetTaskID)
	if err != nil {
		t.Fatalf("retry task: %v", err)
	}
	if retryTask.Status != domain.TaskStatusCreated {
		t.Fatalf("expected retry task status CREATED, got %s", retryTask.Status)
	}
	if retryTask.AssigneeAgent != dispatchResult.Tasks[1].AssigneeAgent {
		t.Fatalf("expected retry task to preserve assignee %s, got %s", dispatchResult.Tasks[1].AssigneeAgent, retryTask.AssigneeAgent)
	}
	if retryTask.ID == targetTaskID {
		t.Fatal("expected retry task to have a new id")
	}
}

func TestGenerateTaskContextSlicesByRole(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-context-engine",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Context Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo 全栈功能",
		Content:     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		Constraints: []string{"后端使用 Go", "前端使用 Vue"},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if _, err := svc.GenerateContract(ctx, project.ID); err != nil {
		t.Fatalf("generate contract: %v", err)
	}
	dispatchResult, err := svc.DispatchTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("dispatch tasks: %v", err)
	}

	backendContext, err := svc.GenerateTaskContext(ctx, project.ID, dispatchResult.Tasks[0].ID)
	if err != nil {
		t.Fatalf("generate backend context: %v", err)
	}
	frontendContext, err := svc.GenerateTaskContext(ctx, project.ID, dispatchResult.Tasks[1].ID)
	if err != nil {
		t.Fatalf("generate frontend context: %v", err)
	}

	if backendContext.Context.Role != "backend" {
		t.Fatalf("expected backend role, got %s", backendContext.Context.Role)
	}
	if frontendContext.Context.Role != "frontend" {
		t.Fatalf("expected frontend role, got %s", frontendContext.Context.Role)
	}
	if len(backendContext.Context.Sources) != 3 {
		t.Fatalf("expected 3 context sources, got %d", len(backendContext.Context.Sources))
	}
	if backendContext.Context.Sections[2].Title != "API Contract" {
		t.Fatalf("expected backend API Contract section, got %s", backendContext.Context.Sections[2].Title)
	}
	if frontendContext.Context.Sections[1].Title != "UX Scope" {
		t.Fatalf("expected frontend UX Scope section, got %s", frontendContext.Context.Sections[1].Title)
	}
	if strings.Join(backendContext.Context.Sections[2].Items, " ") == strings.Join(frontendContext.Context.Sections[2].Items, " ") {
		t.Fatal("expected backend and frontend context sections to be different")
	}
}

func TestGenerateTaskContextVersioning(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-context-version",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Context Version Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 全栈功能",
		Content: "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if _, err := svc.GenerateContract(ctx, project.ID); err != nil {
		t.Fatalf("generate contract: %v", err)
	}
	dispatchResult, err := svc.DispatchTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("dispatch tasks: %v", err)
	}

	first, err := svc.GenerateTaskContext(ctx, project.ID, dispatchResult.Tasks[0].ID)
	if err != nil {
		t.Fatalf("generate first context: %v", err)
	}
	second, err := svc.GenerateTaskContext(ctx, project.ID, dispatchResult.Tasks[0].ID)
	if err != nil {
		t.Fatalf("generate second context: %v", err)
	}
	if first.Context.Version != 1 || second.Context.Version != 2 {
		t.Fatalf("expected versions 1 and 2, got %d and %d", first.Context.Version, second.Context.Version)
	}

	latest, err := svc.GetLatestTaskContext(ctx, project.ID, dispatchResult.Tasks[0].ID)
	if err != nil {
		t.Fatalf("get latest context: %v", err)
	}
	if latest.Context.Version != 2 {
		t.Fatalf("expected latest context version 2, got %d", latest.Context.Version)
	}
}

func TestStatusMatrixAggregatesTasksAndAgents(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-status-matrix",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Status Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo 全栈功能",
		Content:     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		Constraints: []string{"后端使用 Go", "前端使用 Vue"},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if _, err := svc.GenerateContract(ctx, project.ID); err != nil {
		t.Fatalf("generate contract: %v", err)
	}
	dispatchResult, err := svc.DispatchTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("dispatch tasks: %v", err)
	}
	parallelResult, err := svc.StartParallelRun(ctx, project.ID, ParallelRunInput{
		TaskIDs: []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	if err != nil {
		t.Fatalf("start parallel run: %v", err)
	}
	for _, started := range parallelResult.Started {
		waitForSucceededRun(t, svc, project.ID, started.Run.ID)
	}

	matrixView, err := svc.GetStatusMatrix(ctx, project.ID)
	if err != nil {
		t.Fatalf("get status matrix: %v", err)
	}
	if matrixView.SelectedProjectID != project.ID {
		t.Fatalf("expected selected project id %s, got %s", project.ID, matrixView.SelectedProjectID)
	}
	if len(matrixView.Matrices) != 1 {
		t.Fatalf("expected 1 matrix, got %d", len(matrixView.Matrices))
	}

	matrix := matrixView.Matrices[0]
	if matrix.TotalTasks < 4 {
		t.Fatalf("expected at least 4 tasks, got %d", matrix.TotalTasks)
	}
	if matrix.CompletedTasks < 2 {
		t.Fatalf("expected at least 2 completed tasks, got %d", matrix.CompletedTasks)
	}
	if matrix.ReadyTasks < 1 {
		t.Fatalf("expected at least 1 ready task, got %d", matrix.ReadyTasks)
	}

	foundBackend := false
	foundIntegration := false
	for _, agent := range matrix.AgentMatrix {
		if agent.Agent == "go-backend-agent" && agent.Status == "COMPLETED" {
			foundBackend = true
		}
		if agent.Agent == "integration-agent" && agent.Status == "READY" {
			foundIntegration = true
		}
	}
	if !foundBackend {
		t.Fatal("expected go-backend-agent to appear as COMPLETED")
	}
	if !foundIntegration {
		t.Fatal("expected integration-agent to appear as READY")
	}
}

func TestPrivateSandboxIsolation(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-private-sandbox",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Sandbox Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo 全栈功能",
		Content:     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		Constraints: []string{"后端使用 Go", "前端使用 Vue"},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if _, err := svc.GenerateContract(ctx, project.ID); err != nil {
		t.Fatalf("generate contract: %v", err)
	}
	dispatchResult, err := svc.DispatchTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("dispatch tasks: %v", err)
	}

	backendTaskID := dispatchResult.Tasks[0].ID
	frontendTaskID := dispatchResult.Tasks[1].ID
	if err := svc.MarkTaskSandboxFailure(ctx, project.ID, backendTaskID, "simulated private sandbox crash"); err != nil {
		t.Fatalf("inject sandbox failure: %v", err)
	}

	parallelResult, err := svc.StartParallelRun(ctx, project.ID, ParallelRunInput{
		TaskIDs: []string{backendTaskID, frontendTaskID},
	})
	if err != nil {
		t.Fatalf("start parallel run: %v", err)
	}
	if len(parallelResult.Started) != 2 {
		t.Fatalf("expected 2 started runs, got %d", len(parallelResult.Started))
	}

	runByTask := make(map[string]domain.AgentRun, 2)
	for _, started := range parallelResult.Started {
		runByTask[started.Task.ID] = started.Run
	}

	backendStatus := waitForRunTerminal(t, svc, project.ID, runByTask[backendTaskID].ID)
	frontendStatus := waitForRunTerminal(t, svc, project.ID, runByTask[frontendTaskID].ID)

	if backendStatus.Run.Status != domain.RunStatusFailed {
		t.Fatalf("expected backend run to fail, got %s", backendStatus.Run.Status)
	}
	if frontendStatus.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected frontend run to succeed, got %s", frontendStatus.Run.Status)
	}

	backendSandbox, err := svc.GetRunSandbox(ctx, project.ID, runByTask[backendTaskID].ID)
	if err != nil {
		t.Fatalf("get backend sandbox: %v", err)
	}
	frontendSandbox, err := svc.GetRunSandbox(ctx, project.ID, runByTask[frontendTaskID].ID)
	if err != nil {
		t.Fatalf("get frontend sandbox: %v", err)
	}

	if backendSandbox.Sandbox.ID == frontendSandbox.Sandbox.ID {
		t.Fatal("expected isolated sandbox ids for parallel runs")
	}
	if backendSandbox.Sandbox.RootPath == frontendSandbox.Sandbox.RootPath {
		t.Fatal("expected isolated sandbox root paths for parallel runs")
	}
	if backendSandbox.Sandbox.Status != domain.SandboxStatusFailed {
		t.Fatalf("expected backend sandbox FAILED, got %s", backendSandbox.Sandbox.Status)
	}
	if frontendSandbox.Sandbox.Status != domain.SandboxStatusReleased {
		t.Fatalf("expected frontend sandbox RELEASED, got %s", frontendSandbox.Sandbox.Status)
	}
	if _, err := os.Stat(filepath.Join(backendSandbox.Sandbox.RootPath, "sandbox-error.log")); err != nil {
		t.Fatalf("expected sandbox error log to exist: %v", err)
	}
	if len(frontendStatus.Artifacts) != 1 {
		t.Fatalf("expected frontend run to produce one artifact, got %d", len(frontendStatus.Artifacts))
	}

	sandboxes, err := svc.ListSandboxes(ctx, project.ID)
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(sandboxes))
	}
}

func TestMergeSharedSandboxSuccess(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-shared-sandbox-success",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)

	result, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected merge to pass, got %+v", result)
	}
	if result.Sandbox.Scope != "SHARED" {
		t.Fatalf("expected shared sandbox scope, got %s", result.Sandbox.Scope)
	}
	if result.Sandbox.Status != domain.SandboxStatusReleased {
		t.Fatalf("expected shared sandbox RELEASED, got %s", result.Sandbox.Status)
	}
	if len(result.ArtifactIDs) != 2 {
		t.Fatalf("expected 2 merged artifacts, got %d", len(result.ArtifactIDs))
	}
	if result.RemediationTask != nil {
		t.Fatal("did not expect remediation task for successful merge")
	}
	if _, err := os.Stat(filepath.Join(result.Sandbox.RootPath, "workspace", "manifest.json")); err != nil {
		t.Fatalf("expected shared sandbox manifest: %v", err)
	}
	workspaceManifestPath := filepath.Join(result.Sandbox.WorkspacePath, ".multiagent", "workspace-manifest.json")
	workspaceManifestPayload, err := os.ReadFile(workspaceManifestPath)
	if err != nil {
		t.Fatalf("expected shared workspace manifest: %v", err)
	}
	if !strings.Contains(string(workspaceManifestPayload), `"schemaVersion":"workspace.manifest.v1"`) && !strings.Contains(string(workspaceManifestPayload), `"schemaVersion": "workspace.manifest.v1"`) {
		t.Fatalf("workspace manifest missing schema version: %s", string(workspaceManifestPayload))
	}
	for _, artifactID := range result.ArtifactIDs {
		if _, err := os.Stat(filepath.Join(result.Sandbox.WorkspacePath, "artifacts", artifactID, "metadata", "manifest.json")); err != nil {
			t.Fatalf("expected materialized artifact %s manifest: %v", artifactID, err)
		}
	}

	sandboxes, err := svc.ListSandboxes(ctx, project.ID)
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes) != 3 {
		t.Fatalf("expected 3 sandboxes including shared merge gate, got %d", len(sandboxes))
	}

	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 stable snapshot after successful merge, got %d", len(snapshots))
	}
	if !snapshots[0].Stable || snapshots[0].Branch != "main" {
		t.Fatalf("expected stable main snapshot, got %+v", snapshots[0])
	}
}

func TestGitWorkspaceProviderCreatesPrivateWorktreeAndCommitsRun(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{
		Address:                   ":0",
		ServiceName:               "test-git-workspace-private",
		ArtifactRoot:              t.TempDir(),
		SandboxRoot:               t.TempDir(),
		DefaultAgent:              "manager-agent",
		WorkspaceProvider:         "git",
		WorkspaceGitRepoPath:      repoPath,
		WorkspaceGitBaseRef:       "main",
		RuntimeProvider:           "local",
		RuntimeHTTPMaxAttempts:    1,
		RuntimeHTTPRetryBaseDelay: time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Git Workspace Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)

	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	sandbox := sandboxView.Sandbox
	if sandbox.WorkspaceProvider != "git" {
		t.Fatalf("WorkspaceProvider = %q, want git", sandbox.WorkspaceProvider)
	}
	if sandbox.WorkspaceBranch == "" || sandbox.WorkspaceBaseRef != "main" || sandbox.WorkspaceHeadRef == "" {
		t.Fatalf("expected git workspace refs, got %+v", sandbox)
	}
	if out := runTestGit(t, sandbox.WorkspacePath, "rev-parse", "--is-inside-work-tree"); strings.TrimSpace(out) != "true" {
		t.Fatalf("expected git worktree, got %q", out)
	}
	runTestGit(t, sandbox.WorkspacePath, "cat-file", "-e", sandbox.WorkspaceHeadRef+":tasks/"+planResult.Task.ID+"/bundle/metadata/manifest.json")
	manifestPath := filepath.Join(sandbox.WorkspacePath, ".multiagent", "workspace-manifest.json")
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read workspace manifest: %v", err)
	}
	if !strings.Contains(string(payload), sandbox.WorkspaceHeadRef) {
		t.Fatalf("expected workspace manifest to contain head ref %s: %s", sandbox.WorkspaceHeadRef, string(payload))
	}
}

func TestGitMergeSharedSandboxUsesGitMerge(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{
		Address:                           ":0",
		ServiceName:                       "test-git-workspace-merge",
		ArtifactRoot:                      t.TempDir(),
		SandboxRoot:                       t.TempDir(),
		DefaultAgent:                      "manager-agent",
		WorkspaceProvider:                 "git",
		WorkspaceGitRepoPath:              repoPath,
		WorkspaceGitBaseRef:               "main",
		WorkspaceGitCleanupEnabled:        true,
		WorkspaceGitCleanupDeleteBranches: false,
		RuntimeProvider:                   "local",
		RuntimeHTTPMaxAttempts:            1,
		RuntimeHTTPRetryBaseDelay:         time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	privateHeads := make([]string, 0, 2)
	sandboxes, err := svc.ListSandboxes(ctx, project.ID)
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	for _, sandboxView := range sandboxes {
		if sandboxView.Sandbox.Scope == "PRIVATE" {
			privateHeads = append(privateHeads, sandboxView.Sandbox.WorkspaceHeadRef)
		}
	}
	if len(privateHeads) != 2 {
		t.Fatalf("expected 2 private git heads, got %v", privateHeads)
	}

	result, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	if !result.Passed || result.Sandbox.WorkspaceProvider != "git" || result.Sandbox.WorkspaceHeadRef == "" {
		t.Fatalf("expected released git shared sandbox, got %+v", result)
	}
	for _, head := range privateHeads {
		runTestGit(t, result.Sandbox.WorkspacePath, "merge-base", "--is-ancestor", head, result.Sandbox.WorkspaceHeadRef)
	}
	if result.Cleanup == nil || result.Cleanup.RemovedWorktrees != 2 || result.Cleanup.DeletedBranches != 0 {
		t.Fatalf("expected automatic private worktree cleanup without branch deletion, got %+v", result.Cleanup)
	}
	if dirty := strings.TrimSpace(runTestGit(t, result.Sandbox.WorkspacePath, "status", "--porcelain")); dirty != "M .multiagent/workspace-manifest.json" {
		t.Fatalf("expected only workspace manifest metadata to remain uncommitted, got %q", dirty)
	}
	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].WorkspaceChecksum != result.Sandbox.WorkspaceHeadRef || !strings.HasPrefix(snapshots[0].WorkspaceStateRef, "repo://local/") {
		t.Fatalf("expected workspace snapshot ref for shared head, got %+v", snapshots)
	}
}

func TestGitWorkspaceProviderClonesRemoteWhenRepoMissing(t *testing.T) {
	remotePath := initBareGitRemote(t)
	repoPath := filepath.Join(t.TempDir(), "workspace-repo")
	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-git-workspace-remote-clone",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		DefaultAgent:               "manager-agent",
		WorkspaceProvider:          "git",
		WorkspaceGitRepoPath:       repoPath,
		WorkspaceGitRemoteURL:      remotePath,
		WorkspaceGitBaseRef:        "origin/main",
		RuntimeProvider:            "local",
		RuntimeHTTPMaxAttempts:     1,
		RuntimeHTTPRetryBaseDelay:  time.Millisecond,
		WorkspaceGitFetchBeforeUse: true,
		WorkspaceGitCleanupEnabled: false,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Remote Clone Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)

	if out := runTestGit(t, repoPath, "rev-parse", "--is-inside-work-tree"); strings.TrimSpace(out) != "true" {
		t.Fatalf("expected cloned repo, got %q", out)
	}
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	if sandboxView.Sandbox.WorkspaceHeadRef == "" || sandboxView.Sandbox.WorkspaceBaseRef != "origin/main" {
		t.Fatalf("expected remote git refs, got %+v", sandboxView.Sandbox)
	}
}

func TestGitWorkspaceProviderFetchesBeforeCreatingWorktree(t *testing.T) {
	remotePath := initBareGitRemote(t)
	repoPath := filepath.Join(t.TempDir(), "workspace-repo")
	runTestGit(t, t.TempDir(), "clone", remotePath, repoPath)
	remoteWork := cloneBareRemote(t, remotePath)
	if err := os.WriteFile(filepath.Join(remoteWork, "REMOTE.md"), []byte("new base\n"), 0o644); err != nil {
		t.Fatalf("write remote file: %v", err)
	}
	runTestGit(t, remoteWork, "add", "REMOTE.md")
	runTestGit(t, remoteWork, "commit", "-m", "remote update")
	runTestGit(t, remoteWork, "push", "origin", "main")

	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-git-workspace-remote-fetch",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		DefaultAgent:               "manager-agent",
		WorkspaceProvider:          "git",
		WorkspaceGitRepoPath:       repoPath,
		WorkspaceGitRemoteURL:      remotePath,
		WorkspaceGitBaseRef:        "origin/main",
		WorkspaceGitFetchBeforeUse: true,
		WorkspaceGitCleanupEnabled: false,
		RuntimeProvider:            "local",
		RuntimeHTTPMaxAttempts:     1,
		RuntimeHTTPRetryBaseDelay:  time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Remote Fetch Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sandboxView.Sandbox.WorkspacePath, "REMOTE.md")); err != nil {
		t.Fatalf("expected fetched base file in worktree: %v", err)
	}
}

func TestGitWorkspaceProviderPushesPrivateBranch(t *testing.T) {
	remotePath := initBareGitRemote(t)
	repoPath := filepath.Join(t.TempDir(), "workspace-repo")
	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-git-workspace-remote-push-private",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		DefaultAgent:               "manager-agent",
		WorkspaceProvider:          "git",
		WorkspaceGitRepoPath:       repoPath,
		WorkspaceGitRemoteURL:      remotePath,
		WorkspaceGitBaseRef:        "origin/main",
		WorkspaceGitPushEnabled:    true,
		WorkspaceGitCleanupEnabled: false,
		RuntimeProvider:            "local",
		RuntimeHTTPMaxAttempts:     1,
		RuntimeHTTPRetryBaseDelay:  time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Remote Push Private Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	remoteHead := strings.TrimSpace(runTestGit(t, remotePath, "rev-parse", "refs/heads/"+sandboxView.Sandbox.WorkspaceBranch))
	if remoteHead != sandboxView.Sandbox.WorkspaceHeadRef {
		t.Fatalf("remote head = %s, want %s", remoteHead, sandboxView.Sandbox.WorkspaceHeadRef)
	}
}

func TestGitWorkspaceProviderPushesSharedBranch(t *testing.T) {
	remotePath := initBareGitRemote(t)
	repoPath := filepath.Join(t.TempDir(), "workspace-repo")
	cfg := config.Config{
		Address:                           ":0",
		ServiceName:                       "test-git-workspace-remote-push-shared",
		ArtifactRoot:                      t.TempDir(),
		SandboxRoot:                       t.TempDir(),
		DefaultAgent:                      "manager-agent",
		WorkspaceProvider:                 "git",
		WorkspaceGitRepoPath:              repoPath,
		WorkspaceGitRemoteURL:             remotePath,
		WorkspaceGitBaseRef:               "origin/main",
		WorkspaceGitPushEnabled:           true,
		WorkspaceGitCleanupEnabled:        true,
		WorkspaceGitCleanupDeleteBranches: false,
		RuntimeProvider:                   "local",
		RuntimeHTTPMaxAttempts:            1,
		RuntimeHTTPRetryBaseDelay:         time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	result, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	remoteHead := strings.TrimSpace(runTestGit(t, remotePath, "rev-parse", "refs/heads/"+result.Sandbox.WorkspaceBranch))
	if remoteHead != result.Sandbox.WorkspaceHeadRef {
		t.Fatalf("remote shared head = %s, want %s", remoteHead, result.Sandbox.WorkspaceHeadRef)
	}
}

func TestGitRollbackToSnapshotRestoresWorkspaceAtSnapshotCommit(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-rollback-restore", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", WorkspaceGitCleanupEnabled: true, WorkspaceGitCleanupDeleteBranches: false, RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	mergeResult, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].WorkspaceChecksum == "" {
		t.Fatalf("expected git workspace snapshot, got %+v", snapshots)
	}
	originalSnapshot := snapshots[0]
	originalSharedHead := strings.TrimSpace(runTestGit(t, mergeResult.Sandbox.WorkspacePath, "rev-parse", "HEAD"))

	rollback, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: originalSnapshot.ID, Reason: "restore git workspace"})
	if err != nil {
		t.Fatalf("rollback snapshot: %v", err)
	}
	if rollback.Workspace == nil || !rollback.Workspace.Restored {
		t.Fatalf("expected workspace restore result, got %+v", rollback)
	}
	if rollback.Workspace.Sandbox.Scope != "SHARED" || rollback.Workspace.Sandbox.Status != domain.SandboxStatusReleased {
		t.Fatalf("expected released shared rollback sandbox, got %+v", rollback.Workspace.Sandbox)
	}
	restoredHead := strings.TrimSpace(runTestGit(t, rollback.Workspace.Sandbox.WorkspacePath, "rev-parse", "HEAD"))
	if restoredHead != originalSnapshot.WorkspaceChecksum || rollback.Workspace.HeadRef != originalSnapshot.WorkspaceChecksum {
		t.Fatalf("restored head = %s result=%s, want %s", restoredHead, rollback.Workspace.HeadRef, originalSnapshot.WorkspaceChecksum)
	}
	if strings.TrimSpace(runTestGit(t, mergeResult.Sandbox.WorkspacePath, "rev-parse", "HEAD")) != originalSharedHead {
		t.Fatalf("original shared worktree head changed")
	}
	snapshots, err = svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots after rollback: %v", err)
	}
	latest := snapshots[len(snapshots)-1]
	if latest.WorkspaceChecksum != originalSnapshot.WorkspaceChecksum || latest.WorkspaceStateRef != rollback.Workspace.StateRef {
		t.Fatalf("expected rollback snapshot to record restored workspace ref, latest=%+v rollback=%+v", latest, rollback.Workspace)
	}
}

func TestGitRollbackWorkspaceBecomesNextSharedBase(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-rollback-next-base", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", WorkspaceGitCleanupEnabled: true, WorkspaceGitCleanupDeleteBranches: false, RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{TaskIDs: []string{dispatched[0].ID, dispatched[1].ID}, ContractID: contract.ID, Endpoints: append([]domain.ContractEndpoint(nil), contract.Endpoints...), Schemas: append([]domain.ContractSchema(nil), contract.Schemas...)}); err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	rollback, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshots[0].ID})
	if err != nil {
		t.Fatalf("rollback snapshot: %v", err)
	}
	if rollback.Workspace == nil {
		t.Fatalf("expected workspace rollback result")
	}
	nextMerge, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{TaskIDs: []string{dispatched[0].ID, dispatched[1].ID}, ContractID: contract.ID, Endpoints: append([]domain.ContractEndpoint(nil), contract.Endpoints...), Schemas: append([]domain.ContractSchema(nil), contract.Schemas...)})
	if err != nil {
		t.Fatalf("merge after rollback: %v", err)
	}
	runTestGit(t, nextMerge.Sandbox.WorkspacePath, "merge-base", "--is-ancestor", rollback.Workspace.HeadRef, nextMerge.Sandbox.WorkspaceHeadRef)
	snapshots, err = svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots after next merge: %v", err)
	}
	latest := snapshots[len(snapshots)-1]
	if latest.Branch != rollback.ActiveBranch {
		t.Fatalf("expected next snapshot branch %q, got %q", rollback.ActiveBranch, latest.Branch)
	}
}

func TestGitRollbackRejectsWorkspaceChecksumMismatch(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-rollback-checksum", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", WorkspaceGitCleanupEnabled: false, RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{TaskIDs: []string{dispatched[0].ID, dispatched[1].ID}, ContractID: contract.ID, Endpoints: append([]domain.ContractEndpoint(nil), contract.Endpoints...), Schemas: append([]domain.ContractSchema(nil), contract.Schemas...)}); err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	svc.mu.Lock()
	svc.snapshotIndex[snapshots[0].ID].WorkspaceChecksum = strings.TrimSpace(runTestGit(t, repoPath, "rev-parse", "main"))
	svc.mu.Unlock()
	before := len(snapshots)
	if _, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshots[0].ID}); err == nil {
		t.Fatalf("expected checksum mismatch error")
	}
	after, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots after failed rollback: %v", err)
	}
	if len(after) != before {
		t.Fatalf("expected no rollback snapshot after failure, before=%d after=%d", before, len(after))
	}
}

func TestGitRollbackRejectsMalformedWorkspaceStateRef(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-rollback-malformed", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Malformed Restore"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	snapshot, err := svc.recordSnapshotLocked(project.ID, "main", "baseline", true, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	cases := []string{"bad", "repo://remote/multiagent/x/shared/y@HEAD", "repo://local/multiagent/x/shared/y", "repo://local/@HEAD", "repo://local/topic@"}
	for _, ref := range cases {
		t.Run(ref, func(t *testing.T) {
			svc.mu.Lock()
			svc.snapshotIndex[snapshot.ID].WorkspaceStateRef = ref
			svc.snapshotIndex[snapshot.ID].WorkspaceChecksum = strings.TrimSpace(runTestGit(t, repoPath, "rev-parse", "main"))
			svc.mu.Unlock()
			if _, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshot.ID}); err == nil {
				t.Fatalf("expected malformed ref %q to be rejected", ref)
			}
		})
	}
}

func TestRollbackRejectsGitWorkspaceSnapshotWhenProviderIsDirectory(t *testing.T) {
	svc := New(config.Config{Address: ":0", ServiceName: "test-directory-rollback-workspace", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Directory Restore"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	snapshot, err := svc.recordSnapshotLocked(project.ID, "main", "baseline", true, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	svc.mu.Lock()
	svc.snapshotIndex[snapshot.ID].WorkspaceStateRef = "repo://local/multiagent/project/shared/sandbox@abcdef"
	svc.snapshotIndex[snapshot.ID].WorkspaceChecksum = "abcdef"
	svc.mu.Unlock()
	if _, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshot.ID}); err == nil {
		t.Fatalf("expected directory provider to reject git workspace restore")
	}
}

func TestRollbackRejectsRepoStateRefForServiceState(t *testing.T) {
	repoPath := initTempGitRepo(t)
	svc := New(config.Config{Address: ":0", ServiceName: "test-repo-state-ref", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Repo StateRef"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	snapshot, err := svc.recordSnapshotLocked(project.ID, "main", "baseline", true, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	svc.mu.Lock()
	svc.snapshotIndex[snapshot.ID].StateRef = "repo://local/multiagent/project/shared/sandbox@abcdef"
	delete(svc.snapshotState, snapshot.ID)
	svc.mu.Unlock()
	_, err = svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshot.ID})
	if err == nil || !strings.Contains(err.Error(), "repo service-state restore is not implemented") {
		t.Fatalf("expected repo service-state restore error, got %v", err)
	}
}

func TestGitWorkspaceRebaseSucceedsForManagedCleanPrivateSandbox(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-workspace-rebase", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Rebase Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	oldHead := sandboxView.Sandbox.WorkspaceHeadRef
	if err := os.WriteFile(filepath.Join(repoPath, "BASE.md"), []byte("new base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runTestGit(t, repoPath, "add", "BASE.md")
	runTestGit(t, repoPath, "commit", "-m", "advance base")

	result, err := svc.RebaseWorkspaces(ctx, project.ID, RebaseWorkspacesInput{SandboxIDs: []string{sandboxView.Sandbox.ID}, TargetRef: "main"})
	if err != nil {
		t.Fatalf("rebase workspaces: %v", err)
	}
	if result.Rebased != 1 || len(result.Results) != 1 || result.Results[0].Status != "REBASED" {
		t.Fatalf("expected one rebased workspace, got %+v", result)
	}
	updated, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get updated sandbox: %v", err)
	}
	if updated.Sandbox.WorkspaceHeadRef == oldHead || updated.Sandbox.WorkspaceHeadRef != result.Results[0].NewHeadRef {
		t.Fatalf("expected updated head, old=%s result=%+v sandbox=%+v", oldHead, result.Results[0], updated.Sandbox)
	}
	runTestGit(t, updated.Sandbox.WorkspacePath, "merge-base", "--is-ancestor", "main", updated.Sandbox.WorkspaceHeadRef)
	runTestGit(t, updated.Sandbox.WorkspacePath, "cat-file", "-e", updated.Sandbox.WorkspaceHeadRef+":tasks/"+planResult.Task.ID+"/bundle/metadata/manifest.json")
	logs, err := svc.ListAuditLogs(ctx, project.ID)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if !auditContainsAction(logs, "WORKSPACE_REBASE") {
		t.Fatalf("expected WORKSPACE_REBASE audit, got %+v", logs)
	}
}

func TestGitWorkspaceRebaseConflictAbortsAndKeepsOriginalHead(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-workspace-rebase-conflict", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Rebase Conflict Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	oldHead := sandboxView.Sandbox.WorkspaceHeadRef
	conflictPath := filepath.Join(repoPath, "tasks", planResult.Task.ID, "bundle", "README.md")
	if err := os.MkdirAll(filepath.Dir(conflictPath), 0o755); err != nil {
		t.Fatalf("mkdir conflict path: %v", err)
	}
	if err := os.WriteFile(conflictPath, []byte("base conflict\n"), 0o644); err != nil {
		t.Fatalf("write conflict file: %v", err)
	}
	runTestGit(t, repoPath, "add", filepath.ToSlash(filepath.Join("tasks", planResult.Task.ID, "bundle", "README.md")))
	runTestGit(t, repoPath, "commit", "-m", "conflicting base")

	result, err := svc.RebaseWorkspaces(ctx, project.ID, RebaseWorkspacesInput{SandboxIDs: []string{sandboxView.Sandbox.ID}, TargetRef: "main"})
	if err != nil {
		t.Fatalf("rebase workspaces: %v", err)
	}
	if result.Failed != 1 || len(result.Results) != 1 || result.Results[0].Status != "FAILED" || !result.Results[0].RebaseAborted {
		t.Fatalf("expected aborted conflict, got %+v", result)
	}
	if _, err := os.Stat(result.Results[0].ConflictLog); err != nil {
		t.Fatalf("expected conflict log: %v", err)
	}
	currentHead := strings.TrimSpace(runTestGit(t, sandboxView.Sandbox.WorkspacePath, "rev-parse", "HEAD"))
	if currentHead != oldHead {
		t.Fatalf("expected old head %s after abort, got %s", oldHead, currentHead)
	}
	updated, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get updated sandbox: %v", err)
	}
	if updated.Sandbox.WorkspaceHeadRef != oldHead {
		t.Fatalf("expected sandbox metadata to keep old head %s, got %s", oldHead, updated.Sandbox.WorkspaceHeadRef)
	}
	alerts, err := svc.ListAlerts(ctx, project.ID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if !alertContainsType(alerts, "WORKSPACE_REBASE_CONFLICT") {
		t.Fatalf("expected conflict alert, got %+v", alerts)
	}
}

func TestGitWorkspaceRebaseRejectsDirtyContentWorktree(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-workspace-rebase-dirty", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Rebase Dirty Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	oldHead := sandboxView.Sandbox.WorkspaceHeadRef
	if err := os.WriteFile(filepath.Join(repoPath, "BASE.md"), []byte("new base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runTestGit(t, repoPath, "add", "BASE.md")
	runTestGit(t, repoPath, "commit", "-m", "advance base")
	if err := os.WriteFile(filepath.Join(sandboxView.Sandbox.WorkspacePath, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	result, err := svc.RebaseWorkspaces(ctx, project.ID, RebaseWorkspacesInput{SandboxIDs: []string{sandboxView.Sandbox.ID}, TargetRef: "main"})
	if err != nil {
		t.Fatalf("rebase workspaces: %v", err)
	}
	if result.Skipped != 1 || result.Results[0].Status != "SKIPPED" || !strings.Contains(result.Results[0].Reason, "uncommitted changes") {
		t.Fatalf("expected dirty skip, got %+v", result)
	}
	currentHead := strings.TrimSpace(runTestGit(t, sandboxView.Sandbox.WorkspacePath, "rev-parse", "HEAD"))
	if currentHead != oldHead {
		t.Fatalf("expected old head %s, got %s", oldHead, currentHead)
	}
}

func TestGitWorkspaceRebaseDryRunReportsAheadBehindWithoutChangingHead(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{Address: ":0", ServiceName: "test-git-workspace-rebase-dry-run", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitBaseRef: "main", RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Rebase Dry Run Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	oldHead := sandboxView.Sandbox.WorkspaceHeadRef
	if err := os.WriteFile(filepath.Join(repoPath, "BASE.md"), []byte("new base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runTestGit(t, repoPath, "add", "BASE.md")
	runTestGit(t, repoPath, "commit", "-m", "advance base")

	result, err := svc.RebaseWorkspaces(ctx, project.ID, RebaseWorkspacesInput{DryRun: true, SandboxIDs: []string{sandboxView.Sandbox.ID}, TargetRef: "main"})
	if err != nil {
		t.Fatalf("rebase dry run: %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Status != "DRY_RUN" || result.Results[0].Ahead == 0 || result.Results[0].Behind == 0 {
		t.Fatalf("expected dry-run ahead/behind, got %+v", result)
	}
	currentHead := strings.TrimSpace(runTestGit(t, sandboxView.Sandbox.WorkspacePath, "rev-parse", "HEAD"))
	if currentHead != oldHead {
		t.Fatalf("expected dry-run to keep head %s, got %s", oldHead, currentHead)
	}
}

func TestGitWorkspaceRebasePublishUsesNonForcePushAndKeepsRemoteOnRejection(t *testing.T) {
	remotePath := initBareGitRemote(t)
	repoPath := filepath.Join(t.TempDir(), "workspace-repo")
	cfg := config.Config{Address: ":0", ServiceName: "test-git-workspace-rebase-publish", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent", WorkspaceProvider: "git", WorkspaceGitRepoPath: repoPath, WorkspaceGitRemoteURL: remotePath, WorkspaceGitBaseRef: "origin/main", WorkspaceGitPushEnabled: true, RuntimeProvider: "local", RuntimeHTTPMaxAttempts: 1, RuntimeHTTPRetryBaseDelay: time.Millisecond}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Rebase Publish Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo", Content: "实现 Todo 交付包"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)
	sandboxView, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	remoteOldHead := strings.TrimSpace(runTestGit(t, remotePath, "rev-parse", "refs/heads/"+sandboxView.Sandbox.WorkspaceBranch))
	remoteWork := cloneBareRemote(t, remotePath)
	if err := os.WriteFile(filepath.Join(remoteWork, "BASE.md"), []byte("new base\n"), 0o644); err != nil {
		t.Fatalf("write remote base: %v", err)
	}
	runTestGit(t, remoteWork, "add", "BASE.md")
	runTestGit(t, remoteWork, "commit", "-m", "advance remote base")
	runTestGit(t, remoteWork, "push", "origin", "main")
	otherWork := cloneBareRemote(t, remotePath)
	runTestGit(t, otherWork, "checkout", sandboxView.Sandbox.WorkspaceBranch)
	if err := os.WriteFile(filepath.Join(otherWork, "REMOTE-BRANCH.md"), []byte("remote branch moved\n"), 0o644); err != nil {
		t.Fatalf("write remote branch move: %v", err)
	}
	runTestGit(t, otherWork, "add", "REMOTE-BRANCH.md")
	runTestGit(t, otherWork, "commit", "-m", "move remote task branch")
	runTestGit(t, otherWork, "push", "origin", sandboxView.Sandbox.WorkspaceBranch)
	remoteMovedHead := strings.TrimSpace(runTestGit(t, remotePath, "rev-parse", "refs/heads/"+sandboxView.Sandbox.WorkspaceBranch))
	if remoteMovedHead == remoteOldHead {
		t.Fatalf("expected remote task branch to move")
	}

	result, err := svc.RebaseWorkspaces(ctx, project.ID, RebaseWorkspacesInput{SandboxIDs: []string{sandboxView.Sandbox.ID}, TargetRef: "origin/main", Fetch: true, Publish: true})
	if err != nil {
		t.Fatalf("rebase publish: %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Status != "REBASED_PUBLISH_FAILED" || result.Rebased != 1 || result.Failed != 1 {
		t.Fatalf("expected publish failure after local rebase, got %+v", result)
	}
	remoteAfter := strings.TrimSpace(runTestGit(t, remotePath, "rev-parse", "refs/heads/"+sandboxView.Sandbox.WorkspaceBranch))
	if remoteAfter != remoteMovedHead {
		t.Fatalf("expected non-force publish to keep remote head %s, got %s", remoteMovedHead, remoteAfter)
	}
}

func TestGitErrorSanitizerRedactsAuthToken(t *testing.T) {
	secret := "ghp_super_secret"
	text := sanitizeGitError("fatal: authentication failed for "+secret, []string{secret})
	if strings.Contains(text, secret) || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("expected token redaction, got %q", text)
	}
}

func TestGitWorkspaceCleanupKeepsBranchesWhenSafeDeleteIsRefused(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{
		Address:                   ":0",
		ServiceName:               "test-git-workspace-cleanup-branches",
		ArtifactRoot:              t.TempDir(),
		SandboxRoot:               t.TempDir(),
		DefaultAgent:              "manager-agent",
		WorkspaceProvider:         "git",
		WorkspaceGitRepoPath:      repoPath,
		WorkspaceGitBaseRef:       "main",
		RuntimeProvider:           "local",
		RuntimeHTTPMaxAttempts:    1,
		RuntimeHTTPRetryBaseDelay: time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	privateBranches := make([]string, 0, 2)
	privateHeads := make([]string, 0, 2)
	for _, sandboxView := range mustListSandboxes(t, svc, ctx, project.ID) {
		if sandboxView.Sandbox.Scope == "PRIVATE" {
			privateBranches = append(privateBranches, sandboxView.Sandbox.WorkspaceBranch)
			privateHeads = append(privateHeads, sandboxView.Sandbox.WorkspaceHeadRef)
		}
	}

	mergeResult, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	cleanup, err := svc.CleanupWorkspaces(ctx, project.ID, CleanupWorkspacesInput{Scope: "PRIVATE", DeleteBranches: true})
	if err != nil {
		t.Fatalf("cleanup workspaces: %v", err)
	}
	if cleanup.DeletedBranches != 0 || cleanup.RemovedWorktrees != len(privateBranches) || cleanup.Skipped != len(privateBranches) {
		t.Fatalf("expected worktrees removed and branch deletion safely skipped, got %+v", cleanup)
	}
	for idx, branch := range privateBranches {
		if out := runTestGit(t, repoPath, "branch", "--list", branch); !strings.Contains(out, branch) {
			t.Fatalf("expected branch %s to remain after non-force cleanup, got %q", branch, out)
		}
		runTestGit(t, mergeResult.Sandbox.WorkspacePath, "merge-base", "--is-ancestor", privateHeads[idx], mergeResult.Sandbox.WorkspaceHeadRef)
	}
	if out := runTestGit(t, repoPath, "branch", "--list", mergeResult.Sandbox.WorkspaceBranch); !strings.Contains(out, mergeResult.Sandbox.WorkspaceBranch) {
		t.Fatalf("expected shared branch to remain, got %q", out)
	}
}

func TestGitWorkspaceCleanupDryRunKeepsWorktrees(t *testing.T) {
	repoPath := initTempGitRepo(t)
	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-git-workspace-cleanup-dry-run",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		DefaultAgent:               "manager-agent",
		WorkspaceProvider:          "git",
		WorkspaceGitRepoPath:       repoPath,
		WorkspaceGitBaseRef:        "main",
		WorkspaceGitCleanupEnabled: false,
		RuntimeProvider:            "local",
		RuntimeHTTPMaxAttempts:     1,
		RuntimeHTTPRetryBaseDelay:  time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	privatePaths := make([]string, 0, 2)
	for _, sandboxView := range mustListSandboxes(t, svc, ctx, project.ID) {
		if sandboxView.Sandbox.Scope == "PRIVATE" {
			privatePaths = append(privatePaths, sandboxView.Sandbox.WorkspacePath)
		}
	}
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	cleanup, err := svc.CleanupWorkspaces(ctx, project.ID, CleanupWorkspacesInput{Scope: "PRIVATE", DryRun: true})
	if err != nil {
		t.Fatalf("cleanup workspaces dry run: %v", err)
	}
	if cleanup.RemovedWorktrees != len(privatePaths) {
		t.Fatalf("expected dry-run planned removals, got %+v", cleanup)
	}
	worktrees := runTestGit(t, repoPath, "worktree", "list", "--porcelain")
	for _, path := range privatePaths {
		if !strings.Contains(worktrees, path) {
			t.Fatalf("expected dry run to keep worktree %s, got\n%s", path, worktrees)
		}
	}
}

func TestMergeSharedSandboxFailsWhenWorkspaceCannotBeCreated(t *testing.T) {
	sandboxRootFile := filepath.Join(t.TempDir(), "sandbox-root-file")
	if err := os.WriteFile(sandboxRootFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write sandbox root file: %v", err)
	}

	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-shared-sandbox-create-error",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()
	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	svc.cfg.SandboxRoot = sandboxRootFile

	_, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "SANDBOX_CREATE_FAILED" {
		t.Fatalf("expected SANDBOX_CREATE_FAILED, got %v", err)
	}
}

func TestMergeSharedSandboxBlocksOnContractConflict(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-shared-sandbox-conflict",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)

	result, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints: []domain.ContractEndpoint{
			{Name: "ListTodo", Method: "GET", Path: "/api/todos"},
		},
		Schemas: []domain.ContractSchema{
			{
				Name: "Todo",
				Fields: []domain.ContractField{
					{Name: "id", Type: "string", Required: true},
					{Name: "title", Type: "number", Required: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	if result.Passed {
		t.Fatal("expected merge to be blocked by contract conflict")
	}
	if result.Sandbox.Status != domain.SandboxStatusFailed {
		t.Fatalf("expected shared sandbox FAILED, got %s", result.Sandbox.Status)
	}
	if len(result.ContractConflicts) == 0 {
		t.Fatal("expected contract conflicts in merge result")
	}
	if result.RemediationTask == nil {
		t.Fatal("expected remediation task when shared sandbox merge conflicts")
	}
	if result.RemediationTask.Type != "CONTRACT_REWORK" {
		t.Fatalf("expected remediation task type CONTRACT_REWORK, got %s", result.RemediationTask.Type)
	}
}

func TestMergeSharedSandboxBlocksOnIntegrationFailure(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-shared-sandbox-failure",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)

	result, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:         []string{dispatched[0].ID, dispatched[1].ID},
		ContractID:      contract.ID,
		Endpoints:       append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:         append([]domain.ContractSchema(nil), contract.Schemas...),
		SimulateFailure: true,
	})
	if err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}
	if result.Passed {
		t.Fatal("expected merge to be blocked by simulated integration failure")
	}
	if result.Sandbox.Status != domain.SandboxStatusFailed {
		t.Fatalf("expected shared sandbox FAILED, got %s", result.Sandbox.Status)
	}
	if len(result.ContractConflicts) != 0 {
		t.Fatalf("expected no contract conflicts for integration failure path, got %d", len(result.ContractConflicts))
	}
	if _, err := os.Stat(filepath.Join(result.Sandbox.RootPath, "integration-error.log")); err != nil {
		t.Fatalf("expected integration error log: %v", err)
	}
}

func TestSharedSandboxFailureAutoRollbackToLatestStableSnapshot(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-shared-sandbox-auto-rollback",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.GenerateTaskContext(ctx, project.ID, dispatched[0].ID); err != nil {
		t.Fatalf("generate backend context: %v", err)
	}
	if _, err := svc.GenerateTaskContext(ctx, project.ID, dispatched[1].ID); err != nil {
		t.Fatalf("generate frontend context: %v", err)
	}

	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("create stable shared snapshot: %v", err)
	}

	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot before failure, got %d", len(snapshots))
	}

	result, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:         []string{dispatched[0].ID, dispatched[1].ID},
		ContractID:      contract.ID,
		Endpoints:       append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:         append([]domain.ContractSchema(nil), contract.Schemas...),
		SimulateFailure: true,
	})
	if err != nil {
		t.Fatalf("merge shared sandbox with simulated failure: %v", err)
	}
	if result.Passed {
		t.Fatal("expected failing merge")
	}
	if result.Rollback == nil {
		t.Fatal("expected automatic rollback result")
	}
	if result.Rollback.RestoredFrom.ID != snapshots[0].ID {
		t.Fatalf("expected rollback to snapshot %s, got %s", snapshots[0].ID, result.Rollback.RestoredFrom.ID)
	}
	if result.Rollback.ActiveBranch == "main" {
		t.Fatalf("expected rollback to create a new branch, got %s", result.Rollback.ActiveBranch)
	}
	if result.Rollback.ClearedContexts < 2 {
		t.Fatalf("expected rollback to clear generated contexts, got %d", result.Rollback.ClearedContexts)
	}
	if _, err := svc.GetLatestTaskContext(ctx, project.ID, dispatched[0].ID); err == nil {
		t.Fatal("expected task context to be cleared after rollback")
	}

	sandboxes, err := svc.ListSandboxes(ctx, project.ID)
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes) != 3 {
		t.Fatalf("expected rollback to restore 3 sandboxes, got %d", len(sandboxes))
	}
}

func TestRollbackToSnapshotResolvesFileStateRef(t *testing.T) {
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-snapshot-file-state-ref",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "file",
		DataRoot:      t.TempDir(),
		DefaultAgent:  "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("create initial stable snapshot: %v", err)
	}
	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	delete(svc.snapshotState, snapshots[0].ID)

	rollback, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshots[0].ID, Reason: "file state ref rollback"})
	if err != nil {
		t.Fatalf("rollback from file state ref: %v", err)
	}
	if rollback.RestoredFrom.ID != snapshots[0].ID {
		t.Fatalf("expected restored snapshot %s, got %s", snapshots[0].ID, rollback.RestoredFrom.ID)
	}
}

func TestRollbackToSnapshotRejectsFileChecksumMismatch(t *testing.T) {
	cfg := config.Config{
		Address:       ":0",
		ServiceName:   "test-snapshot-file-checksum",
		ArtifactRoot:  t.TempDir(),
		SandboxRoot:   t.TempDir(),
		StoreProvider: "file",
		DataRoot:      t.TempDir(),
		DefaultAgent:  "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("create initial stable snapshot: %v", err)
	}
	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	delete(svc.snapshotState, snapshots[0].ID)
	statePath := strings.TrimPrefix(snapshots[0].StateRef, "file://")
	if err := os.WriteFile(statePath, []byte(`{"corrupt":true}`), 0o644); err != nil {
		t.Fatalf("corrupt snapshot state: %v", err)
	}

	_, err = svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{SnapshotID: snapshots[0].ID, Reason: "checksum mismatch"})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "CONFLICT" || !strings.Contains(appErr.Message, "checksum mismatch") {
		t.Fatalf("expected checksum conflict, got %v", err)
	}
}

func TestRollbackToSnapshotCreatesParallelBranchTimeline(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-snapshot-rollback-branch",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.GenerateTaskContext(ctx, project.ID, dispatched[0].ID); err != nil {
		t.Fatalf("generate task context: %v", err)
	}
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("create initial stable snapshot: %v", err)
	}

	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 initial snapshot, got %d", len(snapshots))
	}

	rollback, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{
		SnapshotID: snapshots[0].ID,
		Reason:     "manual rollback verification",
	})
	if err != nil {
		t.Fatalf("rollback to snapshot: %v", err)
	}
	if rollback.ActiveBranch == rollback.PreviousBranch {
		t.Fatalf("expected new branch after rollback, got previous=%s active=%s", rollback.PreviousBranch, rollback.ActiveBranch)
	}
	if rollback.Snapshot.SourceSnapshotID != snapshots[0].ID {
		t.Fatalf("expected rollback snapshot source %s, got %s", snapshots[0].ID, rollback.Snapshot.SourceSnapshotID)
	}

	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("create post-rollback checkpoint: %v", err)
	}

	snapshots, err = svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots after rollback branch work: %v", err)
	}
	if len(snapshots) != 3 {
		t.Fatalf("expected 3 snapshots after rollback branch work, got %d", len(snapshots))
	}
	if snapshots[0].Branch != "main" {
		t.Fatalf("expected original branch main, got %s", snapshots[0].Branch)
	}
	if snapshots[1].Branch != rollback.ActiveBranch || snapshots[2].Branch != rollback.ActiveBranch {
		t.Fatalf("expected rollback branch snapshots on %s, got %s and %s", rollback.ActiveBranch, snapshots[1].Branch, snapshots[2].Branch)
	}
}

func TestApplyHumanOverrideAtSafetyCheckpoint(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-human-override",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Override Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并支持人工接管。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForTaskStatus(t, svc, project.ID, runEnvelope.Run.ID, domain.TaskStatusInProgress)

	overrideResult, err := svc.ApplyHumanOverride(ctx, project.ID, ApplyHumanOverrideInput{
		TaskID:      planResult.Task.ID,
		Operator:    "reviewer",
		Instruction: "请优先保留 README 说明并按人工要求继续执行",
		LockScope:   "TASK",
	})
	if err != nil {
		t.Fatalf("apply human override: %v", err)
	}
	if overrideResult.Task.Status != domain.TaskStatusHumanOverride {
		t.Fatalf("expected task to enter HUMAN_OVERRIDE, got %s", overrideResult.Task.Status)
	}
	if overrideResult.Run == nil || overrideResult.Run.ID != runEnvelope.Run.ID {
		t.Fatalf("expected active run %s in override result, got %+v", runEnvelope.Run.ID, overrideResult.Run)
	}

	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run to succeed after override, got %s", status.Run.Status)
	}
	if !strings.Contains(status.Run.ResultSummary, "applied human override by reviewer") {
		t.Fatalf("expected run summary to include applied override, got %s", status.Run.ResultSummary)
	}
	if status.Task.Status != domain.TaskStatusDone {
		t.Fatalf("expected task DONE after override path, got %s", status.Task.Status)
	}
	foundOverrideTransition := false
	for _, item := range status.Task.Audit {
		if item.To == domain.TaskStatusHumanOverride {
			foundOverrideTransition = true
			break
		}
	}
	if !foundOverrideTransition {
		t.Fatal("expected task audit to include HUMAN_OVERRIDE transition")
	}
}

func TestListCommunicationLogsWithTaskFilter(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-communications",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, _, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.GenerateTaskContext(ctx, project.ID, dispatched[0].ID); err != nil {
		t.Fatalf("generate task context: %v", err)
	}

	items, err := svc.ListCommunicationLogs(ctx, project.ID, "")
	if err != nil {
		t.Fatalf("list communication logs: %v", err)
	}
	if len(items) < 6 {
		t.Fatalf("expected at least 6 communication log entries, got %d", len(items))
	}

	foundDispatch := false
	foundRunStart := false
	foundContext := false
	for _, item := range items {
		switch item.Type {
		case "TASK_DISPATCH":
			foundDispatch = true
		case "RUN_START":
			foundRunStart = true
		case "CONTEXT_INJECTION":
			foundContext = true
		}
	}
	if !foundDispatch || !foundRunStart || !foundContext {
		t.Fatalf("expected dispatch/run/context communication types, got %+v", items)
	}

	filtered, err := svc.ListCommunicationLogs(ctx, project.ID, dispatched[0].ID)
	if err != nil {
		t.Fatalf("list filtered communication logs: %v", err)
	}
	if len(filtered) < 3 {
		t.Fatalf("expected at least 3 communication log entries for task %s, got %d", dispatched[0].ID, len(filtered))
	}
	for _, item := range filtered {
		if item.TaskID != dispatched[0].ID {
			t.Fatalf("expected filtered task id %s, got %s", dispatched[0].ID, item.TaskID)
		}
	}
}

func TestGetTokenCostTrendByTask(t *testing.T) {
	cfg := config.Config{
		Address:             ":0",
		ServiceName:         "test-token-costs",
		ArtifactRoot:        t.TempDir(),
		SandboxRoot:         t.TempDir(),
		DefaultAgent:        "manager-agent",
		TokenBudgetWarnUSD:  0.000001,
		TokenBudgetBlockUSD: 1,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, _, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)

	trend, err := svc.GetTokenCostTrend(ctx, project.ID, "")
	if err != nil {
		t.Fatalf("get token cost trend: %v", err)
	}
	if len(trend.Points) != 2 {
		t.Fatalf("expected 2 token cost points, got %d", len(trend.Points))
	}
	if trend.TotalTokens <= 0 || trend.EstimatedCostUSD <= 0 {
		t.Fatalf("expected positive token totals and cost, got %+v", trend)
	}
	if trend.BudgetStatus != "warning" || trend.BudgetWarnUSD != cfg.TokenBudgetWarnUSD || trend.BudgetBlockUSD != cfg.TokenBudgetBlockUSD {
		t.Fatalf("unexpected budget status: %+v", trend)
	}

	filtered, err := svc.GetTokenCostTrend(ctx, project.ID, dispatched[0].ID)
	if err != nil {
		t.Fatalf("get filtered token cost trend: %v", err)
	}
	if len(filtered.Points) != 1 {
		t.Fatalf("expected 1 filtered token cost point, got %d", len(filtered.Points))
	}
	if filtered.Points[0].TaskID != dispatched[0].ID {
		t.Fatalf("expected filtered task id %s, got %s", dispatched[0].ID, filtered.Points[0].TaskID)
	}
	if filtered.Points[0].TotalTokens <= 0 || filtered.Points[0].EstimatedCostUSD <= 0 {
		t.Fatalf("expected positive filtered token totals and cost, got %+v", filtered.Points[0])
	}
}

func TestStartRunBlocksWhenTokenBudgetWouldBeExceeded(t *testing.T) {
	cfg := config.Config{
		Address:                    ":0",
		ServiceName:                "test-token-budget-block",
		ArtifactRoot:               t.TempDir(),
		SandboxRoot:                t.TempDir(),
		DefaultAgent:               "manager-agent",
		TokenPromptPricePerMillion: 1_000_000,
		TokenOutputPricePerMillion: 1_000_000,
		TokenBudgetBlockUSD:        1,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Budget Block Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "实现 Todo API", Content: "实现 Todo API"}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	_, err = svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err == nil {
		t.Fatal("expected token budget conflict")
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "CONFLICT" {
		t.Fatalf("expected conflict app error, got %v", err)
	}
}

func TestListAuditLogsTracksCriticalActions(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-audit-logs",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := WithActor(context.Background(), "qa-reviewer")

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Audit Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并导出标准交付包。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)

	artifact, err := svc.ExportDelivery(ctx, project.ID, ExportDeliveryInput{RunID: runEnvelope.Run.ID})
	if err != nil {
		t.Fatalf("export delivery: %v", err)
	}
	if _, err := svc.GetArtifact(ctx, project.ID, artifact.ID); err != nil {
		t.Fatalf("download artifact: %v", err)
	}

	items, err := svc.ListAuditLogs(ctx, project.ID)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(items) < 4 {
		t.Fatalf("expected at least 4 audit entries, got %d", len(items))
	}

	foundProjectCreate := false
	foundExport := false
	foundDownload := false
	for _, item := range items {
		if item.Actor != "qa-reviewer" {
			t.Fatalf("expected audit actor qa-reviewer, got %s", item.Actor)
		}
		switch item.Action {
		case "PROJECT_CREATE":
			foundProjectCreate = true
		case "DELIVERY_EXPORT":
			foundExport = true
		case "DELIVERY_DOWNLOAD":
			foundDownload = true
		}
	}
	if !foundProjectCreate || !foundExport || !foundDownload {
		t.Fatalf("expected audit entries for create, export and download, got %+v", items)
	}
}

func TestGetArtifactRejectsPathOutsideConfiguredRoots(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-artifact-path-boundary",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Artifact Boundary Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "delivery.zip")
	if err := os.WriteFile(outsidePath, []byte("zip"), 0o644); err != nil {
		t.Fatalf("write outside artifact: %v", err)
	}
	artifact := &domain.Artifact{
		ID:        "artifact_outside",
		ProjectID: project.ID,
		Kind:      "delivery_bundle",
		URI:       outsidePath,
		CreatedAt: time.Now().UTC(),
	}
	svc.mu.Lock()
	svc.artifacts[artifact.ID] = artifact
	svc.artifactOrder[project.ID] = append(svc.artifactOrder[project.ID], artifact.ID)
	svc.mu.Unlock()

	_, err = svc.GetArtifact(ctx, project.ID, artifact.ID)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "ARTIFACT_PATH_INVALID" {
		t.Fatalf("expected ARTIFACT_PATH_INVALID, got %v", err)
	}
}

func TestGetArtifactMissingFileDoesNotRecordDownload(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-missing-artifact-download",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := WithActor(context.Background(), "qa-reviewer")

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Missing Artifact Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并导出标准交付包。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitForSucceededRun(t, svc, project.ID, runEnvelope.Run.ID)

	artifact, err := svc.ExportDelivery(ctx, project.ID, ExportDeliveryInput{RunID: runEnvelope.Run.ID})
	if err != nil {
		t.Fatalf("export delivery: %v", err)
	}
	if err := os.Remove(artifact.URI); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}

	_, err = svc.GetArtifact(ctx, project.ID, artifact.ID)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "ARTIFACT_MISSING" {
		t.Fatalf("expected ARTIFACT_MISSING error, got %v", err)
	}

	items, err := svc.ListAuditLogs(ctx, project.ID)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	for _, item := range items {
		if item.Action == "DELIVERY_DOWNLOAD" {
			t.Fatalf("did not expect download audit for missing file, got %+v", items)
		}
	}
}

func TestRunFailsWhenHTTPRuntimeProviderUnavailable(t *testing.T) {
	cfg := config.Config{
		Address:         ":0",
		ServiceName:     "test-runtime-unavailable",
		ArtifactRoot:    t.TempDir(),
		SandboxRoot:     t.TempDir(),
		DefaultAgent:    "go-backend-agent",
		RuntimeProvider: "http",
		RuntimeTimeout:  2 * time.Second,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Runtime Unavailable Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo API",
		Content: "实现 Todo API 并返回可交付产物",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", status.Run.Status)
	}
	if !strings.Contains(status.Run.Error, "configured runtime provider") || strings.Contains(status.Run.ResultSummary, "mock") {
		t.Fatalf("expected explicit http provider failure without local fallback, got error=%q summary=%q", status.Run.Error, status.Run.ResultSummary)
	}

	items, err := svc.ListAlerts(ctx, project.ID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(items) == 0 || items[0].Type != "RUN_FAILURE" {
		t.Fatalf("expected RUN_FAILURE alert, got %+v", items)
	}
}

func TestRunFailsOnStructuredRuntimeProviderError(t *testing.T) {
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": "runtime.http.v1",
			"error": map[string]any{
				"code":           "UPSTREAM_UNAVAILABLE",
				"message":        "runtime dependency unavailable",
				"retryable":      true,
				"providerStatus": http.StatusServiceUnavailable,
				"requestId":      "req_service_123",
			},
		})
	}))
	defer runtimeServer.Close()

	cfg := config.Config{
		Address:         ":0",
		ServiceName:     "test-runtime-error",
		ArtifactRoot:    t.TempDir(),
		SandboxRoot:     t.TempDir(),
		DefaultAgent:    "go-backend-agent",
		RuntimeProvider: "http",
		RuntimeEndpoint: runtimeServer.URL,
		RuntimeTimeout:  2 * time.Second,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Runtime Error Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo API",
		Content: "实现 Todo API 并返回可交付产物",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", status.Run.Status)
	}
	for _, want := range []string{"UPSTREAM_UNAVAILABLE", "status=502", "providerStatus=503", "retryable=true", "requestId=req_service_123"} {
		if !strings.Contains(status.Run.Error, want) {
			t.Fatalf("expected run error to contain %q, got %q", want, status.Run.Error)
		}
	}

	items, err := svc.ListAlerts(ctx, project.ID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(items) == 0 || !strings.Contains(items[0].Message, "UPSTREAM_UNAVAILABLE") {
		t.Fatalf("expected alert with provider error, got %+v", items)
	}
}

func TestListAlertsIncludesRunFailures(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-alerts",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Alert Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并模拟私有沙盒失败。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if err := svc.MarkTaskSandboxFailure(ctx, project.ID, planResult.Task.ID, "simulated crash"); err != nil {
		t.Fatalf("mark sandbox failure: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", status.Run.Status)
	}

	items, err := svc.ListAlerts(ctx, project.ID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one alert")
	}
	if items[0].Type != "RUN_FAILURE" || items[0].Severity != "ERROR" {
		t.Fatalf("expected RUN_FAILURE ERROR alert, got %+v", items[0])
	}
}

func TestAlertWebhookReceivesRunFailureNotification(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-alert-webhook",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}

	var (
		mu       sync.Mutex
		received []domain.Alert
	)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload struct {
			Service string       `json:"service"`
			Alert   domain.Alert `json:"alert"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode webhook payload: %v", err)
		}
		mu.Lock()
		received = append(received, payload.Alert)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer sink.Close()
	cfg.AlertWebhookURL = sink.URL

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Webhook Alert Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并模拟私有沙盒失败。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	if err := svc.MarkTaskSandboxFailure(ctx, project.ID, planResult.Task.ID, "simulated crash"); err != nil {
		t.Fatalf("mark sandbox failure: %v", err)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", status.Run.Status)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(received)
		var alert domain.Alert
		if count > 0 {
			alert = received[0]
		}
		mu.Unlock()
		if count > 0 {
			if alert.Type != "RUN_FAILURE" {
				t.Fatalf("expected RUN_FAILURE webhook alert, got %+v", alert)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("expected alert webhook notification")
}

func TestListAlertsIncludesRollbackEvents(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-rollback-alerts",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("create initial stable snapshot: %v", err)
	}

	snapshots, err := svc.ListSnapshots(ctx, project.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) == 0 {
		t.Fatal("expected at least one snapshot")
	}

	if _, err := svc.RollbackToSnapshot(ctx, project.ID, RollbackSnapshotInput{
		SnapshotID: snapshots[0].ID,
		Reason:     "manual rollback verification",
	}); err != nil {
		t.Fatalf("rollback to snapshot: %v", err)
	}

	items, err := svc.ListAlerts(ctx, project.ID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	found := false
	for _, item := range items {
		if item.Type == "SNAPSHOT_ROLLBACK" && item.Severity == "WARN" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected SNAPSHOT_ROLLBACK WARN alert, got %+v", items)
	}
}

func TestCodeLockPreservesHumanContent(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-code-lock",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Code Lock Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:   "实现 Todo 列表的增删改查",
		Content: "实现 Todo 列表的增删改查，并支持人工锁定代码。",
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}

	lockedSource := `package main

import "fmt"

func main() {
	// LOCKED BY HUMAN
	fmt.Println("human locked main")
}
`
	lockResult, err := svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{
		TaskID:    planResult.Task.ID,
		Path:      "generated-app/main.go",
		Content:   lockedSource,
		CreatedBy: "reviewer",
	})
	if err != nil {
		t.Fatalf("apply code lock: %v", err)
	}
	if lockResult.Lock.Path != "generated-app/main.go" {
		t.Fatalf("expected lock path generated-app/main.go, got %s", lockResult.Lock.Path)
	}

	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s", status.Run.Status)
	}

	sandbox, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get run sandbox: %v", err)
	}
	mainPath := filepath.Join(sandbox.Sandbox.RootPath, "workspace", "bundle", "generated-app", "main.go")
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read locked main.go: %v", err)
	}
	if string(data) != lockedSource {
		t.Fatalf("expected locked source to be preserved, got:\n%s", string(data))
	}
	conflictLog := filepath.Join(sandbox.Sandbox.RootPath, "workspace", "bundle", "metadata", "lock-conflicts.log")
	if _, err := os.Stat(conflictLog); err != nil {
		t.Fatalf("expected lock conflict log: %v", err)
	}
}

func TestCodeLockPreservesLateGeneratedHumanContent(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-code-lock-late-file",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Late Lock Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "Todo", Content: "Implement todo app."}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	lockedHTML := "<html><body><!-- LOCKED BY HUMAN -->late generated lock</body></html>\n"
	if _, err := svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{
		TaskID:    planResult.Task.ID,
		Path:      "web-app/index.html",
		Content:   lockedHTML,
		CreatedBy: "reviewer",
	}); err != nil {
		t.Fatalf("apply code lock: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s", status.Run.Status)
	}
	sandbox, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sandbox.Sandbox.WorkspacePath, "bundle", "web-app", "index.html"))
	if err != nil {
		t.Fatalf("read locked html: %v", err)
	}
	if string(data) != lockedHTML {
		t.Fatalf("expected locked html, got %s", string(data))
	}
}

func TestGoSymbolCodeLockReplacesOnlyFunction(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-code-lock-go-symbol",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Go Symbol Lock Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "Todo", Content: "Implement todo app."}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	lockedSource := `package main

import "fmt"

func main() {
	// LOCKED BY HUMAN
	fmt.Println("symbol lock")
}
`
	if _, err := svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{
		TaskID:     planResult.Task.ID,
		Path:       "generated-app/main.go",
		Content:    lockedSource,
		LockMode:   "go_symbol",
		Language:   "go",
		SymbolKind: "func",
		SymbolName: "main",
		CreatedBy:  "reviewer",
	}); err != nil {
		t.Fatalf("apply go symbol lock: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s", status.Run.Status)
	}
	sandbox, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sandbox.Sandbox.WorkspacePath, "bundle", "generated-app", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(data)
	if !strings.Contains(source, `fmt.Println("symbol lock")`) || !strings.Contains(source, "type todo struct") {
		t.Fatalf("expected locked main and preserved generated declarations, got:\n%s", source)
	}
}

func TestGoSymbolCodeLockReconcilesImports(t *testing.T) {
	bundleDir := t.TempDir()
	targetPath := filepath.Join(bundleDir, "generated-app", "main.go")
	targetSource := []byte(`package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type todo struct {
	ID string
}

func main() {
	fmt.Println("generated")
}
`)
	if err := writeFile(targetPath, targetSource); err != nil {
		t.Fatalf("write target source: %v", err)
	}
	lock := &domain.CodeLock{
		Path: "generated-app/main.go",
		Content: `package main

import (
	"log"
	"strings"
)

func main() {
	// LOCKED BY HUMAN
	log.Println(strings.TrimSpace(" human "))
}
`,
		LockMode:   "go_symbol",
		Language:   "go",
		SymbolKind: "func",
		SymbolName: "main",
		CreatedBy:  "reviewer",
	}
	changed, _, err := applyGoSymbolLock(targetPath, lock)
	if err != nil {
		t.Fatalf("apply go symbol lock: %v", err)
	}
	if !changed {
		t.Fatal("expected lock to change target source")
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target source: %v", err)
	}
	source := string(data)
	for _, fragment := range []string{`"log"`, `"strings"`, `log.Println(strings.TrimSpace`, "type todo struct"} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("expected reconciled source to contain %s, got:\n%s", fragment, source)
		}
	}
	for _, fragment := range []string{`"encoding/json"`, `"fmt"`, `"net/http"`} {
		if strings.Contains(source, fragment) {
			t.Fatalf("expected unused import %s to be removed, got:\n%s", fragment, source)
		}
	}
}

func TestGoSymbolCodeLockRequiresMarkerInsideSelectedSymbol(t *testing.T) {
	svc := New(config.Config{ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent"}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Marker Scope Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	_, err = svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{
		Path: "generated-app/main.go",
		Content: `package main

// LOCKED BY HUMAN

func main() {
	println("not actually locked")
}
`,
		LockMode:   "go_symbol",
		Language:   "go",
		SymbolKind: "func",
		SymbolName: "main",
		CreatedBy:  "reviewer",
	})
	if err == nil || !strings.Contains(err.Error(), "marker must be inside selected Go symbol") {
		t.Fatalf("expected marker scope validation error, got %v", err)
	}
}

func TestGoSymbolCodeLockPreservesDocCommentMarker(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-code-lock-doc-marker", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent"}
	svc := New(cfg, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Doc Marker Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "Todo", Content: "Implement todo app."}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	lockedSource := `package main

// LOCKED BY HUMAN
// human owned entrypoint
func main() {
	println("doc marker lock")
}
`
	if _, err := svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{TaskID: planResult.Task.ID, Path: "generated-app/main.go", Content: lockedSource, LockMode: "go_symbol", Language: "go", SymbolKind: "func", SymbolName: "main", CreatedBy: "reviewer"}); err != nil {
		t.Fatalf("apply go symbol lock: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s", status.Run.Status)
	}
	sandbox, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sandbox.Sandbox.WorkspacePath, "bundle", "generated-app", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(data)
	if !strings.Contains(source, "// LOCKED BY HUMAN") || !strings.Contains(source, `println("doc marker lock")`) || !strings.Contains(source, "type todo struct") {
		t.Fatalf("expected doc marker lock and generated declarations, got:\n%s", source)
	}
}

func TestGoSymbolCodeLockCreatesMissingGoFileWithPackageAndImports(t *testing.T) {
	cfg := config.Config{Address: ":0", ServiceName: "test-code-lock-missing-go", ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent"}
	svc := New(cfg, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Missing Go Lock Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{Title: "Todo", Content: "Implement todo app."}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	planResult, err := svc.GeneratePlan(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	lockedSource := `package main

import (
	"fmt"
	"strings"
)

// LOCKED BY HUMAN
func lockedHelper() string {
	return fmt.Sprint(strings.TrimSpace(" helper "))
}
`
	if _, err := svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{TaskID: planResult.Task.ID, Path: "generated-app/locked_helper.go", Content: lockedSource, LockMode: "go_symbol", Language: "go", SymbolKind: "func", SymbolName: "lockedHelper", CreatedBy: "reviewer"}); err != nil {
		t.Fatalf("apply go symbol lock: %v", err)
	}
	runEnvelope, err := svc.StartRun(ctx, project.ID, StartRunInput{TaskID: planResult.Task.ID})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status := waitForRunTerminal(t, svc, project.ID, runEnvelope.Run.ID)
	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run success, got %s", status.Run.Status)
	}
	sandbox, err := svc.GetRunSandbox(ctx, project.ID, runEnvelope.Run.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sandbox.Sandbox.WorkspacePath, "bundle", "generated-app", "locked_helper.go"))
	if err != nil {
		t.Fatalf("read locked helper: %v", err)
	}
	source := string(data)
	for _, fragment := range []string{"package main", `"fmt"`, `"strings"`, "// LOCKED BY HUMAN", "func lockedHelper() string", "fmt.Sprint(strings.TrimSpace"} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("expected missing Go file to contain %s, got:\n%s", fragment, source)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "locked_helper.go", data, parser.ParseComments); err != nil {
		t.Fatalf("expected generated helper to parse: %v\n%s", err, source)
	}
}

func TestGoSymbolCodeLockSupportsMethodTypeVarAndConst(t *testing.T) {
	targetSource := []byte(`package main

type todo struct {
	ID string
}

func (t todo) label() string {
	return t.ID
}

var defaultTitle = "generated"

const maxTodos = 10

func main() {}
`)

	cases := []struct {
		name       string
		symbolKind string
		symbolName string
		locked     string
		want       string
	}{
		{
			name:       "method",
			symbolKind: "method",
			symbolName: "label",
			locked: `package main

func (t todo) label() string {
	// LOCKED BY HUMAN
	return "human"
}
`,
			want: `return "human"`,
		},
		{
			name:       "type",
			symbolKind: "type",
			symbolName: "todo",
			locked: `package main

type todo struct {
	// LOCKED BY HUMAN
	ID string
	Title string
}
`,
			want: `Title string`,
		},
		{
			name:       "var",
			symbolKind: "var",
			symbolName: "defaultTitle",
			locked: `package main

// LOCKED BY HUMAN
var defaultTitle = "human"
`,
			want: `var defaultTitle = "human"`,
		},
		{
			name:       "const",
			symbolKind: "const",
			symbolName: "maxTodos",
			locked: `package main

// LOCKED BY HUMAN
const maxTodos = 99
`,
			want: `const maxTodos = 99`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundleDir := t.TempDir()
			targetPath := filepath.Join(bundleDir, "generated-app", "main.go")
			if err := writeFile(targetPath, targetSource); err != nil {
				t.Fatalf("write target source: %v", err)
			}
			lock := &domain.CodeLock{
				Path:       "generated-app/main.go",
				Content:    tc.locked,
				LockMode:   "go_symbol",
				Language:   "go",
				SymbolKind: tc.symbolKind,
				SymbolName: tc.symbolName,
				CreatedBy:  "reviewer",
			}
			changed, _, err := applyGoSymbolLock(targetPath, lock)
			if err != nil {
				t.Fatalf("apply go symbol lock: %v", err)
			}
			if !changed {
				t.Fatal("expected lock to change target source")
			}
			data, err := os.ReadFile(targetPath)
			if err != nil {
				t.Fatalf("read target source: %v", err)
			}
			source := string(data)
			if !strings.Contains(source, tc.want) || !strings.Contains(source, "func main()") {
				t.Fatalf("expected locked %s and preserved target source, got:\n%s", tc.symbolKind, source)
			}
		})
	}
}

func TestGoSymbolCodeLockReplacesOnlyGroupedValueSpec(t *testing.T) {
	cases := []struct {
		name       string
		symbolKind string
		symbolName string
		target     string
		locked     string
		want       []string
		wantAbsent []string
	}{
		{
			name:       "var",
			symbolKind: "var",
			symbolName: "defaultTitle",
			target: `package main

var (
	defaultTitle = "generated"
	maxTitle     = "keep"
)
`,
			locked: `package main

var (
	// LOCKED BY HUMAN
	defaultTitle = "human"
	ignoredTitle = "should-not-copy"
)
`,
			want:       []string{`defaultTitle`, `"human"`, `maxTitle`, `"keep"`},
			wantAbsent: []string{`ignoredTitle`, `defaultTitle = "generated"`},
		},
		{
			name:       "const",
			symbolKind: "const",
			symbolName: "maxTodos",
			target: `package main

const (
	minTodos = 1
	maxTodos = 10
)
`,
			locked: `package main

const (
	// LOCKED BY HUMAN
	maxTodos = 99
	ignoredTodos = 100
)
`,
			want:       []string{`minTodos = 1`, `maxTodos = 99`},
			wantAbsent: []string{`ignoredTodos`, `maxTodos = 10`},
		},
		{
			name:       "type",
			symbolKind: "type",
			symbolName: "todo",
			target: `package main

type (
	todo struct {
		ID string
	}
	user struct {
		Name string
	}
)
`,
			locked: `package main

type (
	// LOCKED BY HUMAN
	todo struct {
		ID string
		Title string
	}
	ignored struct{}
)
`,
			want:       []string{`Title string`, `user struct`},
			wantAbsent: []string{`ignored struct`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targetPath := filepath.Join(t.TempDir(), "generated-app", "main.go")
			if err := writeFile(targetPath, []byte(tc.target)); err != nil {
				t.Fatalf("write target source: %v", err)
			}
			lock := &domain.CodeLock{Path: "generated-app/main.go", Content: tc.locked, LockMode: "go_symbol", Language: "go", SymbolKind: tc.symbolKind, SymbolName: tc.symbolName, CreatedBy: "reviewer"}
			if _, _, err := applyGoSymbolLock(targetPath, lock); err != nil {
				t.Fatalf("apply go symbol lock: %v", err)
			}
			data, err := os.ReadFile(targetPath)
			if err != nil {
				t.Fatalf("read target source: %v", err)
			}
			source := string(data)
			for _, fragment := range tc.want {
				if !strings.Contains(source, fragment) {
					t.Fatalf("expected grouped %s lock to preserve %s, got:\n%s", tc.symbolKind, fragment, source)
				}
			}
			for _, fragment := range tc.wantAbsent {
				if strings.Contains(source, fragment) {
					t.Fatalf("expected grouped %s lock to exclude %s, got:\n%s", tc.symbolKind, fragment, source)
				}
			}
			if _, err := parser.ParseFile(token.NewFileSet(), targetPath, data, parser.ParseComments); err != nil {
				t.Fatalf("expected rewritten source to parse: %v\n%s", err, source)
			}
		})
	}
}

func TestGoSymbolCodeLockDisambiguatesMethodReceiver(t *testing.T) {
	targetPath := filepath.Join(t.TempDir(), "generated-app", "main.go")
	targetSource := []byte(`package main

type todo struct{}

type user struct{}

func (t todo) label() string {
	return "todo-generated"
}

func (u user) label() string {
	return "user-generated"
}
`)
	if err := writeFile(targetPath, targetSource); err != nil {
		t.Fatalf("write target source: %v", err)
	}
	lock := &domain.CodeLock{
		Path: "generated-app/main.go",
		Content: `package main

func (u user) label() string {
	// LOCKED BY HUMAN
	return "user-human"
}
`,
		LockMode:   "go_symbol",
		Language:   "go",
		SymbolKind: "method",
		SymbolName: "user.label",
		CreatedBy:  "reviewer",
	}
	changed, _, err := applyGoSymbolLock(targetPath, lock)
	if err != nil {
		t.Fatalf("apply go symbol lock: %v", err)
	}
	if !changed {
		t.Fatal("expected lock to change target source")
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target source: %v", err)
	}
	source := string(data)
	if !strings.Contains(source, `return "todo-generated"`) || !strings.Contains(source, `return "user-human"`) || strings.Contains(source, `return "user-generated"`) {
		t.Fatalf("expected only user.label to be replaced, got:\n%s", source)
	}
}

func TestGoSymbolCodeLockDisambiguatesPointerAndGenericMethodReceiver(t *testing.T) {
	targetPath := filepath.Join(t.TempDir(), "generated-app", "main.go")
	targetSource := []byte(`package main

type box[T any] struct{}

type todo struct{}

func (b *box[T]) label() string {
	return "box-generated"
}

func (t todo) label() string {
	return "todo-generated"
}
`)
	if err := writeFile(targetPath, targetSource); err != nil {
		t.Fatalf("write target source: %v", err)
	}
	lock := &domain.CodeLock{
		Path: "generated-app/main.go",
		Content: `package main

func (b *box[T]) label() string {
	// LOCKED BY HUMAN
	return "box-human"
}
`,
		LockMode:   "go_symbol",
		Language:   "go",
		SymbolKind: "method",
		SymbolName: "box.label",
		CreatedBy:  "reviewer",
	}
	if _, _, err := applyGoSymbolLock(targetPath, lock); err != nil {
		t.Fatalf("apply go symbol lock: %v", err)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target source: %v", err)
	}
	source := string(data)
	if !strings.Contains(source, `return "box-human"`) || !strings.Contains(source, `return "todo-generated"`) {
		t.Fatalf("expected only box.label to be replaced, got:\n%s", source)
	}
}

func TestGoSymbolCodeLockRejectsUnknownMethodReceiver(t *testing.T) {
	svc := New(config.Config{ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent"}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Unknown Receiver Lock Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	_, err = svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{
		Path: "generated-app/main.go",
		Content: `package main

type user struct{}

func (u user) label() string {
	// LOCKED BY HUMAN
	return "user"
}
`,
		LockMode:   "go_symbol",
		Language:   "go",
		SymbolKind: "method",
		SymbolName: "todo.label",
		CreatedBy:  "reviewer",
	})
	if err == nil || !strings.Contains(err.Error(), "method todo.label") {
		t.Fatalf("expected unknown method receiver validation error, got %v", err)
	}
}

func TestGoSymbolCodeLockRejectsInvalidInput(t *testing.T) {
	svc := New(config.Config{ArtifactRoot: t.TempDir(), SandboxRoot: t.TempDir(), DefaultAgent: "manager-agent"}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()
	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Invalid Go Lock Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.ApplyCodeLock(ctx, project.ID, ApplyCodeLockInput{
		Path:       "generated-app/main.go",
		Content:    "package main\n\n// LOCKED BY HUMAN\nfunc other() {}\n",
		LockMode:   "go_symbol",
		Language:   "go",
		SymbolKind: "func",
		SymbolName: "main",
	}); err == nil {
		t.Fatal("expected invalid go symbol lock to fail")
	}
}

func TestStartPreviewFromSharedSandbox(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-preview",
		ArtifactRoot: t.TempDir(),
		SandboxRoot:  t.TempDir(),
		DefaultAgent: "manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := New(cfg, logger)
	ctx := context.Background()

	project, contract, dispatched := prepareSharedSandboxMergeScenario(t, svc, ctx)
	if _, err := svc.MergeToSharedSandbox(ctx, project.ID, MergeSharedSandboxInput{
		TaskIDs:    []string{dispatched[0].ID, dispatched[1].ID},
		ContractID: contract.ID,
		Endpoints:  append([]domain.ContractEndpoint(nil), contract.Endpoints...),
		Schemas:    append([]domain.ContractSchema(nil), contract.Schemas...),
	}); err != nil {
		t.Fatalf("merge shared sandbox: %v", err)
	}

	result, err := svc.StartPreview(ctx, project.ID)
	if err != nil {
		t.Fatalf("start preview: %v", err)
	}
	if result.Preview.Status != "READY" {
		t.Fatalf("expected preview READY, got %s", result.Preview.Status)
	}
	if !strings.Contains(result.Preview.URL, "/projects/"+project.ID+"/preview/") {
		t.Fatalf("expected preview URL, got %s", result.Preview.URL)
	}
	if result.RefreshIntervalMs != 3000 {
		t.Fatalf("expected refresh interval 3000, got %d", result.RefreshIntervalMs)
	}
}

func mustListSandboxes(t *testing.T, svc *Service, ctx context.Context, projectID string) []SandboxView {
	t.Helper()
	sandboxes, err := svc.ListSandboxes(ctx, projectID)
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	return sandboxes
}

func initBareGitRemote(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	barePath := filepath.Join(t.TempDir(), "remote.git")
	runTestGit(t, t.TempDir(), "init", "--bare", barePath)
	workPath := cloneBareRemote(t, barePath)
	if err := os.WriteFile(filepath.Join(workPath, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runTestGit(t, workPath, "add", "README.md")
	runTestGit(t, workPath, "commit", "-m", "initial")
	runTestGit(t, workPath, "branch", "-M", "main")
	runTestGit(t, workPath, "push", "origin", "main")
	runTestGit(t, barePath, "symbolic-ref", "HEAD", "refs/heads/main")
	return barePath
}

func auditContainsAction(logs []domain.AuditLog, action string) bool {
	for _, item := range logs {
		if item.Action == action {
			return true
		}
	}
	return false
}

func alertContainsType(alerts []domain.Alert, alertType string) bool {
	for _, item := range alerts {
		if item.Type == alertType {
			return true
		}
	}
	return false
}

func cloneBareRemote(t *testing.T, barePath string) string {
	t.Helper()
	workPath := filepath.Join(t.TempDir(), "work")
	runTestGit(t, t.TempDir(), "clone", barePath, workPath)
	runTestGit(t, workPath, "config", "user.name", "MultiAgentCom Test")
	runTestGit(t, workPath, "config", "user.email", "multiagentcom-test@example.invalid")
	return workPath
}

func initTempGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init")
	runTestGit(t, repoPath, "config", "user.name", "MultiAgentCom Test")
	runTestGit(t, repoPath, "config", "user.email", "multiagentcom-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runTestGit(t, repoPath, "add", "README.md")
	runTestGit(t, repoPath, "commit", "-m", "initial")
	runTestGit(t, repoPath, "branch", "-M", "main")
	return repoPath
}

func runTestGit(t *testing.T, repoPath string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", fullArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(fullArgs, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func prepareSharedSandboxMergeScenario(t *testing.T, svc *Service, ctx context.Context) (*domain.Project, *domain.Contract, []domain.Task) {
	t.Helper()

	project, err := svc.CreateProject(ctx, CreateProjectInput{Name: "Shared Sandbox Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddRequirement(ctx, project.ID, AddRequirementInput{
		Title:       "实现 Todo 全栈功能",
		Content:     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		Constraints: []string{"后端使用 Go", "前端使用 Vue"},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := svc.GeneratePlan(ctx, project.ID); err != nil {
		t.Fatalf("generate plan: %v", err)
	}
	contract, err := svc.GenerateContract(ctx, project.ID)
	if err != nil {
		t.Fatalf("generate contract: %v", err)
	}
	dispatchResult, err := svc.DispatchTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("dispatch tasks: %v", err)
	}

	parallelResult, err := svc.StartParallelRun(ctx, project.ID, ParallelRunInput{
		TaskIDs: []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	if err != nil {
		t.Fatalf("start parallel run: %v", err)
	}
	for _, started := range parallelResult.Started {
		waitForSucceededRun(t, svc, project.ID, started.Run.ID)
	}

	return project, contract, dispatchResult.Tasks
}

func waitForSucceededRun(t *testing.T, svc *Service, projectID, runID string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := svc.GetRunStatus(context.Background(), projectID, runID)
		if err != nil {
			t.Fatalf("get run status: %v", err)
		}
		if status.Run.Status == domain.RunStatusSucceeded {
			if status.Task.Status != domain.TaskStatusDone {
				t.Fatalf("expected task DONE, got %s", status.Task.Status)
			}
			return
		}
		if status.Run.Status == domain.RunStatusFailed {
			t.Fatalf("expected run success, got failure: %s", status.Run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("run %s did not complete before deadline", runID)
}

func waitForRunTerminal(t *testing.T, svc *Service, projectID, runID string) *RunStatusView {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := svc.GetRunStatus(context.Background(), projectID, runID)
		if err != nil {
			t.Fatalf("get run status: %v", err)
		}
		if status.Run.Status == domain.RunStatusSucceeded || status.Run.Status == domain.RunStatusFailed {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("run %s did not reach terminal state before deadline", runID)
	return nil
}

func waitForTaskStatus(t *testing.T, svc *Service, projectID, runID string, expected domain.TaskStatus) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := svc.GetRunStatus(context.Background(), projectID, runID)
		if err != nil {
			t.Fatalf("get run status: %v", err)
		}
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

func readZipJSON(t *testing.T, zipPath, name string, target any) {
	t.Helper()

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip %s: %v", zipPath, err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		body, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", name, err)
		}
		defer body.Close()
		if err := json.NewDecoder(body).Decode(target); err != nil {
			t.Fatalf("decode zip entry %s: %v", name, err)
		}
		return
	}
	t.Fatalf("zip %s missing %s", zipPath, name)
}

func assertZipContains(t *testing.T, zipPath string, expected ...string) {
	t.Helper()

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip %s: %v", zipPath, err)
	}
	defer reader.Close()

	entries := make(map[string]struct{}, len(reader.File))
	for _, file := range reader.File {
		entries[file.Name] = struct{}{}
	}
	for _, item := range expected {
		if _, ok := entries[item]; !ok {
			t.Fatalf("expected zip %s to contain %s", zipPath, item)
		}
	}
}
