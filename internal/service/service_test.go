package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
			)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("run did not complete before deadline")
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
