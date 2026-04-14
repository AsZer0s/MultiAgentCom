package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
	"multiagentcom/internal/service"
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
	if !strings.Contains(string(body), "Status Matrix") {
		t.Fatalf("expected status panel html to contain title, got %s", string(body))
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

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

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

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

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

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("get request: %v", err)
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

func decodeResponse(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, string(body))
	}
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
